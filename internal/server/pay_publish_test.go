package server

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/publish"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
)

func TestSandboxPaymentGrantsOnce(t *testing.T) {
	app := openApp(t, config.Settings{})
	app.Cfg.PaySandbox = true
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "pay@example.com", "password": "password12", "agree": true})
	if registered.StatusCode != 200 {
		t.Fatal(registered.Body)
	}
	token := registered.JSON["token"].(string)
	checkout := postJSON(t, ts.URL+"/api/billing/checkout", token, map[string]any{"tier": "standard", "period": "month"})
	if checkout.StatusCode != 200 || checkout.JSON["amount_cents"] != float64(3000) || !strings.HasPrefix(checkout.JSON["code_url"].(string), "sandbox:") {
		t.Fatal(checkout.StatusCode, checkout.Body)
	}
	no := checkout.JSON["out_trade_no"].(string)
	done := postJSON(t, ts.URL+"/api/billing/sandbox/complete", token, map[string]any{"out_trade_no": no})
	if done.StatusCode != 200 || done.JSON["tier"] != "standard" {
		t.Fatal(done.StatusCode, done.Body)
	}
	me := getJSON(t, ts.URL+"/api/auth/me", token)
	expires := me.JSON["tier_expires_at"]
	again := postJSON(t, ts.URL+"/api/billing/sandbox/complete", token, map[string]any{"out_trade_no": no})
	if again.StatusCode != 200 {
		t.Fatal(again.Body)
	}
	me = getJSON(t, ts.URL+"/api/auth/me", token)
	if me.JSON["tier"] != "standard" || me.JSON["tier_expires_at"] != expires {
		t.Fatal(me.JSON)
	}
	meta := getJSON(t, ts.URL+"/api/meta", "")
	payMeta := meta.JSON["pay"].(map[string]any)
	if payMeta["enabled"] != true || payMeta["sandbox"] != true {
		t.Fatal(payMeta)
	}
}

