package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/content"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/probe"
	"github.com/leoa81761-ops/wzgx/internal/runtime"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/security"
)

func (a *App) adminUsers(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	rows, err := a.Store.ListUsers(r.Context(), 200)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		account := row.Email
		if account == "" {
			account = row.Phone
		}
		label := catalog.TierLabels[row.Tier]
		if label == "" {
			label = row.Tier
		}
		item := map[string]any{
			"id": row.ID, "account": account, "tier": row.Tier, "tier_label": label,
			"tier_expires_at": jtime(row.TierExpiresAt), "is_admin": row.IsAdmin, "is_active": row.IsActive,
			"is_self": admin != nil && row.ID == admin.ID, "created_at": jtimev(row.CreatedAt),
		}
		if pending, err := a.Store.GetPendingTier(r.Context(), nil, row.ID); err != nil {
			return err
		} else if pending != nil {
			pendingLabel := catalog.TierLabels[pending.Tier]
			if pendingLabel == "" {
				pendingLabel = pending.Tier
			}
			item["pending_tier"] = pending.Tier
			item["pending_label"] = pendingLabel
			item["applies_at"] = jtime(row.TierExpiresAt)
		}
		out = append(out, item)
	}
	writeJSON(w, 200, out)
	return nil
}

type tierBody struct {
	Tier      string     `json:"tier"`
	ExpiresAt *time.Time `json:"expires_at"`
	Note      string     `json:"note"`
	When      string     `json:"when"`
}

func (a *App) adminSetTier(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	id, err := pathID(r, "user_id", "找不到用户")
	if err != nil {
		return err
	}
	var body tierBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if _, ok := catalog.TierLabels[body.Tier]; !ok {
		return bad("未知会员档位")
	}
	now := time.Now().UTC()
	var expires *time.Time
	if body.ExpiresAt != nil {
		t := body.ExpiresAt.UTC()
		expires = &t
	}
	if (body.Tier == "standard" || body.Tier == "premium") && expires == nil {
		t := now.Truncate(time.Second).Add(31 * 24 * time.Hour)
		expires = &t
	}
	if body.Tier == "free" || body.Tier == "lifetime" {
		expires = nil
	}
	note := strings.TrimSpace(body.Note)
	if utf8.RuneCountInString(note) > 500 {
		note = string([]rune(note)[:500])
	}
	var stored string
	var storedExpires *time.Time
	var pendingTier string
	var appliesAt *time.Time
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		user, err := a.Store.GetUser(r.Context(), tx, id)
		if err != nil {
			return err
		}
		if user == nil {
			return &AppError{Status: 404, Message: "找不到用户"}
		}
		when := strings.TrimSpace(body.When)
		future := user.TierExpiresAt != nil && schedule.AsUTC(*user.TierExpiresAt).After(now)
		downgrade := catalog.TierRank(body.Tier) < catalog.TierRank(user.Tier)
		scheduleEnd := downgrade && future && when != "now"
		if when == "period_end" {
			scheduleEnd = downgrade && future
		}
		if when == "" && downgrade && future {
			scheduleEnd = true
		}
		short := note
		if utf8.RuneCountInString(short) > 120 {
			short = string([]rune(short)[:120])
		}
		actor := admin.ID
		detail := "档位改为" + catalog.TierLabels[body.Tier]
		if scheduleEnd {
			nextExpires := scheduledExpiry(now, user.TierExpiresAt, body.Tier, expires)
			if err := a.Store.SetPendingTier(r.Context(), tx, user.ID, body.Tier, nextExpires, now); err != nil {
				return err
			}
			exp := ""
			if nextExpires != nil {
				exp = schedule.Format(nextExpires.UTC())
			}
			if err := a.Store.InsertEntitlement(r.Context(), tx, user.ID, admin.ID, body.Tier, note, exp, schedule.Format(now)); err != nil {
				return err
			}
			detail = "到期后改为" + catalog.TierLabels[body.Tier]
			stored = user.Tier
			storedExpires = user.TierExpiresAt
			pendingTier = body.Tier
			appliesAt = user.TierExpiresAt
		} else {
			user.Tier = body.Tier
			user.TierExpiresAt = expires
			if body.Tier == "lifetime" {
				started := now
				user.TierStartedAt = &started
			}
			if err := a.Store.UpdateUser(r.Context(), tx, user); err != nil {
				return err
			}
			if err := a.Store.ClearPendingTier(r.Context(), tx, user.ID); err != nil {
				return err
			}
			exp := ""
			if expires != nil {
				exp = schedule.Format(expires.UTC())
			}
			if err := a.Store.InsertEntitlement(r.Context(), tx, user.ID, admin.ID, body.Tier, note, exp, schedule.Format(now)); err != nil {
				return err
			}
			stored = user.Tier
			storedExpires = expires
		}
		return a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: "set_tier", Target: "user:" + strconv.FormatInt(user.ID, 10),
			Detail: detail + "。备注：" + short, CreatedAt: now,
		})
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{
		"ok": true, "tier": stored, "tier_expires_at": jtime(storedExpires),
		"pending_tier": pendingTier, "applies_at": jtime(appliesAt),
	})
	return nil
}

