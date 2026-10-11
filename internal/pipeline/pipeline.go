package pipeline

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/content"
	"github.com/leoa81761-ops/wzgx/internal/cover"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/render"
	"github.com/leoa81761-ops/wzgx/internal/runtime"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/store"
	"github.com/leoa81761-ops/wzgx/internal/topics"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
	"math/rand"
	"net/http"
)

type Deps struct {
	Store    *store.Store
	Settings config.Settings
	Gateway  *llm.Gateway
	HTTP     *http.Client
	Tokens   *wechat.TokenCache
	Cache    *runtime.Cache
}

type work struct {
	TaskID          int64
	UserID          int64
	Tier            string
	PlanID          int64
	Direction       string
	StyleKey        string
	TemplateKey     string
	WordTarget      int
	ScheduleType    string
	Weekdays        []int
	TimeWindow      string
	AccountID       int64
	AppID           string
	AppSecret       string
	DeliveryMode    string
	Trigger         string
	ScheduleDate    string
	RetryCount      int
	RegenArticleID  *int64
	Locked          *topics.Choice
	ExistingMediaID string
	ExistingThumbID string
	History         []topics.Coverage
	RecentTitles    []string
}

type Built struct {
	Draft  map[string]any
	HTML   string
	Topic  topics.Choice
	Tokens int
	Trace  []map[string]any
}

type pushed struct {
	Status       string
	MediaID      string
	ThumbID      string
	HTML         string
	DeliveryNote string
}

func Execute(ctx context.Context, deps Deps, taskID int64, claim bool, now time.Time) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	w, ok := prepare(ctx, deps, taskID, claim, now)
	if !ok {
		return
	}
	result, err := generate(ctx, deps, w, now)
	if err != nil {
		finishFailure(ctx, deps, taskID, clip(err.Error(), 500), now, authFailed(err))
		return
	}
	finishSuccess(ctx, deps, taskID, result, now)
}

