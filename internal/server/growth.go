package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/growth"
	"github.com/leoa81761-ops/wzgx/internal/hot"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

func (a *App) myReferral(w http.ResponseWriter, r *http.Request, user *model.User) error {
	now := time.Now().UTC()
	var payload map[string]any
	err := a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		code, err := a.Store.EnsureInviteCode(r.Context(), tx, user.ID, "")
		if err != nil {
			return err
		}
		tier, _ := quota.Membership(user, now)
		limits, err := quota.LimitsFor(r.Context(), a.Store, tx, tier)
		if err != nil {
			return err
		}
		rules, err := growth.RulesFromSettings(func(key string) (string, error) {
			return a.Store.GetSetting(r.Context(), tx, key)
		})
		if err != nil {
			return err
		}
		valid, err := a.Store.CountReferrals(r.Context(), tx, user.ID, growth.StatusValid)
		if err != nil {
			return err
		}
		pending, err := a.Store.CountReferrals(r.Context(), tx, user.ID, growth.StatusPending)
		if err != nil {
			return err
		}
		frozen, err := a.Store.CountReferrals(r.Context(), tx, user.ID, growth.StatusFrozen)
		if err != nil {
			return err
		}
		revoked, err := a.Store.CountReferrals(r.Context(), tx, user.ID, growth.StatusRevoked)
		if err != nil {
			return err
		}
		payload = map[string]any{
			"code": code, "path": "/register?invite=" + code,
			"valid_count": valid, "pending_count": pending, "frozen_count": frozen, "revoked_count": revoked,
			"bonus":      growth.Bonus(valid, limits.PeriodQuota, tier, rules),
			"reward_per": rules.RewardPer, "reward_cap": rules.RewardCap, "month_max": rules.MonthMax,
			"daily_cap": rules.DailyCap, "observe_days": rules.ObserveDays, "drafts_need": rules.DraftsNeed,
		}
		return nil
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, payload)
	return nil
}

func (a *App) setPlanConfirm(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "plan_id", "找不到这个写作计划")
	if err != nil {
		return err
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	now := time.Now().UTC()
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		fresh, err := a.Store.GetUser(r.Context(), tx, user.ID)
		if err != nil {
			return err
		}
		plan, err := a.ownPlan(r.Context(), tx, user.ID, id)
		if err != nil {
			return err
		}
		tier, _ := quota.Membership(fresh, now)
		if body.Enabled && tier != "premium" && tier != "lifetime" {
			return bad("选题确认是高级版和买断版的功能")
		}
		plan.ConfirmMode = body.Enabled
		if err := a.Store.UpdatePlan(r.Context(), tx, plan); err != nil {
			return err
		}
		extra, err := a.Store.GetPlanExtra(r.Context(), tx, plan.ID)
		if err != nil {
			return err
		}
		if body.Enabled {
			extra.TopicMode = "confirm"
		} else if extra.TopicMode == "" || extra.TopicMode == "confirm" {
			extra.TopicMode = "auto"
		}
		return a.Store.SavePlanExtra(r.Context(), tx, extra)
	})
	if err != nil {
		return err
	}
	plan, err := a.Store.GetPlan(r.Context(), nil, id)
	if err != nil {
		return err
	}
	view, err := a.planView(r.Context(), plan)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

func (a *App) acceptProposal(w http.ResponseWriter, r *http.Request, user *model.User) error {
	return a.decideProposal(w, r, user, true)
}

func (a *App) rejectProposal(w http.ResponseWriter, r *http.Request, user *model.User) error {
	return a.decideProposal(w, r, user, false)
}

func (a *App) decideProposal(w http.ResponseWriter, r *http.Request, user *model.User, accept bool) error {
	id, err := pathID(r, "plan_id", "找不到这个写作计划")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		plan, err := a.ownPlan(r.Context(), tx, user.ID, id)
		if err != nil {
			return err
		}
		prop, err := a.Store.OpenProposal(r.Context(), tx, plan.ID)
		if err != nil {
			return err
		}
		if prop == nil || prop.Status != "pending" {
			return bad("没有待确认的选题")
		}
		if accept {
			return a.Store.SetProposalStatus(r.Context(), tx, prop.ID, "approved")
		}
		if err := a.Store.InsertVeto(r.Context(), tx, plan.ID, prop.TopicTag, now); err != nil {
			return err
		}
		fresh, err := a.Store.GetUser(r.Context(), tx, user.ID)
		if err != nil {
			return err
		}
		tier, _ := quota.Membership(fresh, now)
		choice, title, err := a.Runner.SuggestTopic(r.Context(), tx, plan, tier, now)
		if err != nil {
			return err
		}
		if choice.TopicTag == "" || choice.TopicTag == prop.TopicTag {
			return bad("没有可换的选题")
		}
		return a.Store.UpdateProposalTopic(r.Context(), tx, prop.ID, choice.TopicTag, title)
	})
	if err != nil {
		return err
	}
	plan, err := a.Store.GetPlan(r.Context(), nil, id)
	if err != nil {
		return err
	}
	view, err := a.planView(r.Context(), plan)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