func scheduledExpiry(now time.Time, current *time.Time, tier string, requested *time.Time) *time.Time {
	if tier == "free" || tier == "lifetime" {
		return nil
	}
	base := now
	if current != nil {
		base = schedule.AsUTC(*current)
	}
	if requested != nil && schedule.AsUTC(*requested).After(base) {
		t := schedule.AsUTC(*requested)
		return &t
	}
	t := base.Add(31 * 24 * time.Hour)
	return &t
}

type activeBody struct {
	Active *bool `json:"active"`
}

func (a *App) adminSetActive(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	id, err := pathID(r, "user_id", "找不到用户")
	if err != nil {
		return err
	}
	var body activeBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if body.Active == nil {
		return bad("请指定是否启用")
	}
	if id == admin.ID && !*body.Active {
		return bad("不能停用当前登录的管理员")
	}
	now := time.Now().UTC()
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		user, err := a.Store.GetUser(r.Context(), tx, id)
		if err != nil {
			return err
		}
		if user == nil {
			return &AppError{Status: 404, Message: "找不到用户"}
		}
		if user.IsAdmin && user.IsActive && !*body.Active {
			n, err := a.Store.CountAdmins(r.Context(), tx, true)
			if err != nil {
				return err
			}
			if n <= 1 {
				return bad("不能停用最后一位管理员")
			}
		}
		user.IsActive = *body.Active
		if err := a.Store.UpdateUser(r.Context(), tx, user); err != nil {
			return err
		}
		if !user.IsActive {
			if err := a.Store.RevokeInvitee(r.Context(), tx, user.ID, "账号已停用"); err != nil {
				return err
			}
		}
		action := "disable_user"
		detail := "停用用户"
		if user.IsActive {
			action = "enable_user"
			detail = "启用用户"
		}
		actor := admin.ID
		return a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: action, Target: "user:" + strconv.FormatInt(user.ID, 10),
			Detail: detail, CreatedAt: now,
		})
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true, "is_active": *body.Active})
	return nil
}

func (a *App) adminDeleteUser(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	id, err := pathID(r, "user_id", "找不到用户")
	if err != nil {
		return err
	}
	if id == admin.ID {
		return bad("不能删除当前登录的管理员")
	}
	now := time.Now().UTC()
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		user, err := a.Store.GetUser(r.Context(), tx, id)
		if err != nil {
			return err
		}
		if user == nil {
			return &AppError{Status: 404, Message: "找不到用户"}
		}
		if user.IsAdmin {
			n, err := a.Store.CountAdmins(r.Context(), tx, false)
			if err != nil {
				return err
			}
			if n <= 1 {
				return bad("不能删除最后一位管理员")
			}
			if user.IsActive {
				active, err := a.Store.CountAdmins(r.Context(), tx, true)
				if err != nil {
					return err
				}
				if active <= 1 {
					return bad("不能删除最后一位启用的管理员")
				}
			}
		}
		actor := admin.ID
		account := user.Email
		if account == "" {
			account = user.Phone
		}
		if err := a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: "delete_user", Target: "user:" + strconv.FormatInt(user.ID, 10),
			Detail: "删除用户 " + account, CreatedAt: now,
		}); err != nil {
			return err
		}
		return a.Store.DeleteUserCascade(r.Context(), tx, user.ID)
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func providerDict(row *model.ServiceConfig, master string) map[string]any {
	hint := ""
	if row.APIKeyEncrypted != "" {
		plain, err := security.DecryptSecret(master, row.APIKeyEncrypted)
		if err != nil {
			hint = "无法解密"
		} else {
			hint = security.MaskSecret(plain)
		}
	}
	headers := row.ExtraHeaders
	if headers == nil {
		headers = map[string]string{}
	}
	params := row.ExtraParams
	if params == nil {
		params = map[string]any{}
	}
	effort, _ := params["reasoning_effort"].(string)
	kind := "text"
	if row.ConfigType == "image_model" {
		kind = "image"
	}
	return map[string]any{
		"id": row.ID, "name": row.Name, "protocol": row.Protocol, "base_url": row.BaseURL, "model": row.ModelName,
		"extra_headers": headers, "extra_params": params, "priority": row.Priority, "enabled": row.Enabled,
		"timeout_seconds": row.TimeoutSeconds, "has_key": row.APIKeyEncrypted != "", "key_hint": hint,
		"config_type": row.ConfigType, "kind": kind, "reasoning_effort": effort,
	}
}

