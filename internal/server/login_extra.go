package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/cover"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/growth"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/sms"
	"github.com/leoa81761-ops/wzgx/internal/store"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
	"github.com/leoa81761-ops/wzgx/internal/wxopen"
)

func (a *App) authOptions(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, 200, map[string]any{
		"password": true, "sms": a.Cfg.SMSEnabled(), "wechat_scan": a.Cfg.WeChatScanEnabled(), "wechat_component": a.Cfg.WeChatComponentEnabled(),
	})
	return nil
}

func (a *App) sendSMS(w http.ResponseWriter, r *http.Request) error {
	if !a.Cfg.SMSEnabled() {
		return bad("短信登录未开通")
	}
	var body struct {
		Phone string `json:"phone"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	phone := strings.TrimSpace(body.Phone)
	if !phoneRe.MatchString(phone) {
		return bad("请填写中国大陆手机号")
	}
	if !a.smsSendIP.allow(clientIP(r)) {
		return bad("尝试次数过多，请稍后再试")
	}
	now := time.Now().UTC()
	code, err := numericCode(6)
	if err != nil {
		return err
	}
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		recent, err := a.Store.CountSMSSince(r.Context(), tx, phone, now.Add(-60*time.Second))
		if err != nil {
			return err
		}
		if recent > 0 {
			return bad("请稍后再获取验证码")
		}
		day, err := a.Store.CountSMSSince(r.Context(), tx, phone, now.Add(-24*time.Hour))
		if err != nil {
			return err
		}
		if day >= 5 {
			return bad("这个手机号今天的验证码次数已用完")
		}
		return a.Store.ReplaceSMSCode(r.Context(), tx, phone, codeHash(a.Cfg.MasterKey, phone, code), now.Add(5*time.Minute), now)
	})
	if err != nil {
		return err
	}
	if err := sms.Send(r.Context(), a.HTTP, a.Cfg, sms.Message{Phone: phone, Code: code}); err != nil {
		return bad(err.Error())
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) loginSMS(w http.ResponseWriter, r *http.Request) error {
	if !a.Cfg.SMSEnabled() {
		return bad("短信登录未开通")
	}
	var body struct {
		Phone         string `json:"phone"`
		Code          string `json:"code"`
		Agree         bool   `json:"agree"`
		InviteCode    string `json:"invite_code"`
		DeviceID      string `json:"device_id"`
		DeviceConsent bool   `json:"device_consent"`
	}
	if !a.smsLoginIP.allow(clientIP(r)) {
		return bad("尝试次数过多，请稍后再试")
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	phone := strings.TrimSpace(body.Phone)
	if !phoneRe.MatchString(phone) || len(strings.TrimSpace(body.Code)) != 6 {
		return bad("请填写手机号和 6 位验证码")
	}
	if !a.smsLoginPhone.allow(phone) {
		return bad("尝试次数过多，请稍后再试")
	}
	now := time.Now().UTC()
	id, hash, expires, _, err := a.Store.LatestSMS(r.Context(), nil, phone)
	if err != nil {
		return err
	}
	if id == 0 || now.After(expires) {
		return bad("验证码不正确或已过期")
	}
	if _, ok, err := a.Store.ConsumeSMSAttempt(r.Context(), id); err != nil {
		return err
	} else if !ok {
		return bad("验证码不正确或已过期")
	}
	expect := codeHash(a.Cfg.MasterKey, phone, strings.TrimSpace(body.Code))
	if !hmac.Equal([]byte(hash), []byte(expect)) {
		return bad("验证码不正确或已过期")
	}
	var user *model.User
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		user, err = a.Store.FindUserByPhone(r.Context(), tx, phone)
		if err != nil {
			return err
		}
		if user != nil {
			if !user.IsActive {
				return &AppError{Status: 403, Message: "账号已停用"}
			}
			ok, err := a.Store.DeleteSMSMatch(r.Context(), tx, id, hash)
			if err != nil {
				return err
			}
			if !ok {
				return bad("验证码不正确或已过期")
			}
			return nil
		}
		if !body.Agree {
			return bad("新手机号请先勾选同意《用户协议》和《隐私政策》")
		}
		ok, err := a.Store.DeleteSMSMatch(r.Context(), tx, id, hash)
		if err != nil {
			return err
		}
		if !ok {
			return bad("验证码不正确或已过期")
		}
		pass, err := randomSecret()
		if err != nil {
			return err
		}
		hashed, err := security.HashPassword(pass)
		if err != nil {
			return err
		}
		user = &model.User{Phone: phone, PasswordHash: hashed, Tier: "free", IsActive: true, CreatedAt: now}
		if err := a.Store.InsertUser(r.Context(), tx, user); err != nil {
			return err
		}
		version := a.activeLegalVersion(r.Context())
		for _, docType := range []string{"user_agreement", "privacy"} {
			if err := a.Store.InsertConsent(r.Context(), tx, &model.Consent{
				UserID: user.ID, DocType: docType, Version: version, ConsentItem: "sms_register",
				IP: clientIP(r), UserAgent: userAgent(r), CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		deviceHash := deviceFrom(r, body.DeviceID, body.DeviceConsent)
		return attachInvite(r.Context(), a.Store, tx, user, body.InviteCode, deviceHash, growth.IPPrefix(clientIP(r)), now)
	})
	if err != nil {
		return err
	}
	token, err := security.MakeToken(user.ID, a.Cfg.JWTSecret, 7)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"token": token, "user": userDict(user)})
	return nil
}

func (a *App) wechatScanStart(w http.ResponseWriter, r *http.Request) error {
	if !a.Cfg.WeChatScanEnabled() {
		return bad("微信扫码登录未开通")
	}
	state, err := randomSecret()
	if err != nil {
		return err
	}
	nonce, err := randomSecret()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := a.Store.SaveOAuthState(r.Context(), nil, store.OAuthState{State: state, Kind: "qr", Nonce: nonce, Expires: now.Add(10 * time.Minute)}, now); err != nil {
		return err
	}
	setFlowCookie(w, wxScanCookie, nonce)
	writeJSON(w, 200, map[string]any{"url": wxopen.ScanURL(a.Cfg.WeChatOpenAppID, a.Cfg.WeChatOpenRedirect, state)})
	return nil
}

func (a *App) wechatScanCallback(w http.ResponseWriter, r *http.Request) error {
	if !a.Cfg.WeChatScanEnabled() {
		return bad("微信扫码登录未开通")
	}
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	now := time.Now().UTC()
	row, err := a.Store.ConsumeOAuthState(r.Context(), nil, state, "qr", flowCookie(r, wxScanCookie), now)
	if err != nil {
		return err
	}
	if row == nil {
		http.Redirect(w, r, "/login?error="+url.QueryEscape("扫码已过期，请重试"), http.StatusFound)
		return nil
	}
	profile, err := wxopen.ExchangeScan(r.Context(), a.HTTP, a.Cfg.WeChatAPIBase, a.Cfg.WeChatOpenAppID, a.Cfg.WeChatOpenSecret, code)
	if err != nil {
		http.Redirect(w, r, "/login?error="+url.QueryEscape(err.Error()), http.StatusFound)
		return nil
	}
	userID, err := a.Store.UserByWeChat(r.Context(), nil, profile.OpenID, profile.UnionID)
	if err != nil {
		return err
	}
	ticket, err := randomSecret()
	if err != nil {
		return err
	}
	ticketNonce, err := randomSecret()
	if err != nil {
		return err
	}
	payload := "new:" + profile.OpenID + ":" + profile.UnionID
	if userID > 0 {
		payload = "user:" + strconv.FormatInt(userID, 10)
	}
	if err := a.Store.SaveOAuthState(r.Context(), nil, store.OAuthState{State: ticket, Kind: "ticket", Payload: payload, Nonce: ticketNonce, Expires: now.Add(10 * time.Minute)}, now); err != nil {
		return err
	}
	setFlowCookie(w, wxScanCookie, ticketNonce)
	http.Redirect(w, r, "/login?ticket="+url.QueryEscape(ticket), http.StatusFound)
	return nil
}

func (a *App) wechatTicket(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Ticket string `json:"ticket"`
		Agree  bool   `json:"agree"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	now := time.Now().UTC()
	row, err := a.Store.GetOAuthState(r.Context(), nil, strings.TrimSpace(body.Ticket))
	if err != nil {
		return err
	}
	if row == nil || row.Kind != "ticket" || now.After(row.Expires) || row.Nonce == "" || row.Nonce != flowCookie(r, wxScanCookie) {
		return bad("扫码登录已过期，请重试")
	}
	var user *model.User
	if strings.HasPrefix(row.Payload, "user:") {
		consumed, err := a.Store.ConsumeOAuthState(r.Context(), nil, row.State, "ticket", row.Nonce, now)
		if err != nil {
			return err
		}
		if consumed == nil {
			return bad("扫码登录已过期，请重试")
		}
		id, _ := strconv.ParseInt(strings.TrimPrefix(consumed.Payload, "user:"), 10, 64)
		user, err = a.Store.GetUser(r.Context(), nil, id)
		if err != nil {
			return err
		}
		if user == nil || !user.IsActive {
			return &AppError{Status: 403, Message: "账号已停用"}
		}
	} else {
		parts := strings.SplitN(strings.TrimPrefix(row.Payload, "new:"), ":", 2)
		if len(parts) != 2 || parts[0] == "" {
			return bad("扫码登录已过期，请重试")
		}
		if !body.Agree {
			writeJSON(w, 200, map[string]any{"need_agree": true})
			return nil
		}
		err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
			pass, err := randomSecret()
			if err != nil {
				return err
			}
			hashed, err := security.HashPassword(pass)
			if err != nil {
				return err
			}
			user = &model.User{PasswordHash: hashed, Tier: "free", IsActive: true, CreatedAt: now}
			if err := a.Store.InsertUser(r.Context(), tx, user); err != nil {
				return err
			}
			if err := a.Store.SaveWeChatIdentity(r.Context(), tx, parts[0], parts[1], user.ID, now); err != nil {
				return err
			}
			version := a.activeLegalVersion(r.Context())
			for _, docType := range []string{"user_agreement", "privacy"} {
				if err := a.Store.InsertConsent(r.Context(), tx, &model.Consent{
					UserID: user.ID, DocType: docType, Version: version, ConsentItem: "wechat_register",
					IP: clientIP(r), UserAgent: userAgent(r), CreatedAt: now,
				}); err != nil {
					return err
				}
			}
			consumed, err := a.Store.ConsumeOAuthState(r.Context(), tx, row.State, "ticket", row.Nonce, now)
			if err != nil {
				return err
			}
			if consumed == nil {
				return bad("扫码登录已过期，请重试")
			}
			_, err = a.Store.EnsureInviteCode(r.Context(), tx, user.ID, "")
			return err
		})
		if err != nil {
			return err
		}
	}
	token, err := security.MakeToken(user.ID, a.Cfg.JWTSecret, 7)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"token": token, "user": userDict(user)})
	return nil
}

