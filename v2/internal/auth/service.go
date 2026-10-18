package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Service struct {
	store Store
	ttl   time.Duration
	now   func() time.Time
	lim   *limiter
}

func NewService(store Store, ttl time.Duration) *Service {
	return &Service{store: store, ttl: ttl, now: time.Now, lim: newLimiter(5, 15*time.Minute)}
}

// Consent is the legal-document acceptance submitted with registration.
type Consent struct {
	TermsVersion   string
	PrivacyVersion string
}

func normEmail(e string) (string, error) {
	e = strings.TrimSpace(strings.ToLower(e))
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e {
		return "", errors.New("邮箱格式不正确")
	}
	return e, nil
}

func checkPassword(pw string) error {
	if utf8.RuneCountInString(pw) < 8 {
		return errors.New("密码至少 8 位")
	}
	if len(pw) > 256 {
		return errors.New("密码过长")
	}
	return nil
}

// Register creates a plain user. Registration must carry an explicit consent (spec §3.5.2).
func (s *Service) Register(ctx context.Context, email, password, name string, c Consent, ip, ua string) (User, error) {
	email, err := normEmail(email)
	if err != nil {
		return User{}, err
	}
	if err := checkPassword(password); err != nil {
		return User{}, err
	}
	if c.TermsVersion == "" || c.PrivacyVersion == "" {
		return User{}, errors.New("请阅读并勾选同意用户协议和隐私政策")
	}
	h, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	u, err := s.store.CreateUser(ctx, NewUser{Email: email, DisplayName: strings.TrimSpace(name), PasswordHash: h, Role: RoleUser})
	if err != nil {
		return User{}, err
	}
	_ = s.store.RecordConsent(ctx, u.ID, "terms", c.TermsVersion, ip, ua)
	_ = s.store.RecordConsent(ctx, u.ID, "privacy", c.PrivacyVersion, ip, ua)
	return u, nil
}

// Login verifies credentials and returns a new session token (the raw cookie value).
func (s *Service) Login(ctx context.Context, email, password, ip, ua string) (User, string, error) {
	email, err := normEmail(email)
	if err != nil {
		return User{}, "", ErrBadLogin
	}
	key := ip + "|" + email
	if !s.lim.allow(key, s.now()) {
		return User{}, "", ErrRateLimited
	}
	u, err := s.store.UserByEmail(ctx, email)
	if err != nil || !VerifyPassword(password, u.PasswordHash) {
		if errors.Is(err, ErrNotFound) {
			VerifyPassword(password, dummyHash) // equalise timing
		}
		s.lim.fail(key, s.now())
		return User{}, "", ErrBadLogin
	}
	if u.Status != "active" {
		return User{}, "", ErrDisabled
	}
	s.lim.reset(key)
	tok, hash, err := newToken()
	if err != nil {
		return User{}, "", err
	}
	if err := s.store.CreateSession(ctx, Session{TokenHash: hash, UserID: u.ID, ExpiresAt: s.now().Add(s.ttl), IP: ip, UserAgent: ua}); err != nil {
		return User{}, "", err
	}
	return u, tok, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	return s.store.DeleteSession(ctx, HashToken(token))
}

// Authenticate resolves a session token to an active user.
func (s *Service) Authenticate(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrNotFound
	}
	u, err := s.store.SessionUser(ctx, HashToken(token), s.now())
	if err != nil {
		return User{}, err
	}
	if u.Status != "active" {
		return User{}, ErrDisabled
	}
	return u, nil
}

// CreateOperator: only a super admin may add operators.
func (s *Service) CreateOperator(ctx context.Context, actor User, email, password, name string) (User, error) {
	if actor.Role != RoleSuperAdmin {
		return User{}, ErrForbidden
	}
	email, err := normEmail(email)
	if err != nil {
		return User{}, err
	}
	if err := checkPassword(password); err != nil {
		return User{}, err
	}
	h, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	id := actor.ID
	u, err := s.store.CreateUser(ctx, NewUser{Email: email, DisplayName: name, PasswordHash: h, Role: RoleOperator, CreatedBy: &id})
	if err != nil {
		return User{}, err
	}
	_ = s.store.Audit(ctx, &id, "staff.create", email, map[string]any{"role": RoleOperator})
	return u, nil
}

