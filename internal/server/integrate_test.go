package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
)

func TestIntegrationOverridesEnvAndMasksSecrets(t *testing.T) {
	app := openApp(t, config.Settings{})
	app.Cfg.SMSAccessKey = "env-access-key"
	app.Cfg.SMSAccessSecret = "env-access-secret-value"
	app.Cfg.SMSSignName = "环境签名"
	app.Cfg.SMSTemplateCode = "SMS_ENV"
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	token := admin.JSON["token"].(string)
	before := getJSON(t, ts.URL+"/api/auth/options", "")
	if before.JSON["sms"] != false || before.JSON["register_verify"] != false {
		t.Fatalf("options %#v", before.JSON)
	}
	denied := putJSON(t, ts.URL+"/api/admin/integrations/register", token, map[string]any{"fields": map[string]string{"verify": "1"}})
	if denied.StatusCode != 400 || !strings.Contains(denied.Body, "短信或邮件") {
		t.Fatal(denied.StatusCode, denied.Body)
	}
	sms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Code":"OK"}`))
	}))
	defer sms.Close()
	saved := putJSON(t, ts.URL+"/api/admin/integrations/sms", token, map[string]any{"fields": map[string]string{
		"provider": "aliyun", "api_base": sms.URL, "access_secret": "",
	}})
	if saved.StatusCode != 200 {
		t.Fatal(saved.StatusCode, saved.Body)
	}
	if strings.Contains(saved.Body, "env-access-secret-value") || strings.Contains(saved.Body, "env-access-key") {
		t.Fatal(saved.Body)
	}
	after := getJSON(t, ts.URL+"/api/auth/options", "")
	if after.JSON["sms"] != true {
		t.Fatal(after.JSON)
	}
	again := putJSON(t, ts.URL+"/api/admin/integrations/sms", token, map[string]any{"fields": map[string]string{
		"provider": "aliyun", "access_secret": "",
	}})
	if again.StatusCode != 200 || strings.Contains(again.Body, "env-access-secret-value") {
		t.Fatal(again.StatusCode, again.Body)
	}
	logs := getJSON(t, ts.URL+"/api/admin/audit-logs", token)
	found := false
	for _, row := range logs.JSONList {
		if row["action"] == "integration_update" && row["target"] == "service_config:sms" {
			found = true
			detail, _ := row["detail"].(string)
			if !strings.Contains(detail, "provider") || strings.Contains(detail, "env-access") || strings.Contains(detail, "aliyun") {
				t.Fatal(detail)
			}
		}
	}
	if !found {
		t.Fatal(logs.Body)
	}
	user := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "reader2@example.com", "password": "password12", "agree": true})
	if user.StatusCode != 200 {
		t.Fatal(user.StatusCode, user.Body)
	}
	badKey := putJSON(t, ts.URL+"/api/admin/integrations/wechat_component", token, map[string]any{"fields": map[string]string{"aes_key": "short"}})
	if badKey.StatusCode != 400 || !strings.Contains(badKey.Body, "43") {
		t.Fatal(badKey.StatusCode, badKey.Body)
	}
	badV3 := putJSON(t, ts.URL+"/api/admin/integrations/wxpay", token, map[string]any{"fields": map[string]string{"api_v3_key": "too-short"}})
	if badV3.StatusCode != 400 || !strings.Contains(badV3.Body, "32") {
		t.Fatal(badV3.StatusCode, badV3.Body)
	}
	badPEM := putJSON(t, ts.URL+"/api/admin/integrations/wxpay", token, map[string]any{"fields": map[string]string{"private_key": "-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----"}})
	if badPEM.StatusCode != 400 || !strings.Contains(badPEM.Body, "PEM") {
		t.Fatal(badPEM.StatusCode, badPEM.Body)
	}
}