func (a *App) attachProposal(ctx context.Context, view map[string]any, planID int64) error {
	prop, err := a.Store.OpenProposal(ctx, nil, planID)
	if err != nil || prop == nil {
		return err
	}
	view["proposal"] = map[string]any{
		"id": prop.ID, "topic_tag": prop.TopicTag, "title": prop.Title, "status": prop.Status,
		"proposed_at": jtimev(prop.ProposedAt), "execute_at": jtimev(prop.ExecuteAt),
		"deadline": jtimev(prop.ProposedAt.Add(24 * time.Hour)),
	}
	return nil
}

func (a *App) adminReferrals(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	rows, err := a.Store.ListReferrals(r.Context(), status, 100)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"id": row.ID, "inviter_id": row.InviterID, "invitee_id": row.InviteeID,
			"inviter": row.InviterAccount, "invitee": row.InviteeAccount,
			"status": row.Status, "reason": row.Reason, "ip": row.IP,
			"observe_until": jtimev(row.ObserveUntil), "created_at": jtimev(row.CreatedAt), "valid_at": jtime(row.ValidAt),
		})
	}
	writeJSON(w, 200, out)
	return nil
}

func (a *App) adminApproveReferral(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	return a.adminReviewReferral(w, r, admin, growth.StatusValid)
}

func (a *App) adminRevokeReferral(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	return a.adminReviewReferral(w, r, admin, growth.StatusRevoked)
}

