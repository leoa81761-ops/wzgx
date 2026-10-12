package store

import (
	"context"
	"database/sql"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
)

const accountCols = `id, user_id, name, app_id, secret_encrypted, subject_type, service_type, verified, delivery_mode, bind_status, permission_report, bound_at, unbound_at, audience, tone, persona_columns, signatures, taboos`

func scanAccount(row interface{ Scan(dest ...any) error }) (*model.WxAccount, error) {
	var a model.WxAccount
	var verified int
	var report, bound, unbound sql.NullString
	err := row.Scan(&a.ID, &a.UserID, &a.Name, &a.AppID, &a.SecretEncrypted, &a.SubjectType, &a.ServiceType, &verified, &a.DeliveryMode, &a.BindStatus, &report, &bound, &unbound, &a.Audience, &a.Tone, &a.PersonaColumns, &a.Signatures, &a.Taboos)
	if err != nil {
		return nil, err
	}
	a.Verified = verified != 0
	a.PermissionReport = decodeMap(nstr(report))
	a.BoundAt = parsePtr(bound)
	a.UnboundAt = parsePtr(unbound)
	return &a, nil
}

func (s *Store) GetAccount(ctx context.Context, q db.Q, id int64) (*model.WxAccount, error) {
	if q == nil {
		q = s.DB
	}
	a, err := scanAccount(q.QueryRow(ctx, `SELECT `+accountCols+` FROM wx_account WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}

func (s *Store) ListAccountsByUser(ctx context.Context, userID int64) ([]model.WxAccount, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+accountCols+` FROM wx_account WHERE user_id = ? AND bind_status != 'unbound' ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAccounts(rows)
}

func (s *Store) ListAccountsByAppID(ctx context.Context, q db.Q, appID string) ([]model.WxAccount, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT `+accountCols+` FROM wx_account WHERE app_id = ? ORDER BY id ASC`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAccounts(rows)
}

func (s *Store) ListActiveAccounts(ctx context.Context, q db.Q, userID int64) ([]model.WxAccount, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT `+accountCols+` FROM wx_account WHERE user_id = ? AND bind_status = 'active' ORDER BY id ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAccounts(rows)
}

func collectAccounts(rows *sql.Rows) ([]model.WxAccount, error) {
	var out []model.WxAccount
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (s *Store) CountActiveAccounts(ctx context.Context, q db.Q, userID int64) (int, error) {
	if q == nil {
		q = s.DB
	}
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM wx_account WHERE user_id = ? AND bind_status = 'active'`, userID).Scan(&n)
	return n, err
}

func (s *Store) InsertAccount(ctx context.Context, q db.Q, a *model.WxAccount) error {
	if q == nil {
		q = s.DB
	}
	return q.QueryRow(ctx, `INSERT INTO wx_account (user_id, name, app_id, secret_encrypted, subject_type, service_type, verified, delivery_mode, bind_status, permission_report, bound_at, unbound_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		a.UserID, a.Name, a.AppID, a.SecretEncrypted, a.SubjectType, a.ServiceType, bint(a.Verified), a.DeliveryMode, a.BindStatus, jsonStr(a.PermissionReport), timeArg(a.BoundAt), timeArg(a.UnboundAt),
	).Scan(&a.ID)
}

func (s *Store) UpdateAccount(ctx context.Context, q db.Q, a *model.WxAccount) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE wx_account SET name=?, secret_encrypted=?, verified=?, delivery_mode=?, bind_status=?, permission_report=?, bound_at=?, unbound_at=? WHERE id=?`,
		a.Name, a.SecretEncrypted, bint(a.Verified), a.DeliveryMode, a.BindStatus, jsonStr(a.PermissionReport), timeArg(a.BoundAt), timeArg(a.UnboundAt), a.ID)
	return err
}

func (s *Store) SaveAccountVoice(ctx context.Context, q db.Q, id int64, audience, tone, columns, signatures, taboos string) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE wx_account SET audience=?, tone=?, persona_columns=?, signatures=?, taboos=? WHERE id=?`,
		audience, tone, columns, signatures, taboos, id)
	return err
}

func (s *Store) LatestActiveAccount(ctx context.Context) (*model.WxAccount, error) {
	a, err := scanAccount(s.DB.QueryRow(ctx, `SELECT `+accountCols+` FROM wx_account WHERE bind_status = 'active' AND secret_encrypted != '' ORDER BY id DESC LIMIT 1`))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}
