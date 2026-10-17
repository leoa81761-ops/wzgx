// Package account: Official Account binding, per-account writing settings, article types and columns.
//
// Only role=user binds accounts, up to the user's max_accounts. Staff (operator, super_admin)
// never bind but can view and edit any account's parameters. Every change is audited.
package account

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"gonghao/internal/audit"
	"gonghao/internal/auth"
	"gonghao/internal/crypto"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrForbidden    = errors.New("没有权限")
	ErrStaffNoBind  = errors.New("管理员账号不能绑定公众号")
	ErrAllowance    = errors.New("已达到可绑定公众号数量上限")
	ErrAppTaken     = errors.New("该公众号已被其他用户绑定")
	ErrCooldown     = errors.New("该公众号解绑未满 30 天，暂不能由其他用户绑定")
	ErrInvalidInput = errors.New("参数不正确")
)

// UnbindCooldown: after unbinding, another user can't bind the same AppID for this long (spec §6.2).
const UnbindCooldown = 30 * 24 * time.Hour

type Sealer interface {
	Seal(string) (string, error)
}

type Service struct {
	DB    *pgxpool.Pool
	Box   Sealer
	Audit audit.Logger
	Now   func() time.Time
}

func New(db *pgxpool.Pool, box Sealer, a audit.Logger) *Service {
	return &Service{DB: db, Box: box, Audit: a, Now: time.Now}
}

type Account struct {
	ID           int64           `json:"id"`
	UserID       int64           `json:"user_id"`
	OwnerEmail   string          `json:"owner_email"`
	Paused       bool            `json:"paused"`
	PausedReason string          `json:"paused_reason"` // user | auto
	ConfirmTopic bool            `json:"confirm_topic"` // topic confirmation mode
	Name         string          `json:"name"`
	AppID        string          `json:"app_id"`
	SecretMask   string          `json:"secret_mask"`
	SubjectType  string          `json:"subject_type"`
	Verified     bool            `json:"verified"`
	BindStatus   string          `json:"bind_status"`
	CheckedAt    *time.Time      `json:"checked_at"`
	CheckReport  json.RawMessage `json:"check_report"` // last bind check: ok, errcode, message, hint
	CreatedAt    time.Time       `json:"created_at"`
}

type BindInput struct {
	Name        string `json:"name"`
	AppID       string `json:"app_id"`
	AppSecret   string `json:"app_secret"`
	SubjectType string `json:"subject_type"`
	Verified    bool   `json:"verified"`
}

var appIDRe = regexp.MustCompile(`^wx[0-9a-f]{16}$`)

// WeChat AppSecrets are 32 letters and digits.
var appSecretRe = regexp.MustCompile(`^[0-9A-Za-z]{32}$`)

const (
	msgAppID     = "AppID 格式不对：应为 wx 开头、共 18 位（wx 加 16 位数字和小写字母），在公众号后台「设置与开发 → 基本配置」里复制"
	msgAppSecret = "AppSecret 格式不对：应为 32 位字母和数字，在公众号后台「设置与开发 → 基本配置」里复制（不要带空格）"
)

func (in *BindInput) normalize() error {
	in.Name = strings.TrimSpace(in.Name)
	in.AppID = strings.TrimSpace(in.AppID)
	in.AppSecret = strings.TrimSpace(in.AppSecret)
	if in.SubjectType == "" {
		in.SubjectType = "personal"
	}
	if in.Name == "" || len([]rune(in.Name)) > 64 {
		return errors.New("请填写公众号名称（64 字以内）")
	}
	if !appIDRe.MatchString(in.AppID) {
		return errors.New(msgAppID)
	}
	if !appSecretRe.MatchString(in.AppSecret) {
		return errors.New(msgAppSecret)
	}
	if in.SubjectType != "personal" && in.SubjectType != "enterprise" {
		return errors.New("主体类型不正确")
	}
	return nil
}

const accCols = `id, user_id, (SELECT u.email FROM users u WHERE u.id = wx_account.user_id), name, app_id, secret_mask, subject_type, verified, bind_status, checked_at, check_report, created_at, paused, paused_reason, confirm_topic`

