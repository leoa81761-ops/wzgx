package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
)

const taskCols = `id, plan_id, user_id, wx_account_id, article_id, regen_of_article_id, idempotency_key, trigger_type, schedule_date, run_at, status, failure_reason, retry_count, priority, started_at, finished_at, duration_ms, tokens, model_trace, created_at, progress_at, skip_draft`

func scanTask(row interface{ Scan(dest ...any) error }) (*model.ArticleTask, error) {
	var t model.ArticleTask
	var articleID, regenID sql.NullInt64
	var started, finished, trace, progress sql.NullString
	var runAt, created string
	err := row.Scan(&t.ID, &t.PlanID, &t.UserID, &t.WxAccountID, &articleID, &regenID, &t.IdempotencyKey, &t.TriggerType, &t.ScheduleDate, &runAt, &t.Status, &t.FailureReason, &t.RetryCount, &t.Priority, &started, &finished, &t.DurationMs, &t.Tokens, &trace, &created, &progress, &t.SkipDraft)
	if err != nil {
		return nil, err
	}
	t.ArticleID = scanNullInt(articleID)
	t.RegenOfArticleID = scanNullInt(regenID)
	t.RunAt = mustTime(runAt)
	t.StartedAt = parsePtr(started)
	t.ProgressAt = parsePtr(progress)
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

func (s *Store) PlanHasOpen(ctx context.Context, q db.Q, planID int64) (bool, error) {
	if q == nil {
		q = s.DB
	}
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM article_task WHERE plan_id = ? AND status IN ('pending', 'running')`, planID).Scan(&n)
	return n > 0, err
}

func (s *Store) CountPlanTasks(ctx context.Context, q db.Q, planID int64, day string, triggers []string) (int, error) {
	if q == nil {
		q = s.DB
	}
	if len(triggers) == 0 {
		triggers = []string{"schedule", "manual"}
	}
	placeholders := make([]string, len(triggers))
	args := []any{planID, day}
	for i, trigger := range triggers {
		placeholders[i] = "?"
		args = append(args, trigger)
	}
	query := `SELECT COUNT(*) FROM article_task WHERE plan_id = ? AND schedule_date = ? AND status IN ('pending', 'running', 'success') AND trigger_type IN (` + strings.Join(placeholders, ",") + `)`
	var n int
	err := q.QueryRow(ctx, query, args...).Scan(&n)
	return n, err
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

func (s *Store) SetTaskSkipDraft(ctx context.Context, q db.Q, id int64, skip int) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE article_task SET skip_draft=? WHERE id=?`, skip, id)
	return err
}

func (s *Store) UpdateTask(ctx context.Context, q db.Q, t *model.ArticleTask) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE article_task SET article_id=?, status=?, failure_reason=?, retry_count=?, priority=?, started_at=?, finished_at=?, duration_ms=?, tokens=?, model_trace=?, run_at=? WHERE id=?`,
		nullInt(t.ArticleID), t.Status, t.FailureReason, t.RetryCount, t.Priority, timeArg(t.StartedAt), timeArg(t.FinishedAt), t.DurationMs, t.Tokens, jsonStr(t.ModelTrace), timeReq(t.RunAt), t.ID)
	return err
}

// ListStuckTaskIDs returns tasks past the hard start cutoff or with no stage progress since the idle cutoff.
// A running task counts, and so does a pending task whose article is still generating,
// which is what a restart leaves behind after the old requeue.
func (s *Store) ListStuckTaskIDs(ctx context.Context, q db.Q, hardCutoff, idleCutoff string) ([]int64, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT DISTINCT t.id FROM article_task t
		LEFT JOIN article a ON a.task_id = t.id
		WHERE t.started_at IS NOT NULL
		  AND (t.started_at < ? OR COALESCE(NULLIF(t.progress_at, ''), t.started_at) < ?)
		  AND (t.status = 'running' OR (t.status = 'pending' AND a.status = 'generating'))`, hardCutoff, idleCutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) TouchProgress(ctx context.Context, q db.Q, id int64, at time.Time) error {
	if q == nil {
		q = s.DB
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := q.Exec(ctx, `UPDATE article_task SET progress_at=? WHERE id=? AND status IN ('running', 'pending')`, at.UTC().Format("2006-01-02T15:04:05Z"), id)
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
	query := `SELECT id FROM article_task WHERE status = 'pending' AND run_at <= ? AND plan_id NOT IN (SELECT plan_id FROM article_task WHERE status = 'running') ORDER BY priority DESC, id ASC LIMIT 1` + lock
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

// LockUserQuota 在当前事务里按用户串行化建任务和占额度，避免并发请求越过每日上限。
// Postgres 用事务级 advisory lock，提交或回滚时自动释放；SQLite 写事务本身串行，不需要。
func (s *Store) LockUserQuota(ctx context.Context, q db.Q, userID int64) error {
	if s.DB.Dialect != "postgres" {
		return nil
	}
	if q == nil {
		return fmt.Errorf("postgres 额度锁定必须在事务里")
	}
	rows, err := q.Query(ctx, `SELECT pg_advisory_xact_lock(7301, CAST(? AS INTEGER))`, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
	}
	return rows.Err()
}
