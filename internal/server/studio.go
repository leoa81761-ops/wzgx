package server

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/cover"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/jobs"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/pipeline"
	"github.com/leoa81761-ops/wzgx/internal/publish"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/render"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
)

type sysRNG struct{}

func (sysRNG) Intn(n int) int {
	if n <= 1 {
		return 0
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0
	}
	return int(binary.BigEndian.Uint64(b[:]) % uint64(n))
}

func pathID(r *http.Request, key, missing string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(key), 10, 64)
	if err != nil || id <= 0 {
		return 0, &AppError{Status: 404, Message: missing}
	}
	return id, nil
}

func asQuota(err error) error {
	var qe *quota.Error
	if errors.As(err, &qe) {
		return bad(qe.Message)
	}
	return err
}

func (a *App) legalList(w http.ResponseWriter, r *http.Request) error {
	rows, err := a.Store.ListLegal(r.Context())
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{"doc_type": row.DocType, "title": row.Title, "version": row.Version})
	}
	writeJSON(w, 200, out)
	return nil
}

func (a *App) legalDetail(w http.ResponseWriter, r *http.Request) error {
	docType := r.PathValue("doc_type")
	var row *model.LegalDoc
	var err error
	if version := strings.TrimSpace(r.URL.Query().Get("version")); version != "" {
		row, err = a.Store.GetLegalVersion(r.Context(), docType, version)
	} else {
		row, err = a.Store.GetLegal(r.Context(), docType)
	}
	if err != nil {
		return err
	}
	if row == nil {
		return &AppError{Status: 404, Message: "没有这份文档"}
	}
	history, err := a.Store.ListLegalVersions(r.Context(), docType)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{
		"doc_type": row.DocType, "title": row.Title, "version": row.Version,
		"content": row.Content, "effective_date": row.EffectiveDate, "status": row.Status,
		"versions": legalRows(history, false),
	})
	return nil
}

func (a *App) meta(w http.ResponseWriter, r *http.Request) error {
	wx, err := a.Cache.WeChat(r.Context(), nil)
	if err != nil {
		return err
	}
	plans, err := a.Store.ListPlanConfigs(r.Context())
	if err != nil {
		return err
	}
	planOut := make([]map[string]any, 0, len(plans))
	for _, item := range plans {
		planOut = append(planOut, map[string]any{
			"tier": item.Tier, "name": item.Name, "price_label": item.PriceLabel,
			"daily_quota": item.DailyQuota, "period_quota": item.PeriodQuota, "period_unit": item.PeriodUnit,
			"max_accounts": item.MaxAccounts, "max_plans": item.MaxPlans, "max_words": item.MaxWords, "summary": item.Summary,
		})
	}
	styles, err := a.styleDicts(r.Context(), true)
	if err != nil {
		return err
	}
	if len(styles) == 0 {
		styles = make([]map[string]any, 0, len(catalog.Styles))
		for _, item := range catalog.Styles {
			styles = append(styles, map[string]any{"key": item.Key, "name": item.Name, "description": item.Description, "tone": item.Tone, "min_tier": item.MinTier, "enabled": true})
		}
	}
	templates, err := a.templateDicts(r.Context(), true)
	if err != nil {
		return err
	}
	if len(templates) == 0 {
		templates = make([]map[string]any, 0, len(catalog.Templates))
		for _, item := range catalog.Templates {
			templates = append(templates, map[string]any{"key": item.Key, "name": item.Name, "description": item.Description, "min_tier": item.MinTier, "enabled": true})
		}
	}
	writeJSON(w, 200, map[string]any{
		"styles": styles, "templates": templates, "directions": catalog.Directions,
		"windows": catalog.Windows, "word_choices": catalog.WordChoices, "columns": catalog.Columns,
		"wechat_mode": wx.Mode, "public_egress_ip": wx.IP, "plans": planOut, "pay": a.payMeta(),
	})
	return nil
}

func (a *App) myQuota(w http.ResponseWriter, r *http.Request, user *model.User) error {
	fresh, err := a.Store.GetUser(r.Context(), nil, user.ID)
	if err != nil {
		return err
	}
	if fresh == nil {
		return &AppError{Status: 401, Message: "请先登录"}
	}
	snap, err := quota.Snapshot(r.Context(), a.Store, nil, fresh, time.Now().UTC())
	if err != nil {
		return err
	}
	writeJSON(w, 200, snap)
	return nil
}

func (a *App) myConsents(w http.ResponseWriter, r *http.Request, user *model.User) error {
	rows, err := a.Store.ListConsents(r.Context(), user.ID)
	if err != nil {
		return err
	}
	docs, err := a.Store.ListLegal(r.Context())
	if err != nil {
		return err
	}
	titles := map[string]string{"wx_credentials": "公众号凭证授权"}
	for _, doc := range docs {
		titles[doc.DocType] = doc.Title
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		title := titles[row.DocType]
		if title == "" {
			title = row.DocType
		}
		out = append(out, map[string]any{
			"id": row.ID, "doc_type": row.DocType, "title": title, "version": row.Version,
			"consent_item": row.ConsentItem, "created_at": jtimev(row.CreatedAt),
		})
	}
	writeJSON(w, 200, out)
	return nil
}

