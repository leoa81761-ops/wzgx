package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/integrate"
)

func TestPublicBaseFillsCallbackURLs(t *testing.T) {
	app := openApp(t, config.Settings{})
	app.Cfg.PublicBaseURL = "https://env.example"
	app.Cfg.WeChatOpenAppID = "wxopen"
	app.Cfg.WeChatOpenSecret = "secret"
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/auth/options", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "evil.example"
	req.Header.Set("X-Forwarded-Host", "evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var options map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&options); err != nil {
		t.Fatal(err)
	}
	if options["wechat_scan"] != true {
		t.Fatalf("scan %#v", options)
	}
	cfg := app.effective(req.Context())
	if strings.Contains(cfg.WeChatOpenRedirect, "evil.example") || cfg.WeChatOpenRedirect != "https://env.example"+integrate.PathWechatScan {
		t.Fatal(cfg.WeChatOpenRedirect)
	}
	if cfg.WxPayNotifyURL != "https://env.example"+integrate.PathWechatPayNotify ||
		cfg.AlipayNotifyURL != "https://env.example"+integrate.PathAlipayNotify ||
		cfg.AlipayReturnURL != "https://env.example"+integrate.PathAlipayReturn ||
		cfg.WeChatComponentRedirect != "https://env.example"+integrate.PathWechatComponent {
		t.Fatalf("%s %s %s %s", cfg.WxPayNotifyURL, cfg.AlipayNotifyURL, cfg.AlipayReturnURL, cfg.WeChatComponentRedirect)
	}
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	token := admin.JSON["token"].(string)
	view := getJSON(t, ts.URL+"/api/admin/integrations", token)
	if strings.Contains(view.Body, "evil.example") {
		t.Fatal(view.Body)
	}
	notify := integrationField(t, view.Body, "wxpay", "notify_url")
	if notify["value"] != "" || notify["placeholder"] != cfg.WxPayNotifyURL {
		t.Fatalf("%#v", notify)
	}
	baseField := integrationField(t, view.Body, "public", "base_url")
	if baseField["value"] != "" || baseField["placeholder"] != integrate.PublicBaseHint || !strings.Contains(asString(baseField["hint"]), "https://env.example") {
		t.Fatalf("%#v", baseField)
	}
	saved := putJSON(t, ts.URL+"/api/admin/integrations/public", token, map[string]any{"fields": map[string]string{"base_url": "https://admin.example/"}})
	if saved.StatusCode != 200 {
		t.Fatal(saved.StatusCode, saved.Body)
	}
	cfg = app.effective(req.Context())
	if cfg.PublicBaseURL != "https://admin.example" || cfg.WxPayNotifyURL != "https://admin.example"+integrate.PathWechatPayNotify {
		t.Fatal(cfg.PublicBaseURL, cfg.WxPayNotifyURL)
	}
	explicit := putJSON(t, ts.URL+"/api/admin/integrations/wxpay", token, map[string]any{"fields": map[string]string{"notify_url": "https://pay.example/hook"}})
	if explicit.StatusCode != 200 {
		t.Fatal(explicit.StatusCode, explicit.Body)
	}
	cfg = app.effective(req.Context())
	if cfg.WxPayNotifyURL != "https://pay.example/hook" || cfg.AlipayReturnURL != "https://admin.example"+integrate.PathAlipayReturn {
		t.Fatal(cfg.WxPayNotifyURL, cfg.AlipayReturnURL)
	}
	for _, item := range []struct{ method, path string }{
		{http.MethodPost, integrate.PathWechatPayNotify},
		{http.MethodPost, integrate.PathAlipayNotify},
		{http.MethodGet, integrate.PathWechatScan},
		{http.MethodGet, integrate.PathWechatComponent},
	} {
		var body io.Reader
		if item.method == http.MethodPost {
			body = strings.NewReader("{}")
		}
		call, err := http.NewRequest(item.method, ts.URL+item.path, body)
		if err != nil {
			t.Fatal(err)
		}
		got, err := http.DefaultClient.Do(call)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(got.Body)
		got.Body.Close()
		if strings.Contains(string(raw), "接口不存在") || strings.Contains(string(raw), "404 page not found") {
			t.Fatal(item.method, item.path, got.StatusCode, string(raw))
		}
	}
}

func integrationField(t *testing.T, body, section, key string) map[string]any {
	t.Helper()
	var view struct {
		Sections []struct {
			Key    string           `json:"key"`
			Fields []map[string]any `json:"fields"`
		} `json:"sections"`
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}
	for _, item := range view.Sections {
		if item.Key != section {
			continue
		}
		for _, field := range item.Fields {
			if field["key"] == key {
				return field
			}
		}
	}
	t.Fatalf("missing %s.%s", section, key)
	return nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
