package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"encoding/json"

	"github.com/leoa81761-ops/wzgx/internal/content"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/store"
	"github.com/leoa81761-ops/wzgx/internal/topics"
	"github.com/leoa81761-ops/wzgx/internal/vertical"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
)

func (a *App) listNotifications(w http.ResponseWriter, r *http.Request, user *model.User) error {
	rows, unread, err := a.Store.ListNotifications(r.Context(), user.ID, 50)
	if err != nil {
		return err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{
			"id": row.ID, "kind": row.Kind, "title": row.Title, "body": row.Body,
			"read": row.ReadAt != nil, "created_at": jtimev(row.CreatedAt),
		})
	}
	writeJSON(w, 200, map[string]any{"items": items, "unread": unread})
	return nil
}

func (a *App) readNotification(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "id", "找不到这条通知")
	if err != nil {
		return err
	}
	if err := a.Store.MarkNotificationRead(r.Context(), user.ID, id, time.Now().UTC()); err != nil {
		return &AppError{Status: 404, Message: "找不到这条通知"}
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) saveFeedback(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "article_id", "找不到这篇文章")
	if err != nil {
		return err
	}
	article, err := a.ownArticle(r.Context(), nil, user.ID, id)
	if err != nil {
		return err
	}
	var body struct {
		Kind  string `json:"kind"`
		Score int    `json:"score"`
		Note  string `json:"note"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	switch body.Kind {
	case "published", "edited_published", "discarded", "handled":
	default:
		return bad("请选择已发布、修改后发布、弃用或已处理")
	}
	if body.Score < 0 || body.Score > 5 {
		return bad("评分应为 0 到 5")
	}
	note := strings.TrimSpace(body.Note)
	if utf8.RuneCountInString(note) > 500 {
		return bad("备注请控制在 500 字以内")
	}
	row := &store.Feedback{UserID: user.ID, ArticleID: article.ID, Kind: body.Kind, Score: body.Score, Note: note, CreatedAt: time.Now().UTC()}
	if err := a.Store.InsertFeedback(r.Context(), nil, row); err != nil {
		return err
	}
	if body.Kind == "published" || body.Kind == "edited_published" {
		if variantID, err := a.Store.ArticleVariant(r.Context(), article.ID); err == nil && variantID > 0 {
			_ = a.Store.IncrementVariantAdopted(r.Context(), variantID)
		}
	}
	writeJSON(w, 200, feedbackDict(*row))
	return nil
}

func feedbackDict(row store.Feedback) map[string]any {
	return map[string]any{
		"id": row.ID, "article_id": row.ArticleID, "kind": row.Kind, "score": row.Score, "note": row.Note,
		"draft_state": row.DraftState, "change_ratio": row.ChangeRatio, "created_at": jtimev(row.CreatedAt),
	}
}

func (a *App) rereadDraft(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "article_id", "找不到这篇文章")
	if err != nil {
		return err
	}
	article, err := a.ownArticle(r.Context(), nil, user.ID, id)
	if err != nil {
		return err
	}
	view := wechat.DraftView{Missing: true}
	if article.DraftMediaID != "" {
		account, err := a.Store.GetAccount(r.Context(), nil, article.WxAccountID)
		if err != nil {
			return err
		}
		if account == nil {
			return bad("公众号凭证不可用，无法回读草稿")
		}
		secret := ""
		if account.SecretEncrypted != "" {
			secret, err = security.DecryptSecret(a.Cfg.MasterKey, account.SecretEncrypted)
			if err != nil {
				return bad(err.Error())
			}
		}
		client, err := a.accountWeChatClient(r.Context(), account, secret)
		if err != nil {
			return err
		}
		raw, err := client.GetDraft(r.Context(), article.DraftMediaID)
		if err != nil {
			if !wechat.MissingDraft(err) {
				return bad("回读草稿失败：" + err.Error())
			}
			view = wechat.DraftView{Missing: true}
		} else {
			view = wechat.ReadDraft(raw)
		}
	}
	diff := wechat.DiffDraft(article.Title, article.BodyHTML, view)
	if err := a.learnEditStyle(r, user, article.BodyHTML, view.Content, diff.State == "modified"); err != nil {
		return err
	}
	row := &store.Feedback{
		UserID: user.ID, ArticleID: article.ID, Kind: "reread", DraftState: diff.State,
		ChangeRatio: diff.ChangeRatio, Note: diff.RemoteTitle, CreatedAt: time.Now().UTC(),
	}
	if err := a.Store.InsertFeedback(r.Context(), nil, row); err != nil {
		return err
	}
	payload := feedbackDict(*row)
	payload["remote_title"] = diff.RemoteTitle
	payload["note_text"] = draftStateText(diff.State)
	writeJSON(w, 200, payload)
	return nil
}

func (a *App) learnEditStyle(r *http.Request, user *model.User, local, remote string, modified bool) error {
	if !modified {
		return nil
	}
	agreed, err := a.Store.HasConsentVersion(r.Context(), user.ID, "style_diff", "1")
	if err != nil || !agreed {
		return err
	}
	fresh, err := a.Store.GetUser(r.Context(), nil, user.ID)
	if err != nil || fresh == nil {
		return err
	}
	tier, _ := quota.Membership(fresh, time.Now().UTC())
	if tier != "premium" && tier != "lifetime" {
		return nil
	}
	raw, err := json.Marshal(content.DiffStyleFeatures(local, remote))
	if err != nil {
		return err
	}
	return a.Store.SaveDiffProfile(r.Context(), user.ID, string(raw), time.Now().UTC())
}

func draftStateText(state string) string {
	switch state {
	case "unchanged":
		return "草稿还在，标题和正文长度与站内记录一致。"
	case "modified":
		return "草稿还在，但标题或正文和站内记录不一样。消失或改动都不能单独证明已经发布。"
	default:
		return "当前读不到这篇草稿。它可能被删除或已发布，这里只记为读不到，不把它算成已发布。"
	}
}

func (a *App) planCalendar(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "plan_id", "找不到这个写作计划")
	if err != nil {
		return err
	}
	plan, err := a.ownPlan(r.Context(), nil, user.ID, id)
	if err != nil {
		return err
	}
	fresh, err := a.Store.GetUser(r.Context(), nil, user.ID)
	if err != nil {
		return err
	}
	count := 8
	if raw := r.URL.Query().Get("count"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 30 {
			return bad("日历条数应为 1 到 30")
		}
		count = n
	}
	rows, err := a.Store.ListCoverage(r.Context(), nil, plan.ID)
	if err != nil {
		return err
	}
	history := make([]topics.Coverage, 0, len(rows))
	for _, row := range rows {
		history = append(history, topics.Coverage{Subtopic: row.Subtopic, Column: row.ColumnType, Angle: row.Angle, CreatedAt: row.CreatedAt})
	}
	vetoes, err := a.Store.ListVetoTags(r.Context(), nil, plan.ID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := quota.ApplyPending(r.Context(), a.Store, nil, fresh, now); err != nil {
		return err
	}
	tier, _ := quota.Membership(fresh, now)
	packs, err := a.Store.ListVerticals(r.Context(), nil, true)
	if err != nil {
		return err
	}
	pack := vertical.Match(plan.Direction, packs)
	weights, err := a.Store.ListTopicWeights(r.Context(), nil, plan.ID)
	if err != nil {
		return err
	}
	tree := topics.ExpandTree(vertical.Subtopics(pack), plan.Direction, tier, weights)
	slots, err := topics.ProjectTree(tree, plan.Direction, tier, plan.ScheduleType, plan.Weekdays, plan.TimeWindow, history, now, count, vetoes)
	if err != nil {
		return bad(err.Error())
	}
	out := make([]map[string]any, 0, len(slots))
	for _, slot := range slots {
		out = append(out, map[string]any{
			"at": jtimev(slot.At), "subtopic": slot.Choice.Subtopic, "column": slot.Choice.Column,
			"angle": slot.Choice.Angle, "topic_tag": slot.Choice.TopicTag,
		})
	}
	writeJSON(w, 200, map[string]any{"items": out})
	return nil
}

func (a *App) addVeto(w http.ResponseWriter, r *http.Request, user *model.User) error {
	return a.changeVeto(w, r, user, true)
}

func (a *App) removeVeto(w http.ResponseWriter, r *http.Request, user *model.User) error {
	return a.changeVeto(w, r, user, false)
}

func (a *App) changeVeto(w http.ResponseWriter, r *http.Request, user *model.User, add bool) error {
	id, err := pathID(r, "plan_id", "找不到这个写作计划")
	if err != nil {
		return err
	}
	plan, err := a.ownPlan(r.Context(), nil, user.ID, id)
	if err != nil {
		return err
	}
	var body struct {
		TopicTag string `json:"topic_tag"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	tag := strings.TrimSpace(body.TopicTag)
	if _, ok := topics.ParseTag(tag); !ok {
		return bad("选题格式应为「子方向|栏目|角度」")
	}
	if add {
		if err := a.Store.AddVeto(r.Context(), plan.ID, tag, time.Now().UTC()); err != nil {
			return err
		}
	} else if err := a.Store.RemoveVeto(r.Context(), plan.ID, tag); err != nil {
		return err
	}
	tags, err := a.Store.ListVetoTags(r.Context(), nil, plan.ID)
	if err != nil {
		return err
	}
	if tags == nil {
		tags = []string{}
	}
	writeJSON(w, 200, map[string]any{"vetoes": tags})
	return nil
}