func accountDict(account *model.WxAccount) map[string]any {
	subject := "企业"
	if account.SubjectType == "personal" {
		subject = "个人"
	}
	service := "服务号"
	if account.ServiceType == "subscription" {
		service = "订阅号"
	}
	verified := "未认证"
	if account.Verified {
		verified = "已认证"
	}
	delivery := "仅手动导出"
	if account.DeliveryMode == "draft" {
		delivery = "写入草稿箱"
	}
	report := account.PermissionReport
	if report == nil {
		report = map[string]any{}
	}
	return map[string]any{
		"id": account.ID, "name": account.Name, "app_id": account.AppID,
		"subject_type": account.SubjectType, "service_type": account.ServiceType, "verified": account.Verified,
		"type_label":    subject + " · " + service + " · " + verified,
		"delivery_mode": account.DeliveryMode, "delivery_label": delivery,
		"bind_status": account.BindStatus, "permission_report": report, "bound_at": jtime(account.BoundAt),
	}
}

func planDict(plan *model.WritingPlan) map[string]any {
	style := catalog.StyleByKey(plan.StyleKey)
	styleName := style.Name
	if styleName == "" {
		styleName = plan.StyleKey
	}
	template := catalog.TemplateByKey(plan.TemplateKey)
	templateName := template.Name
	if templateName == "" {
		templateName = plan.TemplateKey
	}
	label := "每周"
	if plan.ScheduleType == "daily" {
		label = "每天"
	}
	days := plan.Weekdays
	if days == nil {
		days = []int{}
	}
	return map[string]any{
		"id": plan.ID, "wx_account_id": plan.WxAccountID, "direction": plan.Direction, "mode": plan.Mode,
		"style_key": plan.StyleKey, "style_name": styleName, "template_key": plan.TemplateKey, "template_name": templateName,
		"word_target": plan.WordTarget, "daily_count": plan.ArticlesPerDay(), "schedule_type": plan.ScheduleType, "schedule_label": label,
		"weekdays": days, "time_window": plan.TimeWindow, "window_label": catalog.WindowLabel(plan.TimeWindow),
		"status": plan.Status, "pause_reason": plan.PauseReason, "next_run_at": jtime(plan.NextRunAt),
		"confirm_mode": plan.ConfirmMode,
	}
}

func articleDict(article *model.Article, includeHTML bool) map[string]any {
	alts := article.AltTitles
	if alts == nil {
		alts = []any{}
	}
	payload := map[string]any{
		"id": article.ID, "title": article.Title, "summary": article.Summary, "status": article.Status,
		"status_label": statusLabel(article.Status), "failure_reason": article.FailureReason, "delivery_note": article.DeliveryNote,
		"risk_level": article.RiskLevel, "risk_label": riskLabel(article.RiskLevel),
		"draft_media_id": article.DraftMediaID, "template_key": article.TemplateKey, "style_key": article.StyleKey,
		"subtopic": article.Subtopic, "column_type": article.ColumnType, "topic_tag": article.TopicTag,
		"regen_count": article.RegenCount, "alt_titles": alts, "created_at": jtimev(article.CreatedAt),
		"plan_id": article.PlanID, "wx_account_id": article.WxAccountID,
	}
	if includeHTML {
		sources := article.Sources
		if sources == nil {
			sources = []any{}
		}
		payload["body_html"] = article.BodyHTML
		payload["sources"] = sources
	}
	return payload
}

func statusLabel(status string) string {
	switch status {
	case "generating":
		return "生成中"
	case "in_draft":
		return "已入草稿箱"
	case "exported":
		return "已导出"
	case "failed":
		return "失败"
	case "held":
		return "未推送"
	case "publish_wait":
		return "等待发布"
	case "publishing":
		return "发布中"
	case "published":
		return "已发布"
	case "publish_failed":
		return "发布失败"
	case "publish_unknown":
		return "发布结果未知"
	default:
		return status
	}
}

func riskLabel(level string) string {
	switch level {
	case "high":
		return "高风险"
	case "medium":
		return "中风险"
	case "low":
		return "低风险"
	default:
		if level == "" {
			return "低风险"
		}
		return level
	}
}

