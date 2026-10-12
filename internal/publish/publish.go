package publish

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/notify"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/store"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
)

type Deps struct {
	Store    *store.Store
	Settings config.Settings
	HTTP     *http.Client
	Tokens   *wechat.TokenCache
	Notifier notify.Sender
}

type Gate struct {
	Enabled       bool
	Tier          string
	SubjectType   string
	Verified      bool
	DeliveryMode  string
	BindStatus    string
	ProbeBlocked  bool
	Risk          string
	ArticleStatus string
}

// Allow 决定这篇稿能不能自动发布。未开启时不改稿。只放行低风险。
func Allow(g Gate) (bool, string) {
	if !g.Enabled {
		return false, ""
	}
	if g.SubjectType != "enterprise" || !g.Verified {
		return false, "只有企业认证号可以自动发布，这篇仍留在草稿箱"
	}
	if g.DeliveryMode != "draft" || g.BindStatus != "active" {
		return false, "公众号当前不能写入草稿，这篇仍留在草稿箱"
	}
	if g.ProbeBlocked {
		return false, "发布权限探测未通过，这篇仍留在草稿箱"
	}
	switch g.Tier {
	case "standard", "premium", "lifetime":
	default:
		return false, "当前档位不能自动发布，这篇仍留在草稿箱"
	}
	if g.Risk != "low" {
		return false, "只有低风险稿会自动发布，这篇仍留在草稿箱"
	}
	if g.ArticleStatus != "in_draft" && g.ArticleStatus != "publish_wait" && g.ArticleStatus != "publishing" {
		return false, "这篇仍留在草稿箱"
	}
	return true, ""
}

// Ready 决定能不能在计划上打开开关，不看单篇风险。
func Ready(user *model.User, account *model.WxAccount, now time.Time) (bool, string) {
	if account == nil {
		return false, "找不到公众号"
	}
	if account.SubjectType != "enterprise" || !account.Verified {
		return false, "只有企业认证号可以开启自动发布"
	}
	if account.DeliveryMode != "draft" || account.BindStatus != "active" {
		return false, "公众号当前不能写入草稿，不能自动发布"
	}
	if probeBlocked(account.PermissionReport) {
		return false, "发布权限探测未通过，不能自动发布"
	}
	if user == nil {
		return false, "找不到用户"
	}
	tier, _ := quota.Membership(user, now)
	switch tier {
	case "standard", "premium", "lifetime":
		return true, ""
	default:
		return false, "自动发布需要普通版、高级版或买断版"
	}
}

// Arm 在写稿成功的事务里排期。delay 为 0 时调用方应在事务提交后立刻 TryArticle。失败时调用方保持草稿，不要回滚已经写入的稿。
func Arm(ctx context.Context, deps Deps, q db.Q, article *model.Article, plan *model.WritingPlan, now time.Time) (int64, bool, error) {
	if article == nil || plan == nil || article.Status != "in_draft" {
		return 0, false, nil
	}
	opt, err := deps.Store.GetPlanPublish(ctx, q, plan.ID)
	if err != nil {
		return 0, false, err
	}
	if !opt.Enabled {
		return 0, false, nil
	}
	user, err := deps.Store.GetUser(ctx, q, article.UserID)
	if err != nil {
		return 0, false, err
	}
	if user != nil {
		if err := quota.ApplyPending(ctx, deps.Store, q, user, now); err != nil {
			return 0, false, err
		}
	}
	account, err := deps.Store.GetAccount(ctx, q, article.WxAccountID)
	if err != nil {
		return 0, false, err
	}
	tier := "free"
	if user != nil {
		tier, _ = quota.Membership(user, now)
	}
	ok, reason := Allow(Gate{
		Enabled: true, Tier: tier, SubjectType: accountType(account), Verified: account != nil && account.Verified,
		DeliveryMode: accountMode(account), BindStatus: accountBind(account), ProbeBlocked: account != nil && probeBlocked(account.PermissionReport),
		Risk: article.RiskLevel, ArticleStatus: article.Status,
	})
	if !ok {
		if reason != "" {
			article.DeliveryNote = joinNote(article.DeliveryNote, reason)
			if err := deps.Store.UpdateArticle(ctx, q, article); err != nil {
				return 0, false, err
			}
		}
		return 0, false, nil
	}
	delay := opt.DelayMinutes
	if delay < 0 {
		delay = 0
	}
	if delay > 180 {
		delay = 180
	}
	when := now.UTC().Add(time.Duration(delay) * time.Minute)
	article.Status = "publish_wait"
	article.DeliveryNote = joinNote(article.DeliveryNote, "已排入自动发布。审阅窗口内可以在公众号草稿箱里修改，到点后按草稿现状提交。")
	if err := deps.Store.UpdateArticle(ctx, q, article); err != nil {
		return 0, false, err
	}
	row := store.ArticlePublish{
		ArticleID: article.ID, Status: "publish_wait", ScheduledAt: when,
		Note: "等待审阅窗口结束", Polls: 0,
	}
	if err := deps.Store.SaveArticlePublish(ctx, q, row); err != nil {
		return 0, false, err
	}
	return article.ID, delay == 0, nil
}

