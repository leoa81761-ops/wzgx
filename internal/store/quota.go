package store

import (
	"context"
	"database/sql"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
)

func (s *Store) SumHold(ctx context.Context, q db.Q, userID int64, column, key string) (int, error) {
	if q == nil {
		q = s.DB
	}
	query := `SELECT COALESCE(SUM(amount), 0) FROM quota_hold WHERE user_id = ? AND ` + column + ` = ? AND state IN ('held', 'confirmed') AND amount > 0`
	if column != "day_key" && column != "period_key" {
		return 0, sql.ErrNoRows
	}
	var n int
	err := q.QueryRow(ctx, query, userID, key).Scan(&n)
	return n, err
}

func (s *Store) GetHoldByTask(ctx context.Context, q db.Q, taskID int64) (*model.QuotaHold, error) {
	if q == nil {
		q = s.DB
	}
	var h model.QuotaHold
	var created string
	err := q.QueryRow(ctx, `SELECT id, user_id, task_id, day_key, period_key, amount, state, created_at FROM quota_hold WHERE task_id = ?`, taskID).
		Scan(&h.ID, &h.UserID, &h.TaskID, &h.DayKey, &h.PeriodKey, &h.Amount, &h.State, &created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	h.CreatedAt = mustTime(created)
	return &h, nil
}

func (s *Store) InsertHold(ctx context.Context, q db.Q, h *model.QuotaHold) error {
	if q == nil {
		q = s.DB
	}
	return q.QueryRow(ctx, `INSERT INTO quota_hold (user_id, task_id, day_key, period_key, amount, state, created_at) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		h.UserID, h.TaskID, h.DayKey, h.PeriodKey, h.Amount, h.State, timeReq(h.CreatedAt)).Scan(&h.ID)
}

func (s *Store) UpdateHoldState(ctx context.Context, q db.Q, id int64, state string) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE quota_hold SET state = ? WHERE id = ?`, state, id)
	return err
}

func (s *Store) InsertLedger(ctx context.Context, q db.Q, userID int64, changeType string, amount int, taskID *int64, note string, created string) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO quota_ledger (user_id, change_type, amount, task_id, note, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, changeType, amount, nullInt(taskID), note, created)
	return err
}