func (a *App) planView(ctx context.Context, plan *model.WritingPlan) (map[string]any, error) {
	view := planDict(plan)
	title, err := a.Store.GetPin(ctx, nil, plan.ID)
	if err != nil {
		return nil, err
	}
	tags, err := a.Store.ListVetoTags(ctx, nil, plan.ID)
	if err != nil {
		return nil, err
	}
	if tags == nil {
		tags = []string{}
	}
	view["specified_title"] = title
	view["vetoes"] = tags
	if err := a.attachProposal(ctx, view, plan.ID); err != nil {
		return nil, err
	}
	pub, err := a.Store.GetPlanPublish(ctx, nil, plan.ID)
	if err != nil {
		return nil, err
	}
	delay := pub.DelayMinutes
	if pub.UpdatedAt.IsZero() {
		delay = 30
	}
	view["auto_publish"] = pub.Enabled
	view["delay_minutes"] = delay
	account, err := a.Store.GetAccount(ctx, nil, plan.WxAccountID)
	if err != nil {
		return nil, err
	}
	owner, err := a.Store.GetUser(ctx, nil, plan.UserID)
	if err != nil {
		return nil, err
	}
	ready, block := publish.Ready(owner, account, time.Now().UTC())
	view["publish_ready"] = ready
	view["publish_block"] = block
	return view, nil
}

func (a *App) listAccounts(w http.ResponseWriter, r *http.Request, user *model.User) error {
	rows, err := a.Store.ListAccountsByUser(r.Context(), user.ID)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		out = append(out, accountDict(&rows[i]))
	}
	writeJSON(w, 200, out)
	return nil
}

type accountBody struct {
	Name        string `json:"name"`
	AppID       string `json:"app_id"`
	AppSecret   string `json:"app_secret"`
	SubjectType string `json:"subject_type"`
	ServiceType string `json:"service_type"`
	Verified    bool   `json:"verified"`
	Consent     bool   `json:"consent"`
}

func (a *App) bindAccount(w http.ResponseWriter, r *http.Request, user *model.User) error {
	var body accountBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	name := strings.TrimSpace(body.Name)
	appID := strings.TrimSpace(body.AppID)
	secret := strings.TrimSpace(body.AppSecret)
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 40 || utf8.RuneCountInString(appID) < 4 || utf8.RuneCountInString(appID) > 64 || utf8.RuneCountInString(secret) < 4 || utf8.RuneCountInString(secret) > 128 {
		return invalid()
	}
	if !body.Consent {
		return bad("绑定前请勾选同意保存公众号凭证")
	}
	if body.SubjectType != "personal" && body.SubjectType != "enterprise" {
		return bad("请选择主体类型")
	}
	if body.ServiceType != "subscription" && body.ServiceType != "service" {
		return bad("请选择订阅号或服务号")
	}
	if body.SubjectType == "personal" && body.Verified {
		return bad("个人主体无法认证，请不要勾选已认证")
	}
	now := time.Now().UTC()
	var mode, base string
	err := a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		fresh, err := a.Store.GetUser(r.Context(), tx, user.ID)
		if err != nil {
			return err
		}
		if fresh == nil {
			return &AppError{Status: 401, Message: "请先登录"}
		}
		if err := quota.EnforceFreeLimits(r.Context(), a.Store, tx, fresh, now); err != nil {
			return err
		}
		tier, state := quota.Membership(fresh, now)
		if state == "grace" {
			return bad("会员处于宽限期，暂不能新增公众号")
		}
		limits, err := quota.LimitsFor(r.Context(), a.Store, tx, tier)
		if err != nil {
			return err
		}
		count, err := a.Store.CountActiveAccounts(r.Context(), tx, user.ID)
		if err != nil {
			return err
		}
		if count >= limits.MaxAccounts {
			return bad("已达到当前会员可绑定的公众号数量")
		}
		if err := a.assertCanBind(r.Context(), tx, user.ID, appID, now); err != nil {
			return err
		}
		wx, err := a.Cache.WeChat(r.Context(), tx)
		if err != nil {
			return err
		}
		mode = "live"
		if wx.Mode == "mock" || strings.HasPrefix(appID, "wx_mock") {
			mode = "mock"
		}
		base = wx.Base
		return nil
	})
	if err != nil {
		return err
	}
	client := wechat.Build(appID, secret, mode, base, a.HTTP, a.Tokens)
	report := wechat.CheckPermissions(r.Context(), client, cover.CoverPNG("simple"))
	if !report.OK {
		return bad(permissionDetail(report))
	}
	enc, err := security.EncryptSecret(a.Cfg.MasterKey, secret)
	if err != nil {
		return err
	}
	var created *model.WxAccount
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		prior, err := a.Store.ListAccountsByAppID(r.Context(), tx, appID)
		if err != nil {
			return err
		}
		if err := a.assertCanBind(r.Context(), tx, user.ID, appID, now); err != nil {
			return err
		}
		bound := now
		created = &model.WxAccount{
			UserID: user.ID, Name: name, AppID: appID, SecretEncrypted: enc,
			SubjectType: body.SubjectType, ServiceType: body.ServiceType, Verified: body.Verified,
			DeliveryMode: report.DeliveryMode, BindStatus: "active",
			PermissionReport: permissionMap(report),
			BoundAt:          &bound,
		}
		if err := a.Store.InsertAccount(r.Context(), tx, created); err != nil {
			if db.IsUnique(err) {
				return bad("该公众号已绑定在其他账号上")
			}
			return err
		}
		if err := a.Store.InsertConsent(r.Context(), tx, &model.Consent{
			UserID: user.ID, DocType: "wx_credentials", Version: catalog.LegalVersion, ConsentItem: "wx_credentials",
			IP: clientIP(r), UserAgent: userAgent(r), CreatedAt: now,
		}); err != nil {
			return err
		}
		return a.notePriorAppID(r.Context(), tx, user.ID, len(prior) > 0)
	})
	if err != nil {
		return err
	}
	payload := accountDict(created)
	note := ""
	if body.SubjectType == "enterprise" && body.Verified {
		note = "企业认证号可以在写作计划里单独开启自动发布，默认关闭。没开启时仍然只写草稿箱。"
	}
	payload["auto_publish_note"] = note
	writeJSON(w, 200, payload)
	return nil
}