func (a *App) adminReviewReferral(w http.ResponseWriter, r *http.Request, admin *model.User, status string) error {
	id, err := pathID(r, "id", "找不到这条邀请")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		row, err := a.Store.GetReferral(r.Context(), tx, id)
		if err != nil {
			return err
		}
		if row == nil {
			return &AppError{Status: 404, Message: "找不到这条邀请"}
		}
		var validAt *time.Time
		reason := "管理员撤销"
		if status == growth.StatusValid {
			stamp := now
			validAt = &stamp
			reason = "管理员通过"
		}
		if err := a.Store.SetReferralStatus(r.Context(), tx, id, status, reason, validAt); err != nil {
			return err
		}
		actor := admin.ID
		return a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: "referral_review", Target: "referral:" + strconv.FormatInt(id, 10),
			Detail: reason, CreatedAt: now,
		})
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) adminInviteRules(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	rules, err := growth.RulesFromSettings(func(key string) (string, error) {
		return a.Store.GetSetting(r.Context(), nil, key)
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, rulesJSON(rules))
	return nil
}

func (a *App) adminSaveInviteRules(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	var body struct {
		RewardPer   int `json:"reward_per"`
		RewardCap   int `json:"reward_cap"`
		MonthMax    int `json:"month_max"`
		DailyCap    int `json:"daily_cap"`
		ObserveDays int `json:"observe_days"`
		DraftsNeed  int `json:"drafts_need"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	rules := growth.Normalize(growth.Rules{
		RewardPer: body.RewardPer, RewardCap: body.RewardCap, MonthMax: body.MonthMax,
		DailyCap: body.DailyCap, ObserveDays: body.ObserveDays, DraftsNeed: body.DraftsNeed,
	})
	if rules.RewardPer > 20 || rules.RewardCap > 100 || rules.MonthMax > 100 || rules.DailyCap > 20 || rules.ObserveDays > 30 || rules.DraftsNeed > 20 {
		return bad("邀请参数超出可配置范围")
	}
	now := time.Now().UTC()
	err := a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		pairs := []struct{ key, value string }{
			{"invite_reward_per", strconv.Itoa(rules.RewardPer)},
			{"invite_reward_cap", strconv.Itoa(rules.RewardCap)},
			{"invite_month_max", strconv.Itoa(rules.MonthMax)},
			{"invite_daily_cap", strconv.Itoa(rules.DailyCap)},
			{"invite_observe_days", strconv.Itoa(rules.ObserveDays)},
			{"invite_drafts_need", strconv.Itoa(rules.DraftsNeed)},
		}
		for _, pair := range pairs {
			if err := a.Store.SetSetting(r.Context(), tx, pair.key, pair.value, now); err != nil {
				return err
			}
		}
		actor := admin.ID
		return a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: "invite_rules", Target: "app_setting",
			Detail: "更新邀请奖励参数", CreatedAt: now,
		})
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, rulesJSON(rules))
	return nil
}

func rulesJSON(rules growth.Rules) map[string]any {
	return map[string]any{
		"reward_per": rules.RewardPer, "reward_cap": rules.RewardCap, "month_max": rules.MonthMax,
		"daily_cap": rules.DailyCap, "observe_days": rules.ObserveDays, "drafts_need": rules.DraftsNeed,
	}
}

func (a *App) adminHotSources(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	rows, err := a.Store.ListHotSources(r.Context(), nil, false)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		out = append(out, hotSourceDict(&rows[i]))
	}
	writeJSON(w, 200, out)
	return nil
}

func hotSourceDict(row *model.ServiceConfig) map[string]any {
	params := row.ExtraParams
	if params == nil {
		params = map[string]any{}
	}
	return map[string]any{
		"id": row.ID, "name": row.Name, "protocol": row.Protocol, "base_url": row.BaseURL,
		"extra_params": params, "enabled": row.Enabled, "timeout_seconds": row.TimeoutSeconds,
		"has_key": row.APIKeyEncrypted != "", "cache_hours": hot.CacheHours(params),
		"vertical": strParam(params, "vertical"),
	}
}

func strParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	value, _ := params[key].(string)
	return strings.TrimSpace(value)
}

func (a *App) adminSaveHotSource(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	var body struct {
		Name           string         `json:"name"`
		Protocol       string         `json:"protocol"`
		BaseURL        string         `json:"base_url"`
		APIKey         string         `json:"api_key"`
		Enabled        *bool          `json:"enabled"`
		TimeoutSeconds *int           `json:"timeout_seconds"`
		ExtraParams    map[string]any `json:"extra_params"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	name := strings.TrimSpace(body.Name)
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 40 {
		return invalid()
	}
	if body.Protocol != "http" && body.Protocol != "mcp" {
		return bad("热点源协议只支持 http 或 mcp")
	}
	if err := validateHTTPURL(body.BaseURL); err != nil {
		return err
	}
	timeout := 8
	if body.TimeoutSeconds != nil {
		timeout = *body.TimeoutSeconds
	}
	if timeout < 3 || timeout > 30 {
		return bad("超时时间需在 3 到 30 秒之间")
	}
	params := body.ExtraParams
	if params == nil {
		params = map[string]any{}
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	now := time.Now().UTC()
	var saved *model.ServiceConfig
	err := a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		existing, err := a.Store.FindService(r.Context(), tx, "hot_source", name)
		if err != nil {
			return err
		}
		enc := ""
		if existing != nil {
			enc = existing.APIKeyEncrypted
		}
		if strings.TrimSpace(body.APIKey) != "" {
			enc, err = security.EncryptSecret(a.Cfg.MasterKey, strings.TrimSpace(body.APIKey))
			if err != nil {
				return err
			}
		}
		row := &model.ServiceConfig{
			ConfigType: "hot_source", Name: name, Protocol: body.Protocol, BaseURL: strings.TrimSpace(body.BaseURL),
			APIKeyEncrypted: enc, ExtraParams: params, Enabled: enabled, TimeoutSeconds: timeout, UpdatedAt: now,
		}
		if existing == nil {
			if err := a.Store.InsertService(r.Context(), tx, row); err != nil {
				return err
			}
		} else {
			row.ID = existing.ID
			row.Priority = existing.Priority
			row.ModelName = existing.ModelName
			row.ExtraHeaders = existing.ExtraHeaders
			if err := a.Store.UpdateService(r.Context(), tx, row); err != nil {
				return err
			}
		}
		saved = row
		actor := admin.ID
		return a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: "hot_source", Target: "service:" + name, Detail: "保存热点源", CreatedAt: now,
		})
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, hotSourceDict(saved))
	return nil
}

