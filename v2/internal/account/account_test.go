package account_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"gonghao/internal/account"
	"gonghao/internal/audit"
	"gonghao/internal/auth"
	"gonghao/internal/crypto"
	"gonghao/internal/server"
	"gonghao/internal/testdb"
)

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	srv  *server.Server
	acc  *account.Service
	auth *auth.Service
}

func newEnv(t *testing.T) *env {
	pool := testdb.Open(t)
	box, _ := crypto.NewBox("0123456789abcdef0123456789abcdef")
	as := auth.NewService(auth.NewPGStore(pool), time.Hour)
	h := auth.NewHandlers(as, false)
	srv := server.New(server.Options{})
	h.Register(srv)
	acc := account.New(pool, box, audit.PG{DB: pool})
	acc.Register(srv, h)
	return &env{t, pool, srv, acc, as}
}

func (e *env) user(email string) string {
	ctx := context.Background()
	if _, err := e.auth.Register(ctx, email, "password1", "", auth.Consent{TermsVersion: "1", PrivacyVersion: "1"}, "", ""); err != nil {
		e.t.Fatal(err)
	}
	_, tok, err := e.auth.Login(ctx, email, "password1", "", "")
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

func (e *env) staff() string {
	ctx := context.Background()
	e.auth.Bootstrap(ctx, "root@example.com", "rootpass1")
	_, tok, err := e.auth.Login(ctx, "root@example.com", "rootpass1", "", "")
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

func (e *env) do(method, path, tok string, body any) (int, map[string]any, string) {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	if tok != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	var m map[string]any
	json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m, rec.Body.String()
}

func bind(app string) map[string]any {
	return map[string]any{"name": "测试号", "app_id": app, "app_secret": "0123456789abcdef0123456789abcdef"}
}

const app1, app2 = "wx0123456789abcdef", "wx00000000000000aa"

func TestBindRules(t *testing.T) {
	e := newEnv(t)
	u1, u2, root := e.user("u1@example.com"), e.user("u2@example.com"), e.staff()

	code, m, body := e.do("POST", "/api/accounts", u1, bind(app1))
	if code != 201 || m["secret_mask"] != "012****cdef" {
		t.Fatalf("bind: %d %s", code, body)
	}
	if strings.Contains(body, "0123456789abcdef0123456789abcdef") || strings.Contains(body, "cipher") {
		t.Fatal("secret leaked in response")
	}
	var cipher string
	e.pool.QueryRow(context.Background(), `SELECT secret_cipher FROM wx_account WHERE app_id=$1`, app1).Scan(&cipher)
	if !strings.HasPrefix(cipher, "v1:") || strings.Contains(cipher, "0123456789abcdef") {
		t.Fatal("secret not encrypted at rest")
	}
	// allowance = 1
	if code, _, _ := e.do("POST", "/api/accounts", u1, bind(app2)); code != 409 {
		t.Fatalf("allowance not enforced: %d", code)
	}
	// same app by another user
	if code, _, _ := e.do("POST", "/api/accounts", u2, bind(app1)); code != 409 {
		t.Fatalf("app taken: %d", code)
	}
	// staff can't bind, even via the user route
	if code, _, _ := e.do("POST", "/api/accounts", root, bind(app2)); code != 403 {
		t.Fatalf("staff bind: %d", code)
	}
	// bad input
	if code, _, _ := e.do("POST", "/api/accounts", u2, map[string]any{"name": "x", "app_id": "nope", "app_secret": "short"}); code != 400 {
		t.Fatalf("validation: %d", code)
	}
	// staff raises allowance -> second bind works
	var u1id int64
	e.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE email='u1@example.com'`).Scan(&u1id)
	if code, _, b := e.do("PUT", fmt.Sprintf("/api/admin/users/%d/allowance", u1id), root, map[string]any{"max_accounts": 5, "tier": "premium"}); code != 200 {
		t.Fatalf("allowance: %d %s", code, b)
	}
	if code, _, _ := e.do("PUT", fmt.Sprintf("/api/admin/users/%d/allowance", u1id), u1, map[string]any{"max_accounts": 50, "tier": "premium"}); code != 403 {
		t.Fatal("user raised own allowance")
	}
	if code, _, _ := e.do("POST", "/api/accounts", u1, bind(app2)); code != 201 {
		t.Fatal("premium second bind")
	}
	// unbind -> other user blocked for 30 days
	id := int64(m["id"].(float64))
	if code, _, _ := e.do("DELETE", fmt.Sprintf("/api/accounts/%d", id), u2, nil); code != 404 {
		t.Fatal("other user unbound my account")
	}
	if code, _, _ := e.do("DELETE", fmt.Sprintf("/api/accounts/%d", id), u1, nil); code != 200 {
		t.Fatal("unbind")
	}
	if code, m, _ := e.do("POST", "/api/accounts", u2, bind(app1)); code != 409 || !strings.Contains(m["error"].(string), "30") {
		t.Fatalf("cooldown: %d %v", code, m)
	}
	e.acc.Now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	if code, _, _ := e.do("POST", "/api/accounts", u2, bind(app1)); code != 201 {
		t.Fatal("bind after cooldown")
	}
}

func TestProfileColumnsTypesAndAdminEdit(t *testing.T) {
	e := newEnv(t)
	u1, u2, root := e.user("u1@example.com"), e.user("u2@example.com"), e.staff()
	_, m, _ := e.do("POST", "/api/accounts", u1, bind(app1))
	acc := fmt.Sprintf("%d", int64(m["id"].(float64)))

	// article types: staff only
	if code, _, _ := e.do("POST", "/api/admin/article-types", u1, map[string]any{"key": "howto", "name": "方案拆解"}); code != 403 {
		t.Fatal("user created type")
	}
	_, ty, b := e.do("POST", "/api/admin/article-types", root, map[string]any{"key": "howto", "name": "方案拆解", "description": "把一个方案拆成步骤", "enabled": true})
	if ty["id"] == nil {
		t.Fatal(b)
	}
	_, off, _ := e.do("POST", "/api/admin/article-types", root, map[string]any{"key": "news", "name": "快报", "enabled": false})
	if code, _, _ := e.do("POST", "/api/admin/article-types", root, map[string]any{"key": "howto", "name": "重复"}); code != 400 {
		t.Fatal("duplicate type key")
	}
	_, _, list := e.do("GET", "/api/article-types", u1, nil)
	if strings.Contains(list, "快报") || !strings.Contains(list, "方案拆解") {
		t.Fatalf("users must see only enabled types: %s", list)
	}

	// profile: owner saves, other user can't read, staff edits
	prof := map[string]any{"persona": "我是奶奶，第一人称", "signature": "先看现场，再谈方案", "ai_label": false}
	if code, p, _ := e.do("PUT", "/api/accounts/"+acc+"/profile", u1, prof); code != 200 || p["persona"] != "我是奶奶，第一人称" {
		t.Fatal("save profile")
	}
	if code, _, _ := e.do("GET", "/api/accounts/"+acc+"/profile", u2, nil); code != 404 {
		t.Fatal("other user read profile")
	}
	if code, _, _ := e.do("GET", "/api/accounts/"+acc+"/profile", root, nil); code != 403 {
		t.Fatal("staff must use admin routes")
	}
	prof["tone"] = "平实"
	if code, p, _ := e.do("PUT", "/api/admin/accounts/"+acc+"/profile", root, prof); code != 200 || p["tone"] != "平实" {
		t.Fatal("staff edit profile")
	}
	if code, a, _ := e.do("PATCH", "/api/admin/accounts/"+acc, root, map[string]any{"verified": true, "subject_type": "enterprise"}); code != 200 || a["verified"] != true {
		t.Fatal("staff edit account")
	}
	if code, _, _ := e.do("GET", "/api/admin/accounts", u1, nil); code != 403 {
		t.Fatal("user listed all accounts")
	}

	// columns: each picks exactly one enabled type
	col := map[string]any{"name": "方案拆解", "article_type_id": ty["id"], "enabled": true}
	code, c, b := e.do("POST", "/api/accounts/"+acc+"/columns", u1, col)
	if code != 201 || c["article_type_id"] != ty["id"] || c["length_hint"] != nil {
		t.Fatalf("create column: %d %s", code, b)
	}
	if code, _, _ := e.do("POST", "/api/accounts/"+acc+"/columns", u1, map[string]any{"name": "快报", "article_type_id": off["id"]}); code != 400 {
		t.Fatal("disabled type accepted")
	}
	if code, _, _ := e.do("POST", "/api/accounts/"+acc+"/columns", u1, map[string]any{"name": "x", "article_type_id": 9999}); code != 400 {
		t.Fatal("unknown type accepted")
	}
	if code, _, _ := e.do("POST", "/api/accounts/"+acc+"/columns", u2, col); code != 404 {
		t.Fatal("column on someone else's account")
	}
	cid := fmt.Sprintf("%d", int64(c["id"].(float64)))
	col["note"] = "只讲弱电"
	col["length_hint"] = 1500
	if code, c2, _ := e.do("PUT", "/api/admin/accounts/"+acc+"/columns/"+cid, root, col); code != 200 || c2["note"] != "只讲弱电" {
		t.Fatal("staff edit column")
	}
	if code, _, l := e.do("GET", "/api/accounts/"+acc+"/columns", u1, nil); code != 200 || !strings.Contains(l, "只讲弱电") {
		t.Fatal("list columns")
	}
	if code, _, _ := e.do("DELETE", "/api/accounts/"+acc+"/columns/"+cid, u1, nil); code != 200 {
		t.Fatal("delete column")
	}

	// audit trail covers all of it, and never holds the secret
	var n int
	e.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action IN
		('account.bind','article_type.save','account.profile','account.update','column.save','column.delete')`).Scan(&n)
	if n < 8 {
		t.Fatalf("audit rows=%d", n)
	}
	var leak int
	e.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE detail::text LIKE '%0123456789abcdef0123%'`).Scan(&leak)
	if leak != 0 {
		t.Fatal("secret in audit log")
	}
}