func permissionMap(report wechat.PermissionReport) map[string]any {
	return map[string]any{"checks": report.Checks, "can_draft": report.CanDraft, "can_publish": report.CanPublish}
}

func permissionDetail(report wechat.PermissionReport) string {
	for _, check := range report.Checks {
		ok, _ := check["ok"].(bool)
		if ok {
			continue
		}
		if detail, _ := check["detail"].(string); detail != "" {
			return detail
		}
	}
	return "凭证无效，或微信接口拒绝了调用。请检查 AppID、AppSecret 和 IP 白名单。模拟模式可使用任意看起来合法的 AppID。"
}

func tokenCheckFailed(report wechat.PermissionReport) bool {
	if len(report.Checks) == 0 {
		return !report.OK
	}
	name, _ := report.Checks[0]["name"].(string)
	ok, _ := report.Checks[0]["ok"].(bool)
	return name == "access_token" && !ok
}

func (a *App) assertCanBind(ctx context.Context, tx *db.Tx, userID int64, appID string, now time.Time) error {
	rows, err := a.Store.ListAccountsByAppID(ctx, tx, appID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.BindStatus == "active" || row.BindStatus == "auth_invalid" {
			if row.UserID == userID {
				return bad("你已经绑定了这个公众号")
			}
			return bad("该公众号已绑定在其他账号上")
		}
		if row.BindStatus == "unbound" && row.UnboundAt != nil && row.UserID != userID {
			if now.Before(row.UnboundAt.UTC().Add(30 * 24 * time.Hour)) {
				return bad("该公众号解绑后 30 天内不能被其他账号绑定")
			}
		}
	}
	return nil
}

type secretBody struct {
	AppSecret string `json:"app_secret"`
}

func (a *App) recheckAccount(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "account_id", "找不到这个公众号")
	if err != nil {
		return err
	}
	var body secretBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	account, err := a.ownAccount(r.Context(), nil, user.ID, id)
	if err != nil {
		return err
	}
	secret := strings.TrimSpace(body.AppSecret)
	enc := account.SecretEncrypted
	if secret != "" {
		enc, err = security.EncryptSecret(a.Cfg.MasterKey, secret)
		if err != nil {
			return err
		}
	} else if account.SecretEncrypted != "" {
		secret, err = security.DecryptSecret(a.Cfg.MasterKey, account.SecretEncrypted)
		if err != nil {
			return bad(err.Error())
		}
	} else {
		return bad("请重新填写 AppSecret")
	}
	wx, err := a.Cache.WeChat(r.Context(), nil)
	if err != nil {
		return err
	}
	mode := "live"
	if wx.Mode == "mock" || strings.HasPrefix(account.AppID, "wx_mock") {
		mode = "mock"
	}
	client := wechat.Build(account.AppID, secret, mode, wx.Base, a.HTTP, a.Tokens)
	report := wechat.CheckPermissions(r.Context(), client, cover.CoverPNG("simple"))
	var payload map[string]any
	var failed error
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		account, err := a.ownAccount(r.Context(), tx, user.ID, id)
		if err != nil {
			return err
		}
		account.SecretEncrypted = enc
		account.PermissionReport = permissionMap(report)
		if tokenCheckFailed(report) {
			if report.AuthFailed {
				account.BindStatus = "auth_invalid"
				if err := a.Store.UpdateAccount(r.Context(), tx, account); err != nil {
					return err
				}
				failed = bad("凭证仍然无效，账号已标记为授权失效")
				return nil
			}
			if err := a.Store.UpdateAccount(r.Context(), tx, account); err != nil {
				return err
			}
			failed = bad(permissionDetail(report))
			return nil
		}
		account.BindStatus = "active"
		account.DeliveryMode = report.DeliveryMode
		if err := a.Store.UpdateAccount(r.Context(), tx, account); err != nil {
			return err
		}
		plans, err := a.Store.ListPlansByAccount(r.Context(), tx, account.ID)
		if err != nil {
			return err
		}
		for i := range plans {
			if plans[i].Status == "paused" && plans[i].PauseReason == "授权失效" {
				plans[i].Status = "active"
				plans[i].PauseReason = ""
				if err := a.Store.UpdatePlan(r.Context(), tx, &plans[i]); err != nil {
					return err
				}
			}
		}
		payload = accountDict(account)
		return nil
	})
	if err != nil {
		return err
	}
	if failed != nil {
		return failed
	}
	writeJSON(w, 200, payload)
	return nil
}