func (a *App) adminProviders(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	rows, err := a.Store.ListAdminModels(r.Context(), nil)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		out = append(out, providerDict(&rows[i], a.Cfg.MasterKey))
	}
	writeJSON(w, 200, out)
	return nil
}

type providerBody struct {
	Name            string         `json:"name"`
	Protocol        string         `json:"protocol"`
	BaseURL         string         `json:"base_url"`
	APIKey          string         `json:"api_key"`
	Model           string         `json:"model"`
	ExtraHeaders    map[string]any `json:"extra_headers"`
	ExtraParams     map[string]any `json:"extra_params"`
	Priority        *int           `json:"priority"`
	Enabled         *bool          `json:"enabled"`
	TimeoutSeconds  *int           `json:"timeout_seconds"`
	Kind            string         `json:"kind"`
	ReasoningEffort *string        `json:"reasoning_effort"`
}

func parseProvider(body providerBody) (model.ServiceConfig, map[string]string, error) {
	name := strings.TrimSpace(body.Name)
	modelName := strings.TrimSpace(body.Model)
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 40 || utf8.RuneCountInString(modelName) < 1 || utf8.RuneCountInString(modelName) > 120 {
		return model.ServiceConfig{}, nil, invalid()
	}
	if body.Protocol != "openai" && body.Protocol != "anthropic" && body.Protocol != "responses" && body.Protocol != "http" {
		return model.ServiceConfig{}, nil, bad("协议只支持 openai（Chat Completions）、responses（Responses API）、anthropic（Messages）或 http（自定义字段映射）")
	}
	kind := strings.TrimSpace(body.Kind)
	if kind == "" {
		kind = "text"
	}
	if kind != "text" && kind != "image" {
		return model.ServiceConfig{}, nil, bad("用途只支持文本写作或封面生图")
	}
	if kind == "image" && body.Protocol != "openai" {
		return model.ServiceConfig{}, nil, bad("封面生图使用 OpenAI 兼容的 /v1/images/generations，协议请选 openai")
	}
	if body.Protocol == "http" && kind != "text" {
		return model.ServiceConfig{}, nil, bad("自定义字段映射只用于文本")
	}
	if err := validateHTTPURL(body.BaseURL); err != nil {
		return model.ServiceConfig{}, nil, err
	}
	timeout := 60
	if body.TimeoutSeconds != nil {
		timeout = *body.TimeoutSeconds
	}
	if timeout < 5 || timeout > 600 {
		return model.ServiceConfig{}, nil, bad("超时时间需在 5 到 600 秒之间")
	}
	headers, err := stringHeaders(body.ExtraHeaders)
	if err != nil {
		return model.ServiceConfig{}, nil, err
	}
	params := body.ExtraParams
	if params == nil {
		params = map[string]any{}
	}
	if body.ReasoningEffort != nil {
		effort := strings.TrimSpace(*body.ReasoningEffort)
		switch effort {
		case "":
			delete(params, "reasoning_effort")
		case "low", "medium", "high":
			params["reasoning_effort"] = effort
		default:
			return model.ServiceConfig{}, nil, bad("推理档位只支持 low、medium、high，留空表示不发送")
		}
	}
	priority := 100
	if body.Priority != nil {
		priority = *body.Priority
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	configType := "text_model"
	if kind == "image" {
		configType = "image_model"
	}
	return model.ServiceConfig{
		ConfigType: configType, Name: name, Protocol: body.Protocol, BaseURL: strings.TrimSpace(body.BaseURL),
		ModelName: modelName, ExtraHeaders: headers, ExtraParams: params, Priority: priority, Enabled: enabled, TimeoutSeconds: timeout,
	}, headers, nil
}

func stringHeaders(raw map[string]any) (map[string]string, error) {
	if raw == nil {
		return map[string]string{}, nil
	}
	out := map[string]string{}
	for key, value := range raw {
		if strings.TrimSpace(key) == "" {
			return nil, bad("请求头名称不能为空")
		}
		text, ok := value.(string)
		if !ok {
			return nil, bad("请求头的值必须是字符串")
		}
		out[key] = text
	}
	return out, nil
}

func validateHTTPURL(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return bad("接口地址需要以 http:// 或 https:// 开头")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "169.254.169.254" || host == "metadata.google.internal" {
		return bad("不能使用云主机元数据地址")
	}
	return nil
}

func (a *App) adminCreateProvider(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	var body providerBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	row, _, err := parseProvider(body)
	if err != nil {
		return err
	}
	enc, err := security.EncryptSecret(a.Cfg.MasterKey, strings.TrimSpace(body.APIKey))
	if err != nil {
		return err
	}
	row.APIKeyEncrypted = enc
	row.UpdatedAt = time.Now().UTC()
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		if err := a.Store.InsertService(r.Context(), tx, &row); err != nil {
			return err
		}
		actor := admin.ID
		return a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: "provider_create", Target: "service_config:" + strconv.FormatInt(row.ID, 10),
			Detail: "新增模型服务 " + row.Name + "，协议 " + row.Protocol + "，模型 " + row.ModelName, CreatedAt: row.UpdatedAt,
		})
	})
	if err != nil {
		return err
	}
	a.Cache.Invalidate()
	writeJSON(w, 200, providerDict(&row, a.Cfg.MasterKey))
	return nil
}