func TestAutoPublishOptInOnlyForVerifiedPaid(t *testing.T) {
	wechat.ResetMock()
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	personalUser := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "person@example.com", "password": "password12", "agree": true})
	personalToken := personalUser.JSON["token"].(string)
	personal := postJSON(t, ts.URL+"/api/accounts", personalToken, map[string]any{
		"name": "个人号", "app_id": "wx_mock_person", "app_secret": "mock-secret",
		"subject_type": "personal", "service_type": "subscription", "verified": false, "consent": true,
	})
	if personal.StatusCode != 200 {
		t.Fatal(personal.Body)
	}
	personalPlan := postJSON(t, ts.URL+"/api/plans", personalToken, map[string]any{
		"wx_account_id": int64(personal.JSON["id"].(float64)), "direction": "职场成长", "style_key": "warm", "template_key": "fresh",
		"word_target": 800, "schedule_type": "daily", "weekdays": []int{}, "time_window": "night",
	})
	if personalPlan.StatusCode != 200 {
		t.Fatal(personalPlan.Body)
	}
	denied := putJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(int64(personalPlan.JSON["id"].(float64)), 10)+"/publish", personalToken, map[string]any{"enabled": true, "delay_minutes": 0, "consent": true})
	if denied.StatusCode != 400 || !strings.Contains(denied.JSON["detail"].(string), "认证") {
		t.Fatal(denied.StatusCode, denied.Body)
	}
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "pub@example.com", "password": "password12", "agree": true})
	token := registered.JSON["token"].(string)
	userID := int64(registered.JSON["user"].(map[string]any)["id"].(float64))
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	adminToken := admin.JSON["token"].(string)
	tier := putJSON(t, ts.URL+"/api/admin/users/"+strconv.FormatInt(userID, 10)+"/tier", adminToken, map[string]any{"tier": "standard", "expires_at": "2027-12-01T00:00:00Z", "note": "测试"})
	if tier.StatusCode != 200 {
		t.Fatal(tier.Body)
	}
	bound := postJSON(t, ts.URL+"/api/accounts", token, map[string]any{
		"name": "认证号", "app_id": "wx_mock_autopub", "app_secret": "mock-secret",
		"subject_type": "enterprise", "service_type": "subscription", "verified": true, "consent": true,
	})
	if bound.StatusCode != 200 || bound.JSON["permission_report"].(map[string]any)["can_publish"] != true {
		t.Fatal(bound.StatusCode, bound.Body)
	}
	if !strings.Contains(bound.JSON["auto_publish_note"].(string), "默认关闭") {
		t.Fatal(bound.JSON["auto_publish_note"])
	}
	freeUser := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "free-pub@example.com", "password": "password12", "agree": true})
	freeToken := freeUser.JSON["token"].(string)
	freeBound := postJSON(t, ts.URL+"/api/accounts", freeToken, map[string]any{
		"name": "免费认证号", "app_id": "wx_mock_free_pub", "app_secret": "mock-secret",
		"subject_type": "enterprise", "service_type": "subscription", "verified": true, "consent": true,
	})
	freePlan := postJSON(t, ts.URL+"/api/plans", freeToken, map[string]any{
		"wx_account_id": int64(freeBound.JSON["id"].(float64)), "direction": "职场成长", "style_key": "warm", "template_key": "fresh",
		"word_target": 800, "schedule_type": "daily", "weekdays": []int{}, "time_window": "night",
	})
	freeDenied := putJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(int64(freePlan.JSON["id"].(float64)), 10)+"/publish", freeToken, map[string]any{"enabled": true, "delay_minutes": 0, "consent": true})
	if freeDenied.StatusCode != 400 || !strings.Contains(freeDenied.JSON["detail"].(string), "普通版") {
		t.Fatal(freeDenied.StatusCode, freeDenied.Body)
	}
	created := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": int64(bound.JSON["id"].(float64)), "direction": "职场成长", "style_key": "warm", "template_key": "fresh",
		"word_target": 800, "schedule_type": "daily", "weekdays": []int{}, "time_window": "morning",
	})
	if created.StatusCode != 200 {
		t.Fatal(created.Body)
	}
	planID := int64(created.JSON["id"].(float64))
	if created.JSON["auto_publish"] == true {
		t.Fatal("默认应该关闭")
	}
	noConsent := putJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(planID, 10)+"/publish", token, map[string]any{"enabled": true, "delay_minutes": 0, "consent": false})
	if noConsent.StatusCode != 400 {
		t.Fatal(noConsent.Body)
	}
	enabled := putJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(planID, 10)+"/publish", token, map[string]any{"enabled": true, "delay_minutes": 0, "consent": true})
	if enabled.StatusCode != 200 || enabled.JSON["auto_publish"] != true {
		t.Fatal(enabled.StatusCode, enabled.Body)
	}
	generated := postJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(planID, 10)+"/generate", token, map[string]any{})
	if generated.StatusCode != 200 {
		t.Fatal(generated.Body)
	}
	articleID := int64(generated.JSON["id"].(float64))
	article := waitArticle(t, ts.URL, token, articleID)
	if article["status"] != "published" || !strings.Contains(article["article_url"].(string), "mp.weixin.qq.com") {
		t.Fatal(article)
	}
	if wechat.MockPublishCount("wx_mock_autopub") != 1 {
		t.Fatal(wechat.MockPublishCount("wx_mock_autopub"))
	}
	publish.TryArticle(t.Context(), publish.Deps{Store: app.Store, Settings: app.Cfg, HTTP: app.HTTP, Tokens: app.Tokens}, articleID)
	if wechat.MockPublishCount("wx_mock_autopub") != 1 {
		t.Fatal("生成后再查一次又提交了")
	}
	paused := putJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(planID, 10)+"/publish", token, map[string]any{"enabled": false})
	if paused.StatusCode != 200 || paused.JSON["auto_publish"] != false {
		t.Fatal(paused.StatusCode, paused.Body)
	}
}
