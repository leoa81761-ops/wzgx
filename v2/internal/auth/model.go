// Package auth: email+password accounts, cookie sessions, roles.
// Roles: user (binds Official Accounts), operator and super_admin (admin console only).
package auth

import (
	"context"
	"errors"
	"time"
)

type Role string

const (
	RoleUser       Role = "user"
	RoleOperator   Role = "operator"
	RoleSuperAdmin Role = "super_admin"
)

func (r Role) Valid() bool { return r == RoleUser || r == RoleOperator || r == RoleSuperAdmin }

// IsStaff: operators and super admins use only the admin console.
func (r Role) IsStaff() bool { return r == RoleOperator || r == RoleSuperAdmin }

// CanBindAccounts: only plain users bind Official Accounts. Admins never do.
func (r Role) CanBindAccounts() bool { return r == RoleUser }

type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	DisplayName  string    `json:"display_name"`
	Role         Role      `json:"role"`
	Tier         string    `json:"tier"`
	MaxAccounts  int       `json:"max_accounts"`
	QuotaExempt  bool      `json:"quota_exempt"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	PasswordHash string    `json:"-"`
}

type NewUser struct {
	Email        string
	DisplayName  string
	PasswordHash string
	Role         Role
	CreatedBy    *int64
}

type Session struct {
	TokenHash []byte
	UserID    int64
	ExpiresAt time.Time
	IP        string
	UserAgent string
}

var (
	ErrNotFound    = errors.New("not found")
	ErrEmailTaken  = errors.New("email already registered")
	ErrBadLogin    = errors.New("邮箱或密码不正确")
	ErrForbidden   = errors.New("forbidden")
	ErrDisabled    = errors.New("账号已停用")
	ErrRateLimited = errors.New("尝试次数过多，请稍后再试")
)

// Store is the persistence the auth service needs. PGStore implements it.
type Store interface {
	CreateUser(ctx context.Context, u NewUser) (User, error)
	UserByEmail(ctx context.Context, email string) (User, error)
	UserByID(ctx context.Context, id int64) (User, error)
	ListStaff(ctx context.Context) ([]User, error)
	SetStatus(ctx context.Context, id int64, status string) error
	CountRole(ctx context.Context, r Role) (int, error)
	CreateSession(ctx context.Context, s Session) error
	SessionUser(ctx context.Context, tokenHash []byte, now time.Time) (User, error)
	DeleteSession(ctx context.Context, tokenHash []byte) error
	DeleteUserSessions(ctx context.Context, userID int64) error
	RecordConsent(ctx context.Context, userID int64, docType, version, ip, device string) error
	Audit(ctx context.Context, actorID *int64, action, target string, detail map[string]any) error
}
