package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
)

// TavilySecret returns the encrypted key and the last failure notice for one user.
func (s *Store) TavilySecret(ctx context.Context, q db.Q, userID int64) (string, string, error) {
	if q == nil {
		q = s.DB
	}
	var cipher, notice string
	err := q.QueryRow(ctx, `SELECT tavily_key_encrypted, tavily_notice FROM user_secret WHERE user_id = ?`, userID).Scan(&cipher, &notice)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	return cipher, notice, nil
}

// SaveTavily stores one user's encrypted Tavily key. The plaintext never reaches this table.
func (s *Store) SaveTavily(ctx context.Context, userID int64, cipher string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(ctx, `INSERT INTO user_secret (user_id, tavily_key_encrypted, tavily_notice, updated_at) VALUES (?, ?, '', ?)
		ON CONFLICT(user_id) DO UPDATE SET tavily_key_encrypted = excluded.tavily_key_encrypted, tavily_notice = '', updated_at = excluded.updated_at`,
		userID, cipher, now)
	return err
}

// ClearTavily removes one user's key and notice.
func (s *Store) ClearTavily(ctx context.Context, userID int64) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM user_secret WHERE user_id = ?`, userID)
	return err
}

// SetTavilyNotice records a key failure the account page can show. An empty notice clears it.
func (s *Store) SetTavilyNotice(ctx context.Context, userID int64, notice string) error {
	notice = strings.TrimSpace(notice)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(ctx, `INSERT INTO user_secret (user_id, tavily_key_encrypted, tavily_notice, updated_at) VALUES (?, '', ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET tavily_notice = excluded.tavily_notice, updated_at = excluded.updated_at`,
		userID, notice, now)
	return err
}

// TakeSearxng counts one SearXNG search for this user today. False means the daily cap is already used.
func (s *Store) TakeSearxng(ctx context.Context, userID int64, cap int) (bool, error) {
	if userID <= 0 || cap <= 0 {
		return true, nil
	}
	day := schedule.ShanghaiDay(time.Now())
	var count int
	err := s.DB.QueryRow(ctx, `INSERT INTO search_day (user_id, day, count) VALUES (?, ?, 1)
		ON CONFLICT(user_id, day) DO UPDATE SET count = search_day.count + 1
		WHERE search_day.count < ?
		RETURNING count`, userID, day, cap).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return count <= cap, nil
}