func Drain(ctx context.Context, deps Deps, now time.Time) {
	if deps.Store == nil {
		return
	}
	ids, err := deps.Store.ListDuePublishes(ctx, nil, now, 30)
	if err != nil {
		return
	}
	for _, id := range ids {
		TryArticle(ctx, deps, id)
	}
}

var (
	tryMu  sync.Mutex
	trying = map[int64]bool{}
)

func TryArticle(ctx context.Context, deps Deps, articleID int64) {
	if articleID == 0 || deps.Store == nil {
		return
	}
	tryMu.Lock()
	if trying[articleID] {
		tryMu.Unlock()
		return
	}
	trying[articleID] = true
	tryMu.Unlock()
	defer func() {
		tryMu.Lock()
		delete(trying, articleID)
		tryMu.Unlock()
	}()
	row, err := deps.Store.GetArticlePublish(ctx, nil, articleID)
	if err != nil || row.ArticleID == 0 {
		return
	}
	switch row.Status {
	case "published", "publish_failed", "cancelled", "publish_unknown":
		return
	}
	article, err := deps.Store.GetArticle(ctx, nil, articleID)
	if err != nil || article == nil {
		return
	}
	if row.PublishID != "" {
		poll(ctx, deps, article, row)
		return
	}
	if row.Status != "publish_wait" {
		return
	}
	if time.Now().UTC().Before(schedule.AsUTC(row.ScheduledAt)) {
		return
	}
	user, _ := deps.Store.GetUser(ctx, nil, article.UserID)
	account, _ := deps.Store.GetAccount(ctx, nil, article.WxAccountID)
	planOpt, _ := deps.Store.GetPlanPublish(ctx, nil, article.PlanID)
	tier := "free"
	if user != nil {
		tier, _ = quota.Membership(user, time.Now().UTC())
	}
	ok, reason := Allow(Gate{
		Enabled: planOpt.Enabled, Tier: tier, SubjectType: accountType(account), Verified: account != nil && account.Verified,
		DeliveryMode: accountMode(account), BindStatus: accountBind(account), ProbeBlocked: account != nil && probeBlocked(account.PermissionReport),
		Risk: article.RiskLevel, ArticleStatus: "publish_wait",
	})
	if !ok {
		if reason == "" {
			reason = "自动发布已关闭，这篇仍留在草稿箱"
		}
		cancelDraft(ctx, deps, article, row, reason)
		return
	}
	client, err := clientFor(ctx, deps, account)
	if err != nil {
		keepWaiting(ctx, deps, article, row, "暂时读不到公众号凭证，这篇仍留在草稿箱，稍后会再试")
		return
	}
	if _, err := client.GetDraft(ctx, article.DraftMediaID); err != nil {
		if draftMissing(err) {
			cancelDraft(ctx, deps, article, row, "草稿已不在，已取消自动发布。这篇仍留在草稿箱")
			return
		}
		keepWaiting(ctx, deps, article, row, "发布前没有读到草稿，这篇仍留在草稿箱，稍后会再试")
		return
	}
	cap := deps.Settings.PublishDailyCap
	if cap < 1 {
		cap = 20
	}
	allowed, err := deps.Store.ConsumePublishSlot(ctx, nil, account.ID, schedule.ShanghaiDay(time.Now().UTC()), cap)
	if err != nil {
		keepWaiting(ctx, deps, article, row, "暂时没有记上发布次数，这篇仍留在草稿箱，稍后会再试")
		return
	}
	if !allowed {
		cancelDraft(ctx, deps, article, row, "今日自动发布次数已达上限，这篇仍留在草稿箱")
		sendNotice(ctx, deps, article.UserID, "自动发布未提交", "今日自动发布次数已达上限，这篇仍留在草稿箱。")
		return
	}
	publishID, err := client.SubmitPublish(ctx, article.DraftMediaID)
	if err != nil || publishID == "" {
		msg := "自动发布失败，请到公众号助手查看。系统不会再次提交这篇。"
		if err != nil && strings.Contains(err.Error(), "publish_id") {
			msg = "发布接口没有返回单号，为避免重复发布不再重试。请到公众号助手查看。"
		}
		fail(ctx, deps, article, row, "publish_failed", msg)
		sendNotice(ctx, deps, article.UserID, "自动发布失败", msg)
		return
	}
	now := time.Now().UTC()
	row.PublishID = publishID
	row.Status = "publishing"
	row.SubmittedAt = &now
	row.Note = "已提交发布，正在查询结果"
	_ = deps.Store.SaveArticlePublish(ctx, nil, row)
	article.Status = "publishing"
	article.DeliveryNote = joinNote(article.DeliveryNote, "已按草稿箱现状提交发布，系统不会重复提交。")
	_ = deps.Store.UpdateArticle(ctx, nil, article)
	poll(ctx, deps, article, row)
}

