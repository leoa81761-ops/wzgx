package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
	started := getJSON(t, ts.URL+"/api/auth/wechat/start", "")
	if started.StatusCode != 200 {
		t.Fatal(started.Body)
	}
	link, _ := started.JSON["url"].(string)
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	state := parsed.Query().Get("state")
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(ts.URL + "/api/auth/wechat/callback?code=abc&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatal(resp.StatusCode, resp.Header.Get("Location"))
	}
	ticket := loc.Query().Get("ticket")
	need := postJSON(t, ts.URL+"/api/auth/wechat/ticket", "", map[string]any{"ticket": ticket})
	if need.StatusCode != 200 || need.JSON["need_agree"] != true {
		t.Fatal(need.StatusCode, need.Body)
	}
	agreed := postJSON(t, ts.URL+"/api/auth/wechat/ticket", "", map[string]any{"ticket": ticket, "agree": true})
	token, _ := agreed.JSON["token"].(string)
	if agreed.StatusCode != 200 || token == "" {
		t.Fatal(agreed.StatusCode, agreed.Body)
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
	startedBind := postJSON(t, ts.URL+"/api/accounts/component/start", token, map[string]any{})
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
	cb, err := noFollow.Get(ts.URL + "/api/auth/wechat/component/callback?auth_code=auth1&state=" + url.QueryEscape(redirect.Query().Get("state")))
	if err != nil {
		t.Fatal(err)
	}
	cb.Body.Close()
	if cb.StatusCode != http.StatusFound || !strings.Contains(cb.Header.Get("Location"), "bound=1") {
		t.Fatal(cb.StatusCode, cb.Header.Get("Location"))
	}
	accounts := getJSON(t, ts.URL+"/api/accounts", token)
	if len(accounts.JSONList) != 1 || accounts.JSONList[0]["app_id"] != "wx_authorizer_1" {
		t.Fatalf("%s", accounts.Body)
	}
}