func (a *App) setPin(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "plan_id", "找不到这个写作计划")
	if err != nil {
		return err
	}
	plan, err := a.ownPlan(r.Context(), nil, user.ID, id)
	if err != nil {
		return err
	}
	var body struct {
		Title string `json:"title"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	title := strings.TrimSpace(body.Title)
	if title == "" || utf8.RuneCountInString(title) > 64 {
		return bad("指定题目请写 1 到 64 个字")
	}
	if err := a.Store.SetPin(r.Context(), plan.ID, title, time.Now().UTC()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"specified_title": title})
	return nil
}

func (a *App) clearPin(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "plan_id", "找不到这个写作计划")
	if err != nil {
		return err
	}
	plan, err := a.ownPlan(r.Context(), nil, user.ID, id)
	if err != nil {
		return err
	}
	if err := a.Store.ClearPin(r.Context(), nil, plan.ID); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"specified_title": ""})
	return nil
}

func (a *App) createComplaint(w http.ResponseWriter, r *http.Request) error {
	anonymous := !strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "bearer ")
	if anonymous && !a.complaints.allow(clientIP(r)) {
		return &AppError{Status: 429, Message: "提交太频繁，请稍后再试"}
	}
	var body struct {
		Contact string `json:"contact"`
		Kind    string `json:"kind"`
		Content string `json:"content"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	switch body.Kind {
	case "content", "privacy", "account", "other":
	default:
		return bad("请选择投诉类型")
	}
	content := strings.TrimSpace(body.Content)
	if utf8.RuneCountInString(content) < 4 || utf8.RuneCountInString(content) > 2000 {
		return bad("投诉内容请写 4 到 2000 个字")
	}
	contact := strings.TrimSpace(body.Contact)
	var userID *int64
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "bearer ") {
		user, err := a.authenticate(r)
		if err != nil {
			return err
		}
		userID = &user.ID
		if contact == "" {
			contact = user.Email
			if contact == "" {
				contact = user.Phone
			}
		}
	}
	if userID == nil && contact == "" {
		return bad("请留下邮箱或手机号，方便回复")
	}
	row := &store.Complaint{UserID: userID, Contact: contact, Kind: body.Kind, Content: content, Status: "open", CreatedAt: time.Now().UTC()}
	if err := a.Store.InsertComplaint(r.Context(), row); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"id": row.ID, "status": "open"})
	return nil
}

