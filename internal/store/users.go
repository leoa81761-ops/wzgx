package store

import (
	"context"
	"database/sql"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
)

const userCols = `id, email, phone, password_hash, tier, tier_expires_at, tier_started_at, is_admin, is_active, created_at`

func scanUser(row interface{ Scan(dest ...any) error }) (*model.User, error) {
	var u model.User
	var email, phone, exp, started sql.NullString
	var admin, active int
	var created string
	err := row.Scan(&u.ID, &email, &phone, &u.PasswordHash, &u.Tier, &exp, &started, &admin, &active, &created)
	if err != nil {
		return nil, err
	}
	u.Email = nstr(email)
	u.Phone = nstr(phone)
	u.TierExpiresAt = parsePtr(exp)
	u.TierStartedAt = parsePtr(started)
	u.IsAdmin = admin != 0
	u.IsActive = active != 0
	u.CreatedAt = mustTime(created)
	return &u, nil
}

func (s *Store) GetUser(ctx context.Context, q db.Q, id int64) (*model.User, error) {
	if q == nil {
		q = s.DB
	}
	u, err := scanUser(q.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (s *Store) FindUserByEmail(ctx context.Context, q db.Q, email string) (*model.User, error) {
	if q == nil {
		q = s.DB
	}
	u, err := scanUser(q.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email = ?`, email))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (s *Store) FindUserByPhone(ctx context.Context, q db.Q, phone string) (*model.User, error) {
	if q == nil {
		q = s.DB
	}
	u, err := scanUser(q.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE phone = ?`, phone))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (s *Store) FindUserByAccount(ctx context.Context, q db.Q, account string) (*model.User, error) {
	if q == nil {
		q = s.DB
	}
	u, err := scanUser(q.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email = ? OR phone = ?`, account, account))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (s *Store) InsertUser(ctx context.Context, q db.Q, u *model.User) error {
	if q == nil {
		q = s.DB
	}
	var email, phone any
	if u.Email != "" {
		email = u.Email
	}
	if u.Phone != "" {
		phone = u.Phone
	}
	err := q.QueryRow(ctx, `INSERT INTO users (email, phone, password_hash, tier, tier_expires_at, tier_started_at, is_admin, is_active, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		email, phone, u.PasswordHash, u.Tier, timeArg(u.TierExpiresAt), timeArg(u.TierStartedAt), bint(u.IsAdmin), bint(u.IsActive), timeReq(u.CreatedAt),
	).Scan(&u.ID)
	return err
}

func (s *Store) UpdateUser(ctx context.Context, q db.Q, u *model.User) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE users SET tier=?, tier_expires_at=?, tier_started_at=?, is_admin=?, is_active=? WHERE id=?`,
		u.Tier, timeArg(u.TierExpiresAt), timeArg(u.TierStartedAt), bint(u.IsAdmin), bint(u.IsActive), u.ID)
	return err
}

func (s *Store) ListUsers(ctx context.Context, limit int) ([]model.User, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (s *Store) CountAdmins(ctx context.Context, q db.Q, activeOnly bool) (int, error) {
	if q == nil {
		q = s.DB
	}
	query := `SELECT COUNT(*) FROM users WHERE is_admin = 1`
	if activeOnly {
		query += ` AND is_active = 1`
	}
	var n int
	err := q.QueryRow(ctx, query).Scan(&n)
	return n, err
}

func (s *Store) DeleteUserCascade(ctx context.Context, q db.Q, userID int64) error {
	if q == nil {
		q = s.DB
	}
	stmts := []string{
		`DELETE FROM topic_lock WHERE task_id IN (SELECT id FROM article_task WHERE user_id = ?)`,
		`DELETE FROM topic_proposal WHERE plan_id IN (SELECT id FROM writing_plan WHERE user_id = ?)`,
		`DELETE FROM referral WHERE inviter_id = ?`,
		`DELETE FROM referral WHERE invitee_id = ?`,
		`DELETE FROM wechat_identity WHERE user_id = ?`,
		`DELETE FROM wechat_authorizer WHERE wx_account_id IN (SELECT id FROM wx_account WHERE user_id = ?)`,
		`DELETE FROM user_invite WHERE user_id = ?`,
		`DELETE FROM notification WHERE user_id = ?`,
		`DELETE FROM feedback WHERE user_id = ?`,
		`DELETE FROM complaint WHERE user_id = ?`,
		`DELETE FROM article_publish WHERE article_id IN (SELECT id FROM article WHERE user_id = ?)`,
		`DELETE FROM plan_extra WHERE plan_id IN (SELECT id FROM writing_plan WHERE user_id = ?)`,
		`DELETE FROM article_variant WHERE article_id IN (SELECT id FROM article WHERE user_id = ?)`,
		`DELETE FROM style_profile WHERE user_id = ?`,
		`DELETE FROM user_asset WHERE user_id = ?`,
		`DELETE FROM sample_draft WHERE user_id = ?`,
		`DELETE FROM trial_log WHERE user_id = ?`,
		`DELETE FROM plan_publish WHERE plan_id IN (SELECT id FROM writing_plan WHERE user_id = ?)`,
		`DELETE FROM publish_usage WHERE wx_account_id IN (SELECT id FROM wx_account WHERE user_id = ?)`,
		`DELETE FROM pay_order WHERE user_id = ?`,
		`DELETE FROM topic_veto WHERE plan_id IN (SELECT id FROM writing_plan WHERE user_id = ?)`,
		`DELETE FROM plan_pin WHERE plan_id IN (SELECT id FROM writing_plan WHERE user_id = ?)`,
		`DELETE FROM article_source WHERE article_id IN (SELECT id FROM article WHERE user_id = ?)`,
		`DELETE FROM topic_coverage WHERE plan_id IN (SELECT id FROM writing_plan WHERE user_id = ?)`,
		`DELETE FROM quota_hold WHERE user_id = ?`,
		`DELETE FROM quota_ledger WHERE user_id = ?`,
		`DELETE FROM article WHERE user_id = ?`,
		`DELETE FROM article_task WHERE user_id = ?`,
		`DELETE FROM writing_plan WHERE user_id = ?`,
		`DELETE FROM wx_account WHERE user_id = ?`,
		`DELETE FROM consent_log WHERE user_id = ?`,
		`DELETE FROM entitlement WHERE user_id = ?`,
		`DELETE FROM tier_change WHERE user_id = ?`,
		`DELETE FROM users WHERE id = ?`,
	}
	for _, stmt := range stmts {
		if _, err := q.Exec(ctx, stmt, userID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListActiveUserIDs(ctx context.Context, q db.Q) ([]int64, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT id FROM users WHERE is_active = 1 ORDER BY id ASC`)
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
