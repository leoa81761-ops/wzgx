package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gonghao/internal/server"
)

func TestPasswordFormat(t *testing.T) {
	h, err := HashPassword("secret-pass")
	if err != nil {
		t.Fatal(err)
	}
	s, k, ok := strings.Cut(h, "$")
	salt, _ := base64.StdEncoding.DecodeString(s)
	key, _ := base64.StdEncoding.DecodeString(k)
	if !ok || len(salt) != 16 || len(key) != 32 {
		t.Fatalf("format mismatch with v1: %q", h)
	}
	if !VerifyPassword("secret-pass", h) || VerifyPassword("wrong", h) || VerifyPassword("x", "garbage") {
		t.Fatal("verify")
	}
}

func TestRoles(t *testing.T) {
	if !RoleUser.CanBindAccounts() || RoleOperator.CanBindAccounts() || RoleSuperAdmin.CanBindAccounts() {
		t.Fatal("only users bind accounts")
	}
	if RoleUser.IsStaff() || !RoleOperator.IsStaff() || !RoleSuperAdmin.IsStaff() {
		t.Fatal("staff")
	}
}

type env struct {
	t     *testing.T
	store *MemStore
	svc   *Service
	srv   *server.Server
}

func newEnv(t *testing.T) *env {
	st := NewMemStore()
	svc := NewService(st, time.Hour)
	srv := server.New(server.Options{})
	NewHandlers(svc, false).Register(srv)
	return &env{t, st, svc, srv}
}

func (e *env) do(method, path, cookie string, body any) (*httptest.ResponseRecorder, map[string]any) {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec, m
}

func (e *env) login(email, pw string) string {
	rec, m := e.do("POST", "/api/auth/login", "", map[string]string{"email": email, "password": pw})
	if rec.Code != 200 {
		e.t.Fatalf("login %s: %d %v", email, rec.Code, m)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
				e.t.Fatal("cookie flags")
			}
			return c.Value
		}
	}
	e.t.Fatal("no cookie")
	return ""
}

