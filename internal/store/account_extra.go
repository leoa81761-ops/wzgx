package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
)

func (s *Store) InsertPack(ctx context.Context, q db.Q, userID int64, orderNo string, articles int, expires, now time.Time) error {
	if q == nil {
		q = s.DB
	}
	if articles < 1 {
		articles = 1
	}
	_, err := q.Exec(ctx, `INSERT INTO quota_pack (user_id, order_no, articles, remaining, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, orderNo, articles, articles, timeReq(expires), timeReq(now))
	return err
}

func (s *Store) PackRemaining(ctx context.Context, q db.Q, userID int64, now time.Time) (int, error) {
	if q == nil {
		q = s.DB
	}
	var n int
	err := q.QueryRow(ctx, `SELECT COALESCE(SUM(remaining), 0) FROM quota_pack WHERE user_id = ? AND remaining > 0 AND expires_at > ?`, userID, timeReq(now)).Scan(&n)
	return n, err
}

func (s *Store) ConsumePack(ctx context.Context, q db.Q, userID int64, now time.Time) (int64, bool, error) {
	if q == nil {
		q = s.DB
	}
	var id int64
	err := q.QueryRow(ctx, `UPDATE quota_pack SET remaining = remaining - 1 WHERE id = (
		SELECT id FROM quota_pack WHERE user_id = ? AND remaining > 0 AND expires_at > ? ORDER BY expires_at ASC, id ASC LIMIT 1
	) AND remaining > 0 RETURNING id`, userID, timeReq(now)).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func (s *Store) RestorePack(ctx context.Context, q db.Q, packID int64) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE quota_pack SET remaining = remaining + 1 WHERE id = ?`, packID)
	return err
}

func (s *Store) SavePackHold(ctx context.Context, q db.Q, taskID, packID int64) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO pack_hold (task_id, pack_id) VALUES (?, ?)`, taskID, packID)
	return err
}

func (s *Store) TakePackHold(ctx context.Context, q db.Q, taskID int64) (int64, error) {
	if q == nil {
		q = s.DB
	}
	var packID int64
	err := q.QueryRow(ctx, `DELETE FROM pack_hold WHERE task_id = ? RETURNING pack_id`, taskID).Scan(&packID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return packID, err
}

func (s *Store) LatestPaidOrder(ctx context.Context, q db.Q, userID int64) (*PayOrder, error) {
	if q == nil {
		q = s.DB
	}
	row := q.QueryRow(ctx, `SELECT id, user_id, out_trade_no, provider, channel, tier, period, amount_cents, status, code_url, pay_url, provider_trade_no, created_at, paid_at FROM pay_order WHERE user_id = ? AND status = 'paid' AND tier != 'pack' ORDER BY id DESC LIMIT 1`, userID)
	return scanPayOrder(row, "")
}

func (s *Store) CountPaidOrders(ctx context.Context, q db.Q, userID int64) (int, error) {
	if q == nil {
		q = s.DB
	}
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM pay_order WHERE user_id = ? AND status = 'paid' AND tier != 'pack'`, userID).Scan(&n)
	return n, err
}

func (s *Store) CountConfirmedSince(ctx context.Context, q db.Q, userID int64, since time.Time) (int, error) {
	if q == nil {
		q = s.DB
	}
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM quota_hold WHERE user_id = ? AND state = 'confirmed' AND amount > 0 AND created_at >= ?`, userID, timeReq(since)).Scan(&n)
	return n, err
}

func (s *Store) ListExpiringUsers(ctx context.Context) ([]model.User, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+userCols+` FROM users WHERE tier NOT IN ('free', 'lifetime', '') AND tier_expires_at IS NOT NULL AND is_active = 1`)
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

func (s *Store) InsertExpiryNotice(ctx context.Context, userID int64, kind, expiresOn string, now time.Time) (bool, error) {
	_, err := s.DB.Exec(ctx, `INSERT INTO expiry_notice (user_id, kind, expires_on, created_at) VALUES (?, ?, ?, ?)`, userID, kind, expiresOn, timeReq(now))
	if err == nil {
		return true, nil
	}
	if db.IsUnique(err) {
		return false, nil
	}
	return false, err
}

func (s *Store) UpdatePassword(ctx context.Context, q db.Q, userID int64, hash string) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, userID)
	return err
}

func (s *Store) UpdatePhone(ctx context.Context, q db.Q, userID int64, phone string) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE users SET phone = ? WHERE id = ?`, phone, userID)
	return err
}