func (a *App) componentStart(w http.ResponseWriter, r *http.Request, user *model.User) error {
	if !a.Cfg.WeChatComponentEnabled() {
		return bad("微信开放平台授权未开通")
	}
	ticket, err := a.Store.GetSetting(r.Context(), nil, "wechat_component_ticket")
	if err != nil {
		return err
	}
	if ticket == "" {
		return bad("还没有收到微信的验证票据，请先在开放平台填写授权事件接收地址")
	}
	state, err := randomSecret()
	if err != nil {
		return err
	}
	nonce, err := randomSecret()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := a.Store.SaveOAuthState(r.Context(), nil, store.OAuthState{State: state, Kind: "component", UserID: user.ID, Nonce: nonce, Expires: now.Add(10 * time.Minute)}, now); err != nil {
		return err
	}
	setFlowCookie(w, wxBindCookie, nonce)
	redirect := a.Cfg.WeChatComponentRedirect
	if strings.Contains(redirect, "?") {
		redirect += "&state=" + url.QueryEscape(state)
	} else {
		redirect += "?state=" + url.QueryEscape(state)
	}
	link, err := wxopen.PreAuthURL(r.Context(), a.HTTP, a.Cfg.WeChatAPIBase, a.Cfg.WeChatComponentAppID, a.Cfg.WeChatComponentSecret, ticket, redirect)
	if err != nil {
		return bad(err.Error())
	}
	writeJSON(w, 200, map[string]any{"url": link})
	return nil
}