func TestRegisterLoginMeLogout(t *testing.T) {
	e := newEnv(t)
	reg := map[string]string{"email": "A@Example.com", "password": "password1", "terms_version": "1.0", "privacy_version": "1.0"}
	if rec, m := e.do("POST", "/api/auth/register", "", reg); rec.Code != 201 || m["role"] != "user" || m["email"] != "a@example.com" {
		t.Fatalf("register: %d %v", rec.Code, m)
	}
	if len(e.store.Consents) != 2 {
		t.Fatal("consent not recorded")
	}
	if rec, _ := e.do("POST", "/api/auth/register", "", reg); rec.Code != 409 {
		t.Fatal("duplicate email must 409")
	}
	noConsent := map[string]string{"email": "b@example.com", "password": "password1"}
	if rec, _ := e.do("POST", "/api/auth/register", "", noConsent); rec.Code != 400 {
		t.Fatal("registration without consent must fail")
	}
	// role can't be smuggled in
	sneaky := map[string]string{"email": "c@example.com", "password": "password1", "terms_version": "1", "privacy_version": "1", "role": "super_admin"}
	if _, m := e.do("POST", "/api/auth/register", "", sneaky); m["role"] != "user" {
		t.Fatal("role escalation through register")
	}

	tok := e.login("a@example.com", "password1")
	if rec, m := e.do("GET", "/api/auth/me", tok, nil); rec.Code != 200 || m["email"] != "a@example.com" || m["password_hash"] != nil {
		t.Fatalf("me: %d %v", rec.Code, m)
	}
	e.do("POST", "/api/auth/logout", tok, nil)
	if rec, _ := e.do("GET", "/api/auth/me", tok, nil); rec.Code != 401 {
		t.Fatal("session survives logout")
	}
	if rec, _ := e.do("GET", "/api/auth/me", "", nil); rec.Code != 401 {
		t.Fatal("anonymous me")
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	e.svc.Register(context.Background(), "a@example.com", "password1", "", Consent{"1", "1"}, "", "")
	for i := 0; i < 5; i++ {
		if rec, _ := e.do("POST", "/api/auth/login", "", map[string]string{"email": "a@example.com", "password": "bad"}); rec.Code != 401 {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	if rec, _ := e.do("POST", "/api/auth/login", "", map[string]string{"email": "a@example.com", "password": "password1"}); rec.Code != 429 {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
}

func TestSessionExpiry(t *testing.T) {
	e := newEnv(t)
	e.svc.Register(context.Background(), "a@example.com", "password1", "", Consent{"1", "1"}, "", "")
	tok := e.login("a@example.com", "password1")
	e.svc.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if rec, _ := e.do("GET", "/api/auth/me", tok, nil); rec.Code != 401 {
		t.Fatal("expired session accepted")
	}
}

func TestOperatorsBySuperAdminOnly(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if ok, err := e.svc.Bootstrap(ctx, "root@example.com", "rootpass1"); !ok || err != nil {
		t.Fatalf("bootstrap %v %v", ok, err)
	}
	if ok, _ := e.svc.Bootstrap(ctx, "root2@example.com", "rootpass1"); ok {
		t.Fatal("second bootstrap must be no-op")
	}
	e.svc.Register(ctx, "u@example.com", "password1", "", Consent{"1", "1"}, "", "")
	root := e.login("root@example.com", "rootpass1")
	user := e.login("u@example.com", "password1")

	op := map[string]string{"email": "op@example.com", "password": "oppass123", "name": "运营"}
	if rec, _ := e.do("POST", "/api/admin/operators", user, op); rec.Code != 403 {
		t.Fatal("user created operator")
	}
	rec, m := e.do("POST", "/api/admin/operators", root, op)
	if rec.Code != 201 || m["role"] != "operator" {
		t.Fatalf("create operator: %d %v", rec.Code, m)
	}
	opTok := e.login("op@example.com", "oppass123")
	if rec, _ := e.do("POST", "/api/admin/operators", opTok, map[string]string{"email": "op2@example.com", "password": "oppass123"}); rec.Code != 403 {
		t.Fatal("operator created operator")
	}
	if rec, _ := e.do("GET", "/api/admin/operators", root, nil); rec.Code != 200 {
		t.Fatal("list staff")
	}
	// disable operator: its session dies
	id := int64(m["id"].(float64))
	if rec, _ := e.do("POST", "/api/admin/operators/"+itoa(id)+"/status", root, map[string]bool{"active": false}); rec.Code != 200 {
		t.Fatal("disable operator")
	}
	if rec, _ := e.do("GET", "/api/auth/me", opTok, nil); rec.Code != 401 {
		t.Fatal("disabled operator still logged in")
	}
	// super admin can't be disabled through this endpoint
	if rec, _ := e.do("POST", "/api/admin/operators/1/status", root, map[string]bool{"active": false}); rec.Code != 403 {
		t.Fatal("super admin disabled")
	}
	if len(e.store.Audits) < 2 {
		t.Fatal("audit missing")
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestClientIPTrustedProxies(t *testing.T) {
	defer SetTrustedProxies(DefaultTrustedProxies)
	if err := SetTrustedProxies("10.0.0.0/8, 127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if SetTrustedProxies("nope") == nil {
		t.Fatal("bad CIDR accepted")
	}
	SetTrustedProxies("10.0.0.0/8, 127.0.0.1")
	cases := []struct{ remote, xff, want string }{
		{"203.0.113.5:1234", "1.2.3.4", "203.0.113.5"},               // not a proxy: header ignored
		{"127.0.0.1:1234", "198.51.100.7", "198.51.100.7"},           // trusted proxy
		{"127.0.0.1:1234", "6.6.6.6, 198.51.100.7", "198.51.100.7"},  // spoofed left hop ignored
		{"127.0.0.1:1234", "198.51.100.7, 10.1.2.3", "198.51.100.7"}, // chain of trusted proxies
		{"127.0.0.1:1234", "", "127.0.0.1"},                          // no header
		{"127.0.0.1:1234", "junk, 198.51.100.7", "198.51.100.7"},
		{"[::ffff:127.0.0.1]:1", "198.51.100.8", "198.51.100.8"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := ClientIP(r); got != c.want {
			t.Errorf("%s %q: got %s want %s", c.remote, c.xff, got, c.want)
		}
	}
}
