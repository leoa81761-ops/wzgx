package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
)

func (s *Store) GetSetting(ctx context.Context, q db.Q, key string) (string, error) {
	if q == nil {
		q = s.DB
	}
	var value string
	err := q.QueryRow(ctx, `SELECT value FROM app_setting WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (s *Store) SetSetting(ctx context.Context, q db.Q, key, value string, now time.Time) error {
	if q == nil {
		q = s.DB
	}
	stamp := timeReq(now)
	res, err := q.Exec(ctx, `UPDATE app_setting SET value = ?, updated_at = ? WHERE key = ?`, value, stamp, key)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err = q.Exec(ctx, `INSERT INTO app_setting (key, value, updated_at) VALUES (?, ?, ?)`, key, value, stamp)
	return err
}

func (s *Store) ListLegalAll(ctx context.Context) ([]model.LegalDoc, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, doc_type, version, title, content, status, effective_date FROM legal_doc ORDER BY doc_type ASC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectLegal(rows)
}

func (s *Store) ListLegalVersions(ctx context.Context, docType string) ([]model.LegalDoc, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, doc_type, version, title, content, status, effective_date FROM legal_doc WHERE doc_type = ? ORDER BY id DESC`, docType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectLegal(rows)
}

func (s *Store) GetLegalVersion(ctx context.Context, docType, version string) (*model.LegalDoc, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, doc_type, version, title, content, status, effective_date FROM legal_doc WHERE doc_type = ? AND version = ? ORDER BY id DESC LIMIT 1`, docType, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list, err := collectLegal(rows)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

func (s *Store) ArchiveLegal(ctx context.Context, q db.Q, docType string) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE legal_doc SET status = 'archived' WHERE doc_type = ? AND status = 'active'`, docType)
	return err
}

type UsageCost struct {
	UserID   int64
	TaskID   int64
	MonthKey string
	Kind     string
	Units    int
	Cents    int
	Created  time.Time
}

func (s *Store) InsertUsageCost(ctx context.Context, q db.Q, row UsageCost) error {
	if q == nil {
		q = s.DB
	}
	if row.Cents == 0 && row.Units == 0 {
		return nil
	}
	_, err := q.Exec(ctx, `INSERT INTO usage_cost (user_id, task_id, month_key, kind, units, cents, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		row.UserID, row.TaskID, row.MonthKey, row.Kind, row.Units, row.Cents, timeReq(row.Created))
	return err
}

func (s *Store) SumMonthCost(ctx context.Context, q db.Q, userID int64, monthKey string) (int, error) {
	if q == nil {
		q = s.DB
	}
	var n sql.NullInt64
	err := q.QueryRow(ctx, `SELECT SUM(cents) FROM usage_cost WHERE user_id = ? AND month_key = ?`, userID, monthKey).Scan(&n)
	if err != nil {
		return 0, err
	}
	return int(n.Int64), nil
}

func (s *Store) PurgeArticleBodies(ctx context.Context, before string) (int64, error) {
	res, err := s.DB.Exec(ctx, `UPDATE article SET body_json = '', body_html = '', sources = '' WHERE created_at < ? AND body_html != ''`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

type Metrics struct {
	UsersTotal    int
	Users7d       int
	Users30d      int
	PaidUsers     int
	OrdersPaid30d int
	Renewals30d   int
	Articles30d   int
	CostCents30d  int
}

func (s *Store) Metrics(ctx context.Context, since7, since30 string) (Metrics, error) {
	var m Metrics
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&m.UsersTotal); err != nil {
		return m, err
	}
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE created_at >= ?`, since7).Scan(&m.Users7d); err != nil {
		return m, err
	}
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE created_at >= ?`, since30).Scan(&m.Users30d); err != nil {
		return m, err
	}
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE tier != 'free' AND is_active = 1`).Scan(&m.PaidUsers); err != nil {
		return m, err
	}
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM pay_order WHERE status = 'paid' AND COALESCE(paid_at, created_at) >= ?`, since30).Scan(&m.OrdersPaid30d); err != nil {
		return m, err
	}
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM pay_order o WHERE o.status = 'paid' AND COALESCE(o.paid_at, o.created_at) >= ? AND EXISTS (SELECT 1 FROM pay_order p WHERE p.user_id = o.user_id AND p.status = 'paid' AND p.id < o.id)`, since30).Scan(&m.Renewals30d); err != nil {
		return m, err
	}
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM article WHERE created_at >= ? AND status IN ('in_draft', 'exported', 'published', 'publish_failed', 'publish_unknown')`, since30).Scan(&m.Articles30d); err != nil {
		return m, err
	}
	var cents sql.NullInt64
	if err := s.DB.QueryRow(ctx, `SELECT SUM(cents) FROM usage_cost WHERE created_at >= ?`, since30).Scan(&cents); err != nil {
		return m, err
	}
	m.CostCents30d = int(cents.Int64)
	return m, nil
}
