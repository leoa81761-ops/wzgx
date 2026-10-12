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

func TestAdminBoardLegalCostAndPurge(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{
		"account": "board@example.com", "password": "password12", "agree": true,
	})
	if registered.StatusCode != 200 {
		t.Fatal(registered.StatusCode, registered.Body)
	}
	token := registered.JSON["token"].(string)
	userID := int64(registered.JSON["user"].(map[string]any)["id"].(float64))
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	adminToken := admin.JSON["token"].(string)

	metrics := getJSON(t, ts.URL+"/api/admin/metrics", adminToken)
	if metrics.StatusCode != 200 || metrics.JSON["ai_label"] != false || intOf(metrics.JSON["users_total"]) < 2 {
		t.Fatal(metrics.StatusCode, metrics.JSON)
	}
	saved := putJSON(t, ts.URL+"/api/admin/cost", adminToken, map[string]any{
		"chat_cents_per_1k": 2, "image_cents": 10, "search_cents": 5,
		"month_cap_cents": 80, "action": "pause", "ai_label": true,
	})
	if saved.StatusCode != 200 || saved.JSON["ai_label"] != true || saved.JSON["action"] != "pause" || intOf(saved.JSON["month_cap_cents"]) != 80 {
		t.Fatal(saved.StatusCode, saved.JSON)
	}
	published := postJSON(t, ts.URL+"/api/admin/legal", adminToken, map[string]any{
		"doc_type": "user_agreement", "version": "0.2", "title": "用户协议",
		"content": "第二版协议，仍然是草案。", "require_reconsent": true,
	})
	if published.StatusCode != 200 || published.JSON["legal_version"] != "0.2" {
		t.Fatal(published.StatusCode, published.JSON)
	}
	old := getJSON(t, ts.URL+"/api/legal/user_agreement?version=0.1-draft", "")
	if old.StatusCode != 200 || old.JSON["status"] != "archived" || !strings.Contains(old.JSON["content"].(string), "待法务审核") {
		t.Fatal(old.StatusCode, old.JSON["status"])
	}
	current := getJSON(t, ts.URL+"/api/legal/user_agreement", "")
	if current.JSON["version"] != "0.2" || current.JSON["status"] != "active" {
		t.Fatal(current.JSON["version"], current.JSON["status"])
	}
	privacy := getJSON(t, ts.URL+"/api/legal/privacy", "")
	if privacy.JSON["version"] != "0.2" {
		t.Fatal("privacy should be copied forward", privacy.JSON["version"])
	}
	stale := getJSON(t, ts.URL+"/api/me/consent-status", token)
	if stale.JSON["needs_reconsent"] != true || stale.JSON["legal_version"] != "0.2" {
		t.Fatal(stale.JSON)
	}
	again := postJSON(t, ts.URL+"/api/me/reconsent", token, map[string]any{"agree": true})
	if again.StatusCode != 200 || again.JSON["needs_reconsent"] != false {
		t.Fatal(again.StatusCode, again.JSON)
	}
	consents := getJSON(t, ts.URL+"/api/admin/consents?user_id="+strconv.FormatInt(userID, 10), adminToken)
	if len(consents.JSONList) < 2 {
		t.Fatal(consents.StatusCode, consents.Body)
	}
	logs := getJSON(t, ts.URL+"/api/admin/audit-logs", adminToken)
	found := false
	for _, row := range logs.JSONList {
		if row["action"] == "legal_publish" && row["detail"] == "0.2" {
			found = true
		}
	}
	if !found {
		t.Fatal(logs.JSONList)
	}

	bound := postJSON(t, ts.URL+"/api/accounts", token, map[string]any{
		"name": "看板号", "app_id": "wx_mock_board", "app_secret": "mock-secret",
		"subject_type": "personal", "service_type": "subscription", "verified": false, "consent": true,
	})
	if bound.StatusCode != 200 {
		t.Fatal(bound.StatusCode, bound.Body)
	}
	accountID := int64(bound.JSON["id"].(float64))
	created := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": accountID, "direction": "职场成长", "style_key": "warm", "template_key": "simple",
		"word_target": 800, "schedule_type": "daily", "weekdays": []int{}, "time_window": "night",
	})
	planID := int64(created.JSON["id"].(float64))
	generated := postJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(planID, 10)+"/generate", token, map[string]any{})
	if generated.StatusCode != 200 {
		t.Fatal(generated.StatusCode, generated.Body)
	}
	articleID := int64(generated.JSON["id"].(float64))
	if generated.JSON["status"] == "generating" {
		generated.JSON = waitArticle(t, ts.URL, token, articleID)
	}
	html, _ := generated.JSON["body_html"].(string)
	if !strings.Contains(html, "本文由人工智能辅助生成") {
		t.Fatal("ai label missing")
	}
	oldStamp := schedule.Format(time.Now().UTC().Add(-40 * 24 * time.Hour))
	if _, err := app.DB.Exec(t.Context(), `UPDATE article SET created_at = ? WHERE id = ?`, oldStamp, articleID); err != nil {
		t.Fatal(err)
	}
	n, err := app.Store.PurgeArticleBodies(t.Context(), schedule.Format(time.Now().UTC().Add(-30*24*time.Hour)))
	if err != nil || n < 1 {
		t.Fatal(n, err)
	}
	detail := getJSON(t, ts.URL+"/api/articles/"+strconv.FormatInt(articleID, 10), token)
	if detail.JSON["title"] == "" || detail.JSON["body_html"] != "" {
		t.Fatalf("title %v html %v", detail.JSON["title"], detail.JSON["body_html"])
	}
}

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}
