package store

import (
	"context"
	"database/sql"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
)

const planCols = `id, user_id, wx_account_id, direction, mode, style_key, template_key, word_target, schedule_type, weekdays, time_window, status, pause_reason, next_run_at, daily_count, confirm_mode, created_at`

func scanPlan(row interface{ Scan(dest ...any) error }) (*model.WritingPlan, error) {
	var p model.WritingPlan
	var weekdays, next sql.NullString
	var created string
	var confirm int
	err := row.Scan(&p.ID, &p.UserID, &p.WxAccountID, &p.Direction, &p.Mode, &p.StyleKey, &p.TemplateKey, &p.WordTarget, &p.ScheduleType, &weekdays, &p.TimeWindow, &p.Status, &p.PauseReason, &next, &p.DailyCount, &confirm, &created)
	p.ConfirmMode = confirm == 1
	if err != nil {
		return nil, err
	}
	p.Weekdays = decodeInts(nstr(weekdays))
	p.NextRunAt = parsePtr(next)
	p.CreatedAt = mustTime(created)
	return &p, nil
}

func (s *Store) GetPlan(ctx context.Context, q db.Q, id int64) (*model.WritingPlan, error) {
	if q == nil {
		q = s.DB
	}
	p, err := scanPlan(q.QueryRow(ctx, `SELECT `+planCols+` FROM writing_plan WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

func (s *Store) ListPlansByUser(ctx context.Context, userID int64) ([]model.WritingPlan, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+planCols+` FROM writing_plan WHERE user_id = ? AND status != 'deleted' ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectPlans(rows)
}

func (s *Store) ListPlansForLimit(ctx context.Context, q db.Q, userID int64) ([]model.WritingPlan, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT `+planCols+` FROM writing_plan WHERE user_id = ? AND status != 'deleted' ORDER BY id ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectPlans(rows)
}

func (s *Store) ListPlansByAccount(ctx context.Context, q db.Q, accountID int64, statuses ...string) ([]model.WritingPlan, error) {
	if q == nil {
		q = s.DB
	}
	query := `SELECT ` + planCols + ` FROM writing_plan WHERE wx_account_id = ?`
	args := []any{accountID}
	if len(statuses) == 1 {
		query += ` AND status = ?`
		args = append(args, statuses[0])
	} else if len(statuses) == 0 {
		query += ` AND status != 'deleted'`
	}
	query += ` ORDER BY id ASC`
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectPlans(rows)
}

func (s *Store) ListDuePlans(ctx context.Context, q db.Q, now string) ([]model.WritingPlan, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT `+planCols+` FROM writing_plan WHERE status = 'active' AND next_run_at IS NOT NULL AND next_run_at <= ? ORDER BY id ASC`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectPlans(rows)
}

func (s *Store) ListPausedQuota(ctx context.Context, q db.Q) ([]model.WritingPlan, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT `+planCols+` FROM writing_plan WHERE status = 'paused' AND pause_reason = '额度已用完' ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectPlans(rows)
}

func collectPlans(rows *sql.Rows) ([]model.WritingPlan, error) {
	var out []model.WritingPlan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *Store) CountPlans(ctx context.Context, q db.Q, userID int64) (int, error) {
	if q == nil {
		q = s.DB
	}
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM writing_plan WHERE user_id = ? AND status != 'deleted'`, userID).Scan(&n)
	return n, err
}

func (s *Store) InsertPlan(ctx context.Context, q db.Q, p *model.WritingPlan) error {
	if q == nil {
		q = s.DB
	}
	if p.DailyCount < 1 {
		p.DailyCount = 1
	}
	return q.QueryRow(ctx, `INSERT INTO writing_plan (user_id, wx_account_id, direction, mode, style_key, template_key, word_target, schedule_type, weekdays, time_window, status, pause_reason, next_run_at, daily_count, confirm_mode, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		p.UserID, p.WxAccountID, p.Direction, p.Mode, p.StyleKey, p.TemplateKey, p.WordTarget, p.ScheduleType, jsonStr(p.Weekdays), p.TimeWindow, p.Status, p.PauseReason, timeArg(p.NextRunAt), p.DailyCount, bint(p.ConfirmMode), timeReq(p.CreatedAt),
	).Scan(&p.ID)
}

func (s *Store) UpdatePlan(ctx context.Context, q db.Q, p *model.WritingPlan) error {
	if q == nil {
		q = s.DB
	}
	if p.DailyCount < 1 {
		p.DailyCount = 1
	}
	_, err := q.Exec(ctx, `UPDATE writing_plan SET status=?, pause_reason=?, next_run_at=?, style_key=?, template_key=?, word_target=?, schedule_type=?, weekdays=?, time_window=?, direction=?, daily_count=?, confirm_mode=? WHERE id=?`,
		p.Status, p.PauseReason, timeArg(p.NextRunAt), p.StyleKey, p.TemplateKey, p.WordTarget, p.ScheduleType, jsonStr(p.Weekdays), p.TimeWindow, p.Direction, p.DailyCount, bint(p.ConfirmMode), p.ID)
	return err
}
