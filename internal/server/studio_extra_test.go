package server

import (
	"encoding/base64"
	"testing"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/pipeline"
	"net/http/httptest"
	"strings"
	"time"
)

func TestTrialSampleProfileAssetAndQuietPause(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "studio@example.com", "password": "password12", "agree": true})
	token := registered.JSON["token"].(string)
	userID := int64(registered.JSON["user"].(map[string]any)["id"].(float64))
	if err := app.Store.SetSetting(t.Context(), nil, "trial_daily_cap", "1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	trial := postJSON(t, ts.URL+"/api/styles/trial", token, map[string]any{"style_key": "warm"})
	if trial.StatusCode != 200 || utf8.RuneCountInString(trial.JSON["text"].(string)) != 200 {
		t.Fatal(trial.StatusCode, trial.Body)
	}
	again := postJSON(t, ts.URL+"/api/styles/trial", token, map[string]any{"style_key": "warm"})
	if again.StatusCode != 400 {
		t.Fatal(again.StatusCode, again.Body)
	}
	sample := postJSON(t, ts.URL+"/api/sample", token, map[string]any{})
	if sample.StatusCode != 200 || sample.JSON["title"] == "" {
		t.Fatal(sample.StatusCode, sample.Body)
	}
	second := postJSON(t, ts.URL+"/api/sample", token, map[string]any{})
	if second.StatusCode != 400 {
		t.Fatal(second.Body)
	}
	denied := postJSON(t, ts.URL+"/api/style-profiles", token, map[string]any{"name": "我的", "samples": []string{"太短", "太短", "太短"}})
	if denied.StatusCode != 400 {
		t.Fatal(denied.Body)
	}
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	tier := putJSON(t, ts.URL+"/api/admin/users/"+itoa(userID)+"/tier", admin.JSON["token"].(string), map[string]any{"tier": "premium", "expires_at": time.Now().Add(40 * 24 * time.Hour).Format(time.RFC3339), "note": "测试", "when": "now"})
	if tier.StatusCode != 200 {
		t.Fatal(tier.Body)
	}
	sampleText := strings.Repeat("这是一句完整的话。", 12)
	profile := postJSON(t, ts.URL+"/api/style-profiles", token, map[string]any{"name": "我的", "samples": []string{sampleText, sampleText, sampleText}})
	if profile.StatusCode != 200 || profile.JSON["features"].(map[string]any)["samples"] != float64(3) {
		t.Fatal(profile.StatusCode, profile.Body)
	}
	png := base64.StdEncoding.EncodeToString([]byte("png"))
	asset := postJSON(t, ts.URL+"/api/assets", token, map[string]any{"name": "封面", "mime": "image/png", "license_note": "自己拍摄", "data_base64": png})
	if asset.StatusCode != 200 {
		t.Fatal(asset.StatusCode, asset.Body)
	}
	bound := postJSON(t, ts.URL+"/api/accounts", token, map[string]any{
		"name": "号", "app_id": "wx_mock_studio", "app_secret": "mock-secret",
		"subject_type": "personal", "service_type": "subscription", "verified": false, "consent": true,
	})
	if bound.StatusCode != 200 {
		t.Fatal(bound.Body)
	}
	created := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": int64(bound.JSON["id"].(float64)), "direction": "职场成长", "style_key": "warm", "template_key": "simple",
		"word_target": 800, "schedule_type": "daily", "weekdays": []int{}, "time_window": "night",
		"opening": "开头固定", "ending": "结尾固定", "column_name": "科普", "column_style": "plain",
	})
	if created.StatusCode != 200 || created.JSON["opening"] != "开头固定" || created.JSON["column_style"] != "plain" {
		t.Fatal(created.StatusCode, created.Body)
	}
	planID := int64(created.JSON["id"].(float64))
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		article := &model.Article{TaskID: int64(i + 1), UserID: userID, PlanID: planID, WxAccountID: int64(bound.JSON["id"].(float64)), Title: "稿", Status: "in_draft", CreatedAt: now}
		if err := app.Store.InsertArticle(t.Context(), nil, article); err != nil {
			t.Fatal(err)
		}
	}
	paused, err := pipeline.PauseIfQuiet(t.Context(), app.deps(), planID, now)
	if err != nil || !paused {
		t.Fatal(paused, err)
	}
	plan, err := app.Store.GetPlan(t.Context(), nil, planID)
	if err != nil || plan.Status != "paused" || plan.PauseReason != "连续 5 篇没有反馈" {
		t.Fatal(plan, err)
	}
}