func prepare(ctx context.Context, deps Deps, taskID int64, claim bool, now time.Time) (*work, bool) {
	var w *work
	ok := false
	err := deps.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		task, err := deps.Store.GetTask(ctx, tx, taskID)
		if err != nil || task == nil {
			return err
		}
		if claim {
			if task.Status != "pending" {
				return nil
			}
			task.Status = "running"
			task.StartedAt = &now
		} else if task.Status != "running" {
			return nil
		} else if task.StartedAt == nil {
			task.StartedAt = &now
		}
		user, err := deps.Store.GetUser(ctx, tx, task.UserID)
		if err != nil {
			return err
		}
		plan, err := deps.Store.GetPlan(ctx, tx, task.PlanID)
		if err != nil {
			return err
		}
		account, err := deps.Store.GetAccount(ctx, tx, task.WxAccountID)
		if err != nil {
			return err
		}
		if user == nil || plan == nil || account == nil {
			task.Status = "failed"
			task.FailureReason = "任务关联的数据不存在"
			_ = deps.Store.UpdateTask(ctx, tx, task)
			_ = quota.Release(ctx, deps.Store, tx, task.ID, "数据缺失，退回额度")
			return nil
		}
		historyRows, err := deps.Store.ListCoverage(ctx, tx, plan.ID)
		if err != nil {
			return err
		}
		titles, err := deps.Store.RecentTitles(ctx, tx, plan.ID, 20)
		if err != nil {
			return err
		}
		var article *model.Article
		var locked *topics.Choice
		existingMedia, existingThumb := "", ""
		if task.RegenOfArticleID != nil {
			article, err = deps.Store.GetArticle(ctx, tx, *task.RegenOfArticleID)
			if err != nil {
				return err
			}
			if article == nil {
				task.Status = "failed"
				task.FailureReason = "找不到要重写的文章"
				_ = deps.Store.UpdateTask(ctx, tx, task)
				_ = quota.Release(ctx, deps.Store, tx, task.ID, "文章不存在，退回额度")
				return nil
			}
			if choice, yes := topics.ParseTag(article.TopicTag); yes {
				locked = &choice
			}
			existingMedia = article.DraftMediaID
			existingThumb = article.ThumbMediaID
			article.Status = "generating"
			article.FailureReason = ""
			if err := deps.Store.UpdateArticle(ctx, tx, article); err != nil {
				return err
			}
			task.ArticleID = &article.ID
		}
		secret := ""
		if account.SecretEncrypted != "" {
			secret, err = security.DecryptSecret(deps.Settings.MasterKey, account.SecretEncrypted)
			if err != nil {
				return err
			}
		}
		tier, _ := quota.Membership(user, now)
		if article == nil {
			article, err = deps.Store.GetArticleByTask(ctx, tx, task.ID)
			if err != nil {
				return err
			}
		}
		if article == nil {
			article = &model.Article{
				TaskID: task.ID, UserID: user.ID, PlanID: plan.ID, WxAccountID: account.ID,
				Title: "正在生成", Status: "generating", TemplateKey: plan.TemplateKey, StyleKey: plan.StyleKey,
				PromptVersion: catalog.PromptVersion, CreatedAt: now,
			}
			if err := deps.Store.InsertArticle(ctx, tx, article); err != nil {
				return err
			}
			task.ArticleID = &article.ID
		}
		if err := deps.Store.UpdateTask(ctx, tx, task); err != nil {
			return err
		}
		var history []topics.Coverage
		for _, row := range historyRows {
			history = append(history, topics.Coverage{Subtopic: row.Subtopic, Column: row.ColumnType, Angle: row.Angle, CreatedAt: row.CreatedAt})
		}
		w = &work{
			TaskID: task.ID, UserID: user.ID, Tier: tier, PlanID: plan.ID, Direction: plan.Direction,
			StyleKey: plan.StyleKey, TemplateKey: plan.TemplateKey, WordTarget: plan.WordTarget,
			ScheduleType: plan.ScheduleType, Weekdays: plan.Weekdays, TimeWindow: plan.TimeWindow,
			AccountID: account.ID, AppID: account.AppID, AppSecret: secret, DeliveryMode: account.DeliveryMode,
			Trigger: task.TriggerType, ScheduleDate: task.ScheduleDate, RetryCount: task.RetryCount,
			RegenArticleID: task.RegenOfArticleID, Locked: locked, ExistingMediaID: existingMedia, ExistingThumbID: existingThumb,
			History: history, RecentTitles: titles,
		}
		ok = true
		return nil
	})
	if err != nil {
		finishFailure(ctx, deps, taskID, clip(err.Error(), 500), now, false)
		return nil, false
	}
	return w, ok
}

