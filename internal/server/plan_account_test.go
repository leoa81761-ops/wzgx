package server

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
)

func TestPlansStayIndependentPerAccount(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "plans@example.com", "password": "password12", "agree": true})
	if registered.StatusCode != 200 {
		t.Fatal(registered.StatusCode, registered.Body)
	}
	token := registered.JSON["token"].(string)
	userID := int64(registered.JSON["user"].(map[string]any)["id"].(float64))
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	tier := putJSON(t, ts.URL+"/api/admin/users/"+strconv.FormatInt(userID, 10)+"/tier", admin.JSON["token"].(string), map[string]any{
		"tier": "premium", "expires_at": time.Now().Add(40 * 24 * time.Hour).Format(time.RFC3339), "note": "测试", "when": "now",
	})
	if tier.StatusCode != 200 {
		t.Fatal(tier.Body)
	}
	other := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "other-plan@example.com", "password": "password12", "agree": true})
	otherToken := other.JSON["token"].(string)
	png := "aGVsbG8="
	asset := postJSON(t, ts.URL+"/api/assets", otherToken, map[string]any{"name": "别人的图", "mime": "image/png", "license_note": "自有", "data_base64": png})
	if asset.StatusCode != 200 {
		t.Fatal(asset.StatusCode, asset.Body)
	}
	foreignAsset := int64(asset.JSON["id"].(float64))

	first := bindPlanAccount(t, ts.URL, token, "弱电陈工", "wx_mock_elv")
	second := bindPlanAccount(t, ts.URL, token, "奶奶不当家", "wx_mock_family")
	elv := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": first, "direction": "弱电陈工", "style_key": "plain", "template_key": "simple",
		"word_target": 1500, "schedule_type": "daily", "time_window": "morning", "daily_count": 1,
		"keywords": "验收,机房", "audience": "物业工程负责人", "banned_words": "1000V",
		"vertical_key": "elv", "topic_mode": "rotate", "image_mode": "none",
		"opening": "先说结论", "ending": "转给同事",
	})
	if elv.StatusCode != 200 {
		t.Fatal(elv.StatusCode, elv.Body)
	}
	family := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": second, "direction": "奶奶不当家", "style_key": "warm", "template_key": "simple",
		"word_target": 800, "schedule_type": "weekly", "weekdays": []int{2, 4}, "time_window": "evening", "daily_count": 1,
		"keywords": "家常,收纳", "audience": "带孙子的家庭", "banned_words": "偏方",
		"vertical_key": "family", "topic_mode": "hot", "image_mode": "cover",
		"opening": "从晚饭说起", "ending": "明天继续",
	})
	if family.StatusCode != 200 {
		t.Fatal(family.StatusCode, family.Body)
	}
	elvID := int64(elv.JSON["id"].(float64))
	familyID := int64(family.JSON["id"].(float64))
	if elv.JSON["account_name"] != "弱电陈工" || elv.JSON["keywords"] != "验收,机房" || elv.JSON["vertical_key"] != "elv" || elv.JSON["topic_mode"] != "rotate" || elv.JSON["image_mode"] != "none" {
		t.Fatal(elv.JSON)
	}
	if family.JSON["audience"] != "带孙子的家庭" || family.JSON["opening"] != "从晚饭说起" || family.JSON["wx_account_id"] == elv.JSON["wx_account_id"] {
		t.Fatal(family.JSON)
	}
	stolen := putJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(elvID, 10), token, map[string]any{"asset_id": foreignAsset})
	if stolen.StatusCode != 400 || !strings.Contains(stolen.Body, "素材") {
		t.Fatal(stolen.StatusCode, stolen.Body)
	}
	moved := putJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(elvID, 10), token, map[string]any{"wx_account_id": second})
	if moved.StatusCode != 400 || !strings.Contains(moved.Body, "另一个号") {
		t.Fatal(moved.StatusCode, moved.Body)
	}
	updated := putJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(elvID, 10), token, map[string]any{
		"keywords": "验收,线槽", "audience": "项目经理", "style_key": "warm", "word_target": 800,
	})
	if updated.StatusCode != 200 || updated.JSON["keywords"] != "验收,线槽" || updated.JSON["audience"] != "项目经理" || updated.JSON["opening"] != "先说结论" || updated.JSON["vertical_key"] != "elv" {
		t.Fatal(updated.StatusCode, updated.JSON)
	}
	kept := getJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(familyID, 10), token)
	if kept.StatusCode != 200 || kept.JSON["keywords"] != "家常,收纳" || kept.JSON["audience"] != "带孙子的家庭" || kept.JSON["style_key"] != "warm" || kept.JSON["opening"] != "从晚饭说起" {
		t.Fatal(kept.StatusCode, kept.JSON)
	}
	listed := getJSON(t, ts.URL+"/api/plans", token)
	if len(listed.JSONList) != 2 {
		t.Fatal(listed.Body)
	}
	paused := postJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(familyID, 10)+"/pause", token, map[string]any{})
	if paused.StatusCode != 200 || paused.JSON["status"] != "paused" {
		t.Fatal(paused.StatusCode, paused.JSON)
	}
	manual := postJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(familyID, 10)+"/generate", token, map[string]any{})
	if manual.StatusCode != 200 || manual.JSON["id"] == nil {
		t.Fatal(manual.StatusCode, manual.Body)
	}
	still := getJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(familyID, 10), token)
	if still.JSON["status"] != "paused" {
		t.Fatal(still.JSON["status"])
	}
	resumed := postJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(familyID, 10)+"/resume", token, map[string]any{})
	if resumed.StatusCode != 200 || resumed.JSON["status"] != "active" || resumed.JSON["next_run_at"] == nil || resumed.JSON["next_run_at"] == "" {
		t.Fatal(resumed.StatusCode, resumed.JSON)
	}
	unknown := putJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(elvID, 10), token, map[string]any{"vertical_key": "no-such-pack"})
	if unknown.StatusCode != 400 {
		t.Fatal(unknown.StatusCode, unknown.Body)
	}
}

func bindPlanAccount(t *testing.T, base, token, name, appID string) int64 {
	t.Helper()
	bound := postJSON(t, base+"/api/accounts", token, map[string]any{
		"name": name, "app_id": appID, "app_secret": "mock-secret",
		"subject_type": "personal", "service_type": "subscription", "verified": false, "consent": true,
	})
	if bound.StatusCode != 200 {
		t.Fatal(bound.StatusCode, bound.Body)
	}
	return int64(bound.JSON["id"].(float64))
}
