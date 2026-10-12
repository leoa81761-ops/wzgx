package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/wxopen"
)

func TestSMSLoginStaysOffUntilConfigured(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	options := getJSON(t, ts.URL+"/api/auth/options", "")
	if options.JSON["password"] != true || options.JSON["sms"] != false || options.JSON["wechat_scan"] != false {
		t.Fatalf("%#v", options.JSON)
	}
	off := postJSON(t, ts.URL+"/api/auth/sms/send", "", map[string]any{"phone": "13800138000"})
	if off.StatusCode != 400 {
		t.Fatal(off.StatusCode, off.Body)
	}
	var code string
	sms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal([]byte(r.URL.Query().Get("TemplateParam")), &payload)
		code = payload.Code
		_, _ = io.WriteString(w, `{"Code":"OK"}`)
	}))
	defer sms.Close()
	app.Cfg.SMSProvider = "aliyun"
	app.Cfg.SMSAccessKey = "id"
	app.Cfg.SMSAccessSecret = "secret"
	app.Cfg.SMSSignName = "公号智写"
	app.Cfg.SMSTemplateCode = "SMS_1"
	app.Cfg.SMSAPIBase = sms.URL
	on := getJSON(t, ts.URL+"/api/auth/options", "")
	if on.JSON["sms"] != true {
		t.Fatal(on.JSON)
	}
	sent := postJSON(t, ts.URL+"/api/auth/sms/send", "", map[string]any{"phone": "13800138000"})
	if sent.StatusCode != 200 || len(code) != 6 {
		t.Fatal(sent.StatusCode, sent.Body, code)
	}
	again := postJSON(t, ts.URL+"/api/auth/sms/send", "", map[string]any{"phone": "13800138000"})
	if again.StatusCode != 400 {
		t.Fatal(again.Body)
	}
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	badCode := postJSON(t, ts.URL+"/api/auth/sms/login", "", map[string]any{"phone": "13800138000", "code": wrong, "agree": true})
	if badCode.StatusCode != 400 {
		t.Fatal(badCode.Body)
	}
	noAgree := postJSON(t, ts.URL+"/api/auth/sms/login", "", map[string]any{"phone": "13800138000", "code": code})
	if noAgree.StatusCode != 400 {
		t.Fatal(noAgree.Body)
	}
	created := postJSON(t, ts.URL+"/api/auth/sms/login", "", map[string]any{"phone": "13800138000", "code": code, "agree": true})
	if created.StatusCode != 200 || created.JSON["token"] == "" {
		t.Fatal(created.StatusCode, created.Body)
	}
	sent = postJSON(t, ts.URL+"/api/auth/sms/send", "", map[string]any{"phone": "13800138000"})
	if sent.StatusCode != 200 || len(code) != 6 {
		t.Fatal(sent.StatusCode, sent.Body, code)
	}
	againLogin := postJSON(t, ts.URL+"/api/auth/sms/login", "", map[string]any{"phone": "13800138000", "code": code})
	if againLogin.StatusCode != 200 || againLogin.JSON["token"] == "" {
		t.Fatal(againLogin.StatusCode, againLogin.Body)
	}
}