func TestIntegrationProbesAndRegisterCode(t *testing.T) {
	app := openApp(t, config.Settings{})
	secret := "db-sms-secret-value"
	var sawCode string
	smsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal([]byte(r.URL.Query().Get("TemplateParam")), &payload)
		sawCode = payload.Code
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Code":"OK"}`))
	}))
	defer smsSrv.Close()
	hookHits := 0
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hookHits++
		w.WriteHeader(204)
	}))
	defer hook.Close()
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/v3/pay/transactions/out-trade-no/"):
			if !strings.Contains(r.Header.Get("Authorization"), "WECHATPAY2-SHA256-RSA2048") {
				http.Error(w, `{"code":"SIGN_ERROR","message":"签名错误"}`, 401)
				return
			}
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"code":"ORDER_NOT_EXIST","message":"订单不存在"}`))
		case r.URL.Path == "/gateway":
			_ = r.ParseForm()
			if r.Form.Get("method") != "alipay.trade.query" || r.Form.Get("sign") == "" {
				http.Error(w, "bad", 400)
				return
			}
			_, _ = w.Write([]byte(`{"alipay_trade_query_response":{"code":"40004","sub_code":"ACQ.TRADE_NOT_EXIST","msg":"Business Failed"}}`))
		case strings.Contains(r.URL.Path, "component/api_component_token"):
			_, _ = w.Write([]byte(`{"component_access_token":"comp-token"}`))
		default:
			if r.URL.Query().Get("secret") == "" {
				http.Error(w, `{"errcode":40013}`, 200)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":7200}`))
		}
	}))
	defer tokenSrv.Close()
	smtpHost, smtpPort, smtpBody := serveSMTP(t)
	app.Cfg.WeChatAPIBase = tokenSrv.URL
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	token := admin.JSON["token"].(string)
	user := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "plain@example.com", "password": "password12", "agree": true})
	if user.StatusCode != 200 {
		t.Fatal(user.StatusCode, user.Body)
	}
	userToken := user.JSON["token"].(string)
	forbidden := putJSON(t, ts.URL+"/api/admin/integrations/sms", userToken, map[string]any{"fields": map[string]string{"provider": "aliyun"}})
	if forbidden.StatusCode != 403 {
		t.Fatal(forbidden.StatusCode, forbidden.Body)
	}
	smsSave := putJSON(t, ts.URL+"/api/admin/integrations/sms", token, map[string]any{"fields": map[string]string{
		"provider": "aliyun", "access_key": "sms-key", "access_secret": secret,
		"sign_name": "公号智写", "template_code": "SMS_1", "api_base": smsSrv.URL,
	}})
	if smsSave.StatusCode != 200 || strings.Contains(smsSave.Body, secret) {
		t.Fatal(smsSave.StatusCode, smsSave.Body)
	}
	keep := putJSON(t, ts.URL+"/api/admin/integrations/sms", token, map[string]any{"fields": map[string]string{
		"provider": "aliyun", "access_secret": "",
	}})
	if keep.StatusCode != 200 || strings.Contains(keep.Body, secret) {
		t.Fatal(keep.StatusCode, keep.Body)
	}
	probe := postJSON(t, ts.URL+"/api/admin/integrations/sms/test", token, map[string]any{"phone": "13800138000"})
	if probe.StatusCode != 200 {
		t.Fatal(probe.StatusCode, probe.Body)
	}
	on := putJSON(t, ts.URL+"/api/admin/integrations/register", token, map[string]any{"fields": map[string]string{"verify": "1"}})
	if on.StatusCode != 200 {
		t.Fatal(on.StatusCode, on.Body)
	}
	options := getJSON(t, ts.URL+"/api/auth/options", "")
	if options.JSON["register_verify"] != true || options.JSON["sms"] != true {
		t.Fatal(options.JSON)
	}
	missing := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "13800138009", "password": "password12", "agree": true})
	if missing.StatusCode != 400 {
		t.Fatal(missing.StatusCode, missing.Body)
	}
	sent := postJSON(t, ts.URL+"/api/auth/register/code", "", map[string]any{"account": "13800138009"})
	if sent.StatusCode != 200 || len(sawCode) != 6 {
		t.Fatal(sent.StatusCode, sent.Body, sawCode)
	}
	code := sawCode
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, _ := json.Marshal(map[string]any{"account": "13800138009", "password": "password12", "agree": true, "code": "000000"})
			resp, err := http.Post(ts.URL+"/api/auth/register", "application/json", strings.NewReader(string(raw)))
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()
	_, _, _, attempts, err := app.Store.LatestSMS(context.Background(), nil, registerCodeKey("13800138009"))
	if err != nil || attempts != 5 {
		t.Fatal(attempts, err)
	}
	blocked := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "13800138009", "password": "password12", "agree": true, "code": code})
	if blocked.StatusCode != 400 {
		t.Fatal(blocked.StatusCode, blocked.Body)
	}
	sent2 := postJSON(t, ts.URL+"/api/auth/register/code", "", map[string]any{"account": "13900139000"})
	if sent2.StatusCode != 200 || len(sawCode) != 6 {
		t.Fatal(sent2.StatusCode, sent2.Body, sawCode)
	}
	okReg := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "13900139000", "password": "password12", "agree": true, "code": sawCode})
	if okReg.StatusCode != 200 {
		t.Fatal(okReg.StatusCode, okReg.Body)
	}
	mailSave := putJSON(t, ts.URL+"/api/admin/integrations/smtp", token, map[string]any{"fields": map[string]string{
		"host": smtpHost, "port": smtpPort, "from": "bot@example.com",
	}})
	if mailSave.StatusCode != 200 {
		t.Fatal(mailSave.StatusCode, mailSave.Body)
	}
	mailProbe := postJSON(t, ts.URL+"/api/admin/integrations/smtp/test", token, map[string]any{})
	if mailProbe.StatusCode != 200 {
		t.Fatal(mailProbe.StatusCode, mailProbe.Body)
	}
	blockedMail := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "new-mail@example.com", "password": "password12", "agree": true})
	if blockedMail.StatusCode != 400 {
		t.Fatal(blockedMail.StatusCode, blockedMail.Body)
	}
	mailCode := postJSON(t, ts.URL+"/api/auth/register/code", "", map[string]any{"account": "new-mail@example.com"})
	if mailCode.StatusCode != 200 || !strings.Contains(smtpBody(), "验证码") {
		t.Fatal(mailCode.StatusCode, mailCode.Body, smtpBody())
	}
	mailDigits := ""
	bodyText := smtpBody()
	for i := 0; i+6 <= len(bodyText); i++ {
		part := bodyText[i : i+6]
		if strings.Trim(part, "0123456789") == "" {
			mailDigits = part
		}
	}
	mailReg := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{"account": "new-mail@example.com", "password": "password12", "agree": true, "code": mailDigits})
	if mailReg.StatusCode != 200 {
		t.Fatal(mailReg.StatusCode, mailReg.Body)
	}
	hookSave := putJSON(t, ts.URL+"/api/admin/integrations/notify", token, map[string]any{"fields": map[string]string{"webhook_url": hook.URL + "/hook/secret-token-value"}})
	if hookSave.StatusCode != 200 || strings.Contains(hookSave.Body, "secret-token-value") {
		t.Fatal(hookSave.StatusCode, hookSave.Body)
	}
	hookProbe := postJSON(t, ts.URL+"/api/admin/integrations/notify/test", token, map[string]any{})
	if hookProbe.StatusCode != 200 || hookHits != 1 {
		t.Fatal(hookProbe.StatusCode, hookProbe.Body, hookHits)
	}
	app.credTest = newIPWindow(1, time.Hour)
	first := postJSON(t, ts.URL+"/api/admin/integrations/notify/test", token, map[string]any{})
	second := postJSON(t, ts.URL+"/api/admin/integrations/notify/test", token, map[string]any{})
	if first.StatusCode != 200 || second.StatusCode != 400 || !strings.Contains(second.Body, "频繁") {
		t.Fatal(first.StatusCode, second.StatusCode, second.Body)
	}
	privPEM, certPEM := testPayMaterial(t)
	pubPEM := testPublicPEM(t, privPEM)
	wx := putJSON(t, ts.URL+"/api/admin/integrations/wxpay", token, map[string]any{"fields": map[string]string{
		"mch_id": "mch", "app_id": "wxapp", "serial": "SERIAL", "api_v3_key": "0123456789abcdef0123456789abcdef",
		"private_key": privPEM, "platform_cert": certPEM, "notify_url": "https://example.com/api/pay/wechat/notify",
		"api_base": tokenSrv.URL,
	}})
	if wx.StatusCode != 200 || strings.Contains(wx.Body, "0123456789abcdef0123456789abcdef") {
		t.Fatal(wx.StatusCode, wx.Body)
	}
	app.credTest = newIPWindow(8, time.Hour)
	if resp := postJSON(t, ts.URL+"/api/admin/integrations/wxpay/test", token, map[string]any{}); resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode, resp.Body)
	}
	ali := putJSON(t, ts.URL+"/api/admin/integrations/alipay", token, map[string]any{"fields": map[string]string{
		"app_id": "2021001", "private_key": privPEM, "public_key": pubPEM,
		"notify_url": "https://example.com/api/pay/alipay/notify", "gateway": tokenSrv.URL + "/gateway",
	}})
	if ali.StatusCode != 200 {
		t.Fatal(ali.StatusCode, ali.Body)
	}
	if resp := postJSON(t, ts.URL+"/api/admin/integrations/alipay/test", token, map[string]any{}); resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode, resp.Body)
	}
	openSave := putJSON(t, ts.URL+"/api/admin/integrations/wechat_open", token, map[string]any{"fields": map[string]string{
		"app_id": "wxopen", "secret": "open-secret-value", "redirect_url": "https://example.com/api/auth/wechat/callback",
	}})
	if openSave.StatusCode != 200 || strings.Contains(openSave.Body, "open-secret-value") {
		t.Fatal(openSave.StatusCode, openSave.Body)
	}
	if resp := postJSON(t, ts.URL+"/api/admin/integrations/wechat_open/test", token, map[string]any{}); resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode, resp.Body)
	}
	aesKey := "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	comp := putJSON(t, ts.URL+"/api/admin/integrations/wechat_component", token, map[string]any{"fields": map[string]string{
		"app_id": "wxcomp", "secret": "comp-secret-value", "token": "ctoken", "aes_key": aesKey,
		"redirect_url": "https://example.com/api/auth/wechat/component/callback",
	}})
	if comp.StatusCode != 200 || strings.Contains(comp.Body, "comp-secret-value") || strings.Contains(comp.Body, aesKey) {
		t.Fatal(comp.StatusCode, comp.Body)
	}
	if err := app.Store.SetSetting(context.Background(), nil, "wechat_component_ticket", "ticket-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if resp := postJSON(t, ts.URL+"/api/admin/integrations/wechat_component/test", token, map[string]any{}); resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode, resp.Body)
	}
	tpl := putJSON(t, ts.URL+"/api/admin/integrations/wechat_tpl", token, map[string]any{"fields": map[string]string{
		"app_id": "wxtpl", "secret": "tpl-secret-value", "template_id": "TPL_1", "api_base": tokenSrv.URL,
	}})
	if tpl.StatusCode != 200 || strings.Contains(tpl.Body, "tpl-secret-value") {
		t.Fatal(tpl.StatusCode, tpl.Body)
	}
	if resp := postJSON(t, ts.URL+"/api/admin/integrations/wechat_tpl/test", token, map[string]any{}); resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode, resp.Body)
	}
	if getJSON(t, ts.URL+"/api/auth/options", "").JSON["wechat_scan"] != true {
		t.Fatal("scan did not flip on")
	}
}

