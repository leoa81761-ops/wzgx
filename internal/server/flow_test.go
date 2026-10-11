package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
)

func TestRegisterRequiresConsentAndFullFlow(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	denied := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "13800138000", "password": "password12", "agree": false})
	if denied.StatusCode != 400 || !strings.Contains(denied.JSON["detail"].(string), "同意") {
		t.Fatal(denied.StatusCode, denied.JSON)
	}
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "reader@example.com", "password": "password12", "agree": true})
	if registered.StatusCode != 200 {
		t.Fatal(registered.StatusCode, registered.Body)
	}
	token := registered.JSON["token"].(string)
	userID := int64(registered.JSON["user"].(map[string]any)["id"].(float64))
	consents := getJSON(t, ts.URL+"/api/me/consents", token)
	types := map[string]bool{}
	for _, item := range consents.JSONList {
		types[item["doc_type"].(string)] = true
	}
	if !types["user_agreement"] || !types["privacy"] {
		t.Fatal(consents.JSONList)
	}
	legal := getJSON(t, ts.URL+"/api/legal/user_agreement", "")
	if legal.StatusCode != 200 || !strings.Contains(legal.JSON["content"].(string), "待法务审核") {
		t.Fatal(legal.StatusCode)
	}
	bound := postJSON(t, ts.URL+"/api/accounts", token, map[string]any{
		"name": "我的订阅号", "app_id": "wx_mock_demo", "app_secret": "mock-secret",
		"subject_type": "personal", "service_type": "subscription", "verified": false, "consent": true,
	})
	if bound.StatusCode != 200 {
		t.Fatal(bound.StatusCode, bound.Body)
	}
	if bound.JSON["delivery_mode"] != "draft" || bound.JSON["permission_report"].(map[string]any)["can_draft"] != true {
		t.Fatal(bound.JSON)
	}
	accountID := int64(bound.JSON["id"].(float64))
	created := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": accountID, "direction": "职场成长", "style_key": "casual", "template_key": "simple",
		"word_target": 800, "schedule_type": "weekly", "weekdays": []int{1, 3, 5}, "time_window": "morning",
	})
	if created.StatusCode != 200 {
		t.Fatal(created.StatusCode, created.Body)
	}
	if created.JSON["time_window"] != "night" || created.JSON["schedule_type"] != "weekly" {
		t.Fatal(created.JSON)
	}
	planID := int64(created.JSON["id"].(float64))
	generated := postJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(planID, 10)+"/generate", token, map[string]any{})
	if generated.StatusCode != 200 {
		t.Fatal(generated.StatusCode, generated.Body)
	}
	articleID := int64(generated.JSON["id"].(float64))
	if generated.JSON["status"] == "generating" {
		generated.JSON = waitArticle(t, ts.URL, token, articleID)
	}
	if generated.JSON["status"] != "in_draft" || !strings.HasPrefix(generated.JSON["draft_media_id"].(string), "mock_draft_") {
		t.Fatal(generated.JSON)
	}
	html := generated.JSON["body_html"].(string)
	if !strings.Contains(html, "style=") || !strings.Contains(html, "轻松口语") {
		t.Fatal(html)
	}
	again := postJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(planID, 10)+"/generate", token, map[string]any{})
	if again.StatusCode != 400 || !strings.Contains(again.JSON["detail"].(string), "1 批") {
		t.Fatal(again.StatusCode, again.JSON)
	}
	regenerated := postJSON(t, ts.URL+"/api/articles/"+strconv.FormatInt(articleID, 10)+"/regenerate", token, map[string]any{})
	if regenerated.StatusCode != 200 {
		t.Fatal(regenerated.StatusCode, regenerated.Body)
	}
	if regenerated.JSON["status"] == "generating" {
		regenerated.JSON = waitArticle(t, ts.URL, token, articleID)
	}
	if regenerated.JSON["draft_media_id"] != generated.JSON["draft_media_id"] || regenerated.JSON["regen_count"] != float64(1) {
		t.Fatal(regenerated.JSON)
	}
	blocked := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": accountID, "direction": "投资理财", "style_key": "formal", "template_key": "business",
		"word_target": 800, "schedule_type": "daily", "weekdays": []int{}, "time_window": "night",
	})
	if blocked.StatusCode != 400 {
		t.Fatal(blocked.StatusCode, blocked.Body)
	}
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	if admin.StatusCode != 200 {
		t.Fatal(admin.StatusCode, admin.Body)
	}
	adminToken := admin.JSON["token"].(string)
	provider := postJSON(t, ts.URL+"/api/admin/providers", adminToken, map[string]any{
		"name": "Claude", "protocol": "anthropic", "base_url": "https://api.anthropic.com", "api_key": "sk-ant-secret",
		"model": "claude-test", "extra_headers": map[string]string{"X-Tenant": "demo"}, "extra_params": map[string]any{"temperature": 0.4},
		"priority": 1, "enabled": true, "timeout_seconds": 60,
	})
	if provider.StatusCode != 200 || !strings.HasSuffix(provider.JSON["key_hint"].(string), "cret") || strings.Contains(provider.Body, "sk-ant-secret") {
		t.Fatal(provider.StatusCode, provider.Body)
	}
	backup := postJSON(t, ts.URL+"/api/admin/providers", adminToken, map[string]any{
		"name": "兼容接口", "protocol": "openai", "base_url": "https://llm.example.com/v1", "api_key": "sk-openai",
		"model": "demo-writer", "extra_headers": map[string]string{}, "extra_params": map[string]any{},
		"priority": 2, "enabled": true, "timeout_seconds": 60,
	})
	if backup.StatusCode != 200 {
		t.Fatal(backup.Body)
	}
	saved := putJSON(t, ts.URL+"/api/admin/routes", adminToken, map[string]any{
		"stage": "draft", "tier": "*", "provider_ids": []int64{int64(provider.JSON["id"].(float64)), int64(backup.JSON["id"].(float64))},
	})
	if saved.StatusCode != 200 {
		t.Fatal(saved.Body)
	}
	routes := getJSON(t, ts.URL+"/api/admin/routes", adminToken)
	var found bool
	for _, item := range routes.JSON["routes"].([]any) {
		row := item.(map[string]any)
		if row["stage"] == "draft" && row["tier"] == "*" {
			ids := row["provider_ids"].([]any)
			if len(ids) == 2 && ids[0] == provider.JSON["id"] && ids[1] == backup.JSON["id"] {
				found = true
			}
		}
	}
	if !found {
		t.Fatal(routes.JSON)
	}
	tier := putJSON(t, ts.URL+"/api/admin/users/"+strconv.FormatInt(userID, 10)+"/tier", adminToken, map[string]any{"tier": "standard", "expires_at": "2026-12-01T00:00:00+08:00", "note": "线下收款 30 元"})
	if tier.StatusCode != 200 {
		t.Fatal(tier.Body)
	}
	me := getJSON(t, ts.URL+"/api/auth/me", token)
	if me.JSON["tier"] != "standard" {
		t.Fatal(me.JSON)
	}
	page := getRaw(t, ts.URL+"/", "")
	if page.StatusCode != 200 || !strings.Contains(page.Body, "公号智写") && !strings.Contains(page.Body, "前端尚未构建") {
		t.Fatal(page.StatusCode, page.Body)
	}
}

