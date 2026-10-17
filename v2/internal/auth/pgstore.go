package auth

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PGStore struct{ db *pgxpool.Pool }

func NewPGStore(db *pgxpool.Pool) *PGStore { return &PGStore{db: db} }

const userCols = `id, email, display_name, role, tier, max_accounts, quota_exempt, status, created_at, password_hash`

func scanUser(row pgx.Row) (User, error) {
	var u User
	var role string
	err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &role, &u.Tier, &u.MaxAccounts, &u.QuotaExempt, &u.Status, &u.CreatedAt, &u.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	u.Role = Role(role)
	return u, err
}

func (s *PGStore) CreateUser(ctx context.Context, n NewUser) (User, error) {
	u, err := scanUser(s.db.QueryRow(ctx, `INSERT INTO users (email, display_name, password_hash, role, created_by)
		VALUES ($1,$2,$3,$4,$5) RETURNING `+userCols, n.Email, n.DisplayName, n.PasswordHash, string(n.Role), n.CreatedBy))
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return User{}, ErrEmailTaken
	}
	return u, err
}

func (s *PGStore) UserByEmail(ctx context.Context, e string) (User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(email)=lower($1)`, e))
}

func (s *PGStore) UserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
}

func (s *PGStore) ListStaff(ctx context.Context) ([]User, error) {
	rows, err := s.db.Query(ctx, `SELECT `+userCols+` FROM users WHERE role IN ('operator','super_admin') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *PGStore) SetStatus(ctx context.Context, id int64, st string) error {
	t, err := s.db.Exec(ctx, `UPDATE users SET status=$2, updated_at=now() WHERE id=$1`, id, st)
	if err == nil && t.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *PGStore) CountRole(ctx context.Context, r Role) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM users WHERE role=$1`, string(r)).Scan(&n)
	return n, err
}

func (s *PGStore) CreateSession(ctx context.Context, x Session) error {
	_, err := s.db.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at, ip, user_agent) VALUES ($1,$2,$3,$4,$5)`,
		x.TokenHash, x.UserID, x.ExpiresAt, x.IP, x.UserAgent)
	return err
}

func (s *PGStore) SessionUser(ctx context.Context, h []byte, now time.Time) (User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT u.id, u.email, u.display_name, u.role, u.tier, u.max_accounts, u.quota_exempt,
		u.status, u.created_at, u.password_hash FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=$1 AND s.expires_at > $2`, h, now))
}

func (s *PGStore) DeleteSession(ctx context.Context, h []byte) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, h)
	return err
}

func (s *PGStore) DeleteUserSessions(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, id)
	return err
}

func (s *PGStore) RecordConsent(ctx context.Context, id int64, doc, ver, ip, dev string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO consent_log (user_id, doc_type, version, ip, device) VALUES ($1,$2,$3,$4,$5)`, id, doc, ver, ip, dev)
	return err
}

func (s *PGStore) Audit(ctx context.Context, actor *int64, action, target string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	b, _ := json.Marshal(detail)
	_, err := s.db.Exec(ctx, `INSERT INTO audit_log (actor_id, action, target, detail) VALUES ($1,$2,$3,$4)`, actor, action, target, b)
	return err
}
