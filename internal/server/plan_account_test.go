package server

import (
	"context"
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
		"tone": "务实，少形容词", "persona_columns": "现场笔记", "signatures": "先看现场，再谈方案", "taboos": "保证中标",
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
		"tone": "像家里人说话", "persona_columns": "带娃记", "signatures": "先把今天过好", "taboos": "恐吓式育儿",
	})
	if family.StatusCode != 200 {
		t.Fatal(family.StatusCode, family.Body)
	}
	elvID := int64(elv.JSON["id"].(float64))
	familyID := int64(family.JSON["id"].(float64))
	if elv.JSON["account_name"] != "弱电陈工" || elv.JSON["keywords"] != "验收,机房" || elv.JSON["vertical_key"] != "elv" || elv.JSON["topic_mode"] != "rotate" || elv.JSON["image_mode"] != "none" || elv.JSON["tone"] != "务实，少形容词" || elv.JSON["suggested_tone"] == "" {
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
	if updated.StatusCode != 200 || updated.JSON["keywords"] != "验收,线槽" || updated.JSON["audience"] != "项目经理" || updated.JSON["opening"] != "先说结论" || updated.JSON["vertical_key"] != "elv" || updated.JSON["tone"] != "务实，少形容词" || updated.JSON["taboos"] != "保证中标" {
		t.Fatal(updated.StatusCode, updated.JSON)
	}
	kept := getJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(familyID, 10), token)
	if kept.StatusCode != 200 || kept.JSON["keywords"] != "家常,收纳" || kept.JSON["audience"] != "带孙子的家庭" || kept.JSON["style_key"] != "warm" || kept.JSON["opening"] != "从晚饭说起" || kept.JSON["tone"] != "像家里人说话" {
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

func TestAccountPersonaIsInherited(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "voice@example.com", "password": "password12", "agree": true})
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
	accountID := bindPlanAccount(t, ts.URL, token, "弱电陈工", "wx_mock_voice")
	first := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": accountID, "direction": "弱电智能化", "style_key": "warm", "template_key": "simple",
		"word_target": 800, "schedule_type": "daily", "time_window": "night", "daily_count": 1,
		"tone": "先写的语气", "audience": "现场读者", "persona_columns": "现场笔记", "signatures": "先看现场", "taboos": "包过验收",
	})
	if first.StatusCode != 200 {
		t.Fatal(first.StatusCode, first.Body)
	}
	second := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": accountID, "direction": "职场成长", "style_key": "warm", "template_key": "simple",
		"word_target": 800, "schedule_type": "daily", "time_window": "night", "daily_count": 1,
		"tone": "后写的语气",
	})
	if second.StatusCode != 200 {
		t.Fatal(second.StatusCode, second.Body)
	}
	before := accountByID(t, getJSON(t, ts.URL+"/api/accounts", token).JSONList, accountID)
	if before["tone"] != "" || before["audience"] != "" {
		t.Fatal(before)
	}
	if err := app.DB.MigrateAccountVoice(context.Background()); err != nil {
		t.Fatal(err)
	}
	migrated := accountByID(t, getJSON(t, ts.URL+"/api/accounts", token).JSONList, accountID)
	if migrated["tone"] != "先写的语气" || migrated["audience"] != "现场读者" || migrated["persona_columns"] != "现场笔记" || migrated["signatures"] != "先看现场" || migrated["taboos"] != "包过验收" {
		t.Fatal(migrated)
	}
	firstID := strconv.FormatInt(int64(first.JSON["id"].(float64)), 10)
	secondID := strconv.FormatInt(int64(second.JSON["id"].(float64)), 10)
	kept := getJSON(t, ts.URL+"/api/plans/"+firstID, token)
	if kept.JSON["tone"] != "先写的语气" || kept.JSON["effective_tone"] != "先写的语气" || kept.JSON["account_tone"] != "先写的语气" {
		t.Fatal(kept.JSON)
	}
	later := getJSON(t, ts.URL+"/api/plans/"+secondID, token)
	if later.JSON["tone"] != "后写的语气" || later.JSON["effective_tone"] != "后写的语气" || later.JSON["account_tone"] != "先写的语气" {
		t.Fatal(later.JSON)
	}
	saved := putJSON(t, ts.URL+"/api/accounts/"+strconv.FormatInt(accountID, 10)+"/voice", token, map[string]any{
		"audience": "现场负责人", "tone": "账号语气", "persona_columns": "验收清单", "signatures": "先看现场", "taboos": "包过",
	})
	if saved.StatusCode != 200 || saved.JSON["tone"] != "账号语气" {
		t.Fatal(saved.StatusCode, saved.Body)
	}
	blank := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": accountID, "direction": "个人成长", "style_key": "warm", "template_key": "simple",
		"word_target": 800, "schedule_type": "daily", "time_window": "night", "daily_count": 1,
	})
	if blank.StatusCode != 200 {
		t.Fatal(blank.StatusCode, blank.Body)
	}
	if blank.JSON["tone"] != "" || blank.JSON["audience"] != "" || blank.JSON["account_tone"] != "账号语气" || blank.JSON["effective_tone"] != "账号语气" || blank.JSON["effective_audience"] != "现场负责人" {
		t.Fatal(blank.JSON)
	}
	overrideID := strconv.FormatInt(int64(blank.JSON["id"].(float64)), 10)
	updated := putJSON(t, ts.URL+"/api/plans/"+overrideID, token, map[string]any{"tone": "计划覆盖"})
	if updated.StatusCode != 200 || updated.JSON["tone"] != "计划覆盖" || updated.JSON["effective_tone"] != "计划覆盖" || updated.JSON["account_tone"] != "账号语气" {
		t.Fatal(updated.StatusCode, updated.JSON)
	}
	again := accountByID(t, getJSON(t, ts.URL+"/api/accounts", token).JSONList, accountID)
	if again["tone"] != "账号语气" || again["audience"] != "现场负责人" {
		t.Fatal(again)
	}
}

func accountByID(t *testing.T, rows []map[string]any, id int64) map[string]any {
	t.Helper()
	for _, row := range rows {
		if int64(row["id"].(float64)) == id {
			return row
		}
	}
	t.Fatalf("missing account %d", id)
	return nil
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