func generate(ctx context.Context, deps Deps, w *work, now time.Time) (map[string]any, error) {
	var sensitive []string
	var wx runtime.WeChat
	var image *llm.Provider
	providers := map[string][]llm.Provider{}
	err := deps.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		var err error
		sensitive, err = deps.Cache.Words(ctx, tx)
		if err != nil {
			return err
		}
		wx, err = deps.Cache.WeChat(ctx, tx)
		if err != nil {
			return err
		}
		for _, stage := range []string{"topic", "research", "outline", "draft", "review"} {
			providers[stage], err = deps.Cache.Providers(ctx, tx, stage, w.Tier)
			if err != nil {
				return err
			}
		}
		images, err := deps.Store.ListImageModels(ctx, tx, true)
		if err != nil {
			return err
		}
		if len(images) > 0 {
			provider := runtime.ToProvider(images[0], deps.Settings.MasterKey)
			image = &provider
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	built, err := Build(ctx, deps.Gateway, w.input(), sensitive, providers, now)
	if err != nil {
		return nil, err
	}
	push, err := pushDraft(ctx, deps, w, built, wx, image)
	if err != nil {
		return nil, err
	}
	html := built.HTML
	if push.HTML != "" {
		html = push.HTML
	}
	return map[string]any{"draft": built.Draft, "html": html, "topic": built.Topic, "tokens": built.Tokens, "trace": built.Trace, "status": push.Status, "draft_media_id": push.MediaID, "thumb_media_id": push.ThumbID, "delivery_note": push.DeliveryNote}, nil
}

// Input 是一次写作需要的业务参数，连通测试和正式任务共用。
type Input struct {
	Direction    string
	StyleKey     string
	TemplateKey  string
	WordTarget   int
	Tier         string
	History      []topics.Coverage
	RecentTitles []string
	Locked       *topics.Choice
}

func (w *work) input() Input {
	return Input{Direction: w.Direction, StyleKey: w.StyleKey, TemplateKey: w.TemplateKey, WordTarget: w.WordTarget, Tier: w.Tier, History: w.History, RecentTitles: w.RecentTitles, Locked: w.Locked}
}

func Build(ctx context.Context, gateway *llm.Gateway, w Input, sensitive []string, providers map[string][]llm.Provider, now time.Time) (Built, error) {
	topic := topics.Pick(w.Direction, w.Tier, w.History, now)
	if w.Locked != nil {
		topic = *w.Locked
	}
	style := catalog.StyleByKey(w.StyleKey)
	if style.Key == "" {
		style = catalog.StyleByKey("warm")
	}
	base := map[string]any{
		"direction": w.Direction, "subtopic": topic.Subtopic, "column": topic.Column, "angle": topic.Angle,
		"style_key": w.StyleKey, "style_label": style.Name, "style_hint": style.Description,
		"word_target": w.WordTarget, "recent_titles": w.RecentTitles, "tier": w.Tier,
	}
	var trace []map[string]any
	tokens := 0
	topicData, completion, err := gateway.CompleteJSON(ctx, providers["topic"], llm.StageMessages("选题", base), llm.StageSchema("选题"), validateTopic)
	if err != nil {
		return Built{}, err
	}
	tokens += completion.PromptTokens + completion.CompletionTokens
	trace = append(trace, traceRows("topic", completion)...)
	topicTitle := clip(str(topicData["title"]), 64)
	if topicTitle == "" {
		topicTitle = clip(topic.Subtopic+"："+topic.Angle, 64)
	}
	var researchSources []any
	if w.Tier != "free" && len(providers["research"]) > 0 {
		researchPayload := cloneMap(base)
		researchPayload["topic_title"] = topicTitle
		completion, err = gateway.CompleteFirst(ctx, providers["research"], llm.StageMessages("资料检索", researchPayload), llm.StageSchema("资料检索"))
		if err != nil {
			trace = append(trace, map[string]any{"stage": "research", "ok": false, "error": clip(err.Error(), 300)})
		} else {
			notes, sources := llm.InterpretResearch(completion.Content)
			if notes != "" {
				base["research_notes"] = notes
			}
			if len(sources) > 0 {
				listed := make([]any, len(sources))
				for i := range sources {
					listed[i] = sources[i]
				}
				base["research_sources"] = listed
				researchSources = listed
			}
			tokens += completion.PromptTokens + completion.CompletionTokens
			trace = append(trace, traceRows("research", completion)...)
		}
	}
	outlinePayload := cloneMap(base)
	outlinePayload["topic_title"] = topicTitle
	outline, completion, err := gateway.CompleteJSON(ctx, providers["outline"], llm.StageMessages("大纲", outlinePayload), llm.StageSchema("大纲"), validateOutline)
	if err != nil {
		return Built{}, err
	}
	tokens += completion.PromptTokens + completion.CompletionTokens
	trace = append(trace, traceRows("outline", completion)...)
	draftPayload := cloneMap(outlinePayload)
	draftPayload["outline"] = outline
	draft, completion, err := gateway.CompleteJSON(ctx, providers["draft"], llm.StageMessages("正文", draftPayload), llm.StageSchema("正文"), validateDraft)
	if err != nil {
		return Built{}, err
	}
	tokens += completion.PromptTokens + completion.CompletionTokens
	trace = append(trace, traceRows("draft", completion)...)
	reviewPayload := cloneMap(draftPayload)
	reviewPayload["draft"] = draft
	reviewPayload["issues"] = content.FindSensitive(draft, sensitive)
	reviewed, completion, err := gateway.CompleteJSON(ctx, providers["review"], llm.StageMessages("审核", reviewPayload), llm.StageSchema("审核"), validateDraft)
	if err != nil {
		return Built{}, err
	}
	tokens += completion.PromptTokens + completion.CompletionTokens
	trace = append(trace, traceRows("review", completion)...)
	cleaned, _ := content.StripCliches(reviewed).(map[string]any)
	draft = cleaned
	hits := content.FindSensitive(draft, sensitive)
	if len(hits) > 0 {
		reviewPayload["draft"] = draft
		reviewPayload["issues"] = hits
		reviewed, completion, err = gateway.CompleteJSON(ctx, providers["review"], llm.StageMessages("审核", reviewPayload), llm.StageSchema("审核"), validateDraft)
		if err != nil {
			return Built{}, err
		}
		tokens += completion.PromptTokens + completion.CompletionTokens
		trace = append(trace, traceRows("review", completion)...)
		cleaned, _ = content.StripCliches(reviewed).(map[string]any)
		draft = cleaned
		hits = content.FindSensitive(draft, sensitive)
		if len(hits) > 0 {
			return Built{}, &llm.Error{Message: "审核未通过，命中敏感词：" + strings.Join(hits, "、")}
		}
	}
	if w.Tier != "free" && len(researchSources) > 0 {
		draft["sources"] = mergeSources(asList(draft["sources"]), researchSources)
	}
	if w.Tier == "free" {
		draft["sources"] = []any{}
	}
	title := str(draft["title"])
	if title == "" {
		title = topicTitle
	}
	draft["title"] = clip(title, 64)
	footnotes := []any{"请发布前审阅。"}
	if strings.Contains(w.Direction, "母婴") {
		footnotes = append(footnotes, "本文仅供科普，不能替代医生诊疗意见。")
	}
	draft["footnotes"] = footnotes
	html, err := render.RenderWithinLimit(draft, w.TemplateKey, false)
	if err != nil {
		return Built{}, err
	}
	return Built{Draft: draft, HTML: html, Topic: topic, Tokens: tokens, Trace: trace}, nil
}

func pushDraft(ctx context.Context, deps Deps, w *work, article Built, wx runtime.WeChat, image *llm.Provider) (pushed, error) {
	thumb := w.ExistingThumbID
	if w.DeliveryMode != "draft" {
		return pushed{Status: "exported", HTML: article.HTML, DeliveryNote: "当前公众号为手动导出模式，请复制排版 HTML 后粘贴到公众号编辑器。"}, nil
	}
	mode := "live"
	if wx.Mode == "mock" || strings.HasPrefix(w.AppID, "wx_mock") {
		mode = "mock"
	}
	base := wx.Base
	if base == "" {
		base = "https://api.weixin.qq.com"
	}
	client := wechat.Build(w.AppID, w.AppSecret, mode, base, deps.HTTP, deps.Tokens)
	html := article.HTML
	outHTML := html
	if thumb == "" {
		png := cover.CoverPNG(w.TemplateKey)
		if generated := generatedCover(ctx, deps, image, str(article.Draft["title"]), w.Direction); len(generated) > 0 {
			png = generated
		}
		id, err := client.UploadThumb(ctx, png, "cover.png")
		if err != nil {
			if authFailed(err) {
				return pushed{}, err
			}
			return pushed{Status: "exported", ThumbID: thumb, HTML: outHTML, DeliveryNote: "写入草稿失败，已改为手动导出：" + err.Error()}, nil
		}
		thumb = id
	}
	if imageURL, err := client.UploadContentImage(ctx, cover.ContentPNG(), "inline.png"); err != nil {
		if authFailed(err) {
			return pushed{}, err
		}
	} else if imageURL != "" {
		html = render.InjectImage(html, imageURL)
		outHTML = html
	}
	draft := wechat.DraftArticle{Title: str(article.Draft["title"]), Digest: str(article.Draft["summary"]), Content: html, Author: "公号智写", ThumbMediaID: thumb}
	var mediaID string
	var err error
	if w.ExistingMediaID != "" {
		mediaID, err = client.UpdateDraft(ctx, w.ExistingMediaID, draft)
	} else {
		mediaID, err = client.AddDraft(ctx, draft)
	}
	if err != nil {
		if authFailed(err) {
			return pushed{}, err
		}
		return pushed{Status: "exported", ThumbID: thumb, HTML: outHTML, DeliveryNote: "写入草稿失败，已改为手动导出：" + err.Error()}, nil
	}
	return pushed{Status: "in_draft", MediaID: mediaID, ThumbID: thumb, HTML: outHTML}, nil
}

func finishSuccess(ctx context.Context, deps Deps, taskID int64, result map[string]any, now time.Time) {
	_ = deps.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		task, err := deps.Store.GetTask(ctx, tx, taskID)
		if err != nil || task == nil || task.ArticleID == nil {
			return err
		}
		article, err := deps.Store.GetArticle(ctx, tx, *task.ArticleID)
		if err != nil || article == nil {
			return err
		}
		plan, err := deps.Store.GetPlan(ctx, tx, task.PlanID)
		if err != nil || plan == nil {
			return err
		}
		draft, _ := result["draft"].(map[string]any)
		topic, _ := result["topic"].(topics.Choice)
		article.Title = str(draft["title"])
		article.AltTitles = asList(draft["alt_titles"])
		article.Summary = str(draft["summary"])
		article.BodyJSON = draft
		article.BodyHTML = str(result["html"])
		article.Sources = asList(draft["sources"])
		article.ThumbMediaID = str(result["thumb_media_id"])
		article.DraftMediaID = str(result["draft_media_id"])
		article.TemplateKey = plan.TemplateKey
		article.TemplateVersion = catalog.TemplateVersion
		article.StyleKey = plan.StyleKey
		article.PromptVersion = catalog.PromptVersion
		article.Status = str(result["status"])
		article.FailureReason = ""
		article.DeliveryNote = str(result["delivery_note"])
		article.Subtopic = topic.Subtopic
		article.ColumnType = topic.Column
		article.TopicTag = topic.TopicTag
		article.RiskLevel = "low"
		if task.TriggerType == "regenerate" {
			article.RegenCount++
		}
		if err := deps.Store.UpdateArticle(ctx, tx, article); err != nil {
			return err
		}
		if task.TriggerType != "regenerate" {
			if err := deps.Store.InsertCoverage(ctx, tx, &model.TopicCoverage{PlanID: plan.ID, Subtopic: topic.Subtopic, ColumnType: topic.Column, Angle: topic.Angle, TopicTag: topic.TopicTag, CreatedAt: now}); err != nil {
				return err
			}
		}
		var sources []model.ArticleSource
		for i, raw := range asList(draft["sources"]) {
			source, _ := raw.(map[string]any)
			if source == nil {
				continue
			}
			url := str(source["url"])
			sources = append(sources, model.ArticleSource{Seq: i + 1, SourceType: clip(str(source["source_type"]), 32), Name: str(source["name"]), Title: str(source["title"]), PublishedOn: str(source["published_on"]), RawURL: url, DisplayURL: render.DisplayURL(url), Accessible: "未校验"})
		}
		if err := deps.Store.ReplaceSources(ctx, tx, article.ID, sources); err != nil {
			return err
		}
		if err := quota.Confirm(ctx, deps.Store, tx, task.ID); err != nil {
			return err
		}
		task.Status = "success"
		task.FailureReason = ""
		task.Tokens = intOf(result["tokens"])
		if trace, ok := result["trace"].([]map[string]any); ok {
			task.ModelTrace = trace
		}
		stampDuration(task, time.Now().UTC())
		if err := deps.Store.UpdateTask(ctx, tx, task); err != nil {
			return err
		}
		if task.TriggerType != "regenerate" {
			next, err := schedule.ComputeNextRun(schedule.TomorrowStart(now), plan.ScheduleType, plan.Weekdays, plan.TimeWindow, sysRNG{})
			if err == nil {
				plan.NextRunAt = &next
			}
		}
		if plan.Status == "paused" && plan.PauseReason == "额度已用完" {
			plan.Status = "active"
			plan.PauseReason = ""
		}
		return deps.Store.UpdatePlan(ctx, tx, plan)
	})
}