func TestAdminCanDisableAndDeleteUsers(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "cleanup@example.com", "password": "password12", "agree": true})
	if registered.StatusCode != 200 {
		t.Fatal(registered.StatusCode, registered.Body)
	}
	userID := int64(registered.JSON["user"].(map[string]any)["id"].(float64))
	token := registered.JSON["token"].(string)
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	if admin.StatusCode != 200 {
		t.Fatal(admin.StatusCode, admin.Body)
	}
	adminToken := admin.JSON["token"].(string)
	adminID := int64(admin.JSON["user"].(map[string]any)["id"].(float64))
	self := sendJSON(t, http.MethodDelete, ts.URL+"/api/admin/users/"+strconv.FormatInt(adminID, 10), adminToken, nil)
	if self.StatusCode != 400 {
		t.Fatal(self.StatusCode, self.Body)
	}
	disabled := putJSON(t, ts.URL+"/api/admin/users/"+strconv.FormatInt(userID, 10)+"/active", adminToken, map[string]any{"active": false})
	if disabled.StatusCode != 200 {
		t.Fatal(disabled.StatusCode, disabled.Body)
	}
	blocked := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "cleanup@example.com", "password": "password12"})
	if blocked.StatusCode != 403 || !strings.Contains(blocked.Body, "停用") {
		t.Fatal(blocked.StatusCode, blocked.Body)
	}
	_ = token
	enabled := putJSON(t, ts.URL+"/api/admin/users/"+strconv.FormatInt(userID, 10)+"/active", adminToken, map[string]any{"active": true})
	if enabled.StatusCode != 200 {
		t.Fatal(enabled.Body)
	}
	again := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "cleanup@example.com", "password": "password12"})
	if again.StatusCode != 200 {
		t.Fatal(again.StatusCode, again.Body)
	}
	deleted := sendJSON(t, http.MethodDelete, ts.URL+"/api/admin/users/"+strconv.FormatInt(userID, 10), adminToken, nil)
	if deleted.StatusCode != 200 {
		t.Fatal(deleted.StatusCode, deleted.Body)
	}
	gone := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "cleanup@example.com", "password": "password12"})
	if gone.StatusCode != 401 {
		t.Fatal(gone.StatusCode, gone.Body)
	}
	users := getJSON(t, ts.URL+"/api/admin/users", adminToken)
	for _, row := range users.JSONList {
		if row["account"] == "cleanup@example.com" {
			t.Fatal(users.JSONList)
		}
		if row["is_self"] == true && row["is_active"] != true {
			t.Fatal(row)
		}
	}
}

