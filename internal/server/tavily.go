package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/security"
)

func (a *App) getTavily(w http.ResponseWriter, r *http.Request, user *model.User) error {
	view, err := a.tavilyView(r.Context(), user.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

func (a *App) saveTavily(w http.ResponseWriter, r *http.Request, user *model.User) error {
	var body struct {
		APIKey string `json:"api_key"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	key := strings.TrimSpace(body.APIKey)
	if msg := tavilyKeyOK(key); msg != "" {
		return bad(msg)
	}
	cipher, notice, err := a.Store.TavilySecret(r.Context(), nil, user.ID)
	if err != nil {
		return err
	}
	enc, err := security.EncryptSecret(a.Cfg.MasterKey, key)
	if err != nil {
		return err
	}
	if err := a.Store.SaveTavily(r.Context(), user.ID, enc); err != nil {
		return err
	}
	action := "tavily_key_set"
	if strings.TrimSpace(cipher) != "" {
		action = "tavily_key_replace"
	}
	_ = notice
	actor := user.ID
	if err := a.Store.InsertAudit(r.Context(), nil, model.AuditLog{
		ActorID: &actor, Action: action, Target: "user:" + strconv.FormatInt(user.ID, 10), Detail: "saved", CreatedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	view, err := a.tavilyView(r.Context(), user.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

func (a *App) deleteTavily(w http.ResponseWriter, r *http.Request, user *model.User) error {
	if err := a.Store.ClearTavily(r.Context(), user.ID); err != nil {
		return err
	}
	actor := user.ID
	if err := a.Store.InsertAudit(r.Context(), nil, model.AuditLog{
		ActorID: &actor, Action: "tavily_key_delete", Target: "user:" + strconv.FormatInt(user.ID, 10), Detail: "deleted", CreatedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"set": false, "mask": "", "notice": ""})
	return nil
}

func (a *App) testTavily(w http.ResponseWriter, r *http.Request, user *model.User) error {
	cipher, _, err := a.Store.TavilySecret(r.Context(), nil, user.ID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cipher) == "" {
		return bad("还没有保存 Tavily 密钥")
	}
	key, err := security.DecryptSecret(a.Cfg.MasterKey, cipher)
	if err != nil {
		return bad("无法解密密钥，请重新保存")
	}
	if err := a.probeUserTavily(r.Context(), key); err != nil {
		actor := user.ID
		detail := "fail"
		var appErr *AppError
		if errors.As(err, &appErr) {
			detail = appErr.Message
		}
		_ = a.Store.InsertAudit(r.Context(), nil, model.AuditLog{
			ActorID: &actor, Action: "tavily_key_test", Target: "user:" + strconv.FormatInt(user.ID, 10), Detail: detail, CreatedAt: time.Now().UTC(),
		})
		if errors.As(err, &appErr) && (appErr.Message == "密钥无效" || appErr.Message == "额度已用完") {
			_ = a.Store.SetTavilyNotice(r.Context(), user.ID, tavilyNotice(appErr.Message))
		}
		return err
	}
	_ = a.Store.SetTavilyNotice(r.Context(), user.ID, "")
	actor := user.ID
	if err := a.Store.InsertAudit(r.Context(), nil, model.AuditLog{
		ActorID: &actor, Action: "tavily_key_test", Target: "user:" + strconv.FormatInt(user.ID, 10), Detail: "ok", CreatedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	view, err := a.tavilyView(r.Context(), user.ID)
	if err != nil {
		return err
	}
	view["ok"] = true
	writeJSON(w, 200, view)
	return nil
}

func (a *App) probeUserTavily(ctx context.Context, key string) error {
	endpoint := strings.TrimSpace(a.TavilyProbeURL)
	if endpoint == "" {
		endpoint = llm.DefaultSearchEndpoint("tavily")
	}
	rows, err := llm.SearchProvider(ctx, a.HTTP, llm.SearchTarget{URL: endpoint, APIKey: key, Provider: "tavily", Owner: "user"}, "综合布线")
	if err != nil {
		var keyErr *llm.KeyError
		if errors.As(err, &keyErr) {
			if keyErr.Kind == "quota" {
				return bad("额度已用完")
			}
			return bad("密钥无效")
		}
		return bad("连通失败")
	}
	if len(rows) == 0 {
		return bad("检索没有返回结果")
	}
	return nil
}

func (a *App) tavilyView(ctx context.Context, userID int64) (map[string]any, error) {
	cipher, notice, err := a.Store.TavilySecret(ctx, nil, userID)
	if err != nil {
		return nil, err
	}
	mask := ""
	if strings.TrimSpace(cipher) != "" {
		plain, err := security.DecryptSecret(a.Cfg.MasterKey, cipher)
		if err != nil {
			return map[string]any{"set": true, "mask": "****", "notice": "无法解密密钥，请重新保存", "error": "无法解密凭证，请确认主密钥未变更"}, nil
		}
		mask = security.MaskSecret(plain)
	}
	return map[string]any{"set": mask != "", "mask": mask, "notice": notice}, nil
}

func tavilyKeyOK(key string) string {
	if utf8.RuneCountInString(key) < 12 || utf8.RuneCountInString(key) > 200 {
		return "请填写 Tavily 密钥"
	}
	if strings.ContainsAny(key, " \t\r\n") {
		return "密钥里不能有空格"
	}
	return ""
}

func tavilyNotice(kind string) string {
	if kind == "额度已用完" {
		return "你的 Tavily 额度已用完，生成会改用站内检索。可以更换密钥，或等额度恢复后再试。"
	}
	return "你的 Tavily 密钥无效，生成会改用站内检索。请更换密钥。"
}