func finishFailure(ctx context.Context, deps Deps, taskID int64, message string, now time.Time, auth bool) {
	_ = deps.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		task, err := deps.Store.GetTask(ctx, tx, taskID)
		if err != nil || task == nil {
			return err
		}
		plan, _ := deps.Store.GetPlan(ctx, tx, task.PlanID)
		var article *model.Article
		if task.ArticleID != nil {
			article, _ = deps.Store.GetArticle(ctx, tx, *task.ArticleID)
		}
		retry := false
		if plan != nil && task.ScheduleDate != "" && !auth {
			if end, err := schedule.WindowEnd(task.ScheduleDate, plan.TimeWindow); err == nil {
				retry = schedule.ShouldRetry(task.TriggerType, task.RetryCount, now, end)
			}
		}
		if auth {
			if account, _ := deps.Store.GetAccount(ctx, tx, task.WxAccountID); account != nil {
				account.BindStatus = "auth_invalid"
				_ = deps.Store.UpdateAccount(ctx, tx, account)
			}
			plans, _ := deps.Store.ListPlansByAccount(ctx, tx, task.WxAccountID, "active")
			for i := range plans {
				plans[i].Status = "paused"
				plans[i].PauseReason = "授权失效"
				_ = deps.Store.UpdatePlan(ctx, tx, &plans[i])
			}
			message = "公众号授权失效，请重新绑定。未扣除额度。"
		}
		if retry {
			task.Status = "pending"
			task.RetryCount++
			task.FailureReason = message
			task.RunAt = now
			if article != nil {
				article.Status = "generating"
				article.FailureReason = "生成失败，将在时段内重试：" + message
				_ = deps.Store.UpdateArticle(ctx, tx, article)
			}
			return deps.Store.UpdateTask(ctx, tx, task)
		}
		if task.TriggerType == "schedule" {
			task.Status = "abandoned"
		} else {
			task.Status = "failed"
		}
		task.FailureReason = message
		stampDuration(task, time.Now().UTC())
		if article != nil {
			article.Status = "failed"
			article.FailureReason = message
			_ = deps.Store.UpdateArticle(ctx, tx, article)
		}
		if err := deps.Store.UpdateTask(ctx, tx, task); err != nil {
			return err
		}
		return quota.Release(ctx, deps.Store, tx, task.ID, "失败退回额度")
	})
}

