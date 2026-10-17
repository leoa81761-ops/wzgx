package svccfg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"gonghao/internal/audit"
	"gonghao/internal/auth"
	"gonghao/internal/crypto"
	"gonghao/internal/gateway"
	"gonghao/internal/server"
	"gonghao/internal/svccfg"
	"gonghao/internal/testdb"
)

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	srv  *server.Server
	auth *auth.Service
}

func newEnv(t *testing.T) *env {
	pool := testdb.Open(t)
	box, _ := crypto.NewBox("0123456789abcdef0123456789abcdef")
	as := auth.NewService(auth.NewPGStore(pool), time.Hour)
	h := auth.NewHandlers(as, false)
	srv := server.New(server.Options{})
	h.Register(srv)
	gw := gateway.New(gateway.PGRecorder{DB: pool})
	gw.Sleep = func(context.Context, time.Duration) error { return nil }
	gw.MinCall = time.Millisecond
	(&svccfg.Service{DB: pool, Box: box, Audit: audit.PG{DB: pool}, GW: gw}).Register(srv, h)
	return &env{t, pool, srv, as}
}

func (e *env) login(email, pw string) string {
	_, tok, err := e.auth.Login(context.Background(), email, pw, "", "")
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
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	var m map[string]any
	json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m, rec.Body.String()
}

const secretKey = "sk-live-0123456789SECRETabcd"