func (a *App) unbindAccount(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "account_id", "找不到这个公众号")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		account, err := a.ownAccount(r.Context(), tx, user.ID, id)
		if err != nil {
			return err
		}
		account.BindStatus = "unbound"
		account.SecretEncrypted = ""
		account.UnboundAt = &now
		if err := a.Store.UpdateAccount(r.Context(), tx, account); err != nil {
			return err
		}
		plans, err := a.Store.ListPlansByAccount(r.Context(), tx, account.ID)
		if err != nil {
			return err
		}
		for i := range plans {
			plans[i].Status = "paused"
			plans[i].PauseReason = "已解绑"
			if err := a.Store.UpdatePlan(r.Context(), tx, &plans[i]); err != nil {
				return err
			}
		}
		return a.Store.RevokeInvitee(r.Context(), tx, user.ID, "公众号已解绑")
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) ownAccount(ctx context.Context, q db.Q, userID, id int64) (*model.WxAccount, error) {
	account, err := a.Store.GetAccount(ctx, q, id)
	if err != nil {
		return nil, err
	}
	if account == nil || account.UserID != userID || account.BindStatus == "unbound" {
		return nil, &AppError{Status: 404, Message: "找不到这个公众号"}
	}
	return account, nil
}

func (a *App) ownPlan(ctx context.Context, q db.Q, userID, id int64) (*model.WritingPlan, error) {
	plan, err := a.Store.GetPlan(ctx, q, id)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.UserID != userID || plan.Status == "deleted" {
		return nil, &AppError{Status: 404, Message: "找不到这个写作计划"}
	}
	return plan, nil
}

func (a *App) ownArticle(ctx context.Context, q db.Q, userID, id int64) (*model.Article, error) {
	article, err := a.Store.GetArticle(ctx, q, id)
	if err != nil {
		return nil, err
	}
	if article == nil || article.UserID != userID {
		return nil, &AppError{Status: 404, Message: "找不到这篇文章"}
	}
	return article, nil
}

func (a *App) listPlans(w http.ResponseWriter, r *http.Request, user *model.User) error {
	rows, err := a.Store.ListPlansByUser(r.Context(), user.ID)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		view, err := a.planView(r.Context(), &rows[i])
		if err != nil {
			return err
		}
		out = append(out, view)
	}
	writeJSON(w, 200, out)
	return nil
}

type planBody struct {
	WxAccountID  int64  `json:"wx_account_id"`
	Direction    string `json:"direction"`
	StyleKey     string `json:"style_key"`
	TemplateKey  string `json:"template_key"`
	WordTarget   int    `json:"word_target"`
	DailyCount   int    `json:"daily_count"`
	ScheduleType string `json:"schedule_type"`
	Weekdays     []int  `json:"weekdays"`
	TimeWindow   string `json:"time_window"`
}