func (a *App) adminDeleteHotSource(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	id, err := pathID(r, "id", "找不到这个热点源")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		row, err := a.Store.GetService(r.Context(), tx, id)
		if err != nil {
			return err
		}
		if row == nil || row.ConfigType != "hot_source" {
			return &AppError{Status: 404, Message: "找不到这个热点源"}
		}
		if err := a.Store.DeleteService(r.Context(), tx, id); err != nil {
			return err
		}
		actor := admin.ID
		return a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: "hot_source_delete", Target: "service:" + strconv.FormatInt(id, 10),
			Detail: "删除热点源 " + row.Name, CreatedAt: now,
		})
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) adminTestHotSource(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	id, err := pathID(r, "id", "找不到这个热点源")
	if err != nil {
		return err
	}
	row, err := a.Store.GetService(r.Context(), nil, id)
	if err != nil {
		return err
	}
	if row == nil || row.ConfigType != "hot_source" {
		return &AppError{Status: 404, Message: "找不到这个热点源"}
	}
	key := ""
	if row.APIKeyEncrypted != "" {
		key, err = security.DecryptSecret(a.Cfg.MasterKey, row.APIKeyEncrypted)
		if err != nil {
			return bad("密钥无法读取")
		}
	}
	timeout := time.Duration(row.TimeoutSeconds) * time.Second
	if timeout < 3*time.Second || timeout > 30*time.Second {
		timeout = 8 * time.Second
	}
	items, err := hot.Fetch(r.Context(), a.HTTP, hot.Source{
		Name: row.Name, Protocol: row.Protocol, BaseURL: row.BaseURL, APIKey: key, Params: row.ExtraParams, Timeout: timeout,
	})
	if err != nil {
		return bad(err.Error())
	}
	preview := make([]map[string]any, 0, 5)
	for _, item := range items {
		if len(preview) >= 5 {
			break
		}
		preview = append(preview, map[string]any{"title": item.Title, "url": item.URL, "heat": item.Heat})
	}
	writeJSON(w, 200, map[string]any{"count": len(items), "preview": preview})
	return nil
}

func (a *App) notePriorAppID(ctx context.Context, q db.Q, userID int64, seenBefore bool) error {
	if !seenBefore {
		return nil
	}
	return a.Store.RevokeInvitee(ctx, q, userID, "公众号曾被绑定")
}

func attachInvite(ctx context.Context, st *store.Store, q db.Q, user *model.User, code, deviceHash, ip string, now time.Time) error {
	if deviceHash != "" {
		ok, err := st.HasActiveConsent(ctx, q, user.ID, "device_fingerprint", "1")
		if err != nil {
			return err
		}
		if !ok {
			deviceHash = ""
		}
	}
	if _, err := st.EnsureInviteCode(ctx, q, user.ID, deviceHash); err != nil {
		return err
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return nil
	}
	inviter, err := st.UserByInviteCode(ctx, q, code)
	if err != nil || inviter == 0 || inviter == user.ID {
		return err
	}
	sameDevice := false
	if deviceHash != "" {
		n, err := st.CountDeviceHash(ctx, q, deviceHash)
		if err != nil {
			return err
		}
		sameDevice = n > 1
	}
	ipCount, err := st.CountIPPrefixToday(ctx, q, ip, now)
	if err != nil {
		return err
	}
	rules, err := growth.RulesFromSettings(func(key string) (string, error) {
		return st.GetSetting(ctx, q, key)
	})
	if err != nil {
		return err
	}
	status, reason := growth.Classify(growth.VirtualPhone(user.Phone), sameDevice, ipCount >= growth.IPBurstCount)
	return st.InsertReferral(ctx, q, &store.Referral{
		InviterID: inviter, InviteeID: user.ID, Status: status, Reason: reason,
		DeviceHash: deviceHash, IP: ip, ObserveUntil: now.Add(time.Duration(rules.ObserveDays) * 24 * time.Hour), CreatedAt: now,
	})
}

func (a *App) deviceForInvite(ctx context.Context, q db.Q, r *http.Request, userID int64, deviceID string, consent bool, now time.Time) (string, error) {
	hash := deviceFrom(r, deviceID, consent)
	if hash == "" {
		return "", nil
	}
	if err := a.Store.InsertConsent(ctx, q, &model.Consent{
		UserID: userID, DocType: "device_fingerprint", Version: "1", ConsentItem: "invite_device",
		IP: clientIP(r), UserAgent: userAgent(r), CreatedAt: now,
	}); err != nil {
		return "", err
	}
	return hash, nil
}

func deviceFrom(r *http.Request, bodyID string, consent bool) string {
	if !consent {
		return ""
	}
	raw := strings.TrimSpace(bodyID)
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("X-Device-Id"))
	}
	if utf8.RuneCountInString(raw) > 128 {
		raw = string([]rune(raw)[:128])
	}
	return growth.DeviceHash(raw)
}
