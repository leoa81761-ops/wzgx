package server

import (
	"crypto/hmac"
	"net/http"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/notify"
	"github.com/leoa81761-ops/wzgx/internal/sms"
)

func registerCodeKey(account string) string { return "reg:" + account }

func (a *App) sendRegisterCode(w http.ResponseWriter, r *http.Request) error {
	cfg := a.effective(r.Context())
	if !cfg.RegisterVerify {
		return bad("注册验证码未开启")
	}
	var body struct {
		Account string `json:"account"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	kind, account, err := classifyAccount(body.Account)
	if err != nil {
		return err
	}
	if kind == "phone" && !cfg.SMSEnabled() {
		return bad("手机注册需要短信验证码，短信还没配好")
	}
	if kind == "email" && strings.TrimSpace(cfg.SMTPHost) == "" {
		return bad("邮箱注册需要邮件验证码，邮件还没配好")
	}
	if !a.smsSendIP.allow(clientIP(r)) {
		return bad("尝试次数过多，请稍后再试")
	}
	now := time.Now().UTC()
	code, err := numericCode(6)
	if err != nil {
		return err
	}
	key := registerCodeKey(account)
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		recent, err := a.Store.CountSMSSince(r.Context(), tx, key, now.Add(-60*time.Second))
		if err != nil {
			return err
		}
		if recent > 0 {
			return bad("请稍后再获取验证码")
		}
		day, err := a.Store.CountSMSSince(r.Context(), tx, key, now.Add(-24*time.Hour))
		if err != nil {
			return err
		}
		if day >= 5 {
			return bad("这个账号今天的验证码次数已用完")
		}
		return a.Store.ReplaceSMSCode(r.Context(), tx, key, codeHash(a.Cfg.MasterKey, key, code), now.Add(5*time.Minute), now)
	})
	if err != nil {
		return err
	}
	if kind == "phone" {
		if err := sms.Send(r.Context(), a.HTTP, cfg, sms.Message{Phone: account, Code: code}); err != nil {
			return bad(err.Error())
		}
	} else if err := (notify.SMTP{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Password: cfg.SMTPPassword, From: cfg.SMTPFrom}).Send(r.Context(), notify.Message{
		Account: account, Title: "注册验证码", Body: "验证码 " + code + "，5 分钟内有效。",
	}); err != nil {
		return bad("邮件发送失败")
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) matchRegisterCode(r *http.Request, cfg config.Settings, account, code string) (int64, string, error) {
	if !a.smsLoginIP.allow(clientIP(r)) {
		return 0, "", bad("尝试次数过多，请稍后再试")
	}
	if !a.smsLoginPhone.allow(account) {
		return 0, "", bad("尝试次数过多，请稍后再试")
	}
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, "", bad("请填写 6 位验证码")
	}
	key := registerCodeKey(account)
	now := time.Now().UTC()
	id, hash, expires, _, err := a.Store.LatestSMS(r.Context(), nil, key)
	if err != nil {
		return 0, "", err
	}
	if id == 0 || now.After(expires) {
		return 0, "", bad("验证码不正确或已过期")
	}
	if _, ok, err := a.Store.ConsumeSMSAttempt(r.Context(), id); err != nil {
		return 0, "", err
	} else if !ok {
		return 0, "", bad("验证码不正确或已过期")
	}
	expect := codeHash(cfg.MasterKey, key, code)
	if !hmac.Equal([]byte(hash), []byte(expect)) {
		return 0, "", bad("验证码不正确或已过期")
	}
	return id, hash, nil
}