func (a *App) adminUpdateProvider(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	id, err := pathID(r, "provider_id", "找不到这个模型服务")
	if err != nil {
		return err
	}
	var body providerBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	parsed, _, err := parseProvider(body)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var saved *model.ServiceConfig
	keyChanged := strings.TrimSpace(body.APIKey) != ""
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		row, err := a.modelRow(r.Context(), tx, id)
		if err != nil {
			return err
		}
		row.ConfigType = parsed.ConfigType
		row.Name = parsed.Name
		row.Protocol = parsed.Protocol
		row.BaseURL = parsed.BaseURL
		if keyChanged {
			enc, err := security.EncryptSecret(a.Cfg.MasterKey, strings.TrimSpace(body.APIKey))
			if err != nil {
				return err
			}
			row.APIKeyEncrypted = enc
		}
		row.ModelName = parsed.ModelName
		row.ExtraHeaders = parsed.ExtraHeaders
		row.ExtraParams = parsed.ExtraParams
		row.Priority = parsed.Priority
		row.Enabled = parsed.Enabled
		row.TimeoutSeconds = parsed.TimeoutSeconds
		row.UpdatedAt = now
		if err := a.Store.UpdateService(r.Context(), tx, row); err != nil {
			return err
		}
		detail := "更新模型服务 " + row.Name + "，协议 " + row.Protocol
		if keyChanged {
			detail += "，已更新密钥"
		}
		actor := admin.ID
		saved = row
		return a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: "provider_update", Target: "service_config:" + strconv.FormatInt(row.ID, 10),
			Detail: detail, CreatedAt: now,
		})
	})
	if err != nil {
		return err
	}
	a.Cache.Invalidate()
	writeJSON(w, 200, providerDict(saved, a.Cfg.MasterKey))
	return nil
}

