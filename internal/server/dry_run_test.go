package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
)

func TestDryRunStaysOutOfTheDraftBox(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{
		"account": "dry@example.com", "password": "password12", "agree": true,
	})
	if registered.StatusCode != 200 {
		t.Fatal(registered.StatusCode, registered.Body)
	}
	token := registered.JSON["token"].(string)
	userID := int64(registered.JSON["user"].(map[string]any)["id"].(float64))
	bound := postJSON(t, ts.URL+"/api/accounts", token, map[string]any{
		"name": "测试号", "app_id": "wx_mock_dry", "app_secret": "mock-secret",
		"subject_type": "personal", "service_type": "subscription", "verified": false, "consent": true,
	})
	if bound.StatusCode != 200 {
		t.Fatal(bound.StatusCode, bound.Body)
	}
	accountID := int64(bound.JSON["id"].(float64))
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	adminToken := admin.JSON["token"].(string)
	tier := putJSON(t, ts.URL+"/api/admin/users/"+strconv.FormatInt(userID, 10)+"/tier", adminToken, map[string]any{"tier": "premium", "expires_at": time.Now().Add(40 * 24 * time.Hour).Format(time.RFC3339), "note": "测试", "when": "now"})
	if tier.StatusCode != 200 {
		t.Fatal(tier.StatusCode, tier.Body)
	}
	exempt := putJSON(t, ts.URL+"/api/admin/users/"+strconv.FormatInt(userID, 10)+"/quota-exempt", adminToken, map[string]any{"quota_exempt": true})
	if exempt.StatusCode != 200 {
		t.Fatal(exempt.StatusCode, exempt.Body)
	}
	created := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": accountID, "direction": "弱电智能化", "style_key": "warm", "template_key": "simple",
		"word_target": 800, "schedule_type": "daily", "weekdays": []int{}, "time_window": "night",
	})
	if created.StatusCode != 200 || created.JSON["dry_run"] != true || created.JSON["dry_run_set"] != true {
		t.Fatal(created.StatusCode, created.Body)
	}
	planID := int64(created.JSON["id"].(float64))
	pushed := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": accountID, "direction": "家庭收纳", "style_key": "warm", "template_key": "simple",
		"word_target": 800, "schedule_type": "daily", "weekdays": []int{}, "time_window": "night",
		"dry_run": false,
	})
	if pushed.StatusCode != 200 || pushed.JSON["dry_run"] != false || pushed.JSON["dry_run_set"] != true {
		t.Fatal(pushed.StatusCode, pushed.Body)
	}
	updated := putJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(planID, 10), token, map[string]any{"dry_run": false})
	if updated.StatusCode != 200 || updated.JSON["dry_run"] != false {
		t.Fatal(updated.StatusCode, updated.Body)
	}
	kept := putJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(planID, 10), token, map[string]any{"direction": "弱电智能化"})
	if kept.StatusCode != 200 || kept.JSON["dry_run"] != false || kept.JSON["dry_run_set"] != true {
		t.Fatal(kept.StatusCode, kept.Body)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/plans/"+strconv.FormatInt(planID, 10)+"/generate", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	empty := readResp(t, resp)
	if empty.StatusCode != 200 {
		t.Fatal(empty.StatusCode, empty.Body)
	}
	var unset int
	if err := app.DB.QueryRow(t.Context(), `SELECT skip_draft FROM article_task WHERE plan_id = ? AND trigger_type = 'manual' ORDER BY id DESC LIMIT 1`, planID).Scan(&unset); err != nil {
		t.Fatal(err)
	}
	if unset != -1 {
		t.Fatal(unset)
	}
	otherID := int64(pushed.JSON["id"].(float64))
	explicit := postJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(otherID, 10)+"/generate", token, map[string]any{"dry_run": true})
	if explicit.StatusCode != 200 {
		t.Fatal(explicit.StatusCode, explicit.Body)
	}
	var skip int
	if err := app.DB.QueryRow(t.Context(), `SELECT skip_draft FROM article_task WHERE plan_id = ? AND trigger_type = 'manual' ORDER BY id DESC LIMIT 1`, otherID).Scan(&skip); err != nil {
		t.Fatal(err)
	}
	if skip != 1 {
		t.Fatal(skip)
	}
}