func (a *App) componentCallback(w http.ResponseWriter, r *http.Request) error {
	fail := func(msg string) error {
		http.Redirect(w, r, "/accounts?error="+url.QueryEscape(msg), http.StatusFound)
		return nil
	}
	if !a.Cfg.WeChatComponentEnabled() {
		return fail("微信开放平台授权未开通")
	}
	state := r.URL.Query().Get("state")
	authCode := r.URL.Query().Get("auth_code")
	row, err := a.Store.ConsumeOAuthState(r.Context(), nil, state, "component", flowCookie(r, wxBindCookie), time.Now().UTC())
	if err != nil {
		return err
	}
	if row == nil || row.UserID == 0 {
		return fail("授权已过期，请重试")
	}
	ticket, err := a.Store.GetSetting(r.Context(), nil, "wechat_component_ticket")
	if err != nil || ticket == "" {
		return fail("还没有收到微信的验证票据")
	}
	info, err := wxopen.QueryAuth(r.Context(), a.HTTP, a.Cfg.WeChatAPIBase, a.Cfg.WeChatComponentAppID, a.Cfg.WeChatComponentSecret, ticket, authCode)
	if err != nil {
		return fail(err.Error())
	}
	user, err := a.Store.GetUser(r.Context(), nil, row.UserID)
	if err != nil || user == nil {
		return fail("找不到账号")
	}
	now := time.Now().UTC()
	mode := "live"
	if a.Cfg.WeChatMode == "mock" || strings.HasPrefix(info.AppID, "wx_mock") {
		mode = "mock"
	}
	var client wechat.Client
	if mode == "mock" {
		client = wechat.Build(info.AppID, "component", mode, a.Cfg.WeChatAPIBase, a.HTTP, a.Tokens)
	} else {
		client = wechat.NewAuthorizer(info.AppID, info.RefreshToken, a.Cfg.WeChatComponentAppID, a.Cfg.WeChatComponentSecret, ticket, a.Cfg.WeChatAPIBase, a.HTTP, a.Tokens)
	}
	report := wechat.CheckPermissions(r.Context(), client, cover.CoverPNG("simple"))
	if !report.OK {
		return fail("授权成功，但草稿权限检测没有通过")
	}
	enc, err := security.EncryptSecret(a.Cfg.MasterKey, info.RefreshToken)
	if err != nil {
		return err
	}
	subject := "personal"
	if info.Verified {
		subject = "enterprise"
	}
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		prior, err := a.Store.ListAccountsByAppID(r.Context(), tx, info.AppID)
		if err != nil {
			return err
		}
		if err := a.assertCanBind(r.Context(), tx, user.ID, info.AppID, now); err != nil {
			return err
		}
		bound := now
		account := &model.WxAccount{
			UserID: user.ID, Name: info.Name, AppID: info.AppID, SubjectType: subject, ServiceType: info.ServiceType,
			Verified: info.Verified, DeliveryMode: report.DeliveryMode, BindStatus: "active",
			PermissionReport: permissionMap(report), BoundAt: &bound,
		}
		if err := a.Store.InsertAccount(r.Context(), tx, account); err != nil {
			return err
		}
		if err := a.Store.SaveAuthorizer(r.Context(), tx, account.ID, enc, now); err != nil {
			return err
		}
		if err := a.notePriorAppID(r.Context(), tx, user.ID, len(prior) > 0); err != nil {
			return err
		}
		return a.Store.InsertConsent(r.Context(), tx, &model.Consent{
			UserID: user.ID, DocType: "wx_credentials", Version: catalog.LegalVersion, ConsentItem: "wx_component",
			IP: clientIP(r), UserAgent: userAgent(r), CreatedAt: now,
		})
	})
	if err != nil {
		if appErr, ok := err.(*AppError); ok {
			return fail(appErr.Message)
		}
		return err
	}
	http.Redirect(w, r, "/accounts?bound=1", http.StatusFound)
	return nil
}

