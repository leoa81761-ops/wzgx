package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/notify"
	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/sms"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

func (a *App) recoverSend(w http.ResponseWriter, r *http.Request) error {
	cfg := a.effective(r.Context())
	var body struct {
		Account string `json:"account"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if !a.smsSendIP.allow(clientIP(r)) {
		return bad("尝试次数过多，请稍后再试")
	}
	kind, account, err := classifyAccount(body.Account)
	if err != nil {
		return err
	}
	user, err := a.Store.FindUserByAccount(r.Context(), nil, account)
	if err != nil {
		return err
	}
	if user == nil {
		writeJSON(w, 200, map[string]any{"ok": true})
		return nil
	}
	if kind == "phone" && !cfg.SMSEnabled() {
		return bad("手机号找回需要先配置短信")
	}
	if kind == "email" && strings.TrimSpace(cfg.SMTPHost) == "" {
		return bad("邮箱找回需要先配置邮件")
	}
	code, err := a.issueRecovery(r, account, "reset")
	if err != nil {
		return err
	}
	if kind == "phone" {
		if err := sms.Send(r.Context(), a.HTTP, cfg, sms.Message{Phone: account, Code: code}); err != nil {
			return bad(err.Error())
		}
	} else if err := (notify.SMTP{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Password: cfg.SMTPPassword, From: cfg.SMTPFrom}).Send(r.Context(), notify.Message{
		UserID: user.ID, Kind: "recover", Title: "重置公号智写密码", Account: account, Body: "验证码 " + code + "，10 分钟内有效。",
	}); err != nil {
		return bad("邮件发送失败")
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) recoverReset(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Account  string `json:"account"`
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if utf8.RuneCountInString(body.Password) < 8 {
		return bad("密码至少 8 位")
	}
	_, account, err := classifyAccount(body.Account)
	if err != nil {
		return err
	}
	id, hash, ok := a.matchRecovery(r, account, "reset", body.Code)
	if !ok {
		return bad("验证码不正确或已过期")
	}
	hashed, err := security.HashPassword(body.Password)
	if err != nil {
		return err
	}
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		deleted, err := a.Store.DeleteRecoveryMatch(r.Context(), tx, id, hash)
		if err != nil {
			return err
		}
		if !deleted {
			return bad("验证码不正确或已过期")
		}
		user, err := a.Store.FindUserByAccount(r.Context(), tx, account)
		if err != nil {
			return err
		}
		if user == nil || !user.IsActive {
			return bad("验证码不正确或已过期")
		}
		return a.Store.UpdatePassword(r.Context(), tx, user.ID, hashed)
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) rebindSend(w http.ResponseWriter, r *http.Request, user *model.User) error {
	cfg := a.effective(r.Context())
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
	if !cfg.SMSEnabled() {
		return bad("换绑手机需要先配置短信")
	}
	if !a.smsSendIP.allow(clientIP(r)) {
		return bad("尝试次数过多，请稍后再试")
	}
	existing, err := a.Store.FindUserByAccount(r.Context(), nil, phone)
	if err != nil {
		return err
	}
	if existing != nil && existing.ID != user.ID {
		return bad("这个手机号已经注册")
	}
	key := "rebind:" + phone
	code, err := a.issueRecovery(r, key, "rebind")
	if err != nil {
		return err
	}
	if err := sms.Send(r.Context(), a.HTTP, cfg, sms.Message{Phone: phone, Code: code}); err != nil {
		return bad(err.Error())
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) rebindPhone(w http.ResponseWriter, r *http.Request, user *model.User) error {
	var body struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	phone := strings.TrimSpace(body.Phone)
	if !phoneRe.MatchString(phone) {
		return bad("请填写中国大陆手机号")
	}
	key := "rebind:" + phone
	id, hash, ok := a.matchRecovery(r, key, "rebind", body.Code)
	if !ok {
		return bad("验证码不正确或已过期")
	}
	err := a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		deleted, err := a.Store.DeleteRecoveryMatch(r.Context(), tx, id, hash)
		if err != nil {
			return err
		}
		if !deleted {
			return bad("验证码不正确或已过期")
		}
		existing, err := a.Store.FindUserByAccount(r.Context(), tx, phone)
		if err != nil {
			return err
		}
		if existing != nil && existing.ID != user.ID {
			return bad("这个手机号已经注册")
		}
		return a.Store.UpdatePhone(r.Context(), tx, user.ID, phone)
	})
	if err != nil {
		return err
	}
	user.Phone = phone
	writeJSON(w, 200, map[string]any{"ok": true, "user": userDict(user)})
	return nil
}

func (a *App) issueRecovery(r *http.Request, account, purpose string) (string, error) {
	code, err := lifeCode()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		recent, err := a.Store.CountRecoverySince(r.Context(), tx, account, now.Add(-60*time.Second))
		if err != nil {
			return err
		}
		if recent > 0 {
			return bad("请稍后再获取验证码")
		}
		day, err := a.Store.CountRecoverySince(r.Context(), tx, account, now.Add(-24*time.Hour))
		if err != nil {
			return err
		}
		if day >= 5 {
			return bad("这个手机号今天的验证码次数已用完")
		}
		return a.Store.InsertRecovery(r.Context(), tx, account, lifeHash(a.Cfg.MasterKey, account, code), purpose, now.Add(10*time.Minute), now)
	})
	if err != nil {
		return "", err
	}
	return code, nil
}

func (a *App) matchRecovery(r *http.Request, account, purpose, code string) (int64, string, bool) {
	if !a.smsLoginIP.allow(clientIP(r)) || !a.smsLoginPhone.allow(account) {
		return 0, "", false
	}
	id, hash, expires, err := a.Store.LatestRecovery(r.Context(), nil, account, purpose)
	if err != nil || id == 0 || time.Now().After(expires) {
		return 0, "", false
	}
	ok, err := a.Store.ConsumeRecoveryAttempt(r.Context(), id)
	if err != nil || !ok {
		return 0, "", false
	}
	expect := lifeHash(a.Cfg.MasterKey, account, strings.TrimSpace(code))
	if !hmac.Equal([]byte(hash), []byte(expect)) {
		return 0, "", false
	}
	return id, hash, true
}

func (a *App) requestRefund(w http.ResponseWriter, r *http.Request, user *model.User) error {
	now := time.Now().UTC()
	order, err := a.Store.LatestPaidOrder(r.Context(), nil, user.ID)
	if err != nil {
		return err
	}
	count, err := a.Store.CountPaidOrders(r.Context(), nil, user.ID)
	if err != nil {
		return err
	}
	if order == nil || order.PaidAt == nil || count != 1 || now.After(order.PaidAt.Add(7*24*time.Hour)) {
		return bad("只有首次购买且在 7 天内可以申请全额退款")
	}
	used, err := a.Store.CountConfirmedSince(r.Context(), nil, user.ID, *order.PaidAt)
	if err != nil {
		return err
	}
	if used > 0 {
		return bad("已经使用付费额度，不能全额退款")
	}
	changed, err := a.Store.RefundPayOrder(r.Context(), nil, order.ID)
	if err != nil {
		return err
	}
	if !changed {
		return bad("只能标记已支付的订单")
	}
	fresh, err := a.Store.GetUser(r.Context(), nil, user.ID)
	if err != nil || fresh == nil {
		return err
	}
	fresh.Tier = "free"
	fresh.TierExpiresAt = nil
	if err := a.Store.UpdateUser(r.Context(), nil, fresh); err != nil {
		return err
	}
	actor := user.ID
	_ = a.Store.InsertAudit(r.Context(), nil, model.AuditLog{
		ActorID: &actor, Action: "payment_refund", Target: "order:" + order.OutTradeNo,
		Detail: "首次购买 7 天内未使用，已标记退款并降为免费。退款需在支付平台手工操作。", CreatedAt: now,
	})
	writeJSON(w, 200, map[string]any{"ok": true, "status": "refunded"})
	return nil
}

func (a *App) withdrawConsent(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "id", "找不到这条同意记录")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	row, err := a.Store.WithdrawConsent(r.Context(), nil, user.ID, id, now)
	if err != nil {
		return err
	}
	if row == nil {
		return bad("这条同意已经撤回，或不存在")
	}
	if row.DocType == "auto_publish" {
		if err := a.Store.DisableUserPublish(r.Context(), nil, user.ID, now); err != nil {
			return err
		}
	}
	actor := user.ID
	_ = a.Store.InsertAudit(r.Context(), nil, model.AuditLog{
		ActorID: &actor, Action: "consent_withdraw", Target: "consent:" + row.DocType, Detail: row.ConsentItem, CreatedAt: now,
	})
	message := "已撤回。用户协议或隐私政策撤回后，需要重新同意才能继续使用；也可以注销账号。"
	writeJSON(w, 200, map[string]any{"ok": true, "message": message})
	return nil
}

func (a *App) adminSaveContract(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	id, err := pathID(r, "user_id", "找不到这个用户")
	if err != nil {
		return err
	}
	var body struct {
		Company    string `json:"company"`
		ContractNo string `json:"contract_no"`
		Note       string `json:"note"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	now := time.Now().UTC()
	row := store.Contract{
		UserID: id, Company: strings.TrimSpace(body.Company), ContractNo: strings.TrimSpace(body.ContractNo),
		Note: strings.TrimSpace(body.Note), UpdatedAt: now,
	}
	if err := a.Store.SaveContract(r.Context(), nil, row); err != nil {
		return err
	}
	actor := admin.ID
	_ = a.Store.InsertAudit(r.Context(), nil, model.AuditLog{
		ActorID: &actor, Action: "contract_save", Target: "user:" + strconv.FormatInt(id, 10), Detail: row.ContractNo, CreatedAt: now,
	})
	writeJSON(w, 200, map[string]any{"ok": true, "company": row.Company, "contract_no": row.ContractNo, "note": row.Note})
	return nil
}

func (a *App) adminGetContract(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	id, err := pathID(r, "user_id", "找不到这个用户")
	if err != nil {
		return err
	}
	row, err := a.Store.GetContract(r.Context(), id)
	if err != nil {
		return err
	}
	if row == nil {
		writeJSON(w, 200, map[string]any{"user_id": id, "company": "", "contract_no": "", "note": ""})
		return nil
	}
	writeJSON(w, 200, map[string]any{
		"user_id": row.UserID, "company": row.Company, "contract_no": row.ContractNo, "note": row.Note, "updated_at": jtimev(row.UpdatedAt),
	})
	return nil
}

func lifeCode() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, 6)
	for i, b := range buf {
		out[i] = '0' + b%10
	}
	return string(out), nil
}

func lifeHash(secret, account, code string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(account + ":" + code))
	return hex.EncodeToString(mac.Sum(nil))
}