func poll(ctx context.Context, deps Deps, article *model.Article, row store.ArticlePublish) {
	account, err := deps.Store.GetAccount(ctx, nil, article.WxAccountID)
	if err != nil || account == nil {
		return
	}
	client, err := clientFor(ctx, deps, account)
	if err != nil {
		return
	}
	state, err := client.GetPublish(ctx, row.PublishID)
	row.Polls++
	if err != nil {
		if row.Polls >= 120 {
			unknown(ctx, deps, article, row)
			return
		}
		row.Status = "publishing"
		row.ScheduledAt = time.Now().UTC().Add(time.Minute)
		row.Note = "发布结果还没查到，稍后会再查，不会重复提交"
		_ = deps.Store.SaveArticlePublish(ctx, nil, row)
		return
	}
	switch state.Status {
	case 0:
		row.Status = "published"
		row.ArticleURL = state.URL
		row.Note = "已发布"
		_ = deps.Store.SaveArticlePublish(ctx, nil, row)
		article.Status = "published"
		note := "已发布。"
		if state.URL != "" {
			note += "链接：" + state.URL
		}
		article.DeliveryNote = joinNote(article.DeliveryNote, note)
		_ = deps.Store.UpdateArticle(ctx, nil, article)
		sendNotice(ctx, deps, article.UserID, "稿件已发布", note)
	case 1:
		if row.Polls >= 120 {
			unknown(ctx, deps, article, row)
			return
		}
		row.Status = "publishing"
		row.ScheduledAt = time.Now().UTC().Add(time.Minute)
		row.Note = "正在发布，系统会继续查询，不会重复提交"
		_ = deps.Store.SaveArticlePublish(ctx, nil, row)
		article.Status = "publishing"
		article.DeliveryNote = joinNote(article.DeliveryNote, "正在发布，请稍后再看。系统不会重复提交。")
		_ = deps.Store.UpdateArticle(ctx, nil, article)
	case 5, 6:
		msg := "稿件发布后被删除或封禁，请到公众号助手查看。"
		fail(ctx, deps, article, row, "publish_failed", msg)
		sendNotice(ctx, deps, article.UserID, "发布结果异常", msg)
	default:
		msg := "自动发布失败，请到公众号助手查看。系统不会再次提交这篇。"
		fail(ctx, deps, article, row, "publish_failed", msg)
		sendNotice(ctx, deps, article.UserID, "自动发布失败", msg)
	}
}

func unknown(ctx context.Context, deps Deps, article *model.Article, row store.ArticlePublish) {
	msg := "发布结果仍未知，请到公众号助手查看。系统不再重复提交。"
	fail(ctx, deps, article, row, "publish_unknown", msg)
	sendNotice(ctx, deps, article.UserID, "发布结果未知", msg)
}

func fail(ctx context.Context, deps Deps, article *model.Article, row store.ArticlePublish, status, note string) {
	row.Status = status
	row.Note = note
	_ = deps.Store.SaveArticlePublish(ctx, nil, row)
	if status == "publish_unknown" {
		article.Status = "publish_unknown"
	} else {
		article.Status = "publish_failed"
	}
	article.DeliveryNote = joinNote(article.DeliveryNote, note)
	_ = deps.Store.UpdateArticle(ctx, nil, article)
}