func (a *App) adminComplaints(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	rows, err := a.Store.ListComplaints(r.Context(), 80)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var user any
		if row.UserID != nil {
			user = *row.UserID
		}
		out = append(out, map[string]any{
			"id": row.ID, "user_id": user, "contact": row.Contact, "kind": row.Kind, "content": row.Content,
			"status": row.Status, "reply": row.Reply, "created_at": jtimev(row.CreatedAt), "replied_at": jtime(row.RepliedAt),
		})
	}
	writeJSON(w, 200, out)
	return nil
}

func (a *App) adminReplyComplaint(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	id, err := pathID(r, "id", "找不到这条投诉")
	if err != nil {
		return err
	}
	var body struct {
		Status string `json:"status"`
		Reply  string `json:"reply"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if body.Status != "open" && body.Status != "closed" {
		return bad("状态只能是处理中或已关闭")
	}
	reply := strings.TrimSpace(body.Reply)
	if utf8.RuneCountInString(reply) > 2000 {
		return bad("回复请控制在 2000 字以内")
	}
	if err := a.Store.ReplyComplaint(r.Context(), id, body.Status, reply, time.Now().UTC()); err != nil {
		return &AppError{Status: 404, Message: "找不到这条投诉"}
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) adminTasks(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	rows, err := a.Store.ListRecentTasks(r.Context(), 40)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"id": row.ID, "user_id": row.UserID, "plan_id": row.PlanID, "status": row.Status,
			"trigger_type": row.TriggerType, "failure_reason": row.FailureReason,
			"duration_ms": row.DurationMs, "created_at": jtimev(row.CreatedAt),
		})
	}
	writeJSON(w, 200, out)
	return nil
}

func (a *App) exportMe(w http.ResponseWriter, r *http.Request, user *model.User) error {
	fresh, err := a.Store.GetUser(r.Context(), nil, user.ID)
	if err != nil || fresh == nil {
		return err
	}
	accounts, err := a.Store.ListAccountsByUser(r.Context(), user.ID)
	if err != nil {
		return err
	}
	plans, err := a.Store.ListPlansByUser(r.Context(), user.ID)
	if err != nil {
		return err
	}
	articles, err := a.Store.ListArticlesByUser(r.Context(), user.ID, 200)
	if err != nil {
		return err
	}
	consents, err := a.Store.ListConsents(r.Context(), user.ID)
	if err != nil {
		return err
	}
	feedback, err := a.Store.ListFeedbackByUser(r.Context(), user.ID)
	if err != nil {
		return err
	}
	accountOut := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		accountOut = append(accountOut, map[string]any{
			"id": account.ID, "name": account.Name, "app_id": account.AppID,
			"subject_type": account.SubjectType, "service_type": account.ServiceType,
			"verified": account.Verified, "delivery_mode": account.DeliveryMode, "bind_status": account.BindStatus,
		})
	}
	planOut := make([]map[string]any, 0, len(plans))
	for i := range plans {
		planOut = append(planOut, planDict(&plans[i]))
	}
	articleOut := make([]map[string]any, 0, len(articles))
	for i := range articles {
		articleOut = append(articleOut, articleDict(&articles[i], true))
	}
	consentOut := make([]map[string]any, 0, len(consents))
	for _, row := range consents {
		consentOut = append(consentOut, map[string]any{"doc_type": row.DocType, "version": row.Version, "created_at": jtimev(row.CreatedAt)})
	}
	feedbackOut := make([]map[string]any, 0, len(feedback))
	for _, row := range feedback {
		feedbackOut = append(feedbackOut, feedbackDict(row))
	}
	w.Header().Set("Content-Disposition", "attachment; filename=gonghao-export.json")
	writeJSON(w, 200, map[string]any{
		"exported_at": schedule.Format(time.Now().UTC()),
		"user":        userDict(fresh),
		"accounts":    accountOut,
		"plans":       planOut,
		"articles":    articleOut,
		"consents":    consentOut,
		"feedback":    feedbackOut,
	})
	return nil
}

func (a *App) deleteMe(w http.ResponseWriter, r *http.Request, user *model.User) error {
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	fresh, err := a.Store.GetUser(r.Context(), nil, user.ID)
	if err != nil || fresh == nil {
		return &AppError{Status: 401, Message: "请先登录"}
	}
	if !security.VerifyPassword(body.Password, fresh.PasswordHash) {
		return &AppError{Status: 401, Message: "密码不正确"}
	}
	now := time.Now().UTC()
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		if fresh.IsAdmin {
			n, err := a.Store.CountAdmins(r.Context(), tx, false)
			if err != nil {
				return err
			}
			if n <= 1 {
				return bad("不能注销最后一位管理员")
			}
		}
		actor := fresh.ID
		account := fresh.Email
		if account == "" {
			account = fresh.Phone
		}
		if err := a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: "delete_self", Target: "user:" + strconv.FormatInt(fresh.ID, 10),
			Detail: "用户注销 " + account + "，公众号密钥随账号一并删除", CreatedAt: now,
		}); err != nil {
			return err
		}
		return a.Store.DeleteUserCascade(r.Context(), tx, fresh.ID)
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}
