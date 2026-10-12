package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/model"
)

func TestPackCheckoutAndProratedUpgrade(t *testing.T) {
	app := openApp(t, config.Settings{})
	app.Cfg.PaySandbox = true
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "pack@example.com", "password": "password12", "agree": true})
	token := registered.JSON["token"].(string)
	userID := int64(registered.JSON["user"].(map[string]any)["id"].(float64))
	pack := postJSON(t, ts.URL+"/api/billing/checkout", token, map[string]any{"tier": "pack", "period": "pack"})
	if pack.StatusCode != 200 || pack.JSON["amount_cents"] != float64(1900) || pack.JSON["period"] != "pack:10" {
		t.Fatal(pack.StatusCode, pack.Body)
	}
	done := postJSON(t, ts.URL+"/api/billing/sandbox/complete", token, map[string]any{"out_trade_no": pack.JSON["out_trade_no"]})
	if done.StatusCode != 200 || done.JSON["tier"] != "free" {
		t.Fatal(done.StatusCode, done.Body)
	}
	quota := getJSON(t, ts.URL+"/api/me/quota", token)
	if quota.JSON["pack_remaining"] != float64(10) || quota.JSON["tier"] != "free" {
		t.Fatal(quota.Body)
	}

	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	expires := time.Now().UTC().Add(15 * 24 * time.Hour).Format(time.RFC3339)
	tier := putJSON(t, ts.URL+"/api/admin/users/"+itoa(userID)+"/tier", admin.JSON["token"].(string), map[string]any{
		"tier": "standard", "expires_at": expires, "note": "测试补差", "when": "now",
	})
	if tier.StatusCode != 200 {
		t.Fatal(tier.Body)
	}
	before := getJSON(t, ts.URL+"/api/auth/me", token)
	upgrade := postJSON(t, ts.URL+"/api/billing/checkout", token, map[string]any{"tier": "premium", "period": "month"})
	if upgrade.StatusCode != 200 || upgrade.JSON["period"] != "upgrade-keep" {
		t.Fatal(upgrade.StatusCode, upgrade.Body)
	}
	amount := upgrade.JSON["amount_cents"].(float64)
	if amount <= 0 || amount >= 9900 {
		t.Fatal(amount)
	}
	paid := postJSON(t, ts.URL+"/api/billing/sandbox/complete", token, map[string]any{"out_trade_no": upgrade.JSON["out_trade_no"]})
	if paid.StatusCode != 200 || paid.JSON["tier"] != "premium" {
		t.Fatal(paid.StatusCode, paid.Body)
	}
	after := getJSON(t, ts.URL+"/api/auth/me", token)
	if after.JSON["tier"] != "premium" || after.JSON["tier_expires_at"] != before.JSON["tier_expires_at"] {
		t.Fatal(before.JSON["tier_expires_at"], after.JSON)
	}
}

func TestFirstPurchaseRefundAndConsentWithdrawal(t *testing.T) {
	app := openApp(t, config.Settings{})
	app.Cfg.PaySandbox = true
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "refund@example.com", "password": "password12", "agree": true})
	token := registered.JSON["token"].(string)
	userID := int64(registered.JSON["user"].(map[string]any)["id"].(float64))
	order := postJSON(t, ts.URL+"/api/billing/checkout", token, map[string]any{"tier": "standard", "period": "month"})
	paid := postJSON(t, ts.URL+"/api/billing/sandbox/complete", token, map[string]any{"out_trade_no": order.JSON["out_trade_no"]})
	if paid.StatusCode != 200 {
		t.Fatal(paid.Body)
	}
	refunded := postJSON(t, ts.URL+"/api/billing/refund-request", token, map[string]any{})
	if refunded.StatusCode != 200 || refunded.JSON["status"] != "refunded" {
		t.Fatal(refunded.StatusCode, refunded.Body)
	}
	me := getJSON(t, ts.URL+"/api/auth/me", token)
	if me.JSON["tier"] != "free" || me.JSON["tier_expires_at"] != nil {
		t.Fatal(me.JSON)
	}

	again := postJSON(t, ts.URL+"/api/billing/checkout", token, map[string]any{"tier": "standard", "period": "month"})
	if postJSON(t, ts.URL+"/api/billing/sandbox/complete", token, map[string]any{"out_trade_no": again.JSON["out_trade_no"]}).StatusCode != 200 {
		t.Fatal(again.Body)
	}
	hold := &model.QuotaHold{UserID: userID, TaskID: 9001, DayKey: "2026-10-12", PeriodKey: "2026-10", Amount: 1, State: "confirmed", CreatedAt: time.Now().UTC().Add(time.Minute)}
	if err := app.Store.InsertHold(t.Context(), nil, hold); err != nil {
		t.Fatal(err)
	}
	used := postJSON(t, ts.URL+"/api/billing/refund-request", token, map[string]any{})
	if used.StatusCode != 400 || !strings.Contains(used.JSON["detail"].(string), "已经使用") {
		t.Fatal(used.StatusCode, used.Body)
	}

	consents := getJSON(t, ts.URL+"/api/me/consents", token)
	var consentID float64
	for _, row := range consents.JSONList {
		if row["doc_type"] == "user_agreement" && row["withdrawn_at"] == nil {
			consentID = row["id"].(float64)
		}
	}
	if consentID == 0 {
		t.Fatal(consents.Body)
	}
	withdrawn := postJSON(t, ts.URL+"/api/me/consents/"+itoa(int64(consentID))+"/withdraw", token, map[string]any{})
	if withdrawn.StatusCode != 200 {
		t.Fatal(withdrawn.StatusCode, withdrawn.Body)
	}
	status := getJSON(t, ts.URL+"/api/me/consent-status", token)
	if status.JSON["needs_reconsent"] != true {
		t.Fatal(status.Body)
	}

	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	adminToken := admin.JSON["token"].(string)
	saved := putJSON(t, ts.URL+"/api/admin/users/"+itoa(userID)+"/contract", adminToken, map[string]any{
		"company": "示例公司", "contract_no": "HT-1", "note": "线下定制",
	})
	if saved.StatusCode != 200 {
		t.Fatal(saved.Body)
	}
	got := getJSON(t, ts.URL+"/api/admin/users/"+itoa(userID)+"/contract", adminToken)
	if got.JSON["company"] != "示例公司" || got.JSON["contract_no"] != "HT-1" {
		t.Fatal(got.Body)
	}
}