func (s *Store) InsertRecovery(ctx context.Context, q db.Q, account, hash, purpose string, expires, now time.Time) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO recovery_code (account, code_hash, purpose, expires_at, attempts, created_at) VALUES (?, ?, ?, ?, 0, ?)`,
		account, hash, purpose, timeReq(expires), timeReq(now))
	return err
}

func (s *Store) LatestRecovery(ctx context.Context, q db.Q, account, purpose string) (id int64, hash string, expires time.Time, err error) {
	if q == nil {
		q = s.DB
	}
	var exp string
	err = q.QueryRow(ctx, `SELECT id, code_hash, expires_at FROM recovery_code WHERE account = ? AND purpose = ? ORDER BY id DESC LIMIT 1`, account, purpose).Scan(&id, &hash, &exp)
	if err == sql.ErrNoRows {
		return 0, "", time.Time{}, nil
	}
	if err != nil {
		return 0, "", time.Time{}, err
	}
	return id, hash, mustTime(exp), nil
}

func (s *Store) ConsumeRecoveryAttempt(ctx context.Context, id int64) (bool, error) {
	var n int
	err := s.DB.QueryRow(ctx, `UPDATE recovery_code SET attempts = attempts + 1 WHERE id = ? AND attempts < 5 RETURNING attempts`, id).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) CountRecoverySince(ctx context.Context, q db.Q, account string, since time.Time) (int, error) {
	if q == nil {
		q = s.DB
	}
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM recovery_code WHERE account = ? AND created_at >= ?`, account, timeReq(since)).Scan(&n)
	return n, err
}

func (s *Store) DeleteRecoveryMatch(ctx context.Context, q db.Q, id int64, hash string) (bool, error) {
	if q == nil {
		q = s.DB
	}
	var got int64
	err := q.QueryRow(ctx, `DELETE FROM recovery_code WHERE id = ? AND code_hash = ? RETURNING id`, id, hash).Scan(&got)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) WithdrawConsent(ctx context.Context, q db.Q, userID, id int64, now time.Time) (*model.Consent, error) {
	if q == nil {
		q = s.DB
	}
	var c model.Consent
	var created string
	err := q.QueryRow(ctx, `UPDATE consent_log SET withdrawn_at = ? WHERE id = ? AND user_id = ? AND withdrawn_at IS NULL RETURNING id, user_id, doc_type, version, consent_item, ip, user_agent, created_at`,
		timeReq(now), id, userID).Scan(&c.ID, &c.UserID, &c.DocType, &c.Version, &c.ConsentItem, &c.IP, &c.UserAgent, &created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.CreatedAt = mustTime(created)
	c.WithdrawnAt = &now
	return &c, nil
}

func (s *Store) DisableUserPublish(ctx context.Context, q db.Q, userID int64, now time.Time) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE plan_publish SET enabled = 0, updated_at = ? WHERE plan_id IN (SELECT id FROM writing_plan WHERE user_id = ?)`, timeReq(now), userID)
	return err
}

type Contract struct {
	UserID     int64
	Company    string
	ContractNo string
	Note       string
	UpdatedAt  time.Time
}

func (s *Store) SaveContract(ctx context.Context, q db.Q, row Contract) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO user_contract (user_id, company, contract_no, note, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET company = excluded.company, contract_no = excluded.contract_no, note = excluded.note, updated_at = excluded.updated_at`,
		row.UserID, row.Company, row.ContractNo, row.Note, timeReq(row.UpdatedAt))
	return err
}

func (s *Store) GetContract(ctx context.Context, userID int64) (*Contract, error) {
	var row Contract
	var updated string
	err := s.DB.QueryRow(ctx, `SELECT user_id, company, contract_no, note, updated_at FROM user_contract WHERE user_id = ?`, userID).
		Scan(&row.UserID, &row.Company, &row.ContractNo, &row.Note, &updated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	row.UpdatedAt = mustTime(updated)
	return &row, nil
}