func generatedCover(ctx context.Context, deps Deps, image *llm.Provider, title, direction string) []byte {
	if image == nil || deps.Gateway == nil || deps.Gateway.Mock || strings.HasPrefix(image.BaseURL, "mock://") {
		return nil
	}
	prompt := "公众号封面，横图，干净的插画风格，不要文字，不要水印。主题：" + title + "。方向：" + direction
	png, err := llm.GenerateCover(ctx, deps.HTTP, *image, prompt)
	if err != nil || len(png) == 0 {
		return nil
	}
	return png
}

func mergeSources(existing, extra []any) []any {
	seen := map[string]bool{}
	for _, raw := range existing {
		source, _ := raw.(map[string]any)
		if source == nil {
			continue
		}
		if url := str(source["url"]); url != "" {
			seen[url] = true
		}
	}
	for _, raw := range extra {
		if len(existing) >= 8 {
			break
		}
		source, _ := raw.(map[string]any)
		if source == nil {
			continue
		}
		url := str(source["url"])
		if url != "" && seen[url] {
			continue
		}
		existing = append(existing, source)
		if url != "" {
			seen[url] = true
		}
	}
	return existing
}

func stampDuration(task *model.ArticleTask, finished time.Time) {
	task.FinishedAt = &finished
	if task.StartedAt == nil {
		return
	}
	ms := finished.Sub(task.StartedAt.UTC()).Milliseconds()
	if ms < 0 {
		ms = 0
	}
	task.DurationMs = int(ms)
}

