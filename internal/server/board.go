package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/ops"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
)

func (a *App) costPolicy(ctx context.Context) (ops.Policy, error) {
	return ops.LoadPolicy(func(key string) (string, error) {
		return a.Store.GetSetting(ctx, nil, key)
	})
}

func (a *App) activeLegalVersion(ctx context.Context) string {
	v, err := a.Store.GetSetting(ctx, nil, ops.KeyLegalVersion)
	if err != nil || strings.TrimSpace(v) == "" {
		return catalog.LegalVersion
	}
	return strings.TrimSpace(v)
}

func (a *App) adminMetrics(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	now := time.Now().UTC()
	m, err := a.Store.Metrics(r.Context(), schedule.Format(now.Add(-7*24*time.Hour)), schedule.Format(now.Add(-30*24*time.Hour)))
	if err != nil {
		return err
	}
	policy, err := a.costPolicy(r.Context())
	if err != nil {
		return err
	}
	per := 0
	if m.Articles30d > 0 {
		per = m.CostCents30d / m.Articles30d
	}
	conversion := 0.0
	if m.UsersTotal > 0 {
		conversion = float64(m.PaidUsers) / float64(m.UsersTotal)
	}
	writeJSON(w, 200, map[string]any{
		"users_total": m.UsersTotal, "users_7d": m.Users7d, "users_30d": m.Users30d,
		"paid_users": m.PaidUsers, "conversion": conversion,
		"orders_paid_30d": m.OrdersPaid30d, "renewals_30d": m.Renewals30d,
		"articles_30d": m.Articles30d, "cost_cents_30d": m.CostCents30d, "cost_per_article_cents": per,
		"referrals": m.Referrals, "referrals_valid": m.ReferralsValid, "referrals_frozen": m.ReferralsFrozen,
		"ai_label": policy.AILabel, "cost": policyJSON(policy),
		"legal_version": a.activeLegalVersion(r.Context()),
	})
	return nil
}

func policyJSON(p ops.Policy) map[string]any {
	return map[string]any{
		"chat_cents_per_1k": p.ChatCentsPer1K, "image_cents": p.ImageCents, "search_cents": p.SearchCents,
		"month_cap_cents": p.MonthCapCents, "action": p.Action, "ai_label": p.AILabel,
	}
}