func serveSMTP(t *testing.T) (string, string, func() string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var bodyMu sync.Mutex
	last := ""
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
				_, _ = rw.WriteString("220 localhost\r\n")
				_ = rw.Flush()
				for {
					line, err := rw.ReadString('\n')
					if err != nil {
						return
					}
					upper := strings.ToUpper(line)
					switch {
					case strings.HasPrefix(upper, "DATA"):
						_, _ = rw.WriteString("354 go\r\n")
						_ = rw.Flush()
						var msg strings.Builder
						for {
							part, err := rw.ReadString('\n')
							if err != nil || strings.TrimSpace(part) == "." {
								break
							}
							msg.WriteString(part)
						}
						bodyMu.Lock()
						last = msg.String()
						bodyMu.Unlock()
						_, _ = rw.WriteString("250 queued\r\n")
					case strings.HasPrefix(upper, "QUIT"):
						_, _ = rw.WriteString("221 bye\r\n")
						_ = rw.Flush()
						return
					default:
						_, _ = rw.WriteString("250 ok\r\n")
					}
					_ = rw.Flush()
				}
			}(conn)
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	return host, port, func() string {
		bodyMu.Lock()
		defer bodyMu.Unlock()
		return last
	}
}

func TestSearchIntegrationProbe(t *testing.T) {
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") == "" {
			t.Errorf("query %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`<li class="b_algo"><h2><a href="https://www.mohurd.gov.cn/a">综合布线验收</a></h2><p>公开说明。</p></li>`))
	}))
	defer search.Close()
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	token := admin.JSON["token"].(string)
	saved := putJSON(t, ts.URL+"/api/admin/integrations/search", token, map[string]any{"fields": map[string]string{"primary_url": search.URL}})
	if saved.StatusCode != 200 {
		t.Fatal(saved.StatusCode, saved.Body)
	}
	probe := postJSON(t, ts.URL+"/api/admin/integrations/search/test", token, map[string]any{})
	if probe.StatusCode != 200 || !strings.Contains(probe.Body, "连通成功") {
		t.Fatal(probe.StatusCode, probe.Body)
	}
}

func testPayMaterial(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	priv := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return string(priv), string(cert)
}

func testPublicPEM(t *testing.T, privPEM string) string {
	t.Helper()
	block, _ := pem.Decode([]byte(privPEM))
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}