func traceRows(stage string, completion llm.Completion) []map[string]any {
	if len(completion.Attempts) == 0 {
		return []map[string]any{{"stage": stage, "provider_id": completion.ProviderID, "protocol": completion.Protocol, "ok": true}}
	}
	out := make([]map[string]any, 0, len(completion.Attempts))
	for _, attempt := range completion.Attempts {
		row := map[string]any{"stage": stage}
		for k, v := range attempt {
			row[k] = v
		}
		out = append(out, row)
	}
	return out
}

func authFailed(err error) bool {
	var we *wechat.Error
	if errors.As(err, &we) {
		return we.AuthFailed()
	}
	text := ""
	if err != nil {
		text = err.Error()
	}
	return strings.Contains(text, "40001") || strings.Contains(strings.ToLower(text), "invalid credential") || strings.Contains(text, "授权失效")
}

func validateTopic(data map[string]any) (map[string]any, error) {
	title := strings.TrimSpace(str(data["title"]))
	if title == "" {
		return nil, errStr("选题缺少标题")
	}
	data["title"] = clip(title, 64)
	return data, nil
}

func validateOutline(data map[string]any) (map[string]any, error) {
	points, ok := data["points"].([]any)
	if !ok {
		return nil, errStr("大纲格式不正确")
	}
	var cleaned []any
	for _, raw := range points {
		if len(cleaned) >= 5 {
			break
		}
		point, _ := raw.(map[string]any)
		if point == nil {
			continue
		}
		if str(point["heading"]) == "" && str(point["intent"]) == "" {
			continue
		}
		heading := str(point["heading"])
		if heading == "" {
			heading = "小节"
		}
		cleaned = append(cleaned, map[string]any{"heading": clip(heading, 40), "intent": clip(str(point["intent"]), 120)})
	}
	if len(cleaned) < 2 {
		return nil, errStr("大纲至少需要两个小节")
	}
	data["points"] = cleaned
	data["closing"] = clip(str(data["closing"]), 200)
	data["hook"] = clip(str(data["hook"]), 200)
	return data, nil
}