func TestRecoveryResetRebindAndParallelGuesses(t *testing.T) {
	app := openApp(t, config.Settings{})
	var mu sync.Mutex
	var seen []string
	sms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := url.QueryUnescape(r.URL.Query().Get("TemplateParam"))
		var body map[string]string
		_ = json.Unmarshal([]byte(raw), &body)
		mu.Lock()
		seen = append(seen, body["code"])
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Code":"OK"}`))
	}))
	defer sms.Close()
	app.Cfg.SMSProvider = "aliyun"
	app.Cfg.SMSAccessKey = "key"
	app.Cfg.SMSAccessSecret = "secret"
	app.Cfg.SMSSignName = "公号智写"
	app.Cfg.SMSTemplateCode = "SMS_1"
	app.Cfg.SMSAPIBase = sms.URL
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()

	missing := postJSON(t, ts.URL+"/api/auth/recover/send", "", map[string]any{"account": "13900001111"})
	if missing.StatusCode != 200 || missing.JSON["ok"] != true {
		t.Fatal(missing.StatusCode, missing.Body)
	}
	mu.Lock()
	if len(seen) != 0 {
		t.Fatal(seen)
	}
	mu.Unlock()

	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "13800138000", "password": "password12", "agree": true})
	if registered.StatusCode != 200 {
		t.Fatal(registered.Body)
	}
	sent := postJSON(t, ts.URL+"/api/auth/recover/send", "", map[string]any{"account": "13800138000"})
	if sent.StatusCode != 200 {
		t.Fatal(sent.StatusCode, sent.Body)
	}
	code := lastCode(t, &mu, &seen)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	var wg sync.WaitGroup
	client := ts.Client()
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, _ := json.Marshal(map[string]any{"account": "13800138000", "code": wrong, "password": "password99"})
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/recover/reset", strings.NewReader(string(raw)))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()
	var attempts int
	if err := app.DB.QueryRow(t.Context(), `SELECT attempts FROM recovery_code WHERE account = ? AND purpose = 'reset' ORDER BY id DESC LIMIT 1`, "13800138000").Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 5 {
		t.Fatal(attempts)
	}
	late := postJSON(t, ts.URL+"/api/auth/recover/reset", "", map[string]any{"account": "13800138000", "code": code, "password": "password99"})
	if late.StatusCode != 400 {
		t.Fatal(late.StatusCode, late.Body)
	}

	app.smsSendIP = newIPWindow(1, time.Hour)
	other := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "13800138001", "password": "password12", "agree": true})
	otherToken := other.JSON["token"].(string)
	if postJSON(t, ts.URL+"/api/auth/recover/send", "", map[string]any{"account": "13800138001"}).StatusCode != 200 {
		t.Fatal("first send")
	}
	limited := postJSON(t, ts.URL+"/api/auth/recover/send", "", map[string]any{"account": "13800138001"})
	if limited.StatusCode != 400 || !strings.Contains(limited.JSON["detail"].(string), "过多") {
		t.Fatal(limited.StatusCode, limited.Body)
	}
	app.smsSendIP = newIPWindow(20, time.Hour)
	resetCode := lastCode(t, &mu, &seen)
	reset := postJSON(t, ts.URL+"/api/auth/recover/reset", "", map[string]any{"account": "13800138001", "code": resetCode, "password": "password99"})
	if reset.StatusCode != 200 {
		t.Fatal(reset.StatusCode, reset.Body)
	}
	login := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "13800138001", "password": "password99"})
	if login.StatusCode != 200 {
		t.Fatal(login.StatusCode, login.Body)
	}

	rebind := postJSON(t, ts.URL+"/api/me/rebind/send", otherToken, map[string]any{"phone": "13800138009"})
	if rebind.StatusCode != 200 {
		t.Fatal(rebind.StatusCode, rebind.Body)
	}
	bound := postJSON(t, ts.URL+"/api/me/rebind", otherToken, map[string]any{"phone": "13800138009", "code": lastCode(t, &mu, &seen)})
	if bound.StatusCode != 200 || bound.JSON["user"].(map[string]any)["phone"] != "13800138009" {
		t.Fatal(bound.StatusCode, bound.Body)
	}
}