func (a *App) createPlan(w http.ResponseWriter, r *http.Request, user *model.User) error {
	var body planBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	direction := strings.TrimSpace(body.Direction)
	if utf8.RuneCountInString(direction) < 1 || utf8.RuneCountInString(direction) > 40 {
		return invalid()
	}
	for _, word := range catalog.BlockedDirectionWords {
		if strings.Contains(direction, word) {
			return bad("「" + word + "」一类方向风险较高，当前版本不开放自动写作")
		}
	}
	if body.StyleKey == "" || body.TemplateKey == "" {
		return bad("请选择风格和排版模板")
	}
	if body.ScheduleType != "daily" && body.ScheduleType != "weekly" {
		return bad("周期只支持每天或每周")
	}
	weekdays := normalizeDays(body.Weekdays)
	if body.ScheduleType == "weekly" && len(weekdays) == 0 {
		return bad("每周生成请至少选择一天")
	}
	for _, day := range weekdays {
		if day < 1 || day > 7 {
			return bad("星期取值应为 1 到 7")
		}
	}
	if _, _, ok := catalog.WindowHours(body.TimeWindow); !ok {
		return bad("请选择生成时段")
	}
	now := time.Now().UTC()
	var created *model.WritingPlan
	err := a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		fresh, err := a.Store.GetUser(r.Context(), tx, user.ID)
		if err != nil {
			return err
		}
		if fresh == nil {
			return &AppError{Status: 401, Message: "请先登录"}
		}
		if err := quota.EnforceFreeLimits(r.Context(), a.Store, tx, fresh, now); err != nil {
			return err
		}
		tier, state := quota.Membership(fresh, now)
		if state == "grace" {
			return bad("会员处于宽限期，暂不能新增写作方向")
		}
		limits, err := quota.LimitsFor(r.Context(), a.Store, tx, tier)
		if err != nil {
			return err
		}
		if !catalog.WordAllowed(body.WordTarget) || body.WordTarget > limits.MaxWords {
			return bad("当前会员单篇上限为 " + strconv.Itoa(limits.MaxWords) + " 字")
		}
		if err := a.presetAllowed(r.Context(), tier, body.StyleKey, body.TemplateKey); err != nil {
			return err
		}
		count, err := a.Store.CountPlans(r.Context(), tx, user.ID)
		if err != nil {
			return err
		}
		if count >= limits.MaxPlans {
			return bad("已达到当前会员可设置的写作方向数量")
		}
		dailyCount := body.DailyCount
		if dailyCount == 0 {
			dailyCount = 1
		}
		if err := quota.CheckDailyCount(r.Context(), a.Store, tx, fresh, now, 0, dailyCount); err != nil {
			return asQuota(err)
		}
		account, err := a.ownAccount(r.Context(), tx, user.ID, body.WxAccountID)
		if err != nil {
			return err
		}
		if account.BindStatus != "active" {
			return bad("请先完成公众号绑定")
		}
		window := body.TimeWindow
		if tier == "free" {
			window = "night"
		}
		next, err := schedule.ComputeNextRun(now, body.ScheduleType, weekdays, window, sysRNG{})
		if err != nil {
			return bad(err.Error())
		}
		created = &model.WritingPlan{
			UserID: user.ID, WxAccountID: account.ID, Direction: direction, Mode: "managed",
			StyleKey: body.StyleKey, TemplateKey: body.TemplateKey, WordTarget: body.WordTarget, DailyCount: dailyCount,
			ScheduleType: body.ScheduleType, Weekdays: weekdays, TimeWindow: window, Status: "active",
			NextRunAt: &next, CreatedAt: now,
		}
		return a.Store.InsertPlan(r.Context(), tx, created)
	})
	if err != nil {
		return err
	}
	view, err := a.planView(r.Context(), created)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

func (a *App) setPlanDaily(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "plan_id", "找不到这个写作计划")
	if err != nil {
		return err
	}
	var body struct {
		DailyCount int `json:"daily_count"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	now := time.Now().UTC()
	var payload map[string]any
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		plan, err := a.ownPlan(r.Context(), tx, user.ID, id)
		if err != nil {
			return err
		}
		fresh, err := a.Store.GetUser(r.Context(), tx, user.ID)
		if err != nil {
			return err
		}
		if fresh == nil {
			return &AppError{Status: 401, Message: "请先登录"}
		}
		if err := quota.CheckDailyCount(r.Context(), a.Store, tx, fresh, now, plan.ID, body.DailyCount); err != nil {
			return asQuota(err)
		}
		plan.DailyCount = body.DailyCount
		if err := a.Store.UpdatePlan(r.Context(), tx, plan); err != nil {
			return err
		}
		payload = planDict(plan)
		return nil
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, payload)
	return nil
}

func (a *App) adminPlanConfigs(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	rows, err := a.Store.ListPlanConfigs(r.Context())
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, item := range rows {
		out = append(out, map[string]any{
			"tier": item.Tier, "name": item.Name, "price_label": item.PriceLabel,
			"daily_quota": item.DailyQuota, "period_quota": item.PeriodQuota, "period_unit": item.PeriodUnit,
			"summary": item.Summary,
		})
	}
	writeJSON(w, 200, out)
	return nil
}

func (a *App) adminSavePlanConfig(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	tier := r.PathValue("tier")
	switch tier {
	case "free", "standard", "premium", "lifetime":
	default:
		return bad("未知档位")
	}
	var body struct {
		DailyQuota  int `json:"daily_quota"`
		PeriodQuota int `json:"period_quota"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if body.DailyQuota < 1 || body.DailyQuota > 100 {
		return bad("每天篇数应为 1 到 100")
	}
	if body.PeriodQuota < body.DailyQuota || body.PeriodQuota > 10000 {
		return bad("周期额度不能小于每天篇数，也不能超过 10000")
	}
	now := time.Now().UTC()
	var saved map[string]any
	err := a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		row, err := a.Store.GetPlanConfig(r.Context(), tx, tier)
		if err != nil {
			return err
		}
		if row == nil {
			return &AppError{Status: 404, Message: "找不到这个套餐"}
		}
		if err := a.Store.UpdatePlanQuota(r.Context(), tx, tier, body.DailyQuota, body.PeriodQuota, row.Summary); err != nil {
			return err
		}
		actor := admin.ID
		if err := a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: "plan_quota", Target: tier,
			Detail:    strconv.Itoa(body.DailyQuota) + " 篇/天，" + strconv.Itoa(body.PeriodQuota) + " 篇/" + row.PeriodUnit,
			CreatedAt: now,
		}); err != nil {
			return err
		}
		saved = map[string]any{"tier": tier, "daily_quota": body.DailyQuota, "period_quota": body.PeriodQuota, "period_unit": row.PeriodUnit}
		return nil
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, saved)
	return nil
}

