package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/security"
)

func TestUserTavilyKeyIsEncryptedMaskedAndNotShared(t *testing.T) {
	var keys []string
	tavily := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		keys = append(keys, string(raw))
		if strings.Contains(string(raw), "user-key-bbbbbbbb") {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":"usage limit"}`)
			return
		}
		_, _ = io.WriteString(w, `{"results":[{"title":"综合布线验收","url":"https://www.gov.cn/a","content":"公开说明"}]}`)
	}))
	defer tavily.Close()
	app := openApp(t, config.Settings{})
	app.TavilyProbeURL = tavily.URL
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	first := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "tavily-a@example.com", "password": "password12", "agree": true})
	second := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "tavily-b@example.com", "password": "password12", "agree": true})
	if first.StatusCode != 200 || second.StatusCode != 200 {
		t.Fatal(first.StatusCode, second.StatusCode, first.Body, second.Body)
	}
	tokenA := first.JSON["token"].(string)
	tokenB := second.JSON["token"].(string)
	idA := int64(first.JSON["user"].(map[string]any)["id"].(float64))
	idB := int64(second.JSON["user"].(map[string]any)["id"].(float64))
	saved := putJSON(t, ts.URL+"/api/me/tavily", tokenA, map[string]any{"api_key": "user-key-aaaaaaaa"})
	if saved.StatusCode != 200 || saved.JSON["mask"] != "****aaaa" || strings.Contains(saved.Body, "user-key-aaaaaaaa") {
		t.Fatal(saved.StatusCode, saved.Body)
	}
	other := putJSON(t, ts.URL+"/api/me/tavily", tokenB, map[string]any{"api_key": "user-key-bbbbbbbb"})
	if other.StatusCode != 200 || other.JSON["mask"] != "****bbbb" {
		t.Fatal(other.StatusCode, other.Body)
	}
	var cipherA, cipherB string
	if err := app.DB.QueryRow(t.Context(), `SELECT tavily_key_encrypted FROM user_secret WHERE user_id = ?`, idA).Scan(&cipherA); err != nil {
		t.Fatal(err)
	}
	if err := app.DB.QueryRow(t.Context(), `SELECT tavily_key_encrypted FROM user_secret WHERE user_id = ?`, idB).Scan(&cipherB); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cipherA, "user-key") || cipherA == cipherB {
		t.Fatal(cipherA, cipherB)
	}
	plain, err := security.DecryptSecret(app.Cfg.MasterKey, cipherA)
	if err != nil || plain != "user-key-aaaaaaaa" {
		t.Fatal(plain, err)
	}
	viewB := getJSON(t, ts.URL+"/api/me/tavily", tokenB)
	if strings.Contains(viewB.Body, "aaaa") || viewB.JSON["mask"] != "****bbbb" {
		t.Fatal(viewB.Body)
	}
	ok := postJSON(t, ts.URL+"/api/me/tavily/test", tokenA, map[string]any{})
	if ok.StatusCode != 200 || ok.JSON["ok"] != true || strings.Contains(ok.Body, "user-key-aaaaaaaa") {
		t.Fatal(ok.StatusCode, ok.Body)
	}
	quota := postJSON(t, ts.URL+"/api/me/tavily/test", tokenB, map[string]any{})
	if quota.StatusCode != 400 || !strings.Contains(quota.Body, "额度已用完") || strings.Contains(quota.Body, "user-key-bbbbbbbb") {
		t.Fatal(quota.StatusCode, quota.Body)
	}
	again := getJSON(t, ts.URL+"/api/me/tavily", tokenB)
	if !strings.Contains(again.Body, "额度已用完") || strings.Contains(again.Body, "aaaa") {
		t.Fatal(again.Body)
	}
	replaced := putJSON(t, ts.URL+"/api/me/tavily", tokenA, map[string]any{"api_key": "user-key-cccccccc"})
	if replaced.StatusCode != 200 || replaced.JSON["mask"] != "****cccc" {
		t.Fatal(replaced.StatusCode, replaced.Body)
	}
	removed := sendJSON(t, http.MethodDelete, ts.URL+"/api/me/tavily", tokenA, nil)
	if removed.StatusCode != 200 || removed.JSON["set"] != false {
		t.Fatal(removed.StatusCode, removed.Body)
	}
	logs := getJSON(t, ts.URL+"/api/admin/audit-logs", postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"}).JSON["token"].(string))
	seen := map[string]bool{}
	for _, row := range logs.JSONList {
		action, _ := row["action"].(string)
		if strings.HasPrefix(action, "tavily_key_") {
			seen[action] = true
			detail, _ := row["detail"].(string)
			if strings.Contains(detail, "user-key") {
				t.Fatal(detail)
			}
		}
	}
	for _, action := range []string{"tavily_key_set", "tavily_key_replace", "tavily_key_delete", "tavily_key_test"} {
		if !seen[action] {
			t.Fatal(seen)
		}
	}
	allowed, err := app.Store.TakeSearxng(t.Context(), idA, 1)
	if err != nil || !allowed {
		t.Fatal(allowed, err)
	}
	allowed, err = app.Store.TakeSearxng(t.Context(), idA, 1)
	if err != nil || allowed {
		t.Fatal(allowed, err)
	}
	allowed, err = app.Store.TakeSearxng(t.Context(), idB, 1)
	if err != nil || !allowed {
		t.Fatal(allowed, err)
	}
}