func TestDeviceConsentAndStyleDiff(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	plain := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "plain@example.com", "password": "password12", "agree": true})
	plainID := int64(plain.JSON["user"].(map[string]any)["id"].(float64))
	var consents, nonempty int
	if err := app.DB.QueryRow(t.Context(), `SELECT COUNT(*) FROM consent_log WHERE user_id = ? AND doc_type = 'device_fingerprint'`, plainID).Scan(&consents); err != nil {
		t.Fatal(err)
	}
	if err := app.DB.QueryRow(t.Context(), `SELECT COUNT(*) FROM user_invite WHERE user_id = ? AND device_hash != ''`, plainID).Scan(&nonempty); err != nil {
		t.Fatal(err)
	}
	if consents != 0 || nonempty != 0 {
		t.Fatal(consents, nonempty)
	}
	agreed := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{
		"account": "device@example.com", "password": "password12", "agree": true, "device_consent": true, "device_id": "phone-1",
	})
	agreedID := int64(agreed.JSON["user"].(map[string]any)["id"].(float64))
	if err := app.DB.QueryRow(t.Context(), `SELECT COUNT(*) FROM consent_log WHERE user_id = ? AND doc_type = 'device_fingerprint' AND version = '1' AND withdrawn_at IS NULL`, agreedID).Scan(&consents); err != nil {
		t.Fatal(err)
	}
	if err := app.DB.QueryRow(t.Context(), `SELECT COUNT(*) FROM user_invite WHERE user_id = ? AND device_hash != ''`, agreedID).Scan(&nonempty); err != nil {
		t.Fatal(err)
	}
	if consents != 1 || nonempty != 1 {
		t.Fatal(consents, nonempty)
	}

	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	expires := time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339)
	if putJSON(t, ts.URL+"/api/admin/users/"+itoa(agreedID)+"/tier", admin.JSON["token"].(string), map[string]any{
		"tier": "premium", "expires_at": expires, "note": "风格", "when": "now",
	}).StatusCode != 200 {
		t.Fatal("tier")
	}
	user, err := app.Store.GetUser(t.Context(), nil, agreedID)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/articles/1/reread", nil)
	remote := "今天天气很好吗？我觉得要带伞。"
	if err := app.learnEditStyle(req, user, "今天天气很好。", remote, true); err != nil {
		t.Fatal(err)
	}
	var stored int
	if err := app.DB.QueryRow(t.Context(), `SELECT COUNT(*) FROM style_profile WHERE user_id = ?`, agreedID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Fatal("stored a style profile before consent")
	}
	token := agreed.JSON["token"].(string)
	if postJSON(t, ts.URL+"/api/me/consents/style-diff", token, map[string]any{"agree": true}).StatusCode != 200 {
		t.Fatal("consent")
	}
	if err := app.learnEditStyle(req, user, "今天天气很好。", remote, true); err != nil {
		t.Fatal(err)
	}
	var features string
	if err := app.DB.QueryRow(t.Context(), `SELECT features FROM style_profile WHERE user_id = ? AND name = ?`, agreedID, "修改差异").Scan(&features); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(features, "带伞") || strings.Contains(features, "天气") {
		t.Fatal(features)
	}
}

func lastCode(t *testing.T, mu *sync.Mutex, seen *[]string) string {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	if len(*seen) == 0 || (*seen)[len(*seen)-1] == "" {
		t.Fatal(*seen)
	}
	return (*seen)[len(*seen)-1]
}