func (a *App) adminDeleteProvider(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	id, err := pathID(r, "provider_id", "找不到这个模型服务")
	if err != nil {
		return err
	}
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		row, err := a.modelRow(r.Context(), tx, id)
		if err != nil {
			return err
		}
		if err := a.Store.DeleteService(r.Context(), tx, row.ID); err != nil {
			return err
		}
		actor := admin.ID
		return a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: "provider_delete", Target: "service_config:" + strconv.FormatInt(id, 10),
			Detail: row.Name, CreatedAt: time.Now().UTC(),
		})
	})
	if err != nil {
		return err
	}
	a.Cache.Invalidate()
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) modelRow(ctx context.Context, q db.Q, id int64) (*model.ServiceConfig, error) {
	row, err := a.Store.GetService(ctx, q, id)
	if err != nil {
		return nil, err
	}
	if row == nil || (row.ConfigType != "text_model" && row.ConfigType != "image_model") {
		return nil, &AppError{Status: 404, Message: "找不到这个模型服务"}
	}
	return row, nil
}

type testBody struct {
	Live bool `json:"live"`
}

func (a *App) adminTestProvider(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	id, err := pathID(r, "provider_id", "找不到这个模型服务")
	if err != nil {
		return err
	}
	var body testBody
	if r.ContentLength != 0 {
		if err := readJSON(r, &body); err != nil {
			return err
		}
	}
	row, err := a.modelRow(r.Context(), nil, id)
	if err != nil {
		return err
	}
	provider := runtime.ToProvider(*row, a.Cfg.MasterKey)
	if a.Cfg.LLMMode == "mock" && !body.Live {
		writeJSON(w, 200, map[string]any{"ok": true, "mock": true, "protocol": provider.Protocol, "model": provider.Model, "text": "OK"})
		return nil
	}
	timeout := provider.Timeout
	if timeout <= 0 || timeout > 60*time.Second {
		timeout = 60 * time.Second
	}
	trial := provider
	trial.Timeout = timeout
	ctx, cancel := context.WithTimeout(r.Context(), timeout+5*time.Second)
	defer cancel()
	if row.ConfigType == "image_model" {
		png, err := llm.GenerateCover(ctx, a.HTTP, trial, "a simple green leaf, no text")
		if err != nil {
			return bad("连通失败：" + err.Error())
		}
		writeJSON(w, 200, map[string]any{"ok": true, "mock": false, "protocol": provider.Protocol, "model": provider.Model, "text": fmt.Sprintf("封面 %d 字节", len(png))})
		return nil
	}
	gw := *a.Gateway
	gw.Mock = false
	result, err := gw.Complete(ctx, trial, []map[string]string{{"role": "user", "content": "只回复 OK"}})
	if err != nil {
		return bad("连通失败：" + err.Error())
	}
	text := result.Content
	if utf8.RuneCountInString(text) > 200 {
		text = string([]rune(text)[:200])
	}
	writeJSON(w, 200, map[string]any{"ok": true, "mock": false, "protocol": provider.Protocol, "model": provider.Model, "text": text})
	return nil
}

func (a *App) adminRoutes(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	rows, err := a.Store.ListRoutes(r.Context(), nil)
	if err != nil {
		return err
	}
	type key struct{ stage, tier string }
	order := []key{}
	grouped := map[key][]int64{}
	for _, row := range rows {
		k := key{row.Stage, row.Tier}
		if _, ok := grouped[k]; !ok {
			order = append(order, k)
		}
		grouped[k] = append(grouped[k], row.ServiceConfigID)
	}
	routes := make([]map[string]any, 0, len(order))
	for _, k := range order {
		routes = append(routes, map[string]any{"stage": k.stage, "tier": k.tier, "provider_ids": grouped[k]})
	}
	stages := make([]map[string]string, 0, len(catalog.Stages))
	for _, item := range catalog.Stages {
		stages = append(stages, map[string]string{"key": item[0], "label": item[1]})
	}
	tiers := []map[string]string{{"key": "*", "label": "全部档位"}}
	for _, item := range []string{"free", "standard", "premium", "lifetime"} {
		tiers = append(tiers, map[string]string{"key": item, "label": catalog.TierLabels[item]})
	}
	writeJSON(w, 200, map[string]any{"stages": stages, "tiers": tiers, "routes": routes})
	return nil
}

type routeBody struct {
	Stage       string  `json:"stage"`
	Tier        string  `json:"tier"`
	ProviderIDs []int64 `json:"provider_ids"`
}