func TestBindSurfacesIPWhitelist(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":40164,"errmsg":"invalid ip"}`))
	}))
	defer fake.Close()
	app := openApp(t, config.Settings{WeChatMode: "live", WeChatAPIBase: fake.URL})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "ip@example.com", "password": "password12", "agree": true})
	token := registered.JSON["token"].(string)
	bound := postJSON(t, ts.URL+"/api/accounts", token, map[string]any{
		"name": "正式号", "app_id": "wx1234567890abcd", "app_secret": "real-secret",
		"subject_type": "personal", "service_type": "subscription", "verified": false, "consent": true,
	})
	if bound.StatusCode != 400 || !strings.Contains(bound.JSON["detail"].(string), "白名单") {
		t.Fatal(bound.StatusCode, bound.Body)
	}
}

func waitArticle(t *testing.T, base, token string, id int64) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		resp := getJSON(t, base+"/api/articles/"+strconv.FormatInt(id, 10), token)
		if resp.StatusCode != 200 {
			t.Fatal(resp.StatusCode, resp.Body)
		}
		last = resp.JSON
		switch last["status"] {
		case "in_draft", "exported", "failed":
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("生成没有结束: %#v", last)
	return nil
}

func openApp(t *testing.T, extra config.Settings) *App {
	t.Helper()
	cfg := config.Settings{
		DatabaseURL: "sqlite://" + filepath.Join(t.TempDir(), "app.db"), MasterKey: "test-master-key", JWTSecret: "test-jwt-secret",
		AdminEmail: "admin@example.com", AdminPassword: "admin123456", WeChatMode: "mock", WeChatAPIBase: "https://api.weixin.qq.com",
		LLMMode: "mock", MaxConcurrency: 4, SchedulerInterval: time.Minute, StaleTaskSeconds: 900,
	}
	if extra.WeChatMode != "" {
		cfg.WeChatMode = extra.WeChatMode
	}
	if extra.WeChatAPIBase != "" {
		cfg.WeChatAPIBase = extra.WeChatAPIBase
	}
	app, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app
}

type apiResp struct {
	StatusCode int
	Body       string
	JSON       map[string]any
	JSONList   []map[string]any
}

func postJSON(t *testing.T, url, token string, body any) apiResp {
	return sendJSON(t, http.MethodPost, url, token, body)
}
func putJSON(t *testing.T, url, token string, body any) apiResp {
	return sendJSON(t, http.MethodPut, url, token, body)
}

func sendJSON(t *testing.T, method, url, token string, body any) apiResp {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(method, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return readResp(t, resp)
}

func getJSON(t *testing.T, url, token string) apiResp {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return readResp(t, resp)
}

func getRaw(t *testing.T, url, token string) apiResp {
	return getJSON(t, url, token)
}

func readResp(t *testing.T, resp *http.Response) apiResp {
	t.Helper()
	body, _ := io.ReadAll(resp.Body)
	out := apiResp{StatusCode: resp.StatusCode, Body: string(body)}
	if len(body) > 0 && body[0] == '{' {
		_ = json.Unmarshal(body, &out.JSON)
	}
	if len(body) > 0 && body[0] == '[' {
		_ = json.Unmarshal(body, &out.JSONList)
	}
	return out
}
