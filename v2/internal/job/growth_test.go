package job_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gonghao/internal/auth"
)

// from sends a request as a given client address (X-Forwarded-For) and returns the session cookie, if any.
func from(t *testing.T, e *env, ip, method, path, tok string, body any) (int, map[string]any, string) {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("X-Forwarded-For", ip)
	if tok != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	var m map[string]any
	json.Unmarshal(rec.Body.Bytes(), &m)
	cookie := ""
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName {
			cookie = c.Value
		}
	}
	return rec.Code, m, cookie
}

func TestGrowth(t *testing.T) {
	e, root, user, _, _ := setup(t)
	ctx := t.Context()
	var uid int64
	e.pool.QueryRow(ctx, `SELECT id FROM users WHERE email='u@example.com'`).Scan(&uid)

	// pricing: public, from the admin tier settings
	if code, _, b := do(t, e.srv, "PUT", "/api/admin/tiers/standard", root, map[string]any{"name": "标准版", "price_text": "¥99 / 月", "max_accounts": 2, "daily_quota": 3, "visible": true, "sort": 2}); code != 200 {
		t.Fatal(b)
	}
	if code, _, _ := do(t, e.srv, "PUT", "/api/admin/tiers/standard", user, map[string]any{"name": "x"}); code != 403 {
		t.Fatal("user edited a tier")
	}
	do(t, e.srv, "PUT", "/api/admin/settings", root, map[string]any{"auto_pause_after": 0, "contact": "微信 gonghao-admin", "invite_bonus": 2})
	code, p, b := do(t, e.srv, "GET", "/api/site/plans", "", nil)
	if code != 200 || p["contact"] != "微信 gonghao-admin" || !strings.Contains(b, "¥99 / 月") || strings.Contains(b, "买断版") {
		t.Fatalf("plans: %s", b)
	}

	// complaints: public, limited per address, admin list
	c := map[string]any{"kind": "infringe", "content": "这篇文章用了我的原创内容，没有授权", "link": "https://mp.weixin.qq.com/s/x", "contact": "a@b.c"}
	if code, _, _ := from(t, e, "198.51.100.9", "POST", "/api/complaints", "", map[string]any{"kind": "infringe", "content": "短"}); code != 400 {
		t.Fatal("short complaint accepted")
	}
	for i := 0; i < 3; i++ {
		if code, _, _ := from(t, e, "198.51.100.9", "POST", "/api/complaints", "", c); code != 201 {
			t.Fatalf("complaint %d: %d", i, code)
		}
	}
	if code, _, _ := from(t, e, "198.51.100.9", "POST", "/api/complaints", "", c); code != 429 {
		t.Fatal("no rate limit")
	}
	if code, _, _ := from(t, e, "198.51.100.10", "POST", "/api/complaints", "", c); code != 201 {
		t.Fatal("other address blocked")
	}
	var cl []map[string]any
	getList(t, e, "/api/admin/complaints", root, &cl)
	if len(cl) != 4 {
		t.Fatalf("complaints: %d", len(cl))
	}
	if code, _, b := do(t, e.srv, "PUT", fmt.Sprintf("/api/admin/complaints/%v", cl[0]["id"]), root, map[string]any{"status": "handled", "note": "已下架"}); code != 200 {
		t.Fatal(b)
	}

	// invites: code, a valid one, a repeat from the same device, a self-invite from the inviter's address
	_, inv, _ := do(t, e.srv, "GET", "/api/me/invite", user, nil)
	codeStr, _ := inv["code"].(string)
	if len(codeStr) != 8 || inv["bonus_per_invite"] != float64(2) {
		t.Fatalf("invite: %v", inv)
	}
	e.pool.Exec(ctx, `UPDATE users SET reg_ip='203.0.113.1' WHERE id=$1`, uid)
	reg := func(ip, email, dev string) int {
		code, _, _ := from(t, e, ip, "POST", "/api/auth/register", "", map[string]any{"email": email, "password": "password1", "terms_version": "1", "privacy_version": "1", "invite": strings.ToLower(codeStr), "device_id": dev})
		return code
	}
	if reg("198.51.100.20", "b@example.com", "dev-b") != 201 || reg("198.51.100.21", "c@example.com", "dev-b") != 201 || reg("203.0.113.1", "d@example.com", "dev-d") != 201 {
		t.Fatal("register")
	}
	statuses := func() map[string]string {
		out := map[string]string{}
		rows, _ := e.pool.Query(ctx, `SELECT u.email, i.status FROM invite i JOIN users u ON u.id=i.invitee_id`)
		defer rows.Close()
		for rows.Next() {
			var a, s string
			rows.Scan(&a, &s)
			out[a] = s
		}
		return out
	}
	if s := statuses(); s["b@example.com"] != "pending" || s["c@example.com"] != "rejected" || s["d@example.com"] != "rejected" {
		t.Fatalf("invite statuses: %v", s)
	}
	// b binds an account and gets an article -> valid, inviter +2
	_, _, bt := from(t, e, "198.51.100.20", "POST", "/api/auth/login", "", map[string]any{"email": "b@example.com", "password": "password1"})
	if code, _, b := do(t, e.srv, "POST", "/api/accounts", bt, map[string]any{"name": "B的号", "app_id": "wx1123456789abcdef", "app_secret": "1123456789abcdef0123456789abcdef"}); code != 201 {
		t.Fatal(b)
	}
	e.jobs.GrowthTick(ctx)
	if statuses()["b@example.com"] != "pending" {
		t.Fatal("valid before any article")
	}
	e.pool.Exec(ctx, `INSERT INTO article (user_id, account_id, title, status) SELECT u.id, a.id, '一篇', 'ready' FROM users u JOIN wx_account a ON a.user_id=u.id WHERE u.email='b@example.com'`)
	if err := e.jobs.GrowthTick(ctx); err != nil {
		t.Fatal(err)
	}
	var bonus int
	e.pool.QueryRow(ctx, `SELECT bonus_articles FROM users WHERE id=$1`, uid).Scan(&bonus)
	if statuses()["b@example.com"] != "valid" || bonus != 2 {
		t.Fatalf("reward: %v bonus %d", statuses(), bonus)
	}
	// bonus articles are used once the daily quota is spent
	e.pool.Exec(ctx, `UPDATE users SET daily_quota=0, quota_exempt=FALSE WHERE id=$1`, uid)
	var accID int64
	e.pool.QueryRow(ctx, `SELECT id FROM wx_account WHERE user_id=$1`, uid).Scan(&accID)
	if code, _, b := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/generate", accID), user, map[string]any{}); code != 202 {
		t.Fatalf("bonus article: %s", b)
	}
	e.pool.QueryRow(ctx, `SELECT bonus_articles FROM users WHERE id=$1`, uid).Scan(&bonus)
	var used bool
	e.pool.QueryRow(ctx, `SELECT used_bonus FROM job ORDER BY id DESC LIMIT 1`).Scan(&used)
	if bonus != 1 || !used {
		t.Fatalf("bonus left %d used %v", bonus, used)
	}
	// cancelled (or failed) -> the bonus article comes back, once
	var jid int64
	e.pool.QueryRow(ctx, `SELECT id FROM job ORDER BY id DESC LIMIT 1`).Scan(&jid)
	if code, _, b := do(t, e.srv, "POST", fmt.Sprintf("/api/admin/jobs/%d/cancel", jid), root, nil); code != 200 {
		t.Fatal(b)
	}
	e.jobs.GrowthTick(ctx)
	e.jobs.GrowthTick(ctx)
	e.pool.QueryRow(ctx, `SELECT bonus_articles FROM users WHERE id=$1`, uid).Scan(&bonus)
	if bonus != 2 {
		t.Fatalf("bonus not refunded once: %d", bonus)
	}

	// export: everything of the user, no secrets
	code, ex, raw := do(t, e.srv, "GET", "/api/me/export", user, nil)
	if code != 200 || len(ex["accounts"].([]any)) != 1 || ex["user"].(map[string]any)["email"] != "u@example.com" {
		t.Fatalf("export: %d %.300s", code, raw)
	}
	if strings.Contains(raw, "0123456789abcdef0123456789abcdef") || strings.Contains(raw, "cipher") || strings.Contains(raw, "tvly-user-key") || strings.Contains(raw, "password") {
		t.Fatal("export leaks a secret")
	}

	// deletion: password, logged out, login cancels, purge after the grace period
	if code, _, _ := do(t, e.srv, "POST", "/api/me/delete", bt, map[string]any{"password": "wrong-pass1"}); code != 400 {
		t.Fatal("deleted with a wrong password")
	}
	if code, _, _ := do(t, e.srv, "POST", "/api/me/delete", bt, map[string]any{"password": "password1"}); code != 200 {
		t.Fatal("delete request")
	}
	if code, _, _ := do(t, e.srv, "GET", "/api/me/today", bt, nil); code != 401 {
		t.Fatal("still logged in after delete request")
	}
	_, lm, bt2 := from(t, e, "198.51.100.20", "POST", "/api/auth/login", "", map[string]any{"email": "b@example.com", "password": "password1"})
	if lm["deletion_cancelled"] != true {
		t.Fatalf("login didn't cancel: %v", lm)
	}
	do(t, e.srv, "POST", "/api/me/delete", bt2, map[string]any{"password": "password1"})
	e.jobs.PurgeDeleted(ctx) // still in the grace period
	var n int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM article a JOIN users u ON u.id=a.user_id WHERE u.email='b@example.com'`).Scan(&n)
	if n != 1 {
		t.Fatal("purged too early")
	}
	e.pool.Exec(ctx, `UPDATE users SET delete_requested_at=now() - interval '8 days' WHERE email='b@example.com'`)
	if err := e.jobs.PurgeDeleted(ctx); err != nil {
		t.Fatal(err)
	}
	var left int
	e.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM users WHERE email='b@example.com') + (SELECT count(*) FROM wx_account WHERE app_id='wx1123456789abcdef')`).Scan(&left)
	if left != 0 {
		t.Fatal("data left after purge")
	}
	if code, _, _ := from(t, e, "198.51.100.20", "POST", "/api/auth/login", "", map[string]any{"email": "b@example.com", "password": "password1"}); code != 401 {
		t.Fatal("purged user can log in")
	}
	var users []map[string]any
	getList(t, e, "/api/admin/users", root, &users)
	for _, u := range users {
		if strings.HasPrefix(u["email"].(string), "deleted-") {
			t.Fatal("purged user listed")
		}
	}
}