func validateDraft(data map[string]any) (map[string]any, error) {
	title := strings.TrimSpace(str(data["title"]))
	sections, ok := data["sections"].([]any)
	if title == "" || !ok {
		return nil, errStr("正文缺少标题或小节")
	}
	var normalized []any
	for _, raw := range sections {
		if len(normalized) >= 6 {
			break
		}
		section, _ := raw.(map[string]any)
		if section == nil {
			continue
		}
		var paragraphs []any
		for _, item := range asList(section["paragraphs"]) {
			text := strings.TrimSpace(str(item))
			if text != "" && len(paragraphs) < 4 {
				paragraphs = append(paragraphs, text)
			}
		}
		var items []any
		for _, item := range asList(section["list"]) {
			text := strings.TrimSpace(str(item))
			if text != "" && len(items) < 6 {
				items = append(items, text)
			}
		}
		heading := strings.TrimSpace(str(section["heading"]))
		if heading == "" && len(paragraphs) == 0 {
			continue
		}
		normalized = append(normalized, map[string]any{"heading": clip(heading, 40), "paragraphs": paragraphs, "list": items})
	}
	if len(normalized) == 0 {
		return nil, errStr("正文没有可用小节")
	}
	var sources []any
	for _, raw := range asList(data["sources"]) {
		if len(sources) >= 8 {
			break
		}
		source, _ := raw.(map[string]any)
		if source == nil {
			continue
		}
		url := str(source["url"])
		if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			url = ""
		}
		name := strings.TrimSpace(str(source["name"]))
		sourceTitle := strings.TrimSpace(str(source["title"]))
		if name == "" && sourceTitle == "" {
			continue
		}
		sources = append(sources, map[string]any{"name": clip(name, 80), "title": clip(sourceTitle, 120), "published_on": clip(str(source["published_on"]), 32), "url": clip(url, 300), "source_type": clip(strOr(source["source_type"], "other"), 32)})
	}
	alts := []any{}
	for _, item := range asList(data["alt_titles"]) {
		if len(alts) >= 5 {
			break
		}
		text := strings.TrimSpace(str(item))
		if text != "" {
			alts = append(alts, clip(text, 64))
		}
	}
	return map[string]any{"title": clip(title, 64), "alt_titles": alts, "summary": clip(str(data["summary"]), 120), "sections": normalized, "closing": clip(str(data["closing"]), 300), "sources": sources}, nil
}

type strErr string

func (e strErr) Error() string { return string(e) }
func errStr(s string) error    { return strErr(s) }

func str(v any) string {
	s, _ := v.(string)
	return s
}

func strOr(v any, fallback string) string {
	if s := str(v); s != "" {
		return s
	}
	return fallback
}

func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

func intOf(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	default:
		return 0
	}
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}

type sysRNG struct{}

func (sysRNG) Intn(n int) int { return rand.Intn(n) }