func TestConfigCenter(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.auth.Bootstrap(ctx, "root@example.com", "rootpass1")
	root := e.login("root@example.com", "rootpass1")
	rootU, _ := e.auth.Authenticate(ctx, root)
	e.auth.CreateOperator(ctx, rootU, "op@example.com", "oppass123", "")
	op := e.login("op@example.com", "oppass123")
	e.auth.Register(ctx, "u@example.com", "password1", "", auth.Consent{TermsVersion: "1", PrivacyVersion: "1"}, "", "")
	user := e.login("u@example.com", "password1")

	var calls int32
	var lastBody map[string]any
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		json.NewDecoder(r.Body).Decode(&lastBody)
		if r.Header.Get("Authorization") != "Bearer "+secretKey {
			http.Error(w, "bad key", 401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 { // first reply empty: test must retry like generation does
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"model\":\"reported-model\",\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer up.Close()

	in := map[string]any{"kind": "text", "name": "写作主力", "base_url": up.URL + "/v1", "api_key": secretKey,
		"model": "configured-model", "reasoning_effort": "high", "idle_timeout_s": 240, "enabled": true}
	if code, _, _ := e.do("POST", "/api/admin/configs", op, in); code != 403 {
		t.Fatal("operator created config")
	}
	if code, _, _ := e.do("GET", "/api/admin/configs", user, nil); code != 403 {
		t.Fatal("user read configs")
	}
	code, c, body := e.do("POST", "/api/admin/configs", root, in)
	if code != 201 || c["key_mask"] != "sk-****abcd" || c["has_key"] != true || c["idle_timeout_s"] != float64(240) || c["first_byte_timeout_s"] != float64(30) {
		t.Fatalf("create: %d %s", code, body)
	}
	if strings.Contains(body, "SECRET") {
		t.Fatal("key returned")
	}
	cid := int64(c["id"].(float64))
	// operator can read, never sees the key
	if code, _, b := e.do("GET", "/api/admin/configs", op, nil); code != 200 || strings.Contains(b, "SECRET") || strings.Contains(b, "cipher") {
		t.Fatalf("operator list: %d %s", code, b)
	}
	var cipher string
	e.pool.QueryRow(ctx, `SELECT key_cipher FROM service_config WHERE id=$1`, cid).Scan(&cipher)
	if strings.Contains(cipher, "SECRET") {
		t.Fatal("key not encrypted")
	}
	// update without api_key keeps the key
	delete(in, "api_key")
	in["reasoning_effort"] = "medium"
	if code, c2, _ := e.do("PUT", fmt.Sprintf("/api/admin/configs/%d", cid), root, in); code != 200 || c2["has_key"] != true || c2["reasoning_effort"] != "medium" {
		t.Fatal("update kept key")
	}
	in["reasoning_effort"] = "high"
	e.do("PUT", fmt.Sprintf("/api/admin/configs/%d", cid), root, in)

	// validation
	bad := []map[string]any{
		{"kind": "text", "name": "x", "base_url": "ftp://x", "model": "m"},
		{"kind": "text", "name": "x", "base_url": "https://x", "model": ""},
		{"kind": "text", "name": "x", "base_url": "https://x", "model": "m", "params": map[string]any{"stream": false}},
		{"kind": "text", "name": "x", "base_url": "https://x", "model": "m", "first_byte_timeout_s": 60, "total_timeout_s": 30},
		{"kind": "weird", "name": "x"},
	}
	for i, b := range bad {
		if code, _, _ := e.do("POST", "/api/admin/configs", root, b); code != 400 {
			t.Errorf("bad config %d accepted: %d", i, code)
		}
	}
	// non-model kinds (e.g. SMTP) store credentials too
	_, smtp, _ := e.do("POST", "/api/admin/configs", root, map[string]any{"kind": "notify", "name": "邮件", "protocol": "smtp", "api_key": "smtp-password-123456", "params": map[string]any{"host": "smtp.example.com"}})
	if smtp["has_key"] != true {
		t.Fatal("notify config")
	}

	// connection test: streams through the gateway, retries the empty reply, passes reasoning effort through
	code, res, b := e.do("POST", fmt.Sprintf("/api/admin/configs/%d/test", cid), root, nil)
	if code != 200 || res["ok"] != true || res["reply"] != "OK" || res["model"] != "reported-model" {
		t.Fatalf("test: %d %s", code, b)
	}
	if atts := res["attempts"].([]any); len(atts) != 2 || atts[0].(map[string]any)["outcome"] != "empty" {
		t.Fatalf("attempts %v", atts)
	}
	if lastBody["reasoning_effort"] != "high" || lastBody["stream"] != true || lastBody["model"] != "configured-model" {
		t.Fatalf("upstream body %v", lastBody)
	}
	var calls2 int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM model_call WHERE config_id=$1 AND stage='config_test'`, cid).Scan(&calls2)
	if calls2 != 2 {
		t.Fatalf("model_call rows=%d", calls2)
	}
	if code, _, _ := e.do("POST", fmt.Sprintf("/api/admin/configs/%d/test", cid), op, nil); code != 403 {
		t.Fatal("operator ran test")
	}
	// clear key -> test fails cleanly with 401 attempt
	in["clear_key"] = true
	e.do("PUT", fmt.Sprintf("/api/admin/configs/%d", cid), root, in)
	if _, res, _ := e.do("POST", fmt.Sprintf("/api/admin/configs/%d/test", cid), root, nil); res["ok"] != false || res["error"] == nil {
		t.Fatalf("test without key: %v", res)
	}
	delete(in, "clear_key")
	in["api_key"] = secretKey
	e.do("PUT", fmt.Sprintf("/api/admin/configs/%d", cid), root, in)

	// routing
	_, bak, _ := e.do("POST", "/api/admin/configs", root, map[string]any{"kind": "text", "name": "备用", "base_url": "https://b.example/v1", "model": "m2", "enabled": true})
	bid := int64(bak["id"].(float64))
	if code, _, b := e.do("PUT", "/api/admin/routes/write", root, map[string]any{"config_ids": []int64{cid, bid}}); code != 200 {
		t.Fatalf("route: %d %s", code, b)
	}
	sid := int64(smtp["id"].(float64))
	for i, ids := range [][]int64{{}, {cid, cid}, {sid}, {99999}} {
		if code, _, _ := e.do("PUT", "/api/admin/routes/write", root, map[string]any{"config_ids": ids}); code != 400 {
			t.Errorf("bad route %d accepted", i)
		}
	}
	if code, _, _ := e.do("PUT", "/api/admin/routes/nope", root, map[string]any{"config_ids": []int64{cid}}); code != 400 {
		t.Fatal("bad stage")
	}
	if code, _, _ := e.do("PUT", "/api/admin/routes/write", op, map[string]any{"config_ids": []int64{cid}}); code != 403 {
		t.Fatal("operator edited route")
	}
	if code, m, _ := e.do("GET", "/api/admin/routes", op, nil); code != 200 || len(m["routes"].([]any)) != 1 {
		t.Fatal("list routes")
	}
	// the gateway loader sees the route, primary first, key decrypted
	box, _ := crypto.NewBox("0123456789abcdef0123456789abcdef")
	cfgs, err := gateway.LoadStage(ctx, e.pool, box, "write", "free")
	if err != nil || len(cfgs) != 2 || cfgs[0].ID != cid || cfgs[0].APIKey != secretKey || cfgs[0].ReasoningEffort != "high" || cfgs[0].Idle != 240*time.Second {
		t.Fatalf("LoadStage %+v %v", cfgs, err)
	}
	if code, _, _ := e.do("DELETE", fmt.Sprintf("/api/admin/configs/%d", cid), root, nil); code != 409 {
		t.Fatal("deleted routed config")
	}
	e.do("DELETE", "/api/admin/routes/write", root, nil)
	if code, _, _ := e.do("DELETE", fmt.Sprintf("/api/admin/configs/%d", bid), root, nil); code != 200 {
		t.Fatal("delete config")
	}

	// audit never holds the key
	var n, leak int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action LIKE 'config.%' OR action LIKE 'route.%'`).Scan(&n)
	e.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE detail::text LIKE '%SECRET%' OR detail::text LIKE '%smtp-password%'`).Scan(&leak)
	if n < 8 || leak != 0 {
		t.Fatalf("audit n=%d leak=%d", n, leak)
	}
}