func (a *App) componentNotify(w http.ResponseWriter, r *http.Request) error {
	if !a.Cfg.WeChatComponentEnabled() {
		http.Error(w, "未开通", http.StatusNotFound)
		return nil
	}
	timestamp := r.URL.Query().Get("timestamp")
	nonce := r.URL.Query().Get("nonce")
	signature := r.URL.Query().Get("msg_signature")
	if r.Method == http.MethodGet {
		plain, err := wxopen.DecryptMessage(a.Cfg.WeChatComponentAESKey, a.Cfg.WeChatComponentToken, timestamp, nonce, signature, r.URL.Query().Get("echostr"))
		if err != nil {
			http.Error(w, "签名不正确", http.StatusForbidden)
			return nil
		}
		_, _ = w.Write(plain)
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	encrypted := wxopen.ExtractEncrypt(raw)
	plain, err := wxopen.DecryptMessage(a.Cfg.WeChatComponentAESKey, a.Cfg.WeChatComponentToken, timestamp, nonce, signature, encrypted)
	if err != nil {
		http.Error(w, "签名不正确", http.StatusForbidden)
		return nil
	}
	ticket, err := wxopen.ParseTicket(plain)
	if err == nil && ticket.InfoType == "component_verify_ticket" && ticket.Value != "" {
		if err := a.Store.SetSetting(r.Context(), nil, "wechat_component_ticket", ticket.Value, time.Now().UTC()); err != nil {
			return err
		}
	}
	_, _ = w.Write([]byte("success"))
	return nil
}

func (a *App) accountWeChatClient(ctx context.Context, account *model.WxAccount, secret string) (wechat.Client, error) {
	wx, err := a.Cache.WeChat(ctx, nil)
	if err != nil {
		return nil, err
	}
	mode := "live"
	if wx.Mode == "mock" || strings.HasPrefix(account.AppID, "wx_mock") {
		mode = "mock"
	}
	refresh := ""
	if secret == "" {
		enc, err := a.Store.AuthorizerRefresh(ctx, nil, account.ID)
		if err != nil {
			return nil, err
		}
		if enc != "" {
			refresh, err = security.DecryptSecret(a.Cfg.MasterKey, enc)
			if err != nil {
				return nil, bad(err.Error())
			}
		}
	}
	base := wx.Base
	if base == "" {
		base = "https://api.weixin.qq.com"
	}
	if refresh != "" && a.Cfg.WeChatComponentEnabled() && mode != "mock" && !strings.HasPrefix(account.AppID, "wx_mock") {
		ticket, err := a.Store.GetSetting(ctx, nil, "wechat_component_ticket")
		if err != nil {
			return nil, err
		}
		return wechat.NewAuthorizer(account.AppID, refresh, a.Cfg.WeChatComponentAppID, a.Cfg.WeChatComponentSecret, ticket, base, a.HTTP, a.Tokens), nil
	}
	if secret == "" {
		if refresh == "" {
			return nil, bad("请重新填写 AppSecret")
		}
		secret = "component"
	}
	return wechat.Build(account.AppID, secret, mode, base, a.HTTP, a.Tokens), nil
}

const (
	wxScanCookie = "wx_scan"
	wxBindCookie = "wx_bind"
)

func setFlowCookie(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: 600, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func flowCookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil || c == nil {
		return ""
	}
	return c.Value
}

func codeHash(secret, phone, code string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(phone + ":" + code))
	return hex.EncodeToString(mac.Sum(nil))
}

func numericCode(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = '0' + b%10
	}
	return string(out), nil
}

func randomSecret() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