func (a *App) adminSaveCost(w http.ResponseWriter, r *http.Request, actor *model.User) error {
	var body struct {
		ChatCentsPer1K int    `json:"chat_cents_per_1k"`
		ImageCents     int    `json:"image_cents"`
		SearchCents    int    `json:"search_cents"`
		MonthCapCents  int    `json:"month_cap_cents"`
		Action         string `json:"action"`
		AILabel        bool   `json:"ai_label"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	policy := ops.Normalize(ops.Policy{
		ChatCentsPer1K: body.ChatCentsPer1K, ImageCents: body.ImageCents, SearchCents: body.SearchCents,
		MonthCapCents: body.MonthCapCents, Action: body.Action, AILabel: body.AILabel,
	})
	now := time.Now().UTC()
	label := "0"
	if policy.AILabel {
		label = "1"
	}
	err := a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		pairs := []struct{ k, v string }{
			{ops.KeyChatCentsPer1K, strconv.Itoa(policy.ChatCentsPer1K)},
			{ops.KeyImageCents, strconv.Itoa(policy.ImageCents)},
			{ops.KeySearchCents, strconv.Itoa(policy.SearchCents)},
			{ops.KeyMonthCapCents, strconv.Itoa(policy.MonthCapCents)},
			{ops.KeyBreakerAction, policy.Action},
			{ops.KeyAILabel, label},
		}
		for _, pair := range pairs {
			if err := a.Store.SetSetting(r.Context(), tx, pair.k, pair.v, now); err != nil {
				return err
			}
		}
		return a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor.ID, Action: "cost_policy", Target: "ops",
			Detail:    "月上限 " + strconv.Itoa(policy.MonthCapCents) + " 分，动作 " + policy.Action + "，AI 标识 " + label,
			CreatedAt: now,
		})
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, policyJSON(policy))
	return nil
}

func (a *App) adminConsents(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	id, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if err != nil || id <= 0 {
		return bad("请提供 user_id")
	}
	rows, err := a.Store.ListConsents(r.Context(), id)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"id": row.ID, "user_id": row.UserID, "doc_type": row.DocType, "version": row.Version,
			"consent_item": row.ConsentItem, "ip": row.IP, "created_at": jtimev(row.CreatedAt),
		})
	}
	writeJSON(w, 200, out)
	return nil
}

var legalTypes = map[string]bool{
	"user_agreement": true, "privacy": true, "disclaimer": true, "membership": true,
	"content_rules": true, "invite_rules": true, "page_home": true, "page_help": true, "page_about": true,
}

func (a *App) adminLegal(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	rows, err := a.Store.ListLegalAll(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, 200, legalRows(rows, true))
	return nil
}

func (a *App) adminPublishLegal(w http.ResponseWriter, r *http.Request, actor *model.User) error {
	var body struct {
		DocType          string `json:"doc_type"`
		Version          string `json:"version"`
		Title            string `json:"title"`
		Content          string `json:"content"`
		EffectiveDate    string `json:"effective_date"`
		RequireReconsent bool   `json:"require_reconsent"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	body.DocType = strings.TrimSpace(body.DocType)
	body.Version = strings.TrimSpace(body.Version)
	body.Title = strings.TrimSpace(body.Title)
	body.Content = strings.TrimSpace(body.Content)
	body.EffectiveDate = strings.TrimSpace(body.EffectiveDate)
	if !legalTypes[body.DocType] || body.Version == "" || body.Title == "" || body.Content == "" {
		return bad("请填写文档类型、版本、标题和正文")
	}
	if len(body.Version) > 40 || len(body.Title) > 80 {
		return bad("版本或标题过长")
	}
	if body.RequireReconsent && body.DocType != "user_agreement" && body.DocType != "privacy" {
		return bad("只有用户协议和隐私政策的重大变更会要求重新同意")
	}
	if body.EffectiveDate == "" {
		body.EffectiveDate = time.Now().UTC().Format("2006-01-02")
	}
	exists, err := a.Store.HasLegal(r.Context(), nil, body.DocType, body.Version)
	if err != nil {
		return err
	}
	if exists {
		return bad("这个版本已经存在")
	}
	now := time.Now().UTC()
	var otherDoc *model.LegalDoc
	if body.RequireReconsent {
		other := "privacy"
		if body.DocType == "privacy" {
			other = "user_agreement"
		}
		otherDoc, err = a.Store.GetLegal(r.Context(), other)
		if err != nil {
			return err
		}
	}
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		if err := a.Store.ArchiveLegal(r.Context(), tx, body.DocType); err != nil {
			return err
		}
		if err := a.Store.InsertLegal(r.Context(), tx, model.LegalDoc{
			DocType: body.DocType, Version: body.Version, Title: body.Title, Content: body.Content,
			Status: "active", EffectiveDate: body.EffectiveDate,
		}); err != nil {
			return err
		}
		if body.RequireReconsent {
			other := "privacy"
			if body.DocType == "privacy" {
				other = "user_agreement"
			}
			current := otherDoc
			if current != nil && current.Version != body.Version {
				has, err := a.Store.HasLegal(r.Context(), tx, other, body.Version)
				if err != nil {
					return err
				}
				if !has {
					if err := a.Store.ArchiveLegal(r.Context(), tx, other); err != nil {
						return err
					}
					if err := a.Store.InsertLegal(r.Context(), tx, model.LegalDoc{
						DocType: other, Version: body.Version, Title: current.Title, Content: current.Content,
						Status: "active", EffectiveDate: body.EffectiveDate,
					}); err != nil {
						return err
					}
				}
			}
			if err := a.Store.SetSetting(r.Context(), tx, ops.KeyLegalVersion, body.Version, now); err != nil {
				return err
			}
		}
		return a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor.ID, Action: "legal_publish", Target: body.DocType,
			Detail: body.Version, CreatedAt: now,
		})
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true, "version": body.Version, "legal_version": a.activeLegalVersion(r.Context())})
	return nil
}

func legalRows(rows []model.LegalDoc, withContent bool) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		item := map[string]any{
			"doc_type": row.DocType, "version": row.Version, "title": row.Title,
			"status": row.Status, "effective_date": row.EffectiveDate,
		}
		if withContent {
			item["content"] = row.Content
		}
		out = append(out, item)
	}
	return out
}
