package store

import (
	"context"
	"database/sql"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
)

const taskCols = `id, plan_id, user_id, wx_account_id, article_id, regen_of_article_id, idempotency_key, trigger_type, schedule_date, run_at, status, failure_reason, retry_count, priority, started_at, finished_at, duration_ms, tokens, model_trace, created_at`

func scanTask(row interface{ Scan(dest ...any) error }) (*model.ArticleTask, error) {
	var t model.ArticleTask
	var articleID, regenID sql.NullInt64
	var started, finished, trace sql.NullString
	var runAt, created string
	err := row.Scan(&t.ID, &t.PlanID, &t.UserID, &t.WxAccountID, &articleID, &regenID, &t.IdempotencyKey, &t.TriggerType, &t.ScheduleDate, &runAt, &t.Status, &t.FailureReason, &t.RetryCount, &t.Priority, &started, &finished, &t.DurationMs, &t.Tokens, &trace, &created)
	if err != nil {
		return nil, err
	}
	t.ArticleID = scanNullInt(articleID)
	t.RegenOfArticleID = scanNullInt(regenID)
	t.RunAt = mustTime(runAt)
	t.StartedAt = parsePtr(started)
	t.FinishedAt = parsePtr(finished)
	t.ModelTrace = decodeTrace(nstr(trace))
	t.CreatedAt = mustTime(created)
	return &t, nil
}

func (s *Store) GetTask(ctx context.Context, q db.Q, id int64) (*model.ArticleTask, error) {
	if q == nil {
		q = s.DB
	}
	t, err := scanTask(q.QueryRow(ctx, `SELECT `+taskCols+` FROM article_task WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func (s *Store) FindScheduleTask(ctx context.Context, q db.Q, planID int64, day string) (*model.ArticleTask, error) {
	if q == nil {
		q = s.DB
	}
	t, err := scanTask(q.QueryRow(ctx, `SELECT `+taskCols+` FROM article_task WHERE plan_id = ? AND schedule_date = ? AND trigger_type = 'schedule' ORDER BY id ASC LIMIT 1`, planID, day))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func (s *Store) BatchTaken(ctx context.Context, q db.Q, accountID int64, day string) (bool, error) {
	if q == nil {
		q = s.DB
	}
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM article_task WHERE wx_account_id = ? AND schedule_date = ? AND trigger_type IN ('schedule', 'manual') AND status IN ('pending', 'running', 'success')`, accountID, day).Scan(&n)
	return n > 0, err
}

func (s *Store) UserHasRunning(ctx context.Context, q db.Q, userID int64) (bool, error) {
	if q == nil {
		q = s.DB
	}
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM article_task WHERE user_id = ? AND status = 'running'`, userID).Scan(&n)
	return n > 0, err
}

func (s *Store) InsertTask(ctx context.Context, q db.Q, t *model.ArticleTask) error {
	if q == nil {
		q = s.DB
	}
	return q.QueryRow(ctx, `INSERT INTO article_task (plan_id, user_id, wx_account_id, article_id, regen_of_article_id, idempotency_key, trigger_type, schedule_date, run_at, status, failure_reason, retry_count, priority, started_at, finished_at, duration_ms, tokens, model_trace, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		t.PlanID, t.UserID, t.WxAccountID, nullInt(t.ArticleID), nullInt(t.RegenOfArticleID), t.IdempotencyKey, t.TriggerType, t.ScheduleDate, timeReq(t.RunAt), t.Status, t.FailureReason, t.RetryCount, t.Priority, timeArg(t.StartedAt), timeArg(t.FinishedAt), t.DurationMs, t.Tokens, jsonStr(t.ModelTrace), timeReq(t.CreatedAt),
	).Scan(&t.ID)
}

func (s *Store) UpdateTask(ctx context.Context, q db.Q, t *model.ArticleTask) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE article_task SET article_id=?, status=?, failure_reason=?, retry_count=?, priority=?, started_at=?, finished_at=?, duration_ms=?, tokens=?, model_trace=?, run_at=? WHERE id=?`,
		nullInt(t.ArticleID), t.Status, t.FailureReason, t.RetryCount, t.Priority, timeArg(t.StartedAt), timeArg(t.FinishedAt), t.DurationMs, t.Tokens, jsonStr(t.ModelTrace), timeReq(t.RunAt), t.ID)
	return err
}

func (s *Store) RequeueStale(ctx context.Context, q db.Q, cutoff, now string) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE article_task SET status='pending', run_at=?, failure_reason='运行超时，已重新入队' WHERE status='running' AND started_at IS NOT NULL AND started_at < ?`, now, cutoff)
	return err
}

func (s *Store) ClaimOne(ctx context.Context, tx *db.Tx, now string) (int64, bool, error) {
	lock := ""
	if tx != nil && s.DB.Dialect == "postgres" {
		lock = " FOR UPDATE SKIP LOCKED"
	}
	query := `SELECT id FROM article_task WHERE status = 'pending' AND run_at <= ? AND user_id NOT IN (SELECT user_id FROM article_task WHERE status = 'running') ORDER BY priority DESC, id ASC LIMIT 1` + lock
	var id int64
	err := tx.QueryRow(ctx, query, now).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	res, err := tx.Exec(ctx, `UPDATE article_task SET status='running', started_at=? WHERE id=? AND status='pending'`, now, id)
	if err != nil {
		return 0, false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return 0, false, nil
	}
	return id, true, nil
}
