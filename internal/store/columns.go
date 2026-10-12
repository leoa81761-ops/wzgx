package store

import (
	"context"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/columns"
	"github.com/leoa81761-ops/wzgx/internal/db"
)

func (s *Store) ListAccountColumns(ctx context.Context, q db.Q, accountID int64, status string) ([]columns.Column, error) {
	if q == nil {
		q = s.DB
	}
	query := `SELECT id, wx_account_id, name, positioning, structure, word_target, image_style, weight, sort_order, status FROM account_column WHERE wx_account_id = ?`
	args := []any{accountID}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY sort_order ASC, id ASC`
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []columns.Column
	for rows.Next() {
		var col columns.Column
		if err := rows.Scan(&col.ID, &col.AccountID, &col.Name, &col.Positioning, &col.Structure, &col.WordTarget, &col.ImageStyle, &col.Weight, &col.SortOrder, &col.Status); err != nil {
			return nil, err
		}
		out = append(out, col)
	}
	if out == nil {
		out = []columns.Column{}
	}
	return out, rows.Err()
}

func (s *Store) InsertAccountColumn(ctx context.Context, q db.Q, col *columns.Column, now time.Time) error {
	if q == nil {
		q = s.DB
	}
	return q.QueryRow(ctx, `INSERT INTO account_column (wx_account_id, name, positioning, structure, word_target, image_style, weight, sort_order, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		col.AccountID, col.Name, col.Positioning, col.Structure, col.WordTarget, col.ImageStyle, col.Weight, col.SortOrder, col.Status, timeReq(now),
	).Scan(&col.ID)
}

func (s *Store) UpdateAccountColumn(ctx context.Context, q db.Q, col columns.Column) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE account_column SET name=?, positioning=?, structure=?, word_target=?, image_style=?, weight=?, sort_order=?, status=? WHERE id=? AND wx_account_id=?`,
		col.Name, col.Positioning, col.Structure, col.WordTarget, col.ImageStyle, col.Weight, col.SortOrder, col.Status, col.ID, col.AccountID)
	return err
}

func (s *Store) DeleteAccountColumn(ctx context.Context, q db.Q, accountID, id int64) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `DELETE FROM account_column WHERE id=? AND wx_account_id=?`, id, accountID)
	return err
}

func (s *Store) DeleteAccountColumns(ctx context.Context, q db.Q, accountID int64) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `DELETE FROM account_column WHERE wx_account_id=?`, accountID)
	return err
}

// BackfillSuggestedColumns adds suggestions for active accounts that were bound before columns existed.
func (s *Store) BackfillSuggestedColumns(ctx context.Context, q db.Q, now time.Time) error {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT id, name, persona_columns FROM wx_account WHERE bind_status = 'active' AND NOT EXISTS (SELECT 1 FROM account_column c WHERE c.wx_account_id = wx_account.id)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type pending struct {
		id      int64
		name    string
		columns string
	}
	var accounts []pending
	for rows.Next() {
		var row pending
		if err := rows.Scan(&row.id, &row.name, &row.columns); err != nil {
			return err
		}
		accounts = append(accounts, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, account := range accounts {
		history, err := s.ColumnNames(ctx, q, account.id)
		if err != nil {
			return err
		}
		suggested := columns.Suggest(account.name, "", account.columns, history)
		if err := s.ReplaceSuggestedColumns(ctx, q, account.id, suggested, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ReplaceSuggestedColumns(ctx context.Context, q db.Q, accountID int64, rows []columns.Column, now time.Time) error {
	if q == nil {
		q = s.DB
	}
	if _, err := q.Exec(ctx, `DELETE FROM account_column WHERE wx_account_id=? AND status='suggested'`, accountID); err != nil {
		return err
	}
	for i := range rows {
		rows[i].AccountID = accountID
		rows[i].Status = "suggested"
		rows[i].SortOrder = i
		if err := s.InsertAccountColumn(ctx, q, &rows[i], now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ActivateColumns(ctx context.Context, q db.Q, accountID int64, ids []int64) error {
	if q == nil {
		q = s.DB
	}
	if len(ids) == 0 {
		_, err := q.Exec(ctx, `UPDATE account_column SET status='active' WHERE wx_account_id=? AND status='suggested'`, accountID)
		return err
	}
	for _, id := range ids {
		if _, err := q.Exec(ctx, `UPDATE account_column SET status='active' WHERE id=? AND wx_account_id=?`, id, accountID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ColumnNames(ctx context.Context, q db.Q, accountID int64) ([]string, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT column_type FROM article WHERE wx_account_id = ? AND column_type != '' ORDER BY id DESC LIMIT 20`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	seen := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, rows.Err()
}