func TestWeChatScanAndComponent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "sns/oauth2/access_token"):
			_ = json.NewEncoder(w).Encode(map[string]any{"openid": "openid-1", "unionid": "union-1"})
		case strings.Contains(r.URL.Path, "api_component_token"):
			_ = json.NewEncoder(w).Encode(map[string]any{"component_access_token": "comp-token", "expires_in": 7200})
		case strings.Contains(r.URL.Path, "api_create_preauthcode"):
			_ = json.NewEncoder(w).Encode(map[string]any{"pre_auth_code": "pre-1"})
		case strings.Contains(r.URL.Path, "api_query_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_info": map[string]any{
				"authorizer_appid": "wx_authorizer_1", "authorizer_refresh_token": "refresh-1",
			}})
		case strings.Contains(r.URL.Path, "api_get_authorizer_info"):
			_ = json.NewEncoder(w).Encode(map[string]any{"authorizer_info": map[string]any{
				"nick_name": "测试号", "service_type_info": map[string]any{"id": 0}, "verify_type_info": map[string]any{"id": -1},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	app := openApp(t, config.Settings{WeChatAPIBase: upstream.URL, WeChatMode: "mock"})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	off := getJSON(t, ts.URL+"/api/auth/wechat/start", "")
	if off.StatusCode != 400 {
		t.Fatal(off.Body)
	}
	app.Cfg.WeChatOpenAppID = "wxopen"
	app.Cfg.WeChatOpenSecret = "secret"
	app.Cfg.WeChatOpenRedirect = ts.URL + "/api/auth/wechat/callback"
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	started := browserJSON(t, browser, http.MethodGet, ts.URL+"/api/auth/wechat/start", "", nil)
	if started.StatusCode != 200 {
		t.Fatal(started.Body)
	}
	link, _ := started.JSON["url"].(string)
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	state := parsed.Query().Get("state")
	bare := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	stolen, err := bare.Get(ts.URL + "/api/auth/wechat/callback?code=abc&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatal(err)
	}
	stolen.Body.Close()
	if stolen.StatusCode != http.StatusFound || !strings.Contains(stolen.Header.Get("Location"), "error=") {
		t.Fatal(stolen.StatusCode, stolen.Header.Get("Location"))
	}
	resp, err := browser.Get(ts.URL + "/api/auth/wechat/callback?code=abc&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatal(resp.StatusCode, resp.Header.Get("Location"))
	}
	replay, err := browser.Get(ts.URL + "/api/auth/wechat/callback?code=abc&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatal(err)
	}
	replay.Body.Close()
	if replay.StatusCode != http.StatusFound || !strings.Contains(replay.Header.Get("Location"), "error=") {
		t.Fatal(replay.StatusCode, replay.Header.Get("Location"))
	}
	ticket := loc.Query().Get("ticket")
	withoutCookie := postJSON(t, ts.URL+"/api/auth/wechat/ticket", "", map[string]any{"ticket": ticket, "agree": true})
	if withoutCookie.StatusCode != 400 {
		t.Fatal(withoutCookie.Body)
	}
	need := browserJSON(t, browser, http.MethodPost, ts.URL+"/api/auth/wechat/ticket", "", map[string]any{"ticket": ticket})
	if need.StatusCode != 200 || need.JSON["need_agree"] != true {
		t.Fatal(need.StatusCode, need.Body)
	}
	agreed := browserJSON(t, browser, http.MethodPost, ts.URL+"/api/auth/wechat/ticket", "", map[string]any{"ticket": ticket, "agree": true})
	token, _ := agreed.JSON["token"].(string)
	if agreed.StatusCode != 200 || token == "" {
		t.Fatal(agreed.StatusCode, agreed.Body)
	}
	againTicket := browserJSON(t, browser, http.MethodPost, ts.URL+"/api/auth/wechat/ticket", "", map[string]any{"ticket": ticket, "agree": true})
	if againTicket.StatusCode != 400 {
		t.Fatal(againTicket.Body)
	}
	me := getJSON(t, ts.URL+"/api/auth/me", token)
	if me.StatusCode != 200 {
		t.Fatal(me.Body)
	}

	key := strings.TrimRight(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), "=")
	app.Cfg.WeChatComponentAppID = "wxcomp"
	app.Cfg.WeChatComponentSecret = "csecret"
	app.Cfg.WeChatComponentToken = "ctoken"
	app.Cfg.WeChatComponentAESKey = key
	app.Cfg.WeChatComponentRedirect = ts.URL + "/api/auth/wechat/component/callback"
	early := postJSON(t, ts.URL+"/api/accounts/component/start", token, map[string]any{})
	if early.StatusCode != 400 {
		t.Fatal(early.Body)
	}
	plain := `<xml><AppId>wxcomp</AppId><InfoType>component_verify_ticket</InfoType><ComponentVerifyTicket>ticket-9</ComponentVerifyTicket></xml>`
	encrypted, signature, err := wxopen.EncryptMessage(key, "ctoken", "wxcomp", "11", "22", plain)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/wechat/component/notify?timestamp=11&nonce=22&msg_signature="+signature, strings.NewReader("<xml><Encrypt>"+encrypted+"</Encrypt></xml>"))
	if err != nil {
		t.Fatal(err)
	}
	notify, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(notify.Body)
	notify.Body.Close()
	if notify.StatusCode != 200 || string(body) != "success" {
		t.Fatal(notify.StatusCode, string(body))
	}
	stored, err := app.Store.GetSetting(context.Background(), nil, "wechat_component_ticket")
	if err != nil || stored != "ticket-9" {
		t.Fatal(stored, err)
	}
	startedBind := browserJSON(t, browser, http.MethodPost, ts.URL+"/api/accounts/component/start", token, map[string]any{})
	if startedBind.StatusCode != 200 {
		t.Fatal(startedBind.Body)
	}
	page, _ := startedBind.JSON["url"].(string)
	pageURL, err := url.Parse(page)
	if err != nil {
		t.Fatal(err)
	}
	redirect, err := url.Parse(pageURL.Query().Get("redirect_uri"))
	if err != nil {
		t.Fatal(err)
	}
	bindState := redirect.Query().Get("state")
	foreign, err := bare.Get(ts.URL + "/api/auth/wechat/component/callback?auth_code=auth1&state=" + url.QueryEscape(bindState))
	if err != nil {
		t.Fatal(err)
	}
	foreign.Body.Close()
	if foreign.StatusCode != http.StatusFound || !strings.Contains(foreign.Header.Get("Location"), "error=") {
		t.Fatal(foreign.StatusCode, foreign.Header.Get("Location"))
	}
	cb, err := browser.Get(ts.URL + "/api/auth/wechat/component/callback?auth_code=auth1&state=" + url.QueryEscape(bindState))
	if err != nil {
		t.Fatal(err)
	}
	cb.Body.Close()
	if cb.StatusCode != http.StatusFound || !strings.Contains(cb.Header.Get("Location"), "bound=1") {
		t.Fatal(cb.StatusCode, cb.Header.Get("Location"))
	}
	second, err := browser.Get(ts.URL + "/api/auth/wechat/component/callback?auth_code=auth1&state=" + url.QueryEscape(bindState))
	if err != nil {
		t.Fatal(err)
	}
	second.Body.Close()
	if second.StatusCode != http.StatusFound || !strings.Contains(second.Header.Get("Location"), "error=") {
		t.Fatal(second.StatusCode, second.Header.Get("Location"))
	}
	accounts := getJSON(t, ts.URL+"/api/accounts", token)
	if len(accounts.JSONList) != 1 || accounts.JSONList[0]["app_id"] != "wx_authorizer_1" {
		t.Fatalf("%s", accounts.Body)
	}
}

func TestSMSParallelGuessesStayAtFive(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	code := enableSMS(t, app)
	phone := "13900139000"
	sent := postJSON(t, ts.URL+"/api/auth/sms/send", "", map[string]any{"phone": phone})
	if sent.StatusCode != 200 || code() == "" {
		t.Fatal(sent.StatusCode, sent.Body, code())
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	badStatus := 0
	var mu sync.Mutex
	for i := 0; i < 20; i++ {
		wg.Add(1)
		guess := fmt.Sprintf("%06d", i)
		if guess == code() {
			guess = "999999"
		}
		go func(guess string) {
			defer wg.Done()
			<-start
			resp := postJSON(t, ts.URL+"/api/auth/sms/login", "", map[string]any{"phone": phone, "code": guess, "agree": true})
			if resp.StatusCode != 400 {
				mu.Lock()
				badStatus++
				mu.Unlock()
			}
		}(guess)
	}
	close(start)
	wg.Wait()
	if badStatus != 0 {
		t.Fatal(badStatus)
	}
	var attempts int
	if err := app.DB.QueryRow(context.Background(), `SELECT attempts FROM sms_code WHERE phone = ?`, phone).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 5 {
		t.Fatal(attempts)
	}
	correct := postJSON(t, ts.URL+"/api/auth/sms/login", "", map[string]any{"phone": phone, "code": code(), "agree": true})
	if correct.StatusCode != 400 {
		t.Fatal(correct.StatusCode, correct.Body)
	}
}

func TestSMSIPAndPhoneRateLimits(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	enableSMS(t, app)
	app.smsSendIP = newIPWindow(2, time.Hour)
	first := postJSON(t, ts.URL+"/api/auth/sms/send", "", map[string]any{"phone": "13700000001"})
	second := postJSON(t, ts.URL+"/api/auth/sms/send", "", map[string]any{"phone": "13700000002"})
	third := postJSON(t, ts.URL+"/api/auth/sms/send", "", map[string]any{"phone": "13700000003"})
	if first.StatusCode != 200 || second.StatusCode != 200 || third.StatusCode != 400 || !strings.Contains(third.Body, "尝试次数过多") {
		t.Fatal(first.StatusCode, second.StatusCode, third.Body)
	}
	app.smsLoginPhone = newIPWindow(2, time.Hour)
	phone := "13700000001"
	for i := 0; i < 2; i++ {
		resp := postJSON(t, ts.URL+"/api/auth/sms/login", "", map[string]any{"phone": phone, "code": "000000"})
		if resp.StatusCode != 400 || strings.Contains(resp.Body, "尝试次数过多") {
			t.Fatal(resp.Body)
		}
	}
	limited := postJSON(t, ts.URL+"/api/auth/sms/login", "", map[string]any{"phone": phone, "code": "000000"})
	if limited.StatusCode != 400 || !strings.Contains(limited.Body, "尝试次数过多") {
		t.Fatal(limited.Body)
	}
}

func enableSMS(t *testing.T, app *App) func() string {
	t.Helper()
	var code string
	var mu sync.Mutex
	sms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal([]byte(r.URL.Query().Get("TemplateParam")), &payload)
		mu.Lock()
		code = payload.Code
		mu.Unlock()
		_, _ = io.WriteString(w, `{"Code":"OK"}`)
	}))
	t.Cleanup(sms.Close)
	app.Cfg.SMSProvider = "aliyun"
	app.Cfg.SMSAccessKey = "id"
	app.Cfg.SMSAccessSecret = "secret"
	app.Cfg.SMSSignName = "公号智写"
	app.Cfg.SMSTemplateCode = "SMS_1"
	app.Cfg.SMSAPIBase = sms.URL
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return code
	}
}

func browserJSON(t *testing.T, client *http.Client, method, rawURL, token string, body any) apiResp {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, rawURL, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return readResp(t, resp)
}