func scanAcc(r pgx.Row) (Account, error) {
	var a Account
	err := r.Scan(&a.ID, &a.UserID, &a.OwnerEmail, &a.Name, &a.AppID, &a.SecretMask, &a.SubjectType, &a.Verified, &a.BindStatus, &a.CheckedAt, &a.CheckReport, &a.CreatedAt, &a.Paused, &a.PausedReason, &a.ConfirmTopic)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// Bind adds an Official Account for a user. The allowance check and insert run in one transaction
// with the user row locked, so two parallel binds can't exceed max_accounts.
func (s *Service) Bind(ctx context.Context, actor auth.User, in BindInput) (Account, error) {
	if !actor.Role.CanBindAccounts() {
		return Account{}, ErrStaffNoBind
	}
	if err := in.normalize(); err != nil {
		return Account{}, err
	}
	sealed, err := s.Box.Seal(in.AppSecret)
	if err != nil {
		return Account{}, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback(ctx)

	var max int
	if err := tx.QueryRow(ctx, `SELECT max_accounts FROM users WHERE id=$1 FOR UPDATE`, actor.ID).Scan(&max); err != nil {
		return Account{}, err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM wx_account WHERE user_id=$1 AND bind_status='bound'`, actor.ID).Scan(&n); err != nil {
		return Account{}, err
	}
	if n >= max {
		return Account{}, ErrAllowance
	}
	var other int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM wx_account WHERE app_id=$1 AND user_id<>$2 AND bind_status='unbound' AND unbound_at > $3`,
		in.AppID, actor.ID, s.Now().Add(-UnbindCooldown)).Scan(&other); err != nil {
		return Account{}, err
	}
	if other > 0 {
		return Account{}, ErrCooldown
	}
	a, err := scanAcc(tx.QueryRow(ctx, `INSERT INTO wx_account (user_id, name, app_id, secret_cipher, secret_mask, subject_type, verified)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+accCols,
		actor.ID, in.Name, in.AppID, sealed, crypto.Mask(in.AppSecret), in.SubjectType, in.Verified))
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return Account{}, ErrAppTaken
	}
	if err != nil {
		return Account{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO account_profile (account_id) VALUES ($1)`, a.ID); err != nil {
		return Account{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Account{}, err
	}
	s.Audit.Log(ctx, actor.ID, "account.bind", in.AppID, map[string]any{"account_id": a.ID})
	return a, nil
}

// Get returns an account the actor may see: own accounts for users, any for staff.
func (s *Service) Get(ctx context.Context, actor auth.User, id int64) (Account, error) {
	a, err := scanAcc(s.DB.QueryRow(ctx, `SELECT `+accCols+` FROM wx_account WHERE id=$1`, id))
	if err != nil {
		return a, err
	}
	if !actor.Role.IsStaff() && a.UserID != actor.ID {
		return Account{}, ErrNotFound // don't reveal other users' accounts
	}
	return a, nil
}

// List: a user's own bound accounts; staff see all (optionally filtered by userID).
func (s *Service) List(ctx context.Context, actor auth.User, userID int64) ([]Account, error) {
	var rows pgx.Rows
	var err error
	switch {
	case !actor.Role.IsStaff():
		rows, err = s.DB.Query(ctx, `SELECT `+accCols+` FROM wx_account WHERE user_id=$1 AND bind_status='bound' ORDER BY id`, actor.ID)
	case userID > 0:
		rows, err = s.DB.Query(ctx, `SELECT `+accCols+` FROM wx_account WHERE user_id=$1 ORDER BY id`, userID)
	default:
		rows, err = s.DB.Query(ctx, `SELECT `+accCols+` FROM wx_account ORDER BY id`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		a, err := scanAcc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type AccountPatch struct {
	Name        *string `json:"name"`
	AppSecret   *string `json:"app_secret"` // write-only; never returned
	SubjectType *string `json:"subject_type"`
	Verified    *bool   `json:"verified"`
}

func (s *Service) Update(ctx context.Context, actor auth.User, id int64, p AccountPatch) (Account, error) {
	a, err := s.Get(ctx, actor, id)
	if err != nil {
		return a, err
	}
	changed := []string{}
	if p.Name != nil {
		n := strings.TrimSpace(*p.Name)
		if n == "" || len([]rune(n)) > 64 {
			return a, ErrInvalidInput
		}
		a.Name = n
		changed = append(changed, "name")
	}
	if p.SubjectType != nil {
		if *p.SubjectType != "personal" && *p.SubjectType != "enterprise" {
			return a, ErrInvalidInput
		}
		a.SubjectType = *p.SubjectType
		changed = append(changed, "subject_type")
	}
	if p.Verified != nil {
		a.Verified = *p.Verified
		changed = append(changed, "verified")
	}
	if _, err := s.DB.Exec(ctx, `UPDATE wx_account SET name=$2, subject_type=$3, verified=$4, updated_at=now() WHERE id=$1`,
		id, a.Name, a.SubjectType, a.Verified); err != nil {
		return a, err
	}
	if p.AppSecret != nil {
		sec := strings.TrimSpace(*p.AppSecret)
		if !appSecretRe.MatchString(sec) {
			return a, errors.New(msgAppSecret)
		}
		sealed, err := s.Box.Seal(sec)
		if err != nil {
			return a, err
		}
		a.SecretMask = crypto.Mask(sec)
		if _, err := s.DB.Exec(ctx, `UPDATE wx_account SET secret_cipher=$2, secret_mask=$3, updated_at=now() WHERE id=$1`, id, sealed, a.SecretMask); err != nil {
			return a, err
		}
		changed = append(changed, "app_secret")
	}
	s.Audit.Log(ctx, actor.ID, "account.update", a.AppID, map[string]any{"account_id": id, "fields": changed})
	return a, nil
}

func (s *Service) Unbind(ctx context.Context, actor auth.User, id int64) error {
	a, err := s.Get(ctx, actor, id)
	if err != nil {
		return err
	}
	if _, err := s.DB.Exec(ctx, `UPDATE wx_account SET bind_status='unbound', unbound_at=$2, updated_at=now() WHERE id=$1 AND bind_status='bound'`, id, s.Now()); err != nil {
		return err
	}
	s.Audit.Log(ctx, actor.ID, "account.unbind", a.AppID, map[string]any{"account_id": id})
	return nil
}

// SetAllowance (staff): how many accounts a user may bind, plus tier and quota exemption.
func (s *Service) SetAllowance(ctx context.Context, actor auth.User, userID int64, maxAccounts int, tier string, exempt bool) error {
	if !actor.Role.IsStaff() {
		return ErrForbidden
	}
	if maxAccounts < 0 || maxAccounts > 100 || strings.TrimSpace(tier) == "" {
		return ErrInvalidInput
	}
	t, err := s.DB.Exec(ctx, `UPDATE users SET max_accounts=$2, tier=$3, quota_exempt=$4, updated_at=now() WHERE id=$1 AND role='user'`,
		userID, maxAccounts, tier, exempt)
	if err != nil {
		return err
	}
	if t.RowsAffected() == 0 {
		return ErrNotFound
	}
	s.Audit.Log(ctx, actor.ID, "user.allowance", "", map[string]any{"user_id": userID, "max_accounts": maxAccounts, "tier": tier, "quota_exempt": exempt})
	return nil
}
