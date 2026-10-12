package server

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/growth"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/security"
)

var (
	phoneRe = regexp.MustCompile(`^1[3-9]\d{9}$`)
	emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

func invalid() error { return &AppError{Status: 422, Message: "请求参数不正确"} }

func classifyAccount(value string) (kind, account string, err error) {
	raw := strings.TrimSpace(value)
	if phoneRe.MatchString(raw) {
		return "phone", raw, nil
	}
	if emailRe.MatchString(raw) {
		return "email", strings.ToLower(raw), nil
	}
	return "", "", bad("请输入有效的手机号或邮箱")
}

var legalDocs = []string{"user_agreement", "privacy"}

func (a *App) needsReconsent(ctx context.Context, userID int64) (bool, error) {
	for _, docType := range legalDocs {
		ok, err := a.Store.HasConsentVersion(ctx, userID, docType, a.activeLegalVersion(ctx))
		if err != nil {
			return false, err
		}
		if !ok {
			return true, nil
		}
	}
	return false, nil
}

func (a *App) consentStatus(w http.ResponseWriter, r *http.Request, user *model.User) error {
	needs, err := a.needsReconsent(r.Context(), user.ID)
	if err != nil {
		return err
	}
	version := a.activeLegalVersion(r.Context())
	missing := []string{}
	if needs {
		for _, docType := range legalDocs {
			ok, err := a.Store.HasConsentVersion(r.Context(), user.ID, docType, version)
			if err != nil {
				return err
			}
			if !ok {
				missing = append(missing, docType)
			}
		}
	}
	writeJSON(w, 200, map[string]any{
		"legal_version": version, "needs_reconsent": needs, "missing": missing,
	})
	return nil
}

func (a *App) reconsent(w http.ResponseWriter, r *http.Request, user *model.User) error {
	var body struct {
		Agree bool `json:"agree"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if !body.Agree {
		return bad("请先阅读并勾选同意更新后的《用户协议》和《隐私政策》")
	}
	now := time.Now().UTC()
	version := a.activeLegalVersion(r.Context())
	err := a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		for _, docType := range legalDocs {
			ok, err := a.Store.HasConsentVersion(r.Context(), user.ID, docType, version)
			if err != nil {
				return err
			}
			if ok {
				continue
			}
			if err := a.Store.InsertConsent(r.Context(), tx, &model.Consent{
				UserID: user.ID, DocType: docType, Version: version, ConsentItem: "reconsent",
				IP: clientIP(r), UserAgent: userAgent(r), CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true, "needs_reconsent": false, "legal_version": version})
	return nil
}

func userDict(u *model.User) map[string]any {
	account := u.Email
	if account == "" {
		account = u.Phone
	}
	var email, phone any
	if u.Email != "" {
		email = u.Email
	}
	if u.Phone != "" {
		phone = u.Phone
	}
	return map[string]any{
		"id": u.ID, "email": email, "phone": phone, "account": account,
		"tier": u.Tier, "is_admin": u.IsAdmin, "tier_expires_at": jtime(u.TierExpiresAt),
	}
}

type registerBody struct {
	Account       string `json:"account"`
	Password      string `json:"password"`
	Agree         bool   `json:"agree"`
	InviteCode    string `json:"invite_code"`
	DeviceID      string `json:"device_id"`
	DeviceConsent bool   `json:"device_consent"`
	Code          string `json:"code"`
}

type loginBody struct {
	Account  string `json:"account"`
	Password string `json:"password"`
}

func (a *App) register(w http.ResponseWriter, r *http.Request) error {
	var body registerBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if utf8.RuneCountInString(strings.TrimSpace(body.Account)) < 3 || utf8.RuneCountInString(body.Account) > 255 || utf8.RuneCountInString(body.Password) < 8 || utf8.RuneCountInString(body.Password) > 72 {
		return invalid()
	}
	if !body.Agree {
		return bad("请先阅读并勾选同意《用户协议》和《隐私政策》")
	}
	kind, account, err := classifyAccount(body.Account)
	if err != nil {
		return err
	}
	cfg := a.effective(r.Context())
	var codeID int64
	var codeHashValue string
	if cfg.RegisterVerify {
		if kind == "phone" && !cfg.SMSEnabled() {
			return bad("手机注册需要短信验证码，短信还没配好")
		}
		if kind == "email" && strings.TrimSpace(cfg.SMTPHost) == "" {
			return bad("邮箱注册需要邮件验证码，邮件还没配好")
		}
		codeID, codeHashValue, err = a.matchRegisterCode(r, cfg, account, body.Code)
		if err != nil {
			return err
		}
	}
	hash, err := security.HashPassword(body.Password)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	user := &model.User{PasswordHash: hash, Tier: "free", IsActive: true, CreatedAt: now}
	if kind == "email" {
		user.Email = account
	} else {
		user.Phone = account
	}
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		existing, err := a.Store.FindUserByAccount(r.Context(), tx, account)
		if err != nil {
			return err
		}
		if existing != nil {
			return bad("该手机号或邮箱已注册")
		}
		if codeID != 0 {
			ok, err := a.Store.DeleteSMSMatch(r.Context(), tx, codeID, codeHashValue)
			if err != nil {
				return err
			}
			if !ok {
				return bad("验证码不正确或已过期")
			}
		}
		if err := a.Store.InsertUser(r.Context(), tx, user); err != nil {
			if db.IsUnique(err) {
				return bad("该手机号或邮箱已注册")
			}
			return err
		}
		version := a.activeLegalVersion(r.Context())
		for _, docType := range []string{"user_agreement", "privacy"} {
			if err := a.Store.InsertConsent(r.Context(), tx, &model.Consent{
				UserID: user.ID, DocType: docType, Version: version, ConsentItem: "register",
				IP: clientIP(r), UserAgent: userAgent(r), CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		deviceHash, err := a.deviceForInvite(r.Context(), tx, r, user.ID, body.DeviceID, body.DeviceConsent, now)
		if err != nil {
			return err
		}
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

func (a *App) login(w http.ResponseWriter, r *http.Request) error {
	var body loginBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	kind, account, err := classifyAccount(body.Account)
	if err != nil {
		return err
	}
	var user *model.User
	if kind == "email" {
		user, err = a.Store.FindUserByEmail(r.Context(), nil, account)
	} else {
		user, err = a.Store.FindUserByPhone(r.Context(), nil, account)
	}
	if err != nil {
		return err
	}
	if user == nil || !security.VerifyPassword(body.Password, user.PasswordHash) {
		return &AppError{Status: 401, Message: "账号或密码不正确"}
	}
	if !user.IsActive {
		return &AppError{Status: 403, Message: "账号已停用"}
	}
	token, err := security.MakeToken(user.ID, a.Cfg.JWTSecret, 7)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"token": token, "user": userDict(user)})
	return nil
}