func normalizeDays(days []int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(days))
	for _, day := range days {
		if seen[day] {
			continue
		}
		seen[day] = true
		out = append(out, day)
	}
	sort.Ints(out)
	return out
}

func (a *App) pausePlan(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "plan_id", "找不到这个写作计划")
	if err != nil {
		return err
	}
	var payload map[string]any
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		plan, err := a.ownPlan(r.Context(), tx, user.ID, id)
		if err != nil {
			return err
		}
		plan.Status = "paused"
		plan.PauseReason = "用户暂停"
		if err := a.Store.UpdatePlan(r.Context(), tx, plan); err != nil {
			return err
		}
		payload = planDict(plan)
		return nil
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, payload)
	return nil
}

func (a *App) resumePlan(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "plan_id", "找不到这个写作计划")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var payload map[string]any
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		plan, err := a.ownPlan(r.Context(), tx, user.ID, id)
		if err != nil {
			return err
		}
		fresh, err := a.Store.GetUser(r.Context(), tx, user.ID)
		if err != nil {
			return err
		}
		if plan.PauseReason == "额度已用完" {
			snap, err := quota.Snapshot(r.Context(), a.Store, tx, fresh, now)
			if err != nil {
				return err
			}
			dailyUsed, _ := snap["daily_used"].(int)
			dailyLimit, _ := snap["daily_limit"].(int)
			periodUsed, _ := snap["period_used"].(int)
			periodLimit, _ := snap["period_limit"].(int)
			if dailyUsed >= dailyLimit || periodUsed >= periodLimit {
				return bad("额度仍不足，暂时不能恢复")
			}
		}
		plan.Status = "active"
		plan.PauseReason = ""
		if plan.NextRunAt == nil || !plan.NextRunAt.UTC().After(now) {
			next, err := schedule.ComputeNextRun(now, plan.ScheduleType, plan.Weekdays, plan.TimeWindow, sysRNG{})
			if err != nil {
				return bad(err.Error())
			}
			plan.NextRunAt = &next
		}
		if err := a.Store.UpdatePlan(r.Context(), tx, plan); err != nil {
			return err
		}
		payload = planDict(plan)
		return nil
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, payload)
	return nil
}

func (a *App) deletePlan(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "plan_id", "找不到这个写作计划")
	if err != nil {
		return err
	}
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		plan, err := a.ownPlan(r.Context(), tx, user.ID, id)
		if err != nil {
			return err
		}
		plan.Status = "deleted"
		return a.Store.UpdatePlan(r.Context(), tx, plan)
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) generateNow(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "plan_id", "找不到这个写作计划")
	if err != nil {
		return err
	}
	return a.runGeneration(w, r, user, id, 0, "manual")
}

func (a *App) regenerate(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "article_id", "找不到这篇文章")
	if err != nil {
		return err
	}
	return a.runGeneration(w, r, user, 0, id, "regenerate")
}

func (a *App) runGeneration(w http.ResponseWriter, r *http.Request, user *model.User, planID, articleID int64, trigger string) error {
	now := time.Now().UTC()
	var err error
	var taskID int64
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		fresh, err := a.Store.GetUser(r.Context(), tx, user.ID)
		if err != nil {
			return err
		}
		if fresh == nil {
			return &AppError{Status: 401, Message: "请先登录"}
		}
		if err := quota.EnforceFreeLimits(r.Context(), a.Store, tx, fresh, now); err != nil {
			return err
		}
		var plan *model.WritingPlan
		var account *model.WxAccount
		var regen *model.Article
		if trigger == "regenerate" {
			regen, err = a.ownArticle(r.Context(), tx, user.ID, articleID)
			if err != nil {
				return err
			}
			plan, err = a.ownPlan(r.Context(), tx, user.ID, regen.PlanID)
			if err != nil {
				return err
			}
			account, err = a.ownAccount(r.Context(), tx, user.ID, regen.WxAccountID)
			if err != nil {
				return err
			}
		} else {
			plan, err = a.ownPlan(r.Context(), tx, user.ID, planID)
			if err != nil {
				return err
			}
			account, err = a.ownAccount(r.Context(), tx, user.ID, plan.WxAccountID)
			if err != nil {
				return err
			}
		}
		task, err := jobs.Create(r.Context(), a.Store, tx, fresh, plan, account, trigger, now, regen)
		if err != nil {
			if db.IsUnique(err) {
				return bad("这个时间点已经排过，请稍后再试")
			}
			return asQuota(err)
		}
		taskID = task.ID
		return nil
	})
	if err != nil {
		return err
	}
	var article *model.Article
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		task, err := a.Store.GetTask(r.Context(), tx, taskID)
		if err != nil || task == nil {
			return err
		}
		if trigger == "regenerate" {
			article, err = a.Store.GetArticle(r.Context(), tx, articleID)
			if err != nil || article == nil {
				return err
			}
			article.Status = "generating"
			article.FailureReason = ""
			article.DeliveryNote = ""
			if err := a.Store.UpdateArticle(r.Context(), tx, article); err != nil {
				return err
			}
		} else {
			article = &model.Article{
				TaskID: task.ID, UserID: user.ID, PlanID: task.PlanID, WxAccountID: task.WxAccountID,
				Title: "正在生成", Status: "generating", CreatedAt: now,
			}
			if plan, err := a.Store.GetPlan(r.Context(), tx, task.PlanID); err == nil && plan != nil {
				article.TemplateKey = plan.TemplateKey
				article.StyleKey = plan.StyleKey
			}
			article.PromptVersion = catalog.PromptVersion
			if err := a.Store.InsertArticle(r.Context(), tx, article); err != nil {
				return err
			}
		}
		task.ArticleID = &article.ID
		return a.Store.UpdateTask(r.Context(), tx, task)
	})
	if err != nil {
		return err
	}
	a.enqueue(taskID)
	writeJSON(w, 200, articleDict(article, true))
	return nil
}

