package server

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
)

func TestLibraryTierPreviewAndDowngrade(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "lib@example.com", "password": "password12", "agree": true})
	if registered.StatusCode != 200 {
		t.Fatal(registered.StatusCode, registered.Body)
	}
	token := registered.JSON["token"].(string)
	userID := int64(registered.JSON["user"].(map[string]any)["id"].(float64))
	bound := postJSON(t, ts.URL+"/api/accounts", token, map[string]any{
		"name": "预览号", "app_id": "wx_mock_lib", "app_secret": "mock-secret",
		"subject_type": "personal", "service_type": "subscription", "verified": false, "consent": true,
	})
	if bound.StatusCode != 200 {
		t.Fatal(bound.StatusCode, bound.Body)
	}
	accountID := int64(bound.JSON["id"].(float64))
	denied := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": accountID, "direction": "职场成长", "style_key": "story", "template_key": "simple",
		"word_target": 800, "schedule_type": "daily", "weekdays": []int{}, "time_window": "night",
	})
	if denied.StatusCode != 400 || !strings.Contains(denied.JSON["detail"].(string), "风格") {
		t.Fatal(denied.StatusCode, denied.Body)
	}
	created := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": accountID, "direction": "弱电智能化", "style_key": "warm", "template_key": "simple",
		"word_target": 800, "schedule_type": "daily", "weekdays": []int{}, "time_window": "night",
	})
	if created.StatusCode != 200 {
		t.Fatal(created.StatusCode, created.Body)
	}
	planID := int64(created.JSON["id"].(float64))
	calendar := getJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(planID, 10)+"/calendar?count=1", token)
	if calendar.StatusCode != 200 {
		t.Fatal(calendar.Body)
	}
	items := calendar.JSON["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["subtopic"] != "综合布线" {
		t.Fatal(calendar.JSON)
	}
	generated := postJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(planID, 10)+"/generate", token, map[string]any{})
	if generated.StatusCode != 200 {
		t.Fatal(generated.StatusCode, generated.Body)
	}
	articleID := int64(generated.JSON["id"].(float64))
	if generated.JSON["status"] == "generating" {
		generated.JSON = waitArticle(t, ts.URL, token, articleID)
	}
	preview := getJSON(t, ts.URL+"/api/articles/"+strconv.FormatInt(articleID, 10)+"?template=business", token)
	html, _ := preview.JSON["body_html"].(string)
	if !strings.Contains(html, "#1e3a5f") || !strings.Contains(preview.JSON["theme_note"].(string), "没有写回草稿") {
		t.Fatal(preview.JSON["theme_note"], html)
	}
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	adminToken := admin.JSON["token"].(string)
	verticals := getJSON(t, ts.URL+"/api/admin/verticals", adminToken)
	found := false
	for _, item := range verticals.JSONList {
		if item["key"] == "elv" {
			found = true
		}
	}
	if !found {
		t.Fatal(verticals.Body)
	}
	saved := putJSON(t, ts.URL+"/api/admin/verticals", adminToken, map[string]any{
		"key": "travel", "name": "出行", "keywords": "徒步", "subtopics": "路线\n装备", "enabled": true,
	})
	if saved.StatusCode != 200 {
		t.Fatal(saved.Body)
	}
	up := putJSON(t, ts.URL+"/api/admin/users/"+strconv.FormatInt(userID, 10)+"/tier", adminToken, map[string]any{
		"tier": "standard", "expires_at": time.Now().UTC().Add(40 * 24 * time.Hour).Format(time.RFC3339), "note": "测试开通",
	})
	if up.StatusCode != 200 || up.JSON["tier"] != "standard" || up.JSON["pending_tier"] != "" {
		t.Fatal(up.Body)
	}
	down := putJSON(t, ts.URL+"/api/admin/users/"+strconv.FormatInt(userID, 10)+"/tier", adminToken, map[string]any{
		"tier": "free", "note": "到期降回",
	})
	if down.StatusCode != 200 || down.JSON["tier"] != "standard" || down.JSON["pending_tier"] != "free" {
		t.Fatal(down.Body)
	}
	quota := getJSON(t, ts.URL+"/api/me/quota", token)
	if quota.JSON["tier"] != "standard" || quota.JSON["pending_tier"] != "free" {
		t.Fatal(quota.JSON)
	}
	past := schedule.Format(time.Now().UTC().Add(-time.Hour))
	if _, err := app.DB.SQL.Exec(`UPDATE users SET tier_expires_at = ? WHERE id = ?`, past, userID); err != nil {
		t.Fatal(err)
	}
	applied := getJSON(t, ts.URL+"/api/me/quota", token)
	if applied.JSON["tier"] != "free" || applied.JSON["pending_tier"] != nil {
		t.Fatal(applied.JSON)
	}
}