func cancelDraft(ctx context.Context, deps Deps, article *model.Article, row store.ArticlePublish, note string) {
	row.Status = "cancelled"
	row.Note = note
	_ = deps.Store.SaveArticlePublish(ctx, nil, row)
	article.Status = "in_draft"
	article.DeliveryNote = joinNote(article.DeliveryNote, note)
	_ = deps.Store.UpdateArticle(ctx, nil, article)
}

func keepWaiting(ctx context.Context, deps Deps, article *model.Article, row store.ArticlePublish, note string) {
	row.Status = "publish_wait"
	row.ScheduledAt = time.Now().UTC().Add(time.Minute)
	row.Note = note
	_ = deps.Store.SaveArticlePublish(ctx, nil, row)
	article.DeliveryNote = joinNote(article.DeliveryNote, note)
	_ = deps.Store.UpdateArticle(ctx, nil, article)
}

func clientFor(ctx context.Context, deps Deps, account *model.WxAccount) (wechat.Client, error) {
	if account == nil {
		return nil, errString("找不到公众号")
	}
	secret := ""
	var err error
	if account.SecretEncrypted != "" {
		secret, err = security.DecryptSecret(deps.Settings.MasterKey, account.SecretEncrypted)
		if err != nil {
			return nil, err
		}
	}
	refresh := ""
	if secret == "" && deps.Store != nil {
		enc, err := deps.Store.AuthorizerRefresh(ctx, nil, account.ID)
		if err != nil {
			return nil, err
		}
		if enc != "" {
			refresh, err = security.DecryptSecret(deps.Settings.MasterKey, enc)
			if err != nil {
				return nil, err
			}
		}
	}
	mode := "live"
	if deps.Settings.WeChatMode == "mock" || strings.HasPrefix(account.AppID, "wx_mock") {
		mode = "mock"
	}
	if refresh != "" && deps.Settings.WeChatComponentEnabled() && mode != "mock" {
		ticket, _ := deps.Store.GetSetting(ctx, nil, "wechat_component_ticket")
		return wechat.NewAuthorizer(account.AppID, refresh, deps.Settings.WeChatComponentAppID, deps.Settings.WeChatComponentSecret, ticket, deps.Settings.WeChatAPIBase, deps.HTTP, deps.Tokens), nil
	}
	return wechat.Build(account.AppID, secret, mode, deps.Settings.WeChatAPIBase, deps.HTTP, deps.Tokens), nil
}

func sendNotice(ctx context.Context, deps Deps, userID int64, title, body string) {
	if userID == 0 || deps.Store == nil {
		return
	}
	note := &store.Notification{UserID: userID, Kind: "publish", Title: title, Body: clip(body, 500), CreatedAt: time.Now().UTC()}
	if err := deps.Store.InsertNotification(ctx, nil, note); err != nil {
		return
	}
	if deps.Notifier == nil {
		return
	}
	account := ""
	if user, err := deps.Store.GetUser(ctx, nil, userID); err == nil && user != nil {
		account = user.Email
		if account == "" {
			account = user.Phone
		}
	}
	_ = deps.Notifier.Send(ctx, notify.Message{UserID: userID, Kind: "publish", Title: title, Body: note.Body, Account: account})
}

func probeBlocked(report map[string]any) bool {
	if report == nil {
		return false
	}
	value, ok := report["can_publish"]
	if !ok {
		return false
	}
	flag, ok := value.(bool)
	return ok && !flag
}

func draftMissing(err error) bool {
	if err == nil {
		return false
	}
	if wechat.InvalidMedia(err) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "找不到草稿") || strings.Contains(msg, "不存在")
}

func accountType(account *model.WxAccount) string {
	if account == nil {
		return ""
	}
	return account.SubjectType
}

func accountMode(account *model.WxAccount) string {
	if account == nil {
		return ""
	}
	return account.DeliveryMode
}

func accountBind(account *model.WxAccount) string {
	if account == nil {
		return ""
	}
	return account.BindStatus
}

func joinNote(a, b string) string {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" {
		return b
	}
	if b == "" || strings.Contains(a, b) {
		return a
	}
	return a + " " + b
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

type errString string

func (e errString) Error() string { return string(e) }
