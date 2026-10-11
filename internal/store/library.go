package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
)

type StyleRow struct {
	Key         string
	Name        string
	Description string
	Tone        string
	MinTier     string
	Enabled     bool
}

type TemplateRow struct {
	Key         string
	Name        string
	Description string
	MinTier     string
	Enabled     bool
}

type VerticalPack struct {
	ID          int64
	Key         string
	Name        string
	Keywords    string
	Subtopics   string
	RedLines    string
	SourceAllow string
	ImageRules  string
	Disclaimer  string
	Enabled     bool
}

type PendingTier struct {
	Tier      string
	ExpiresAt *time.Time
}

func (s *Store) ListStyles(ctx context.Context, q db.Q) ([]StyleRow, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT key, name, description, tone, min_tier, enabled FROM style_preset ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StyleRow
	for rows.Next() {
		var row StyleRow
		var enabled int
		if err := rows.Scan(&row.Key, &row.Name, &row.Description, &row.Tone, &row.MinTier, &enabled); err != nil {
			return nil, err
		}
		row.Enabled = enabled != 0
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) SaveStyle(ctx context.Context, q db.Q, row StyleRow) error {
	if q == nil {
		q = s.DB
	}
	res, err := q.Exec(ctx, `UPDATE style_preset SET name=?, description=?, tone=?, min_tier=?, enabled=? WHERE key=?`,
		row.Name, row.Description, row.Tone, row.MinTier, bint(row.Enabled), row.Key)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return nil
	}
	_, err = q.Exec(ctx, `INSERT INTO style_preset (key, name, description, enabled, min_tier, tone) VALUES (?, ?, ?, ?, ?, ?)`,
		row.Key, row.Name, row.Description, bint(row.Enabled), row.MinTier, row.Tone)
	return err
}

func (s *Store) ListTemplates(ctx context.Context, q db.Q) ([]TemplateRow, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT key, name, description, min_tier, enabled FROM layout_template ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TemplateRow
	for rows.Next() {
		var row TemplateRow
		var enabled int
		if err := rows.Scan(&row.Key, &row.Name, &row.Description, &row.MinTier, &enabled); err != nil {
			return nil, err
		}
		row.Enabled = enabled != 0
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) SaveTemplate(ctx context.Context, q db.Q, row TemplateRow) error {
	if q == nil {
		q = s.DB
	}
	res, err := q.Exec(ctx, `UPDATE layout_template SET name=?, description=?, min_tier=?, enabled=? WHERE key=?`,
		row.Name, row.Description, row.MinTier, bint(row.Enabled), row.Key)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return nil
	}
	_, err = q.Exec(ctx, `INSERT INTO layout_template (key, name, description, version, enabled, min_tier) VALUES (?, ?, ?, '1', ?, ?)`,
		row.Key, row.Name, row.Description, bint(row.Enabled), row.MinTier)
	return err
}

func (s *Store) ListVerticals(ctx context.Context, q db.Q, enabledOnly bool) ([]VerticalPack, error) {
	if q == nil {
		q = s.DB
	}
	query := `SELECT id, key, name, keywords, subtopics, red_lines, source_allow, image_rules, disclaimer, enabled FROM vertical_pack`
	if enabledOnly {
		query += ` WHERE enabled = 1`
	}
	query += ` ORDER BY id ASC`
	rows, err := q.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VerticalPack
	for rows.Next() {
		var row VerticalPack
		var enabled int
		if err := rows.Scan(&row.ID, &row.Key, &row.Name, &row.Keywords, &row.Subtopics, &row.RedLines, &row.SourceAllow, &row.ImageRules, &row.Disclaimer, &enabled); err != nil {
			return nil, err
		}
		row.Enabled = enabled != 0
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) SaveVertical(ctx context.Context, q db.Q, row *VerticalPack, at time.Time) error {
	if q == nil {
		q = s.DB
	}
	res, err := q.Exec(ctx, `UPDATE vertical_pack SET name=?, keywords=?, subtopics=?, red_lines=?, source_allow=?, image_rules=?, disclaimer=?, enabled=?, updated_at=? WHERE key=?`,
		row.Name, row.Keywords, row.Subtopics, row.RedLines, row.SourceAllow, row.ImageRules, row.Disclaimer, bint(row.Enabled), timeReq(at), row.Key)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return nil
	}
	return q.QueryRow(ctx, `INSERT INTO vertical_pack (key, name, keywords, subtopics, red_lines, source_allow, image_rules, disclaimer, enabled, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		row.Key, row.Name, row.Keywords, row.Subtopics, row.RedLines, row.SourceAllow, row.ImageRules, row.Disclaimer, bint(row.Enabled), timeReq(at)).Scan(&row.ID)
}

func (s *Store) GetPendingTier(ctx context.Context, q db.Q, userID int64) (*PendingTier, error) {
	if q == nil {
		q = s.DB
	}
	var tier string
	var exp sql.NullString
	err := q.QueryRow(ctx, `SELECT pending_tier, pending_expires_at FROM tier_change WHERE user_id = ?`, userID).Scan(&tier, &exp)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &PendingTier{Tier: tier, ExpiresAt: parsePtr(exp)}, nil
}

func (s *Store) SetPendingTier(ctx context.Context, q db.Q, userID int64, tier string, expires *time.Time, at time.Time) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO tier_change (user_id, pending_tier, pending_expires_at, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET pending_tier = excluded.pending_tier, pending_expires_at = excluded.pending_expires_at, created_at = excluded.created_at`,
		userID, tier, timeArg(expires), timeReq(at))
	return err
}

func (s *Store) ClearPendingTier(ctx context.Context, q db.Q, userID int64) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `DELETE FROM tier_change WHERE user_id = ?`, userID)
	return err
}