func (a *App) adminSaveRoutes(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	var body routeBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if body.Tier == "" {
		body.Tier = "*"
	}
	known := false
	for _, item := range catalog.Stages {
		if item[0] == body.Stage {
			known = true
		}
	}
	if !known {
		return bad("未知环节")
	}
	if body.Tier != "*" {
		if _, ok := catalog.TierLabels[body.Tier]; !ok {
			return bad("未知档位")
		}
	}
	ids := body.ProviderIDs
	if ids == nil {
		ids = []int64{}
	}
	now := time.Now().UTC()
	err := a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		for _, providerID := range ids {
			row, err := a.Store.GetService(r.Context(), tx, providerID)
			if err != nil {
				return err
			}
			if row == nil || row.ConfigType != "text_model" {
				return bad("只能把文本模型排进写作环节")
			}
		}
		if err := a.Store.ReplaceRoutes(r.Context(), tx, body.Stage, body.Tier, ids); err != nil {
			return err
		}
		parts := make([]string, 0, len(ids))
		for _, id := range ids {
			parts = append(parts, strconv.FormatInt(id, 10))
		}
		actor := admin.ID
		return a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: "stage_route", Target: body.Stage + ":" + body.Tier,
			Detail: "回退顺序：" + strings.Join(parts, ","), CreatedAt: now,
		})
	})
	if err != nil {
		return err
	}
	a.Cache.Invalidate()
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) wechatRow(ctx context.Context, q db.Q) (*model.ServiceConfig, error) {
	row, err := a.Store.FindService(ctx, q, "wechat", "wechat")
	if err != nil {
		return nil, err
	}
	if row != nil {
		return row, nil
	}
	row = &model.ServiceConfig{
		ConfigType: "wechat", Name: "wechat", Protocol: "wechat", BaseURL: a.Cfg.WeChatAPIBase,
		ExtraParams: map[string]any{"mode": a.Cfg.WeChatMode, "public_egress_ip": a.Cfg.PublicEgressIP},
		Enabled:     true, UpdatedAt: time.Now().UTC(),
	}
	if err := a.Store.InsertService(ctx, q, row); err != nil {
		return nil, err
	}
	return row, nil
}

func (a *App) adminGetWechat(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	row, err := a.wechatRow(r.Context(), nil)
	if err != nil {
		return err
	}
	mode, _ := row.ExtraParams["mode"].(string)
	if mode == "" {
		mode = "mock"
	}
	ip, _ := row.ExtraParams["public_egress_ip"].(string)
	writeJSON(w, 200, map[string]any{"mode": mode, "api_base": row.BaseURL, "public_egress_ip": ip})
	return nil
}

type wechatBody struct {
	Mode           string `json:"mode"`
	APIBase        string `json:"api_base"`
	PublicEgressIP string `json:"public_egress_ip"`
}

func (a *App) adminSaveWechat(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	var body wechatBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if body.Mode != "mock" && body.Mode != "live" {
		return bad("微信模式只能是 mock 或 live")
	}
	if err := validateHTTPURL(body.APIBase); err != nil {
		return err
	}
	now := time.Now().UTC()
	err := a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		row, err := a.wechatRow(r.Context(), tx)
		if err != nil {
			return err
		}
		row.BaseURL = strings.TrimSpace(body.APIBase)
		row.ExtraParams = map[string]any{"mode": body.Mode, "public_egress_ip": strings.TrimSpace(body.PublicEgressIP)}
		row.UpdatedAt = now
		if err := a.Store.UpdateService(r.Context(), tx, row); err != nil {
			return err
		}
		actor := admin.ID
		return a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: "wechat_settings", Target: "wechat", Detail: "模式 " + body.Mode, CreatedAt: now,
		})
	})
	if err != nil {
		return err
	}
	a.Cache.Invalidate()
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) adminConnectivity(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	if r.Body != nil && r.ContentLength != 0 {
		var discard map[string]any
		_ = readJSON(r, &discard)
	}
	cfg, err := a.connectivityConfig(r.Context())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	steps, err := probe.Run(ctx, cfg)
	if err != nil {
		detail := formatSteps(steps)
		if detail == "" {
			detail = err.Error()
		}
		return bad(detail)
	}
	writeJSON(w, 200, map[string]any{"ok": true, "steps": steps})
	return nil
}