// SetOperatorStatus disables or re-enables an operator (super admin only). Super admins can't be disabled here.
func (s *Service) SetOperatorStatus(ctx context.Context, actor User, id int64, active bool) error {
	if actor.Role != RoleSuperAdmin {
		return ErrForbidden
	}
	u, err := s.store.UserByID(ctx, id)
	if err != nil {
		return err
	}
	if u.Role != RoleOperator {
		return ErrForbidden
	}
	st := "disabled"
	if active {
		st = "active"
	}
	if err := s.store.SetStatus(ctx, id, st); err != nil {
		return err
	}
	if !active {
		_ = s.store.DeleteUserSessions(ctx, id)
	}
	aid := actor.ID
	return s.store.Audit(ctx, &aid, "staff.status", u.Email, map[string]any{"status": st})
}

// Bootstrap creates the first super admin if none exists. No-op otherwise.
func (s *Service) Bootstrap(ctx context.Context, email, password string) (bool, error) {
	if email == "" {
		return false, nil
	}
	n, err := s.store.CountRole(ctx, RoleSuperAdmin)
	if err != nil || n > 0 {
		return false, err
	}
	email, err = normEmail(email)
	if err != nil {
		return false, err
	}
	if err := checkPassword(password); err != nil {
		return false, err
	}
	h, err := HashPassword(password)
	if err != nil {
		return false, err
	}
	if _, err := s.store.CreateUser(ctx, NewUser{Email: email, PasswordHash: h, Role: RoleSuperAdmin, DisplayName: "超级管理员"}); err != nil {
		return false, err
	}
	_ = s.store.Audit(ctx, nil, "staff.bootstrap", email, nil)
	return true, nil
}

func newToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	t := base64.RawURLEncoding.EncodeToString(b)
	return t, HashToken(t), nil
}

func HashToken(t string) []byte { h := sha256.Sum256([]byte(t)); return h[:] }

var dummyHash, _ = HashPassword("dummy-password-for-timing")

// limiter: at most max failures per key per window.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
}

func newLimiter(max int, w time.Duration) *limiter {
	return &limiter{max: max, window: w, fails: map[string][]time.Time{}}
}

func (l *limiter) prune(key string, now time.Time) []time.Time {
	var keep []time.Time
	for _, t := range l.fails[key] {
		if now.Sub(t) < l.window {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.fails, key)
	} else {
		l.fails[key] = keep
	}
	return keep
}

func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key, now)) < l.max
}

func (l *limiter) fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[key] = append(l.prune(key, now), now)
}

func (l *limiter) reset(key string) { l.mu.Lock(); delete(l.fails, key); l.mu.Unlock() }

// ChangePassword checks the current password, sets the new one and ends every session of the
// user (they log in again with the new password).
func (s *Service) ChangePassword(ctx context.Context, actor User, old, new string) error {
	u, err := s.store.UserByID(ctx, actor.ID)
	if err != nil {
		return err
	}
	if !VerifyPassword(old, u.PasswordHash) {
		return errors.New("当前密码不正确")
	}
	if err := checkPassword(new); err != nil {
		return err
	}
	h, err := HashPassword(new)
	if err != nil {
		return err
	}
	if err := s.store.SetPassword(ctx, u.ID, h); err != nil {
		return err
	}
	_ = s.store.DeleteUserSessions(ctx, u.ID)
	id := u.ID
	_ = s.store.Audit(ctx, &id, "auth.password", "", nil)
	return nil
}

// ConfirmPassword checks the user's current password (for sensitive actions like account deletion).
func (s *Service) ConfirmPassword(ctx context.Context, userID int64, password string) error {
	u, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !VerifyPassword(password, u.PasswordHash) {
		return errors.New("密码不正确")
	}
	return nil
}

// LogoutAll ends every session of the user.
func (s *Service) LogoutAll(ctx context.Context, userID int64) error {
	return s.store.DeleteUserSessions(ctx, userID)
}