func (a *App) enqueue(taskID int64) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		if err := a.Pool.Acquire(ctx); err != nil {
			a.failQueued(taskID, "生成队列已满，请稍后再试")
			return
		}
		defer a.Pool.Release()
		pipeline.Execute(ctx, a.deps(), taskID, false, time.Now().UTC())
	}()
}

func (a *App) failQueued(taskID int64, message string) {
	_ = a.DB.WithTx(context.Background(), func(tx *db.Tx) error {
		task, err := a.Store.GetTask(context.Background(), tx, taskID)
		if err != nil || task == nil || (task.Status != "running" && task.Status != "pending") {
			return err
		}
		task.Status = "failed"
		task.FailureReason = message
		finished := time.Now().UTC()
		task.FinishedAt = &finished
		if task.StartedAt != nil {
			task.DurationMs = int(finished.Sub(task.StartedAt.UTC()).Milliseconds())
		}
		if task.ArticleID != nil {
			if article, err := a.Store.GetArticle(context.Background(), tx, *task.ArticleID); err == nil && article != nil {
				article.Status = "failed"
				article.FailureReason = message
				_ = a.Store.UpdateArticle(context.Background(), tx, article)
			}
		}
		if err := a.Store.UpdateTask(context.Background(), tx, task); err != nil {
			return err
		}
		return quota.Release(context.Background(), a.Store, tx, task.ID, "排队失败，退回额度")
	})
}

func (a *App) listArticles(w http.ResponseWriter, r *http.Request, user *model.User) error {
	rows, err := a.Store.ListArticlesByUser(r.Context(), user.ID, 100)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		out = append(out, articleDict(&rows[i], false))
	}
	writeJSON(w, 200, out)
	return nil
}

func (a *App) articleDetail(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "article_id", "找不到这篇文章")
	if err != nil {
		return err
	}
	article, err := a.ownArticle(r.Context(), nil, user.ID, id)
	if err != nil {
		return err
	}
	payload := articleDict(article, true)
	templateKey := article.TemplateKey
	requested := strings.TrimSpace(r.URL.Query().Get("template"))
	if requested != "" && requested != article.TemplateKey {
		ok, err := a.knownTemplate(r.Context(), requested)
		if err != nil {
			return err
		}
		if ok {
			templateKey = requested
		}
	}
	dark := r.URL.Query().Get("theme") == "dark"
	if len(article.BodyJSON) > 0 && (dark || templateKey != article.TemplateKey) {
		if html, err := render.RenderWithinLimit(article.BodyJSON, templateKey, dark); err == nil {
			payload["body_html"] = html
			var notes []string
			if dark {
				notes = append(notes, "暗色预览只在站内查看，写入草稿箱的仍是浅色模板。")
			}
			if templateKey != article.TemplateKey {
				notes = append(notes, "当前预览用的排版还没有写回草稿。")
			}
			payload["theme_note"] = strings.Join(notes, "")
		}
	}
	payload["preview_template"] = templateKey
	if row, err := a.Store.GetArticlePublish(r.Context(), nil, article.ID); err == nil && row.ArticleID != 0 {
		payload["publish_id"] = row.PublishID
		payload["article_url"] = row.ArticleURL
		payload["publish_note"] = row.Note
	}
	writeJSON(w, 200, payload)
	return nil
}

func (a *App) exportArticle(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "article_id", "找不到这篇文章")
	if err != nil {
		return err
	}
	article, err := a.ownArticle(r.Context(), nil, user.ID, id)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="article-`+strconv.FormatInt(id, 10)+`.html"`)
	_, _ = w.Write([]byte(article.BodyHTML))
	return nil
}