func formatSteps(steps []probe.Step) string {
	parts := make([]string, 0, len(steps))
	for _, step := range steps {
		mark := "成功"
		if !step.OK {
			mark = "失败"
		}
		line := step.Name + "：" + mark
		if step.Detail != "" {
			line += " " + step.Detail
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, "；")
}

func (a *App) connectivityConfig(ctx context.Context) (probe.Config, error) {
	cfg := probe.FromEnv()
	cfg.HTTP = a.HTTP
	if strings.TrimSpace(os.Getenv("WX_APP_ID")) == "" || strings.TrimSpace(os.Getenv("WX_APP_SECRET")) == "" {
		account, err := a.Store.LatestActiveAccount(ctx)
		if err != nil {
			return cfg, err
		}
		if account != nil {
			cfg.AppID = account.AppID
			secret, err := security.DecryptSecret(a.Cfg.MasterKey, account.SecretEncrypted)
			if err != nil {
				return cfg, err
			}
			cfg.Secret = secret
		}
	}
	if strings.TrimSpace(os.Getenv("WECHAT_API_BASE")) == "" {
		wx, err := a.Cache.WeChat(ctx, nil)
		if err != nil {
			return cfg, err
		}
		if wx.Base != "" {
			cfg.WeChatBase = wx.Base
		}
	}
	needModel := strings.TrimSpace(os.Getenv("LLM_BASE_URL")) == "" || strings.TrimSpace(os.Getenv("LLM_API_KEY")) == "" || strings.TrimSpace(os.Getenv("LLM_MODEL")) == ""
	if needModel || strings.TrimSpace(os.Getenv("LLM_PROTOCOL")) == "" {
		var row *model.ServiceConfig
		routes, err := a.Store.RoutesFor(ctx, nil, "draft", "*")
		if err != nil {
			return cfg, err
		}
		if len(routes) > 0 {
			row, err = a.Store.GetService(ctx, nil, routes[0].ServiceConfigID)
			if err != nil {
				return cfg, err
			}
		}
		if row == nil || !row.Enabled || row.ConfigType != "text_model" {
			models, err := a.Store.ListTextModels(ctx, nil, true)
			if err != nil {
				return cfg, err
			}
			if len(models) > 0 {
				row = &models[0]
			}
		}
		if row != nil && row.ConfigType == "text_model" {
			provider := runtime.ToProvider(*row, a.Cfg.MasterKey)
			if strings.TrimSpace(os.Getenv("LLM_BASE_URL")) == "" {
				cfg.LLMBase = provider.BaseURL
			}
			if strings.TrimSpace(os.Getenv("LLM_API_KEY")) == "" {
				cfg.LLMKey = provider.APIKey
			}
			if strings.TrimSpace(os.Getenv("LLM_MODEL")) == "" {
				cfg.LLMModel = provider.Model
			}
			if strings.TrimSpace(os.Getenv("LLM_PROTOCOL")) == "" && provider.Protocol != "" {
				cfg.Protocol = provider.Protocol
			}
			if strings.TrimSpace(os.Getenv("LLM_EXTRA_HEADERS")) == "" && len(provider.ExtraHeaders) > 0 {
				cfg.Headers = provider.ExtraHeaders
			}
			if strings.TrimSpace(os.Getenv("LLM_TIMEOUT_SECONDS")) == "" && provider.Timeout > 0 {
				cfg.Timeout = provider.Timeout
			}
		}
	}
	return cfg, nil
}

func (a *App) adminGetWords(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	words, err := a.Cache.Words(r.Context(), nil)
	if err != nil {
		return err
	}
	row, err := a.Store.FindService(r.Context(), nil, "audit", "local_words")
	if err != nil {
		return err
	}
	custom := []string{}
	if row != nil {
		custom = stringList(row.ExtraParams["words"])
	}
	if words == nil {
		words = []string{}
	}
	writeJSON(w, 200, map[string]any{"words": words, "custom": custom})
	return nil
}

type wordsBody struct {
	Words []string `json:"words"`
}

func (a *App) adminSaveWords(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	var body wordsBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	custom := []string{}
	seen := map[string]bool{}
	for _, word := range body.Words {
		cleaned := strings.TrimSpace(word)
		if cleaned == "" || seen[cleaned] {
			continue
		}
		if utf8.RuneCountInString(cleaned) > 40 {
			cleaned = string([]rune(cleaned)[:40])
		}
		seen[cleaned] = true
		custom = append(custom, cleaned)
	}
	now := time.Now().UTC()
	err := a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		row, err := a.Store.FindService(r.Context(), tx, "audit", "local_words")
		if err != nil {
			return err
		}
		if row == nil {
			row = &model.ServiceConfig{ConfigType: "audit", Name: "local_words", Protocol: "local", Enabled: true, UpdatedAt: now, ExtraParams: map[string]any{"words": custom}}
			if err := a.Store.InsertService(r.Context(), tx, row); err != nil {
				return err
			}
		} else {
			row.ExtraParams = map[string]any{"words": custom}
			row.UpdatedAt = now
			if err := a.Store.UpdateService(r.Context(), tx, row); err != nil {
				return err
			}
		}
		actor := admin.ID
		return a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: "sensitive_words", Target: "audit",
			Detail: strconv.Itoa(len(custom)) + " 个自定义词", CreatedAt: now,
		})
	})
	if err != nil {
		return err
	}
	a.Cache.Invalidate()
	writeJSON(w, 200, map[string]any{"words": content.EffectiveWords(custom), "custom": custom})
	return nil
}

