package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
)

func TestStatsChartReportAndLayout(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{
		"account": "stats@example.com", "password": "password12", "agree": true,
	})
	if registered.StatusCode != 200 {
		t.Fatal(registered.StatusCode, registered.Body)
	}
	token := registered.JSON["token"].(string)
	user := registered.JSON["user"].(map[string]any)
	userID := int64(user["id"].(float64))
	bound := postJSON(t, ts.URL+"/api/accounts", token, map[string]any{
		"name": "认证号", "app_id": "wx_mock_certified", "app_secret": "mock-secret",
		"subject_type": "enterprise", "service_type": "service", "verified": true, "consent": true,
	})
	if bound.StatusCode != 200 {
		t.Fatal(bound.StatusCode, bound.Body)
	}
	accountID := int64(bound.JSON["id"].(float64))
	pulled := postJSON(t, ts.URL+"/api/accounts/"+itoa(accountID)+"/stats/pull", token, map[string]any{})
	if pulled.StatusCode != 400 || !strings.Contains(pulled.Body, "同意") {
		t.Fatal(pulled.StatusCode, pulled.Body)
	}
	agreed := postJSON(t, ts.URL+"/api/me/consents/analytics", token, map[string]any{"agree": true})
	if agreed.StatusCode != 200 {
		t.Fatal(agreed.StatusCode, agreed.Body)
	}
	wechat.SetMockDatacube("wx_mock_certified", "/datacube/getarticleread", map[string]any{"list": []any{
		map[string]any{"ref_date": "2026-10-10", "msgid": "11_1", "detail": map[string]any{"read_user": float64(8)}},
	}})
	wechat.SetMockDatacube("wx_mock_certified", "/datacube/getarticleshare", map[string]any{"list": []any{
		map[string]any{"ref_date": "2026-10-10", "msgid": "11_1", "detail": map[string]any{"share_user": float64(1)}},
	}})
	wechat.SetMockDatacube("wx_mock_certified", "/datacube/getusersummary", map[string]any{"list": []any{
		map[string]any{"ref_date": "2026-10-10", "new_user": float64(2)},
	}})
	pulled = postJSON(t, ts.URL+"/api/accounts/"+itoa(accountID)+"/stats/pull", token, map[string]any{})
	if pulled.StatusCode != 200 || !strings.Contains(pulled.Body, `"reads":8`) {
		t.Fatal(pulled.StatusCode, pulled.Body)
	}
	for _, day := range []string{"2026-10-08", "2026-10-09", "2026-10-11"} {
		saved := postJSON(t, ts.URL+"/api/accounts/"+itoa(accountID)+"/stats", token, map[string]any{
			"date": day, "reads": 1, "shares": 0, "favors": 0, "follows": 0,
		})
		if saved.StatusCode != 200 {
			t.Fatal(saved.StatusCode, saved.Body)
		}
	}
	early := postJSON(t, ts.URL+"/api/accounts/"+itoa(accountID)+"/reports", token, map[string]any{})
	if early.StatusCode != 400 {
		t.Fatal(early.StatusCode, early.Body)
	}
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	adminToken := admin.JSON["token"].(string)
	upgraded := putJSON(t, ts.URL+"/api/admin/users/"+itoa(userID)+"/tier", adminToken, map[string]any{"tier": "standard", "note": "测试周报"})
	if upgraded.StatusCode != 200 {
		t.Fatal(upgraded.StatusCode, upgraded.Body)
	}
	report := postJSON(t, ts.URL+"/api/accounts/"+itoa(accountID)+"/reports", token, map[string]any{})
	if report.StatusCode != 200 || !strings.Contains(report.Body, "阅读 11") || !strings.Contains(report.Body, "不调整选题权重") {
		t.Fatal(report.StatusCode, report.Body)
	}
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/accounts/"+itoa(accountID)+"/stats.png", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	png, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || len(png) < 8 || string(png[:4]) != "\x89PNG" {
		t.Fatalf("status %d bytes %d", resp.StatusCode, len(png))
	}
	created := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": accountID, "direction": "职场成长", "style_key": "warm", "template_key": "simple",
		"word_target": 800, "schedule_type": "daily", "weekdays": []int{}, "time_window": "night",
	})
	if created.StatusCode != 200 {
		t.Fatal(created.StatusCode, created.Body)
	}
	planID := int64(created.JSON["id"].(float64))
	reset := postJSON(t, ts.URL+"/api/plans/"+itoa(planID)+"/weights/reset", token, map[string]any{})
	if reset.StatusCode != 200 {
		t.Fatal(reset.StatusCode, reset.Body)
	}
	bind := postJSON(t, ts.URL+"/api/me/template-bind", token, map[string]any{"openid": "openid_1"})
	if bind.StatusCode != 400 {
		t.Fatal(bind.StatusCode, bind.Body)
	}
	light := postJSON(t, ts.URL+"/api/admin/layout-checks", adminToken, map[string]any{
		"layout_key": "simple", "device": "iPhone", "theme": "light", "result": "pass", "note": "浅色可读",
	})
	if light.StatusCode != 200 || strings.Contains(light.Body, `"measured":true`) {
		t.Fatal(light.StatusCode, light.Body)
	}
	dark := postJSON(t, ts.URL+"/api/admin/layout-checks", adminToken, map[string]any{
		"layout_key": "simple", "device": "iPhone", "theme": "dark", "result": "pass", "note": "暗色可读",
	})
	if dark.StatusCode != 200 || !strings.Contains(dark.Body, `"measured":true`) {
		t.Fatal(dark.StatusCode, dark.Body)
	}
}
