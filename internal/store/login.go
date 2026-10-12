package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
)

type OAuthState struct {
	State   string
	Kind    string
	UserID  int64
	Payload string
	Nonce   string
	Expires time.Time
}

func (s *Store) ReplaceSMSCode(ctx context.Context, q db.Q, phone, hash string, expires, now time.Time) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO sms_code (phone, code_hash, expires_at, attempts, created_at) VALUES (?, ?, ?, 0, ?)`, phone, hash, timeReq(expires), timeReq(now))
	return err
}

func (s *Store) CountSMSSince(ctx context.Context, q db.Q, phone string, since time.Time) (int, error) {
	if q == nil {
		q = s.DB
	}
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM sms_code WHERE phone = ? AND created_at >= ?`, phone, timeReq(since)).Scan(&n)
	return n, err
}

// ConsumeSMSAttempt increments the guess counter before the code is compared.
// A missing row means the code is gone or already has 5 guesses.
func (s *Store) ConsumeSMSAttempt(ctx context.Context, id int64) (int, bool, error) {
	var n int
	err := s.DB.QueryRow(ctx, `UPDATE sms_code SET attempts = attempts + 1 WHERE id = ? AND attempts < 5 RETURNING attempts`, id).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return n, true, nil
}

// DeleteSMSMatch removes one unused code. Only one caller can win.
func (s *Store) DeleteSMSMatch(ctx context.Context, q db.Q, id int64, hash string) (bool, error) {
	if q == nil {
		q = s.DB
	}
	var got int64
	err := q.QueryRow(ctx, `DELETE FROM sms_code WHERE id = ? AND code_hash = ? RETURNING id`, id, hash).Scan(&got)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) LatestSMS(ctx context.Context, q db.Q, phone string) (id int64, hash string, expires time.Time, attempts int, err error) {
	if q == nil {
		q = s.DB
	}
	var exp string
	err = q.QueryRow(ctx, `SELECT id, code_hash, expires_at, attempts FROM sms_code WHERE phone = ? ORDER BY id DESC LIMIT 1`, phone).Scan(&id, &hash, &exp, &attempts)
	if err == sql.ErrNoRows {
		return 0, "", time.Time{}, 0, nil
	}
	if err != nil {
		return 0, "", time.Time{}, 0, err
	}
	return id, hash, mustTime(exp), attempts, nil
}

func (s *Store) AddSMSAttempt(ctx context.Context, q db.Q, id int64) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE sms_code SET attempts = attempts + 1 WHERE id = ?`, id)
	return err
}

func (s *Store) DeleteSMS(ctx context.Context, q db.Q, id int64) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `DELETE FROM sms_code WHERE id = ?`, id)
	return err
}

func (s *Store) SaveOAuthState(ctx context.Context, q db.Q, row OAuthState, now time.Time) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO oauth_state (state, kind, user_id, payload, nonce, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		row.State, row.Kind, row.UserID, row.Payload, row.Nonce, timeReq(row.Expires), timeReq(now))
	return err
}

func (s *Store) GetOAuthState(ctx context.Context, q db.Q, state string) (*OAuthState, error) {
	if q == nil {
		q = s.DB
	}
	var row OAuthState
	var userID sql.NullInt64
	var exp string
	err := q.QueryRow(ctx, `SELECT state, kind, user_id, payload, nonce, expires_at FROM oauth_state WHERE state = ?`, state).Scan(&row.State, &row.Kind, &userID, &row.Payload, &row.Nonce, &exp)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if userID.Valid {
		row.UserID = userID.Int64
	}
	row.Expires = mustTime(exp)
	return &row, nil
}

// ConsumeOAuthState deletes one unused state. A second caller gets no row.
func (s *Store) ConsumeOAuthState(ctx context.Context, q db.Q, state, kind, nonce string, now time.Time) (*OAuthState, error) {
	if q == nil {
		q = s.DB
	}
	if state == "" || kind == "" || nonce == "" {
		return nil, nil
	}
	var row OAuthState
	var userID sql.NullInt64
	var exp string
	err := q.QueryRow(ctx, `DELETE FROM oauth_state WHERE state = ? AND kind = ? AND nonce = ? AND nonce != '' AND expires_at > ? RETURNING state, kind, user_id, payload, nonce, expires_at`,
		state, kind, nonce, timeReq(now)).Scan(&row.State, &row.Kind, &userID, &row.Payload, &row.Nonce, &exp)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if userID.Valid {
		row.UserID = userID.Int64
	}
	row.Expires = mustTime(exp)
	return &row, nil
}

func (s *Store) DeleteOAuthState(ctx context.Context, q db.Q, state string) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `DELETE FROM oauth_state WHERE state = ?`, state)
	return err
}

func (s *Store) UserByWeChat(ctx context.Context, q db.Q, openID, unionID string) (int64, error) {
	if q == nil {
		q = s.DB
	}
	var id int64
	err := q.QueryRow(ctx, `SELECT user_id FROM wechat_identity WHERE open_id = ?`, openID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	if unionID == "" {
		return 0, nil
	}
	err = q.QueryRow(ctx, `SELECT user_id FROM wechat_identity WHERE union_id = ? AND union_id != '' ORDER BY created_at ASC LIMIT 1`, unionID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

func (s *Store) SaveWeChatIdentity(ctx context.Context, q db.Q, openID, unionID string, userID int64, now time.Time) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO wechat_identity (open_id, union_id, user_id, created_at) VALUES (?, ?, ?, ?)`, openID, unionID, userID, timeReq(now))
	return err
}

func (s *Store) SaveAuthorizer(ctx context.Context, q db.Q, accountID int64, refresh string, now time.Time) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO wechat_authorizer (wx_account_id, refresh_token, updated_at) VALUES (?, ?, ?)`, accountID, refresh, timeReq(now))
	return err
}

func (s *Store) AuthorizerRefresh(ctx context.Context, q db.Q, accountID int64) (string, error) {
	if q == nil {
		q = s.DB
	}
	var token string
	err := q.QueryRow(ctx, `SELECT refresh_token FROM wechat_authorizer WHERE wx_account_id = ?`, accountID).Scan(&token)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return token, err
}