func (a *App) adminGetSourceDomains(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	row, err := a.Store.FindService(r.Context(), nil, "audit", "source_domains")
	if err != nil {
		return err
	}
	if row == nil {
		writeJSON(w, 200, map[string]any{"block": content.DefaultBlockedDomains, "allow": []string{}, "using_defaults": true})
		return nil
	}
	block := stringList(row.ExtraParams["block"])
	allow := stringList(row.ExtraParams["allow"])
	if block == nil {
		block = []string{}
	}
	if allow == nil {
		allow = []string{}
	}
	writeJSON(w, 200, map[string]any{"block": block, "allow": allow, "using_defaults": false})
	return nil
}

type domainsBody struct {
	Block []string `json:"block"`
	Allow []string `json:"allow"`
}

func (a *App) adminSaveSourceDomains(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	var body domainsBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	block := content.NormalizeDomains(body.Block)
	allow := content.NormalizeDomains(body.Allow)
	now := time.Now().UTC()
	err := a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		row, err := a.Store.FindService(r.Context(), tx, "audit", "source_domains")
		if err != nil {
			return err
		}
		params := map[string]any{"block": block, "allow": allow}
		if row == nil {
			row = &model.ServiceConfig{
				ConfigType: "audit", Name: "source_domains", Protocol: "local", Enabled: true,
				UpdatedAt: now, ExtraHeaders: map[string]string{}, ExtraParams: params,
			}
			if err := a.Store.InsertService(r.Context(), tx, row); err != nil {
				return err
			}
		} else {
			row.ExtraParams = params
			row.UpdatedAt = now
			if err := a.Store.UpdateService(r.Context(), tx, row); err != nil {
				return err
			}
		}
		actor := admin.ID
		return a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: "source_domains", Target: "audit",
			Detail: strconv.Itoa(len(block)) + " 个屏蔽，" + strconv.Itoa(len(allow)) + " 个允许", CreatedAt: now,
		})
	})
	if err != nil {
		return err
	}
	a.Cache.Invalidate()
	writeJSON(w, 200, map[string]any{"block": block, "allow": allow, "using_defaults": false})
	return nil
}

func stringList(v any) []string {
	switch items := v.(type) {
	case []string:
		return items
	case []any:
		out := []string{}
		for _, item := range items {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return []string{}
	}
}

func (a *App) adminAudit(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	rows, err := a.Store.ListAudit(r.Context(), 100)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var actor any
		if row.ActorID != nil {
			actor = *row.ActorID
		}
		out = append(out, map[string]any{
			"id": row.ID, "actor_id": actor, "action": row.Action, "target": row.Target,
			"detail": row.Detail, "created_at": jtimev(row.CreatedAt),
		})
	}
	writeJSON(w, 200, out)
	return nil
}
