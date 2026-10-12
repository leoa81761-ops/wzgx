package server

import (
	"net/http/httptest"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
)

func TestSetupPreviewDoesNotUseTrialCap(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "setup@example.com", "password": "password12", "agree": true})
	if registered.StatusCode != 200 {
		t.Fatal(registered.StatusCode, registered.Body)
	}
	token := registered.JSON["token"].(string)
	userID := int64(registered.JSON["user"].(map[string]any)["id"].(float64))
	bound := postJSON(t, ts.URL+"/api/accounts", token, map[string]any{
		"name": "弱电陈工", "app_id": "wx_mock_setup_elv", "app_secret": "mock-secret",
		"subject_type": "personal", "service_type": "subscription", "verified": false, "consent": true,
	})
	if bound.StatusCode != 200 {
		t.Fatal(bound.StatusCode, bound.Body)
	}
	preview := postJSON(t, ts.URL+"/api/setup/preview", token, map[string]any{"account_id": int64(bound.JSON["id"].(float64))})
	if preview.StatusCode != 200 {
		t.Fatal(preview.StatusCode, preview.Body)
	}
	sample, _ := preview.JSON["sample"].(string)
	if utf8.RuneCountInString(sample) != 200 || preview.JSON["runes"] != float64(200) {
		t.Fatal(preview.JSON["runes"], sample)
	}
	if preview.JSON["direction"] != "弱电智能化" || preview.JSON["vertical_key"] != "elv" || preview.JSON["time_window"] != "night" || preview.JSON["style_key"] != "formal" {
		t.Fatal(preview.JSON)
	}
	used, err := app.Store.CountTrials(t.Context(), userID, schedule.ShanghaiDay(time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
	if used != 0 {
		t.Fatal(used)
	}
	created := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": preview.JSON["account_id"], "direction": preview.JSON["direction"],
		"style_key": preview.JSON["style_key"], "template_key": preview.JSON["template_key"],
		"word_target": preview.JSON["word_target"], "schedule_type": "daily", "time_window": preview.JSON["time_window"],
		"daily_count": 1, "keywords": preview.JSON["keywords"], "audience": preview.JSON["audience"],
		"vertical_key": preview.JSON["vertical_key"], "topic_mode": "auto", "image_mode": "auto",
	})
	if created.StatusCode != 200 || created.JSON["direction"] != "弱电智能化" || created.JSON["status"] != "active" {
		t.Fatal(created.StatusCode, created.Body)
	}
}
