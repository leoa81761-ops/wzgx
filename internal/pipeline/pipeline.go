package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/chart"
	"github.com/leoa81761-ops/wzgx/internal/columns"
	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/content"
	"github.com/leoa81761-ops/wzgx/internal/cover"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/editorial"
	"github.com/leoa81761-ops/wzgx/internal/headline"
	"github.com/leoa81761-ops/wzgx/internal/hot"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/notify"
	"github.com/leoa81761-ops/wzgx/internal/ops"
	"github.com/leoa81761-ops/wzgx/internal/persona"
	"github.com/leoa81761-ops/wzgx/internal/publish"
	"github.com/leoa81761-ops/wzgx/internal/quality"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/render"
	"github.com/leoa81761-ops/wzgx/internal/risk"
	"github.com/leoa81761-ops/wzgx/internal/runtime"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/store"
	"github.com/leoa81761-ops/wzgx/internal/topics"
	"github.com/leoa81761-ops/wzgx/internal/vertical"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
	"math/rand"
	"net/http"
	"regexp"
)

type Deps struct {
	Store    *store.Store
	Settings config.Settings
	Gateway  *llm.Gateway
	HTTP     *http.Client
	Tokens   *wechat.TokenCache
	Cache    *runtime.Cache
	Notifier notify.Sender
}

type work struct {
	TaskID            int64
	UserID            int64
	Tier              string
	PlanID            int64
	Direction         string
	StyleKey          string
	TemplateKey       string
	WordTarget        int
	ScheduleType      string
	Weekdays          []int
	TimeWindow        string
	AccountID         int64
	AppID             string
	AppSecret         string
	AuthorizerRefresh string
	DeliveryMode      string
	Trigger           string
	ScheduleDate      string
	RetryCount        int
	RegenArticleID    *int64
	Locked            *topics.Choice
	ExistingMediaID   string
	History           []topics.Coverage
	RecentTitles      []string
	RecentTopics      []string
	ColumnUses        []string
	ForeignTitles     []string
	Vetoed            []string
	SpecifiedTitle    string
	Pack              store.VerticalPack
	StyleName         string
	StyleHint         string
	Weights           map[string]int
	Opening           string
	Ending            string
	ColumnName        string
	ColumnStyle       string
	AssetCover        []byte
	AssetNote         string
	PromptExtra       string
	VariantID         int64
	Audience          string
	Keywords          string
	BannedWords       string
	TopicMode         string
	ImageMode         string
	Tone              string
	PersonaColumns    string
	Signatures        string
	Taboos            string
	TitlePrefer       string
	ActiveColumns     []columns.Column
	ColumnRotation    string
	ColumnWeekdays    map[int]int64
	RunAt             time.Time
}

type Built struct {
	Draft          map[string]any
	HTML           string
	Topic          topics.Choice
	Tokens         int
	Trace          []map[string]any
	MissingSources bool
	Uncited        bool
	LengthShort    bool
	NoFacts        bool
	OffStructure   bool
	NoDatedNews    bool
	Searched       bool
}

type pushed struct {
	Status       string
	MediaID      string
	ThumbID      string
	HTML         string
	DeliveryNote string
	CoverTrace   map[string]any
	RiskLevel    string
	ImageCalls   int
}

type tracedError struct {
	err   error
	trace []map[string]any
}

func (e *tracedError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *tracedError) Unwrap() error { return e.err }

// StuckTaskLimit is how long a task may stay in generating before the watchdog marks it failed.
// Article 50 stayed on generating because prepare writes that status before any model call returns,
// and ScanOnce waits out the worker. A call that never comes back, or a process that dies mid-run,
// never reaches finishFailure, and the old requeue only reset the task after an hour without touching the article.
const StuckTaskLimit = 15 * time.Minute

// StuckTaskReason is stored on the task and the article when the watchdog or the run deadline fires.
const StuckTaskReason = "生成超过 15 分钟未结束，已标记失败。文章在模型返回前就被标成生成中；这次调用没有在时限内结束，调度会一直等着，文章就停在生成中。"

func Execute(ctx context.Context, deps Deps, taskID int64, claim bool, now time.Time) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	w, ok := prepare(ctx, deps, taskID, claim, now)
	if !ok {
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, StuckTaskLimit)
	defer cancel()
	result, err := generate(runCtx, deps, w, now)
	if err != nil {
		message := clip(err.Error(), 500)
		if ctx.Err() == nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(runCtx.Err(), context.DeadlineExceeded)) {
			message = StuckTaskReason
		}
		finishFailure(ctx, deps, taskID, message, now, authFailed(err), traceOf(err))
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
		if err := quota.ApplyPending(ctx, deps.Store, tx, user, now); err != nil {
			return err
		}
		historyRows, err := deps.Store.ListCoverage(ctx, tx, plan.ID)
		if err != nil {
			return err
		}
		titles, err := deps.Store.AccountTitles(ctx, tx, account.ID, 20)
		if err != nil {
			return err
		}
		topicLines, err := deps.Store.AccountTopicLines(ctx, tx, account.ID, 20)
		if err != nil {
			return err
		}
		vetoed, err := deps.Store.ListVetoTags(ctx, tx, plan.ID)
		if err != nil {
			return err
		}
		pinned, err := deps.Store.GetPin(ctx, tx, plan.ID)
		if err != nil {
			return err
		}
		packs, err := deps.Store.ListVerticals(ctx, tx, true)
		if err != nil {
			return err
		}
		extra, err := deps.Store.GetPlanExtra(ctx, tx, plan.ID)
		if err != nil {
			return err
		}
		pack := vertical.Resolve(plan.Direction, extra, packs)
		styleRows, err := deps.Store.ListStyles(ctx, tx)
		if err != nil {
			return err
		}
		styleName, styleHint := "", ""
		for _, row := range styleRows {
			if row.Key != plan.StyleKey {
				continue
			}
			styleName = row.Name
			styleHint = row.Description
			if row.Tone != "" {
				if styleHint != "" {
					styleHint += "。"
				}
				styleHint += row.Tone
			}
			break
		}
		var article *model.Article
		var locked *topics.Choice
		existingMedia := ""
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
			article.Status = "generating"
			article.FailureReason = ""
			if err := deps.Store.UpdateArticle(ctx, tx, article); err != nil {
				return err
			}
			task.ArticleID = &article.ID
		}
		if locked == nil {
			tag, err := deps.Store.GetTopicLock(ctx, tx, task.ID)
			if err != nil {
				return err
			}
			if choice, yes := topics.ParseTag(tag); yes {
				locked = &choice
			}
		}
		secret := ""
		if account.SecretEncrypted != "" {
			secret, err = security.DecryptSecret(deps.Settings.MasterKey, account.SecretEncrypted)
			if err != nil {
				return err
			}
		}
		refresh := ""
		if secret == "" {
			enc, err := deps.Store.AuthorizerRefresh(ctx, tx, account.ID)
			if err != nil {
				return err
			}
			if enc != "" {
				refresh, err = security.DecryptSecret(deps.Settings.MasterKey, enc)
				if err != nil {
					return err
				}
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
		history, err = AppendSiblingCoverage(ctx, deps.Store, tx, user.ID, plan.ID, history)
		if err != nil {
			return err
		}
		siblingTitles, err := deps.Store.SiblingTitles(ctx, tx, user.ID, plan.ID, 20)
		if err != nil {
			return err
		}
		seenTitle := map[string]bool{}
		for _, title := range titles {
			seenTitle[title] = true
		}
		var foreign []string
		for _, title := range siblingTitles {
			title = strings.TrimSpace(title)
			if title == "" || title == "正在生成" || seenTitle[title] {
				continue
			}
			seenTitle[title] = true
			foreign = append(foreign, title)
		}
		prefer, err := deps.Store.GetTitleBias(ctx, tx, account.ID)
		if err != nil {
			return err
		}
		weights, err := deps.Store.ListTopicWeights(ctx, tx, plan.ID)
		if err != nil {
			return err
		}
		voice := persona.Fill(persona.Profile{
			Audience: strings.TrimSpace(extra.Audience), Tone: strings.TrimSpace(extra.Tone),
			Columns: strings.TrimSpace(extra.PersonaColumns), Signatures: strings.TrimSpace(extra.Signatures),
			Taboos: strings.TrimSpace(extra.Taboos),
		}, persona.Profile{
			Audience: strings.TrimSpace(account.Audience), Tone: strings.TrimSpace(account.Tone),
			Columns: strings.TrimSpace(account.PersonaColumns), Signatures: strings.TrimSpace(account.Signatures),
			Taboos: strings.TrimSpace(account.Taboos),
		})
		w = &work{
			TaskID: task.ID, UserID: user.ID, Tier: tier, PlanID: plan.ID, Direction: plan.Direction,
			StyleKey: plan.StyleKey, TemplateKey: plan.TemplateKey, WordTarget: plan.WordTarget,
			ScheduleType: plan.ScheduleType, Weekdays: plan.Weekdays, TimeWindow: plan.TimeWindow,
			AccountID: account.ID, AppID: account.AppID, AppSecret: secret, AuthorizerRefresh: refresh, DeliveryMode: account.DeliveryMode,
			Trigger: task.TriggerType, ScheduleDate: task.ScheduleDate, RetryCount: task.RetryCount,
			RegenArticleID: task.RegenOfArticleID, Locked: locked, ExistingMediaID: existingMedia,
			History: history, RecentTitles: titles, RecentTopics: topicLines, ForeignTitles: foreign, Vetoed: vetoed, SpecifiedTitle: pinned, Pack: pack,
			StyleName: styleName, StyleHint: styleHint, Weights: weights,
			Audience: voice.Audience, Keywords: strings.TrimSpace(extra.Keywords),
			BannedWords: strings.TrimSpace(extra.BannedWords), TopicMode: strings.TrimSpace(extra.TopicMode),
			ImageMode: strings.TrimSpace(extra.ImageMode), Tone: voice.Tone,
			PersonaColumns: voice.Columns, Signatures: voice.Signatures,
			Taboos: voice.Taboos, TitlePrefer: strings.TrimSpace(prefer),
			ColumnRotation: extra.ColumnRotation, ColumnWeekdays: columns.ParseDays(extra.ColumnWeekdays), RunAt: task.RunAt,
		}
		active, err := deps.Store.ListAccountColumns(ctx, tx, account.ID, "active")
		if err != nil {
			return err
		}
		w.ActiveColumns = active
		uses, err := deps.Store.RecentColumnUses(ctx, tx, account.ID)
		if err != nil {
			return err
		}
		if uses == nil {
			uses = []string{}
		}
		w.ColumnUses = uses
		ok = true
		return nil
	})
	if err != nil {
		finishFailure(ctx, deps, taskID, clip(err.Error(), 500), now, false, nil)
		return nil, false
	}
	if ok && w != nil {
		attachStudio(ctx, deps, w)
	}
	return w, ok
}

func generate(ctx context.Context, deps Deps, w *work, now time.Time) (map[string]any, error) {
	if msg := draftWaitMessage(ctx, deps, w); msg != "" {
		return nil, errors.New(msg)
	}
	if w != nil {
		filled := fillPersona(w.input())
		w.Audience = filled.Audience
		w.Tone = filled.Tone
		w.PersonaColumns = filled.PersonaColumns
		w.Signatures = filled.Signatures
		w.Taboos = filled.Taboos
	}
	var sensitive []string
	var wx runtime.WeChat
	var image *llm.Provider
	var policy content.SourcePolicy
	providers := map[string][]llm.Provider{}
	err := deps.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		var err error
		sensitive, err = deps.Cache.Words(ctx, tx)
		if err != nil {
			return err
		}
		sensitive = mergeBanned(sensitive, w.BannedWords)
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
		if use, _, _ := ImagePlan(w.ImageMode, len(w.AssetCover) > 0, true, 1); !use {
			image = nil
		}
		policy, err = loadSourcePolicy(ctx, deps.Store, tx)
		return err
	})
	if err != nil {
		return nil, err
	}
	costPolicy, err := ops.LoadPolicy(func(key string) (string, error) {
		return deps.Store.GetSetting(ctx, nil, key)
	})
	if err != nil {
		return nil, err
	}
	month := schedule.ShanghaiNow(now).Format("2006-01")
	spent, err := deps.Store.SumMonthCost(ctx, nil, w.UserID, month)
	if err != nil {
		return nil, err
	}
	costAction := ops.Decide(spent, costPolicy)
	in := w.input()
	in.AILabel = costPolicy.AILabel
	if err := applyHotTopic(ctx, deps, &in); err != nil {
		return nil, err
	}
	if costAction != "" {
		providers["research"] = nil
		image = nil
		in.CostNote = "本月生成成本已达上限，这篇没有使用联网检索和 AI 配图。"
	}
	variants, _ := deps.Store.ListPromptVariants(ctx, nil, "正文")
	if len(variants) > 0 {
		rows := make([][2]int, len(variants))
		for i, row := range variants {
			rows[i] = [2]int{row.Uses, row.Adopted}
		}
		index := content.PickVariant(rows, len(variants) > 1 && time.Now().UnixNano()%5 == 0, 0)
		if index >= 0 {
			in.PromptExtra = variants[index].Body
			w.VariantID = variants[index].ID
		}
	}
	built, err := Build(ctx, deps.Gateway, in, sensitive, providers, policy, now)
	if err != nil {
		if built.Topic.Column != "" {
			rememberColumn(ctx, deps, w, built.Topic)
		}
		return nil, &tracedError{err: err, trace: built.Trace}
	}
	if deps.Gateway != nil && !deps.Gateway.Mock {
		content.AnnotateAccess(ctx, deps.HTTP, built.Draft)
	}
	var push pushed
	if built.NoFacts || built.LengthShort || built.Uncited || built.OffStructure || built.NoDatedNews {
		note := ""
		if built.NoFacts {
			note = joinNote(note, "无参考来源，未写入公众号草稿。")
		}
		if built.NoDatedNews {
			note = joinNote(note, "快报没有带日期的来源，未写入公众号草稿。")
		}
		if built.LengthShort {
			note = joinNote(note, "篇幅不到目标的 85%，已再扩写，仍偏短，未写入公众号草稿。")
		}
		if built.Uncited {
			note = joinNote(note, "正文没有引用，未写入公众号草稿。")
		}
		if built.OffStructure {
			note = joinNote(note, "栏目结构和正文对不上，未写入公众号草稿。")
		}
		push = pushed{
			Status: "held", HTML: built.HTML, RiskLevel: risk.Grade(w.Direction, built.Draft),
			DeliveryNote: note,
		}
	} else {
		push, err = pushDraft(ctx, deps, w, built, wx, image)
	}
	if err != nil {
		trace := append([]map[string]any{}, built.Trace...)
		if push.CoverTrace != nil {
			trace = append(trace, push.CoverTrace)
		}
		return nil, &tracedError{err: err, trace: trace}
	}
	html := built.HTML
	if push.HTML != "" {
		html = push.HTML
	}
	trace := built.Trace
	if push.CoverTrace != nil {
		trace = append(trace, push.CoverTrace)
	}
	level := push.RiskLevel
	if level == "" {
		level = "low"
	}
	note := push.DeliveryNote
	if built.MissingSources && push.Status != "held" {
		note = joinNote(note, "无参考来源")
	}
	if in.CostNote != "" {
		note = joinNote(note, "本月成本已达上限")
	}
	searchCents := 0
	if built.Searched {
		searchCents = costPolicy.SearchCents
	}
	return map[string]any{
		"draft": built.Draft, "html": html, "topic": built.Topic, "tokens": built.Tokens, "trace": trace,
		"status": push.Status, "draft_media_id": push.MediaID, "thumb_media_id": push.ThumbID,
		"delivery_note": note, "risk_level": level, "cost_action": costAction, "month_key": month,
		"chat_cents":   ops.ChatCents(built.Tokens, costPolicy.ChatCentsPer1K),
		"search_cents": searchCents, "image_cents": push.ImageCalls * costPolicy.ImageCents,
		"image_calls": push.ImageCalls, "variant_id": w.VariantID,
	}, nil
}

// Input 是一次写作需要的业务参数，连通测试和正式任务共用。
type Input struct {
	Direction      string
	StyleKey       string
	TemplateKey    string
	WordTarget     int
	Tier           string
	History        []topics.Coverage
	RecentTitles   []string
	RecentTopics   []string
	ColumnUses     []string
	ForeignTitles  []string
	AccountID      int64
	Locked         *topics.Choice
	Vetoed         []string
	SpecifiedTitle string
	Pack           store.VerticalPack
	StyleName      string
	StyleHint      string
	Weights        map[string]int
	AILabel        bool
	CostNote       string
	Opening        string
	Ending         string
	ColumnName     string
	ColumnStyle    string
	AssetNote      string
	PromptExtra    string
	Audience       string
	Keywords       string
	BannedWords    string
	TopicMode      string
	ImageMode      string
	Tone           string
	PersonaColumns string
	Signatures     string
	Taboos         string
	TitlePrefer    string
	ActiveColumns  []columns.Column
	ColumnRotation string
	ColumnWeekdays map[int]int64
	RunAt          time.Time
}

func hotAllowed(in *Input) bool {
	if in == nil || in.Locked != nil || in.SpecifiedTitle != "" || in.Tier == "free" {
		return false
	}
	return strings.TrimSpace(in.TopicMode) != "rotate"
}

func applyHotTopic(ctx context.Context, deps Deps, in *Input) error {
	if !hotAllowed(in) {
		return nil
	}
	rows, err := deps.Store.ListHotItems(ctx, nil)
	if err != nil || len(rows) == 0 {
		return err
	}
	items := make([]hot.Item, 0, len(rows))
	for _, row := range rows {
		reports := row.Reports
		if reports < 1 {
			reports = 1
		}
		items = append(items, hot.Item{
			Title: row.Title, Summary: row.Summary, Source: row.SourceLabel, SourceName: row.SourceName,
			URL: row.URL, PublishedOn: row.PublishedOn, Heat: row.Heat, Vertical: row.Vertical, Reports: reports,
		})
	}
	items = hot.ForVertical(items, in.Pack.Key, in.Direction)
	used := map[string]bool{}
	for _, row := range in.History {
		used[row.Subtopic+"|"+row.Column+"|"+row.Angle] = true
	}
	for _, tag := range in.Vetoed {
		used[tag] = true
	}
	item, ok := hot.Choose(items, in.Tier, used)
	if !ok {
		return nil
	}
	if choice, yes := topics.ParseTag(hot.Tag(item)); yes {
		in.Locked = &choice
	}
	return nil
}

func (w *work) input() Input {
	return Input{Direction: w.Direction, StyleKey: w.StyleKey, TemplateKey: w.TemplateKey, WordTarget: w.WordTarget, Tier: w.Tier, History: w.History, RecentTitles: w.RecentTitles, RecentTopics: w.RecentTopics, ColumnUses: w.ColumnUses, ForeignTitles: w.ForeignTitles, AccountID: w.AccountID, Locked: w.Locked, Vetoed: w.Vetoed, SpecifiedTitle: w.SpecifiedTitle, Pack: w.Pack, StyleName: w.StyleName, StyleHint: w.StyleHint, Weights: w.Weights, Opening: w.Opening, Ending: w.Ending, ColumnName: w.ColumnName, ColumnStyle: w.ColumnStyle, AssetNote: w.AssetNote, PromptExtra: w.PromptExtra, Audience: w.Audience, Keywords: w.Keywords, BannedWords: w.BannedWords, TopicMode: w.TopicMode, ImageMode: w.ImageMode, Tone: w.Tone, PersonaColumns: w.PersonaColumns, Signatures: w.Signatures, Taboos: w.Taboos, TitlePrefer: w.TitlePrefer, ActiveColumns: w.ActiveColumns, ColumnRotation: w.ColumnRotation, ColumnWeekdays: w.ColumnWeekdays, RunAt: w.RunAt}
}

// AppendSiblingCoverage keeps another account's recent angles out of this plan's rotation.
func AppendSiblingCoverage(ctx context.Context, st *store.Store, q db.Q, userID, planID int64, history []topics.Coverage) ([]topics.Coverage, error) {
	rows, err := st.ListSiblingCoverage(ctx, q, userID, planID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		history = append(history, topics.Coverage{Subtopic: row.Subtopic, Column: row.ColumnType, Angle: row.Angle, CreatedAt: row.CreatedAt})
	}
	return history, nil
}

func fillPersona(in Input) Input {
	suggested := persona.Suggest(in.Direction, in.Pack.Key)
	filled := persona.Fill(persona.Profile{
		Audience: in.Audience, Tone: in.Tone, Columns: in.PersonaColumns, Signatures: in.Signatures, Taboos: in.Taboos,
	}, suggested)
	in.Audience = filled.Audience
	in.Tone = filled.Tone
	in.PersonaColumns = filled.Columns
	in.Signatures = filled.Signatures
	in.Taboos = filled.Taboos
	return in
}

// TopicTree derives the rotation from the account persona when the pack list does not match it.
func TopicTree(pack store.VerticalPack, direction, keywords, planColumns, accountColumns string) []string {
	voice := persona.Effective(
		persona.Profile{Columns: planColumns},
		persona.Profile{Columns: accountColumns},
		persona.Suggest(direction, pack.Key),
	)
	return topics.AccountTree(vertical.Subtopics(pack), pack.Keywords, direction, keywords, voice.Columns)
}

func voiceFields(in Input) map[string]any {
	out := map[string]any{}
	if text := strings.TrimSpace(in.Audience); text != "" {
		out["reader_profile"] = text
	}
	if text := strings.TrimSpace(in.Keywords); text != "" {
		out["keywords"] = text
	}
	if text := strings.TrimSpace(in.BannedWords); text != "" {
		out["banned_words"] = text
	}
	if mode := strings.TrimSpace(in.TopicMode); mode != "" && mode != "auto" {
		out["topic_mode"] = mode
	}
	if text := strings.TrimSpace(in.Tone); text != "" {
		out["tone"] = text
	}
	if text := strings.TrimSpace(in.PersonaColumns); text != "" {
		out["persona_columns"] = text
	}
	if text := strings.TrimSpace(in.Signatures); text != "" {
		out["signatures"] = text
	}
	if text := strings.TrimSpace(in.Taboos); text != "" {
		out["taboos"] = text
	}
	return out
}

func mergeBanned(base []string, extra string) []string {
	out := append([]string{}, base...)
	seen := map[string]bool{}
	for _, word := range out {
		seen[word] = true
	}
	for _, word := range vertical.Words(extra) {
		if word == "" || seen[word] {
			continue
		}
		seen[word] = true
		out = append(out, word)
	}
	return out
}

// ImagePlan decides whether this plan may call an image model, draw an AI cover, and add inline images.
// auto keeps the tier allowance. none skips AI images. cover skips inline images. asset skips AI when the user supplied a cover.
func ImagePlan(mode string, hasAsset, aiCover bool, inline int) (useModel bool, cover bool, inlineOut int) {
	switch strings.TrimSpace(mode) {
	case "none":
		return false, false, 0
	case "cover":
		return true, aiCover, 0
	case "asset":
		if hasAsset {
			return false, false, 0
		}
	}
	return true, aiCover, inline
}

func finishPiece(ctx context.Context, gateway *llm.Gateway, providers map[string][]llm.Provider, draft map[string]any, w Input, fallbackTitle string, base map[string]any, sensitive []string) (map[string]any, int, []map[string]any) {
	if draft == nil {
		return draft, 0, nil
	}
	var tokens int
	var trace []map[string]any
	if strings.TrimSpace(w.CostNote) == "" {
		rewritten, rewriteTokens, rewriteTrace := rewriteWeak(ctx, gateway, providers, draft, base)
		draft = rewritten
		tokens += rewriteTokens
		trace = append(trace, rewriteTrace...)
	}
	if w.WordTarget > 0 && content.BodyRunes(draft) > w.WordTarget*130/100 {
		shortened, condenseTokens, condenseTrace := condenseOnce(ctx, gateway, providers, draft, base)
		draft = shortened
		tokens += condenseTokens
		trace = append(trace, condenseTrace...)
	}
	if strings.TrimSpace(w.CostNote) == "" {
		ranked, titleTokens, titleTrace := rankTitles(ctx, gateway, providers, draft, w, fallbackTitle, base, sensitive)
		draft = ranked
		tokens += titleTokens
		trace = append(trace, titleTrace...)
	}
	return draft, tokens, trace
}

func rewriteWeak(ctx context.Context, gateway *llm.Gateway, providers map[string][]llm.Provider, draft, base map[string]any) (map[string]any, int, []map[string]any) {
	if draft == nil {
		return draft, 0, nil
	}
	need := editorial.NeedsRewrite(draft)
	need.Hook = true
	payload := cloneMap(base)
	payload["draft"] = draft
	payload["rewrite_hook"] = true
	payload["rewrite_headings"] = need.Headings
	payload["rewrite_closing"] = need.Closing
	payload["current_hook"] = draftHook(draft)
	if need.Headings {
		payload["current_headings"] = draftHeadings(draft)
	}
	if need.Closing {
		payload["current_closing"] = strings.TrimSpace(str(draft["closing"]))
	}
	model, completion, err := gateway.CompleteJSON(ctx, pieceProviders(providers), llm.StageMessages("结构润色", payload), llm.StageSchema("结构润色"), validatePolish)
	trace := appendStage(nil, "polish", completion, err)
	tokens := tokensOf(completion)
	next := draft
	if err == nil {
		next = editorial.ApplyNeed(draft, model, need, nil)
		compared, compareTokens, compareTrace := keepBetterHook(ctx, gateway, providers, draft, next, base)
		next = compared
		tokens += compareTokens
		trace = append(trace, compareTrace...)
	}
	if editorial.QualityOf(draft).Motif {
		rewritten, motifTokens, motifTrace := rewriteMotif(ctx, gateway, providers, next, base)
		next = rewritten
		tokens += motifTokens
		trace = append(trace, motifTrace...)
	}
	return next, tokens, trace
}

func keepBetterHook(ctx context.Context, gateway *llm.Gateway, providers map[string][]llm.Provider, before, after, base map[string]any) (map[string]any, int, []map[string]any) {
	old := draftHook(before)
	neu := draftHook(after)
	if old == neu {
		return after, 0, nil
	}
	payload := cloneMap(base)
	payload["original"] = old
	payload["rewrite"] = neu
	model, completion, err := gateway.CompleteJSON(ctx, pieceProviders(providers), llm.StageMessages("开头比较", payload), llm.StageSchema("开头比较"), validateChoice)
	trace := appendStage(nil, "hook", completion, err)
	keep := false
	if err != nil || model == nil {
		keep = editorial.BetterHook(neu, old)
	} else {
		keep = str(model["choice"]) == "rewrite"
	}
	if !keep {
		editorial.SetFirstParagraph(after, old)
	}
	return after, tokensOf(completion), trace
}

func rewriteMotif(ctx context.Context, gateway *llm.Gateway, providers map[string][]llm.Provider, draft, base map[string]any) (map[string]any, int, []map[string]any) {
	payload := cloneMap(base)
	payload["draft"] = draft
	model, completion, err := gateway.CompleteJSON(ctx, pieceProviders(providers), llm.StageMessages("意象改写", payload), llm.StageSchema("正文"), validateDraft)
	trace := appendStage(nil, "motif", completion, err)
	if err != nil || model == nil {
		return draft, tokensOf(completion), trace
	}
	kept, ok := keepMotif(draft, model)
	if !ok {
		return draft, tokensOf(completion), trace
	}
	return kept, tokensOf(completion), trace
}

func condenseOnce(ctx context.Context, gateway *llm.Gateway, providers map[string][]llm.Provider, draft, base map[string]any) (map[string]any, int, []map[string]any) {
	before := content.BodyRunes(draft)
	payload := cloneMap(base)
	payload["draft"] = draft
	payload["current_chars"] = before
	condensed, completion, err := gateway.CompleteJSON(ctx, pieceProviders(providers), llm.StageMessages("缩写", payload), llm.StageSchema("正文"), validateDraft)
	trace := appendStage(nil, "condense", completion, err)
	if err != nil || condensed == nil {
		return draft, tokensOf(completion), trace
	}
	kept, ok := keepCondensed(draft, condensed, intOf(base["word_target"]))
	if !ok {
		return draft, tokensOf(completion), trace
	}
	return kept, tokensOf(completion), trace
}

var (
	citationMarks = regexp.MustCompile(`\[\d+\]`)
	claimNumbers  = regexp.MustCompile(`\d+`)
)

// keepCondensed accepts a shorter draft only when it does not add numbers or change which citations appear.
// Sources stay as they were on the pre-condense draft. Dropping a repeated marker is fine; a new marker, a missing marker, or a new number is rejected.
// A positive floor rejects a result shorter than that many runes. Floor 0 does not add a minimum.
func keepCondensed(original, condensed map[string]any, floor int) (map[string]any, bool) {
	if original == nil || condensed == nil {
		return original, false
	}
	before := content.BodyRunes(original)
	next := content.BodyRunes(condensed)
	if next == 0 || next >= before {
		return original, false
	}
	if floor > 0 && next < floor {
		return original, false
	}
	if citationsChanged(original, condensed) || claimsAdded(original, condensed) {
		return original, false
	}
	copySources(condensed, original)
	return condensed, true
}

func keepMotif(original, next map[string]any) (map[string]any, bool) {
	if original == nil || next == nil {
		return original, false
	}
	if content.BodyRunes(next) == 0 {
		return original, false
	}
	if citationsChanged(original, next) || claimsAdded(original, next) {
		return original, false
	}
	copySources(next, original)
	return next, true
}

func citationsChanged(original, condensed map[string]any) bool {
	before := map[string]bool{}
	for _, mark := range citationMarks.FindAllString(bodyPlain(original), -1) {
		before[mark] = true
	}
	after := map[string]bool{}
	for _, mark := range citationMarks.FindAllString(bodyPlain(condensed), -1) {
		after[mark] = true
	}
	if len(before) != len(after) {
		return true
	}
	for mark := range before {
		if !after[mark] {
			return true
		}
	}
	return false
}

func claimsAdded(original, condensed map[string]any) bool {
	seen := map[string]int{}
	for _, token := range claimNumbers.FindAllString(bodyPlain(original), -1) {
		seen[token]++
	}
	for _, token := range claimNumbers.FindAllString(bodyPlain(condensed), -1) {
		if seen[token] == 0 {
			return true
		}
		seen[token]--
	}
	return false
}

func bodyPlain(draft map[string]any) string {
	var b strings.Builder
	for _, raw := range asList(draft["sections"]) {
		section, _ := raw.(map[string]any)
		if section == nil {
			continue
		}
		b.WriteString(str(section["heading"]))
		b.WriteByte('\n')
		for _, item := range asList(section["paragraphs"]) {
			b.WriteString(str(item))
			b.WriteByte('\n')
		}
		for _, item := range asList(section["list"]) {
			b.WriteString(str(item))
			b.WriteByte('\n')
		}
		if table, _ := section["table"].(map[string]any); table != nil {
			for _, item := range asList(table["headers"]) {
				b.WriteString(str(item))
				b.WriteByte('\n')
			}
			for _, row := range asList(table["rows"]) {
				cells, _ := row.([]any)
				for _, cell := range cells {
					b.WriteString(str(cell))
					b.WriteByte('\n')
				}
			}
		}
	}
	b.WriteString(str(draft["closing"]))
	return b.String()
}

func copySources(dst, src map[string]any) {
	raw, err := json.Marshal(asList(src["sources"]))
	if err != nil {
		return
	}
	var list []any
	if json.Unmarshal(raw, &list) != nil || list == nil {
		list = []any{}
	}
	dst["sources"] = list
}

// reviewSensitive repeats the sensitive-word rewrite on text produced after the main review.
// One rewrite is requested. A remaining hit fails the article the same way the main path does.
func reviewSensitive(ctx context.Context, gateway *llm.Gateway, providers []llm.Provider, draft, reviewPayload map[string]any, sensitive []string) (map[string]any, int, []map[string]any, error) {
	hits := sensitiveHits(draft, sensitive)
	if len(hits) == 0 {
		return draft, 0, nil, nil
	}
	payload := cloneMap(reviewPayload)
	payload["draft"] = draft
	payload["issues"] = hits
	reviewed, completion, err := gateway.CompleteJSON(ctx, providers, llm.StageMessages("审核", payload), llm.StageSchema("审核"), validateReview)
	trace := appendStage(nil, "review", completion, err)
	if err != nil {
		return draft, tokensOf(completion), trace, err
	}
	cleaned, _ := content.StripCliches(reviewed).(map[string]any)
	if cleaned == nil {
		cleaned = draft
	}
	hits = sensitiveHits(cleaned, sensitive)
	if len(hits) > 0 {
		message := "审核未通过，命中敏感词：" + strings.Join(hits, "、")
		trace = append(trace, map[string]any{"stage": "audit", "ok": false, "error": clip(message, 300)})
		return cleaned, tokensOf(completion), trace, &llm.Error{Message: message}
	}
	return cleaned, tokensOf(completion), trace, nil
}

func sensitiveHits(draft map[string]any, words []string) []string {
	seen := map[string]bool{}
	var hits []string
	add := func(list []string) {
		for _, word := range list {
			if word == "" || seen[word] {
				continue
			}
			seen[word] = true
			hits = append(hits, word)
		}
	}
	add(content.FindSensitive(draft, words))
	add(content.FindSensitive(map[string]any{"title": str(draft["title"])}, words))
	return hits
}

func rankTitles(ctx context.Context, gateway *llm.Gateway, providers map[string][]llm.Provider, draft map[string]any, w Input, fallbackTitle string, base map[string]any, sensitive []string) (map[string]any, int, []map[string]any) {
	payload := cloneMap(base)
	payload["draft_title"] = strings.TrimSpace(str(draft["title"]))
	payload["alt_titles"] = asList(draft["alt_titles"])
	model, completion, err := gateway.CompleteJSON(ctx, pieceProviders(providers), llm.StageMessages("标题", payload), llm.StageSchema("标题"), validateTitles)
	trace := appendStage(nil, "titles", completion, err)
	var candidates []headline.Scored
	if err == nil && model != nil {
		candidates = append(candidates, scoredTitles(asList(model["titles"]))...)
	}
	if title := strings.TrimSpace(str(draft["title"])); title != "" {
		candidates = append(candidates, headline.Scored{Title: title})
	}
	candidates = append(candidates, scoredTitles(asList(draft["alt_titles"]))...)
	best, alts := headline.RankScored(candidates, str(draft["summary"]), bodyPlain(draft), append(splitWords(w.Pack.RedLines, w.BannedWords, w.Taboos), sensitive...), w.RecentTitles, w.TitlePrefer)
	tokens := tokensOf(completion)
	if strings.TrimSpace(best.Title) != "" && len(alts) > 0 {
		winner, compareTokens, compareTrace := compareTitles(ctx, gateway, providers, best, alts, base)
		tokens += compareTokens
		trace = append(trace, compareTrace...)
		best, alts = promoteTitle(best, alts, winner)
	}
	title, altAny := chosenTitle(best, alts, w.SpecifiedTitle, str(draft["title"]), fallbackTitle)
	draft["title"] = title
	draft["alt_titles"] = altAny
	return draft, tokens, trace
}

func compareTitles(ctx context.Context, gateway *llm.Gateway, providers map[string][]llm.Provider, best headline.Scored, alts []headline.Scored, base map[string]any) (string, int, []map[string]any) {
	candidates := []string{best.Title}
	for _, alt := range alts {
		if text := strings.TrimSpace(alt.Title); text != "" {
			candidates = append(candidates, text)
		}
	}
	if len(candidates) < 2 {
		return "", 0, nil
	}
	payload := cloneMap(base)
	payload["candidates"] = candidates
	model, completion, err := gateway.CompleteJSON(ctx, pieceProviders(providers), llm.StageMessages("标题比较", payload), llm.StageSchema("标题比较"), validateWinner)
	trace := appendStage(nil, "title_compare", completion, err)
	if err != nil || model == nil {
		return "", tokensOf(completion), trace
	}
	return strings.TrimSpace(str(model["winner"])), tokensOf(completion), trace
}

func promoteTitle(best headline.Scored, alts []headline.Scored, winner string) (headline.Scored, []headline.Scored) {
	winner = strings.TrimSpace(winner)
	if winner == "" || winner == best.Title {
		return best, alts
	}
	found := false
	var next headline.Scored
	rest := []headline.Scored{best}
	for _, alt := range alts {
		if !found && alt.Title == winner {
			next = alt
			found = true
			continue
		}
		rest = append(rest, alt)
	}
	if !found {
		return best, alts
	}
	var out []headline.Scored
	for _, row := range rest {
		if row.Title == next.Title {
			continue
		}
		out = append(out, row)
	}
	return next, out
}

func chosenTitle(best headline.Scored, alts []headline.Scored, specified, current, fallback string) (string, []any) {
	title := strings.TrimSpace(best.Title)
	pinned := strings.TrimSpace(specified)
	if pinned != "" {
		title = pinned
	}
	if title == "" {
		title = strings.TrimSpace(current)
	}
	if title == "" {
		title = strings.TrimSpace(fallback)
	}
	seen := map[string]bool{}
	var out []any
	add := func(row headline.Scored) {
		item := strings.TrimSpace(row.Title)
		if item == "" || item == title || seen[item] {
			return
		}
		seen[item] = true
		if row.HasScores {
			out = append(out, map[string]any{
				"title": item, "hook": row.Hook, "concrete": row.Concrete,
				"consistent": row.Consistent, "persona": row.Persona,
			})
			return
		}
		out = append(out, item)
	}
	if pinned != "" {
		add(best)
	}
	for _, item := range alts {
		add(item)
	}
	if out == nil {
		out = []any{}
	}
	return title, out
}

func scoredTitles(items []any) []headline.Scored {
	out := make([]headline.Scored, 0, len(items))
	for _, item := range items {
		switch row := item.(type) {
		case string:
			if text := strings.TrimSpace(row); text != "" {
				out = append(out, headline.Scored{Title: text})
			}
		case map[string]any:
			text := strings.TrimSpace(str(row["title"]))
			if text == "" {
				text = strings.TrimSpace(str(row["text"]))
			}
			if text == "" {
				continue
			}
			scored := headline.Scored{Title: text}
			if _, ok := row["hook"]; ok {
				scored.HasScores = true
				scored.Hook = intOf(row["hook"])
				scored.Concrete = intOf(row["concrete"])
				scored.Consistent = intOf(row["consistent"])
				scored.Persona = intOf(row["persona"])
			}
			if row["scored"] == true {
				scored.HasScores = true
			}
			out = append(out, scored)
		}
	}
	return out
}

func pieceProviders(providers map[string][]llm.Provider) []llm.Provider {
	if len(providers["review"]) > 0 {
		return providers["review"]
	}
	return providers["draft"]
}

func tokensOf(completion llm.Completion) int {
	return completion.PromptTokens + completion.CompletionTokens
}

func draftHook(draft map[string]any) string {
	sections := asList(draft["sections"])
	if len(sections) == 0 {
		return ""
	}
	section, _ := sections[0].(map[string]any)
	if section == nil {
		return ""
	}
	for _, item := range asList(section["paragraphs"]) {
		if text := strings.TrimSpace(str(item)); text != "" {
			return text
		}
	}
	return ""
}

func draftHeadings(draft map[string]any) []string {
	var out []string
	for _, raw := range asList(draft["sections"]) {
		section, _ := raw.(map[string]any)
		if section == nil {
			continue
		}
		if text := strings.TrimSpace(str(section["heading"])); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func stringList(items []any) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text := strings.TrimSpace(str(item)); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func splitWords(parts ...string) []string {
	var out []string
	for _, part := range parts {
		for _, word := range strings.FieldsFunc(part, func(r rune) bool {
			return r == ',' || r == '，' || r == '\n' || r == '、' || r == ';' || r == '；'
		}) {
			word = strings.TrimSpace(word)
			if word != "" {
				out = append(out, word)
			}
		}
	}
	return out
}

// ApplyColumnChoice replaces pack subtopics with the active column and the account theme.
// titles and covered are the account's recent titles and topic lines. Repeated angles and hook motifs are skipped.
func ApplyColumnChoice(choice topics.Choice, at time.Time, active []columns.Column, rotation string, days map[int]int64, direction, keywords string, history []topics.Coverage, uses, titles, covered []string) (topics.Choice, columns.Column, bool) {
	recent := uses
	if recent == nil {
		for _, row := range history {
			if row.Column != "" {
				recent = append(recent, row.Column)
			}
		}
	}
	col, ok := columns.Pick(active, rotation, columns.WeekdayOf(at), days, recent)
	if !ok {
		return choice, columns.Column{}, false
	}
	subs := columns.Topics(col.Name, col.Positioning, direction, keywords)
	if len(subs) > 0 {
		choice.Subtopic = subs[len(history)%len(subs)]
	}
	choice.Column = col.Name
	choice = topics.Diverge(choice, subs, topics.AngleOptions(choice.Angle), topicMemory(history, titles, covered))
	return choice, col, true
}

func topicMemory(history []topics.Coverage, titles, covered []string) []string {
	out := append([]string{}, titles...)
	out = append(out, covered...)
	start := 0
	if len(history) > 20 {
		start = len(history) - 20
	}
	for _, row := range history[start:] {
		if text := strings.TrimSpace(row.Subtopic); text != "" {
			out = append(out, text)
		}
		if text := strings.TrimSpace(row.Angle); text != "" {
			out = append(out, text)
		}
	}
	out = append(out, topics.RepeatedMotifs(titles)...)
	return out
}

func coverDirection(direction, imageStyle string) string {
	imageStyle = strings.TrimSpace(imageStyle)
	if imageStyle == "" {
		return direction
	}
	if direction == "" {
		return "配图：" + imageStyle
	}
	return direction + "。配图：" + imageStyle
}

func Build(ctx context.Context, gateway *llm.Gateway, w Input, sensitive []string, providers map[string][]llm.Provider, policy content.SourcePolicy, now time.Time) (Built, error) {
	w = fillPersona(w)
	runAt := w.RunAt
	if runAt.IsZero() {
		runAt = now
	}
	topic := topics.PickTree(topics.ExpandTree(topics.AccountTree(vertical.Subtopics(w.Pack), w.Pack.Keywords, w.Direction, w.Keywords, w.PersonaColumns), w.Direction, w.Tier, w.Weights), w.Direction, w.Tier, w.History, now, w.Vetoed...)
	var picked columns.Column
	usingColumn := false
	if w.Locked != nil {
		topic = *w.Locked
	} else if len(w.ActiveColumns) > 0 {
		topic, picked, usingColumn = ApplyColumnChoice(topic, runAt, w.ActiveColumns, w.ColumnRotation, w.ColumnWeekdays, w.Direction, w.Keywords, w.History, w.ColumnUses, w.RecentTitles, w.RecentTopics)
	} else {
		topic = topics.AssignColumn(topic, w.PersonaColumns, w.History)
		topic = topics.Diverge(topic, []string{topic.Subtopic}, topics.AngleOptions(topic.Angle), topicMemory(w.History, w.RecentTitles, w.RecentTopics))
	}
	imageStyle := ""
	if usingColumn {
		if picked.WordTarget > 0 {
			w.WordTarget = picked.WordTarget
		}
		imageStyle = strings.TrimSpace(picked.ImageStyle)
	}
	if w.ColumnName != "" && w.ColumnStyle != "" && topic.Column == w.ColumnName {
		w.StyleKey = w.ColumnStyle
	}
	style := catalog.StyleByKey(w.StyleKey)
	styleName := style.Name
	styleHint := style.Description
	if styleName == "" {
		fallback := catalog.StyleByKey("warm")
		styleName = fallback.Name
		styleHint = fallback.Description
	}
	if w.StyleName != "" {
		styleName = w.StyleName
	}
	if w.StyleHint != "" {
		styleHint = w.StyleHint
	}
	if w.PromptExtra != "" {
		if styleHint != "" {
			styleHint += "。"
		}
		styleHint += w.PromptExtra
	}
	base := map[string]any{
		"direction": w.Direction, "subtopic": topic.Subtopic, "column": topic.Column, "angle": topic.Angle,
		"style_key": w.StyleKey, "style_label": styleName, "style_hint": styleHint,
		"word_target": w.WordTarget, "recent_titles": w.RecentTitles, "avoid_motifs": topics.RepeatedMotifs(w.RecentTitles), "tier": w.Tier,
		"account_id": w.AccountID,
	}
	if w.SpecifiedTitle != "" {
		base["specified_title"] = w.SpecifiedTitle
	}
	if w.Pack.Key != "" {
		base["vertical_key"] = w.Pack.Key
	}
	if w.Pack.Name != "" {
		base["vertical"] = w.Pack.Name
	}
	if w.Pack.RedLines != "" {
		base["red_lines"] = w.Pack.RedLines
	}
	if w.Pack.SourceAllow != "" {
		base["source_allow"] = w.Pack.SourceAllow
	}
	if w.Pack.ImageRules != "" {
		base["image_rules"] = w.Pack.ImageRules
	}
	for key, value := range voiceFields(w) {
		base[key] = value
	}
	structure := editorial.Structure(w.Pack.Key, w.Direction)
	if usingColumn {
		if text := columns.StructureText(picked.Structure, picked.Positioning); text != "" {
			structure = text
		}
		if text := strings.TrimSpace(picked.Positioning); text != "" {
			base["column_positioning"] = text
		}
		base["column_structure"] = columns.NormalizeStructure(picked.Structure)
	}
	base["structure"] = structure
	if imageStyle != "" {
		if styleHint != "" {
			styleHint += "。"
		}
		styleHint += "配图风格：" + imageStyle
		base["style_hint"] = styleHint
		base["image_style"] = imageStyle
	}
	var trace []map[string]any
	tokens := 0
	fail := func(trace []map[string]any, stage string, completion llm.Completion, err error) (Built, error) {
		built, failErr := stageFail(trace, stage, completion, err)
		built.Topic = topic
		return built, failErr
	}
	topicData, completion, err := gateway.CompleteJSON(ctx, providers["topic"], llm.StageMessages("选题", base), llm.StageSchema("选题"), validateTopic)
	if err != nil {
		return fail(trace, "topic", completion, err)
	}
	tokens += completion.PromptTokens + completion.CompletionTokens
	trace = append(trace, traceRows("topic", completion)...)
	topicTitle := clip(str(topicData["title"]), 64)
	if topicTitle == "" {
		topicTitle = clip(topic.Subtopic+"："+topic.Angle, 64)
	}
	topicMemoryLines := topicMemory(w.History, w.RecentTitles, w.RecentTopics)
	if topics.SameSubject(topicTitle, topicMemoryLines) {
		retryPayload := cloneMap(base)
		retryPayload["rejected_title"] = topicTitle
		retryPayload["avoid_topics"] = topicMemoryLines
		retried, retryCompletion, retryErr := gateway.CompleteJSON(ctx, providers["topic"], llm.StageMessages("选题", retryPayload), llm.StageSchema("选题"), validateTopic)
		tokens += retryCompletion.PromptTokens + retryCompletion.CompletionTokens
		trace = append(trace, traceRows("topic", retryCompletion)...)
		if retryErr == nil {
			next := clip(str(retried["title"]), 64)
			if next != "" && !topics.SameSubject(next, append(topicMemoryLines, topicTitle)) {
				topicTitle = next
				topicData = retried
			}
		}
	}
	if topics.SameSubject(topicTitle, topicMemoryLines) {
		subs := []string{topic.Subtopic}
		if usingColumn {
			subs = columns.Topics(picked.Name, picked.Positioning, w.Direction, w.Keywords)
		}
		shifted := topics.Diverge(topic, subs, topics.AngleOptions(topic.Angle), append(topicMemoryLines, topicTitle))
		alt := clip(shifted.Subtopic+"："+shifted.Angle, 64)
		if alt != "" && !topics.SameSubject(alt, topicMemoryLines) {
			topic = shifted
			topicTitle = alt
			base["subtopic"] = topic.Subtopic
			base["angle"] = topic.Angle
			base["column"] = topic.Column
		}
	}
	base["topic_title"] = topicTitle
	var researchSources []any
	searched := false
	if w.Tier != "free" && len(providers["research"]) > 0 {
		searched = true
		researchPayload := cloneMap(base)
		researchPayload["topic_title"] = topicTitle
		blocked := policy.Block
		if blocked == nil {
			blocked = []string{}
		}
		researchPayload["blocked_domains"] = blocked
		if len(policy.Allow) > 0 {
			researchPayload["allowed_domains"] = policy.Allow
		}
		notes, listed, completion, err := fetchResearch(ctx, gateway, providers["research"], researchPayload, policy)
		if err != nil {
			trace = appendStage(trace, "research", completion, err)
		} else {
			if notes != "" {
				base["research_notes"] = notes
			}
			if len(listed) > 0 {
				base["research_sources"] = listed
				researchSources = listed
			}
			tokens += completion.PromptTokens + completion.CompletionTokens
			trace = append(trace, traceRows("research", completion)...)
		}
		if urlCount(researchSources) >= 2 {
			base["writing_mode"] = "sourced"
			sheet := factSheet(researchSources)
			base["fact_sheet"] = sheet
			if sheet != "" {
				base["research_notes"] = sheet
			}
		} else {
			base["writing_mode"] = "experience"
			base["fact_sheet"] = ""
			delete(base, "research_notes")
		}
	}
	noDatedNews := false
	if usingColumn && columns.NormalizeStructure(picked.Structure) == "news" && !sourcesDated(researchSources) {
		if alt, ok := columns.PickNonNews(w.ActiveColumns, picked, w.ColumnUses); ok {
			picked = alt
			topic.Column = alt.Name
			subs := columns.Topics(alt.Name, alt.Positioning, w.Direction, w.Keywords)
			if len(subs) > 0 {
				topic.Subtopic = subs[0]
			}
			topic = topics.Diverge(topic, subs, topics.AngleOptions(topic.Angle), topicMemory(w.History, w.RecentTitles, w.RecentTopics))
			base["column"] = topic.Column
			base["subtopic"] = topic.Subtopic
			base["angle"] = topic.Angle
			base["structure"] = columns.StructureText(alt.Structure, alt.Positioning)
			base["column_structure"] = columns.NormalizeStructure(alt.Structure)
			if text := strings.TrimSpace(alt.Positioning); text != "" {
				base["column_positioning"] = text
			}
			base["topic_title"] = clip(topic.Subtopic+"："+topic.Angle, 64)
			if alt.WordTarget > 0 {
				w.WordTarget = alt.WordTarget
				base["word_target"] = w.WordTarget
			}
			imageStyle = strings.TrimSpace(alt.ImageStyle)
			if imageStyle != "" {
				base["image_style"] = imageStyle
			}
		} else {
			noDatedNews = true
		}
	}
	outlinePayload := cloneMap(base)
	outlinePayload["topic_title"] = topicTitle
	outline, completion, err := gateway.CompleteJSON(ctx, providers["outline"], llm.StageMessages("大纲", outlinePayload), llm.StageSchema("大纲"), validateOutline)
	if err != nil {
		return fail(trace, "outline", completion, err)
	}
	tokens += completion.PromptTokens + completion.CompletionTokens
	trace = append(trace, traceRows("outline", completion)...)
	draftPayload := cloneMap(outlinePayload)
	draftPayload["outline"] = outline
	draft, completion, err := gateway.CompleteJSON(ctx, providers["draft"], llm.StageMessages("正文", draftPayload), llm.StageSchema("正文"), validateDraft)
	if err != nil {
		return fail(trace, "draft", completion, err)
	}
	tokens += completion.PromptTokens + completion.CompletionTokens
	trace = append(trace, traceRows("draft", completion)...)
	fitted, fitTokens, fitTrace, lengthShort := fitBody(ctx, gateway, providers["draft"], draftPayload, draft, w.WordTarget)
	draft = fitted
	tokens += fitTokens
	trace = append(trace, fitTrace...)
	reviewPayload := cloneMap(draftPayload)
	checked, checkTokens, checkTrace := factCheck(ctx, gateway, providers["review"], reviewPayload, draft)
	draft = checked
	tokens += checkTokens
	trace = append(trace, checkTrace...)
	reviewPayload["draft"] = draft
	reviewPayload["issues"] = content.FindSensitive(draft, sensitive)
	reviewed, completion, err := gateway.CompleteJSON(ctx, providers["review"], llm.StageMessages("审核", reviewPayload), llm.StageSchema("审核"), validateReview)
	if err != nil {
		return fail(trace, "review", completion, err)
	}
	tokens += completion.PromptTokens + completion.CompletionTokens
	trace = append(trace, traceRows("review", completion)...)
	cleaned, _ := content.StripCliches(reviewed).(map[string]any)
	draft = cleaned
	hits := content.FindSensitive(draft, sensitive)
	if len(hits) > 0 {
		reviewPayload["draft"] = draft
		reviewPayload["issues"] = hits
		reviewed, completion, err = gateway.CompleteJSON(ctx, providers["review"], llm.StageMessages("审核", reviewPayload), llm.StageSchema("审核"), validateReview)
		if err != nil {
			return fail(trace, "review", completion, err)
		}
		tokens += completion.PromptTokens + completion.CompletionTokens
		trace = append(trace, traceRows("review", completion)...)
		cleaned, _ = content.StripCliches(reviewed).(map[string]any)
		draft = cleaned
		hits = content.FindSensitive(draft, sensitive)
		if len(hits) > 0 {
			message := "审核未通过，命中敏感词：" + strings.Join(hits, "、")
			trace = append(trace, map[string]any{"stage": "audit", "ok": false, "error": clip(message, 300)})
			return Built{Trace: trace, Topic: topic}, &llm.Error{Message: message}
		}
	}
	if w.Tier != "free" {
		merged := asList(draft["sources"])
		if len(researchSources) > 0 {
			merged = mergeSources(merged, researchSources)
		}
		filtered := filterListed(merged, policy)
		if !listedHaveURL(filtered) {
			filtered = filterListed(merged, policy.Relaxed())
		}
		filtered = filterAnySources(ctx, httpClient(gateway), filtered, topicText(base))
		draft["sources"] = preferListed(filtered, sourcePreferMode(base))
	}
	if w.Tier == "free" {
		draft["sources"] = []any{}
	}
	hadSources := w.Tier != "free" && listedHaveURL(asList(draft["sources"]))
	finished, pieceTokens, pieceTrace := finishPiece(ctx, gateway, providers, draft, w, topicTitle, base, sensitive)
	draft = finished
	tokens += pieceTokens
	trace = append(trace, pieceTrace...)
	audited, auditTokens, auditTrace, err := reviewSensitive(ctx, gateway, providers["review"], draft, reviewPayload, sensitive)
	trace = append(trace, auditTrace...)
	tokens += auditTokens
	if err != nil {
		return Built{Trace: trace, Topic: topic}, err
	}
	draft = audited
	draft = polishDraft(draft, w)
	softened, tabooTokens, tabooTrace := softenTaboos(ctx, gateway, providers, draft, base, w.Taboos, w.CostNote)
	draft = softened
	tokens += tabooTokens
	trace = append(trace, tabooTrace...)
	offStructure := false
	if usingColumn && columns.NormalizeStructure(picked.Structure) != "custom" {
		rewritten, structTokens, structTrace, held := rewriteColumnStructure(ctx, gateway, providers, draft, base, picked.Structure, w.CostNote)
		draft = rewritten
		tokens += structTokens
		trace = append(trace, structTrace...)
		offStructure = held
	}
	topped, topTokens, topTrace := topUpLength(ctx, gateway, providers, draft, base, w.WordTarget, w.CostNote)
	draft = polishDraft(topped, w)
	tokens += topTokens
	trace = append(trace, topTrace...)
	motifs := topics.RepeatedMotifs(w.RecentTitles)
	motifs = append(motifs, topics.SubjectHits(bodyPlain(draft), topicMemoryLines)...)
	if strings.TrimSpace(w.CostNote) == "" && bodyHasAny(draft, motifs) {
		motifBase := cloneMap(base)
		motifBase["avoid_motifs"] = motifs
		rewritten, motifTokens, motifTrace := rewriteMotif(ctx, gateway, providers, draft, motifBase)
		draft = rewritten
		tokens += motifTokens
		trace = append(trace, motifTrace...)
	}
	cleaned, clicheTokens, clicheTrace := rewriteCliches(ctx, gateway, providers, draft, base, w.CostNote)
	draft = cleaned
	tokens += clicheTokens
	trace = append(trace, clicheTrace...)
	rewrittenCopy, copyTokens, copyTrace := rewriteCopiedSpans(ctx, gateway, providers, draft, base, w.CostNote)
	draft = editorial.RewriteTaboos(rewrittenCopy, w.Taboos)
	draft = editorial.StripForeignScenes(draft, w.ForeignTitles)
	draft = editorial.StripUnsourcedNames(draft)
	tokens += copyTokens
	trace = append(trace, copyTrace...)
	if usingColumn {
		draft = quality.AlignStructure(picked.Structure, draft)
	}
	experience := str(base["writing_mode"]) == "experience"
	columnStructure := ""
	if usingColumn {
		columnStructure = picked.Structure
	}
	if experience {
		draft = settleExperience(draft, columnStructure, w.WordTarget)
	} else {
		draft = editorial.SupportCitations(draft)
	}
	delete(draft, "_quality")
	cited := citationMarks.MatchString(bodyPlain(draft))
	if w.WordTarget > 0 && content.BodyRunes(draft) < w.WordTarget {
		topped, topTokens, topTrace = topUpLength(ctx, gateway, providers, draft, base, w.WordTarget, w.CostNote)
		draft = topped
		tokens += topTokens
		trace = append(trace, topTrace...)
		if usingColumn {
			draft = quality.AlignStructure(picked.Structure, draft)
		}
		if experience {
			draft = settleExperience(draft, columnStructure, w.WordTarget)
		} else {
			draft = editorial.SupportCitations(draft)
		}
		cited = citationMarks.MatchString(bodyPlain(draft))
	}
	uncited := false
	if experience {
		if !listedHaveURL(asList(draft["sources"])) && listedHaveURL(researchSources) {
			draft["sources"] = researchSources
		}
		draft["source_heading"] = "经验分享"
	} else if w.Tier != "free" && hadSources {
		if cited {
			saved := append([]any{}, asList(draft["sources"])...)
			if !applyCitations(draft) {
				draft["sources"] = saved
				draft["source_heading"] = "延伸阅读"
				uncited = true
			}
		} else {
			draft["source_heading"] = "延伸阅读"
			uncited = true
		}
	}
	missingSources := w.Tier != "free" && searched && !hadSources && !experience
	noFacts := missingSources
	if usingColumn && columns.NormalizeStructure(picked.Structure) != "custom" {
		offStructure = !quality.StructureOK(columns.NormalizeStructure(picked.Structure), draftParagraphs(draft))
	}
	if w.WordTarget > 0 {
		lengthShort = content.BodyRunes(draft) < w.WordTarget*85/100
	}
	footnotes := []any{"请发布前审阅。"}
	if text := footnoteDisclaimer(w.Pack); text != "" {
		footnotes = append(footnotes, text)
	}
	if level := risk.Grade(w.Direction, draft); level == "medium" || level == "high" {
		footnotes = append(footnotes, "涉及健康或专业判断时，请自行核对来源，本文不能替代专业意见。")
	}
	if w.CostNote != "" {
		footnotes = append(footnotes, w.CostNote)
	}
	draft["footnotes"] = footnotes
	if w.AILabel {
		draft["ai_label"] = true
	}
	if w.Opening != "" {
		sections := asList(draft["sections"])
		lead := map[string]any{"paragraphs": []any{w.Opening}}
		draft["sections"] = append([]any{lead}, sections...)
	}
	if w.Ending != "" {
		draft["closing"] = w.Ending
	}
	if w.AssetNote != "" {
		notes := asList(draft["footnotes"])
		draft["footnotes"] = append(notes, "配图来自用户素材："+w.AssetNote)
	}
	if topic.Column != "" {
		draft["column_label"] = topic.Column
	}
	if imageStyle != "" {
		draft["image_style"] = imageStyle
	}
	draft = editorial.ShapeCopy(draft)
	trace = append(trace, map[string]any{
		"stage": "body", "ok": true, "chars": content.BodyRunes(draft),
		"stages": stageCharList(trace),
	})
	html, err := render.RenderWithinLimit(draft, w.TemplateKey, false)
	if err != nil {
		return Built{Trace: trace, Topic: topic}, err
	}
	return Built{Draft: draft, HTML: html, Topic: topic, Tokens: tokens, Trace: trace, MissingSources: missingSources, Uncited: uncited, LengthShort: lengthShort, NoFacts: noFacts, OffStructure: offStructure, NoDatedNews: noDatedNews, Searched: searched}, nil
}

func footnoteDisclaimer(pack store.VerticalPack) string {
	text := strings.TrimSpace(pack.Disclaimer)
	switch pack.Key {
	case "family":
		if text == "" || strings.Contains(text, "医生") {
			return "本文是家庭生活分享，请结合自己的情况判断。"
		}
		return text
	case "elv":
		if text != "" {
			return text
		}
		return "本文是工程科普，不能代替设计图纸和验收规范。"
	default:
		if strings.Contains(text, "医生") {
			return ""
		}
		return text
	}
}

func wechatClient(deps Deps, appID, secret, refresh, mode, base string) wechat.Client {
	if refresh != "" && deps.Settings.WeChatComponentEnabled() && mode != "mock" && !strings.HasPrefix(appID, "wx_mock") {
		ticket, _ := deps.Store.GetSetting(context.Background(), nil, "wechat_component_ticket")
		return wechat.NewAuthorizer(appID, refresh, deps.Settings.WeChatComponentAppID, deps.Settings.WeChatComponentSecret, ticket, base, deps.HTTP, deps.Tokens)
	}
	return wechat.Build(appID, secret, mode, base, deps.HTTP, deps.Tokens)
}

func pushDraft(ctx context.Context, deps Deps, w *work, article Built, wx runtime.WeChat, image *llm.Provider) (pushed, error) {
	level := risk.Grade(w.Direction, article.Draft)
	if hold, note := risk.ShouldHold(level, w.Tier, len(asList(article.Draft["sources"])) > 0); hold {
		return pushed{Status: "held", HTML: article.HTML, DeliveryNote: note, RiskLevel: level}, nil
	}
	if w.DeliveryMode != "draft" {
		return pushed{Status: "exported", HTML: article.HTML, DeliveryNote: "当前公众号为手动导出模式，请复制排版 HTML 后粘贴到公众号编辑器。", RiskLevel: level}, nil
	}
	mode := "live"
	if wx.Mode == "mock" || strings.HasPrefix(w.AppID, "wx_mock") {
		mode = "mock"
	}
	base := wx.Base
	if base == "" {
		base = "https://api.weixin.qq.com"
	}
	client := wechatClient(deps, w.AppID, w.AppSecret, w.AuthorizerRefresh, mode, base)
	html := article.HTML
	outHTML := html
	aiCover, inlineN := catalog.ImageAllowance(w.Tier)
	asset := w.AssetCover
	useModel, aiCover, inlineN := ImagePlan(w.ImageMode, len(asset) > 0, aiCover, inlineN)
	if strings.TrimSpace(w.ImageMode) == "none" {
		asset = nil
	}
	if !useModel {
		image = nil
	}
	var png []byte
	var filename string
	var coverTrace map[string]any
	if len(asset) > 0 {
		if body, name, _, err := cover.PrepareCover(asset); err == nil && len(body) > 0 {
			png, filename = body, name
			coverTrace = map[string]any{"stage": "cover", "ok": true, "source": "library"}
		}
	}
	if len(png) == 0 {
		png, filename, coverTrace = chooseCover(ctx, deps, image, str(article.Draft["title"]), coverDirection(w.Direction, str(article.Draft["image_style"])), w.TemplateKey, aiCover)
	}
	imageCalls := 0
	if coverTrace["source"] == "ai" {
		imageCalls++
	}
	thumb, err := client.UploadThumb(ctx, png, filename)
	if err != nil {
		if authFailed(err) {
			return pushed{}, err
		}
		return pushed{Status: "exported", HTML: outHTML, DeliveryNote: "写入草稿失败，已改为手动导出：" + err.Error(), CoverTrace: coverTrace, RiskLevel: level, ImageCalls: imageCalls}, nil
	}
	if inlineN > 0 && image != nil {
		if n := uploadInlineImages(ctx, deps, client, image, article.Draft, inlineN, w.Pack.ImageRules, coverDirection(w.Direction, str(article.Draft["image_style"]))); n > 0 {
			imageCalls += n
			if rendered, err := render.RenderWithinLimit(article.Draft, w.TemplateKey, false); err == nil {
				html = rendered
				outHTML = rendered
			}
		}
	} else if imageURL, err := client.UploadContentImage(ctx, cover.ContentPNG(), "inline.png"); err != nil {
		if authFailed(err) {
			return pushed{}, err
		}
	} else if imageURL != "" {
		html = render.InjectImage(html, imageURL)
		outHTML = html
	}
	if chart.Attach(article.Draft, func(png []byte) (string, error) {
		return client.UploadContentImage(ctx, png, "chart.png")
	}) > 0 {
		if rendered, err := render.RenderWithinLimit(article.Draft, w.TemplateKey, false); err == nil {
			html = rendered
			outHTML = rendered
		}
	}
	draft := wechat.DraftArticle{Title: str(article.Draft["title"]), Digest: str(article.Draft["summary"]), Content: html, Author: "公号智写", ThumbMediaID: thumb}
	mediaID, thumb, err := placeDraft(ctx, client, w.ExistingMediaID, draft, png, filename)
	if err != nil {
		if authFailed(err) {
			return pushed{}, err
		}
		return pushed{Status: "exported", ThumbID: thumb, HTML: outHTML, DeliveryNote: "写入草稿失败，已改为手动导出：" + err.Error(), CoverTrace: coverTrace, RiskLevel: level, ImageCalls: imageCalls}, nil
	}
	return pushed{Status: "in_draft", MediaID: mediaID, ThumbID: thumb, HTML: outHTML, DeliveryNote: wechat.MassSendNote, CoverTrace: coverTrace, RiskLevel: level, ImageCalls: imageCalls}, nil
}

// placeDraft 先尝试更新仍在草稿箱里的稿件。media_id 或封面失效时重新上传封面，再新建一篇草稿。
// 重写仍优先更新：旧稿还在时不会在草稿箱里留一份过期副本；已被删除或发布时再新建。
func placeDraft(ctx context.Context, client wechat.Client, existing string, draft wechat.DraftArticle, png []byte, filename string) (string, string, error) {
	thumb := draft.ThumbMediaID
	upload := func() error {
		id, err := client.UploadThumb(ctx, png, filename)
		if err != nil {
			return err
		}
		thumb = id
		draft.ThumbMediaID = id
		return nil
	}
	if existing != "" {
		mediaID, err := client.UpdateDraft(ctx, existing, draft)
		if err == nil {
			return mediaID, thumb, nil
		}
		if !wechat.InvalidMedia(err) {
			return "", thumb, err
		}
		if errUp := upload(); errUp != nil {
			return "", thumb, errUp
		}
		mediaID, err = client.AddDraft(ctx, draft)
		if err == nil {
			return mediaID, thumb, nil
		}
		if !wechat.InvalidMedia(err) {
			return "", thumb, err
		}
		if errUp := upload(); errUp != nil {
			return "", thumb, errUp
		}
		mediaID, err = client.AddDraft(ctx, draft)
		return mediaID, thumb, err
	}
	mediaID, err := client.AddDraft(ctx, draft)
	if err == nil || !wechat.InvalidMedia(err) {
		return mediaID, thumb, err
	}
	if errUp := upload(); errUp != nil {
		return "", thumb, errUp
	}
	mediaID, err = client.AddDraft(ctx, draft)
	return mediaID, thumb, err
}

func finishSuccess(ctx context.Context, deps Deps, taskID int64, result map[string]any, now time.Time) {
	var publishNow int64
	var costPaused bool
	var quietPaused bool
	var noticeUser int64
	var inviteChange *store.ReferralChange
	txErr := deps.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		task, err := deps.Store.GetTask(ctx, tx, taskID)
		if err != nil || task == nil || task.ArticleID == nil {
			return err
		}
		if task.Status == "failed" && task.FailureReason == StuckTaskReason {
			return nil
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
		article.RiskLevel = str(result["risk_level"])
		if article.RiskLevel == "" {
			article.RiskLevel = "low"
		}
		if task.TriggerType == "regenerate" {
			article.RegenCount++
		}
		if err := deps.Store.UpdateArticle(ctx, tx, article); err != nil {
			return err
		}
		if article.Status == "in_draft" {
			id, immediate, armErr := publish.Arm(ctx, publishDeps(deps), tx, article, plan, now)
			if armErr != nil {
				article.Status = "in_draft"
				article.DeliveryNote = joinNote(article.DeliveryNote, "自动发布未能排上，稿件已在草稿箱")
				if err := deps.Store.UpdateArticle(ctx, tx, article); err != nil {
					return err
				}
			} else if immediate {
				publishNow = id
			}
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
			access := str(source["accessible"])
			if access == "" {
				access = "未校验"
			}
			sources = append(sources, model.ArticleSource{Seq: i + 1, SourceType: clip(str(source["source_type"]), 32), Name: str(source["name"]), Title: str(source["title"]), PublishedOn: str(source["published_on"]), RawURL: url, DisplayURL: render.DisplayURL(url), Accessible: access})
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
			produced, countErr := deps.Store.CountPlanTasks(ctx, tx, plan.ID, schedule.ShanghaiDay(now), []string{"schedule"})
			if countErr != nil {
				return countErr
			}
			next, err := schedule.NextSlot(now, plan.ScheduleType, plan.Weekdays, plan.TimeWindow, sysRNG{}, produced, plan.ArticlesPerDay())
			if err == nil {
				plan.NextRunAt = &next
			}
		}
		if plan.Status == "paused" && plan.PauseReason == "额度已用完" {
			plan.Status = "active"
			plan.PauseReason = ""
		}
		if str(result["cost_action"]) == ops.ActionPause && plan.Status == "active" {
			plan.Status = "paused"
			plan.PauseReason = "本月成本已达上限"
			costPaused = true
			noticeUser = task.UserID
		}
		if plan.Status == "active" && task.TriggerType != "regenerate" {
			if paused, err := pauseIfQuiet(ctx, deps, tx, plan, now); err != nil {
				return err
			} else if paused {
				plan.Status = "paused"
				plan.PauseReason = "连续 5 篇没有反馈"
				quietPaused = true
				noticeUser = task.UserID
			}
		}
		if variantID := int64(intOf(result["variant_id"])); variantID > 0 && article != nil {
			if err := deps.Store.SaveArticleVariant(ctx, tx, article.ID, variantID); err != nil {
				return err
			}
			if err := deps.Store.IncrementVariantUse(ctx, tx, variantID); err != nil {
				return err
			}
		}
		if err := recordCost(ctx, deps, tx, task.UserID, task.ID, str(result["month_key"]), intOf(result["tokens"]), intOf(result["chat_cents"]), intOf(result["search_cents"]), intOf(result["image_calls"]), intOf(result["image_cents"]), now); err != nil {
			return err
		}
		if err := deps.Store.ClearPin(ctx, tx, plan.ID); err != nil {
			return err
		}
		change, err := deps.Store.SettleReferral(ctx, tx, task.UserID, now)
		if err != nil {
			return err
		}
		if err := deps.Store.UpdatePlan(ctx, tx, plan); err != nil {
			return err
		}
		inviteChange = change
		return nil
	})
	if txErr == nil && inviteChange != nil {
		title, body := "邀请奖励已更新", inviteChange.Reason
		switch inviteChange.Status {
		case "valid":
			title, body = "邀请奖励已生效", "有一条邀请通过了观察期，免费额度已增加。付费期间不加篇，降回免费后会恢复。"
		case "frozen":
			title = "有一条邀请待审核"
			if body == "" {
				body = "这条邀请暂不计入额度，等待管理员审核。"
			}
		default:
			title = "有一条邀请已撤销"
			if body == "" {
				body = "这条邀请不再增加额度。已经写出的文章不会追回。"
			}
		}
		emitNotice(ctx, deps, inviteChange.InviterID, "invite", title, body)
	}
	if publishNow > 0 {
		publish.TryArticle(ctx, publishDeps(deps), publishNow)
	}
	if costPaused && noticeUser > 0 {
		emitNotice(ctx, deps, noticeUser, "cost", "本月成本已达上限", "高成本功能已暂停。调高上限或下个月会自动恢复前，请在写作计划里手动恢复。")
	}
	if quietPaused && noticeUser > 0 && !costPaused {
		emitNotice(ctx, deps, noticeUser, "feedback", "连续 5 篇没有反馈", "这个写作方向已暂停。在文章页选择已发布、修改后发布、弃用或已处理后，可以再恢复。认证号自动发布不使用这条规则。")
	}
	if str(result["status"]) == "held" {
		emitNotice(ctx, deps, taskUserID(ctx, deps, taskID), "risk", "有一篇稿没有写入草稿", str(result["delivery_note"]))
	}
	if str(result["status"]) != "held" && strings.Contains(str(result["delivery_note"]), "无参考来源") {
		emitNotice(ctx, deps, taskUserID(ctx, deps, taskID), "research", "无参考来源", "这篇稿已经在草稿箱，但没有可用的参考来源。发布前请自行核对事实。")
	}
	if strings.Contains(str(result["delivery_note"]), "延伸阅读") {
		emitNotice(ctx, deps, taskUserID(ctx, deps, taskID), "research", "来源放在延伸阅读", "正文没有用序号标出来源，这些来源留在文末延伸阅读，发布前可以自己核对。")
	}
	if str(result["status"]) == "in_draft" {
		body := str(result["delivery_note"])
		if !strings.Contains(body, "不调用群发") {
			body = wechat.MassSendNote
		}
		emitNotice(ctx, deps, taskUserID(ctx, deps, taskID), "draft", "有一篇稿已在草稿箱", body)
	}
}

func recordCost(ctx context.Context, deps Deps, tx *db.Tx, userID, taskID int64, month string, tokens, chatCents, searchCents, imageCalls, imageCents int, now time.Time) error {
	if month == "" || userID == 0 {
		return nil
	}
	rows := []store.UsageCost{
		{UserID: userID, TaskID: taskID, MonthKey: month, Kind: "chat", Units: tokens, Cents: chatCents, Created: now},
		{UserID: userID, TaskID: taskID, MonthKey: month, Kind: "search", Units: 1, Cents: searchCents, Created: now},
		{UserID: userID, TaskID: taskID, MonthKey: month, Kind: "image", Units: imageCalls, Cents: imageCents, Created: now},
	}
	for _, row := range rows {
		if row.Cents <= 0 {
			continue
		}
		if err := deps.Store.InsertUsageCost(ctx, tx, row); err != nil {
			return err
		}
	}
	return nil
}

func taskUserID(ctx context.Context, deps Deps, taskID int64) int64 {
	task, err := deps.Store.GetTask(ctx, nil, taskID)
	if err != nil || task == nil {
		return 0
	}
	return task.UserID
}

func finishFailure(ctx context.Context, deps Deps, taskID int64, message string, now time.Time, auth bool, trace []map[string]any) {
	final := false
	waitNote := false
	var userID int64
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
		if strings.HasPrefix(message, "草稿箱等待：") && !auth {
			task.RetryCount++
			task.FailureReason = message
			if task.RetryCount <= 48 {
				task.Status = "pending"
				task.RunAt = now.Add(20 * time.Minute)
				if article != nil {
					article.Status = "generating"
					article.FailureReason = message
					if err := deps.Store.UpdateArticle(ctx, tx, article); err != nil {
						return err
					}
				}
				if err := deps.Store.UpdateTask(ctx, tx, task); err != nil {
					return err
				}
				if task.RetryCount == 1 {
					waitNote = true
					userID = task.UserID
				}
				return nil
			}
			if plan != nil && plan.Status == "active" {
				plan.Status = "paused"
				plan.PauseReason = "草稿箱暂时不能写入"
				if err := deps.Store.UpdatePlan(ctx, tx, plan); err != nil {
					return err
				}
			}
			message = strings.TrimPrefix(message, "草稿箱等待：") + "多次等待后仍不可用，计划已暂停。处理好后请恢复写作计划。"
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
		if len(trace) > 0 {
			task.ModelTrace = trace
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
		final = true
		userID = task.UserID
		return quota.Release(ctx, deps.Store, tx, task.ID, "失败退回额度")
	})
	if final && userID > 0 {
		emitNotice(ctx, deps, userID, "failure", "生成失败", message)
	}
	if waitNote && userID > 0 {
		emitNotice(ctx, deps, userID, "draft", "草稿箱暂时不能写入", message)
	}
}

func publishDeps(deps Deps) publish.Deps {
	return publish.Deps{Store: deps.Store, Settings: deps.Settings, HTTP: deps.HTTP, Tokens: deps.Tokens, Notifier: deps.Notifier}
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

func emitNotice(ctx context.Context, deps Deps, userID int64, kind, title, body string) {
	if userID == 0 {
		return
	}
	note := &store.Notification{UserID: userID, Kind: kind, Title: title, Body: clip(body, 500), CreatedAt: time.Now().UTC()}
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
	_ = deps.Notifier.Send(ctx, notify.Message{UserID: userID, Kind: kind, Title: title, Body: note.Body, Account: account})
	if deps.Settings.TemplateEnabled() {
		openid, err := deps.Store.TemplateOpenID(ctx, userID)
		if err == nil && openid != "" {
			_ = notify.SendTemplate(ctx, deps.HTTP, deps.Settings.TplAPIBase, deps.Settings.TplAppID, deps.Settings.TplSecret, deps.Settings.TplID, deps.Settings.TplFields, openid, title, note.Body)
		}
	}
}

func generatedCover(ctx context.Context, deps Deps, image *llm.Provider, title, direction string) ([]byte, error) {
	if image == nil || deps.Gateway == nil || deps.Gateway.Mock || strings.HasPrefix(image.BaseURL, "mock://") {
		return nil, nil
	}
	prompt := "公众号封面，横图，干净的插画风格，不要文字，不要水印。主题：" + title + "。方向：" + direction
	return llm.GenerateCover(ctx, deps.HTTP, *image, prompt)
}

func uploadInlineImages(ctx context.Context, deps Deps, client wechat.Client, image *llm.Provider, draft map[string]any, limit int, rules, direction string) int {
	if image == nil || deps.Gateway == nil || deps.Gateway.Mock || strings.HasPrefix(image.BaseURL, "mock://") {
		return 0
	}
	sections := asList(draft["sections"])
	used := 0
	for _, raw := range sections {
		if used >= limit {
			break
		}
		section, _ := raw.(map[string]any)
		if section == nil || str(section["heading"]) == "" {
			continue
		}
		prompt := "公众号正文配图，正方形插画，不要文字，不要水印，不要品牌标识。小节：" + str(section["heading"]) + "。方向：" + direction
		if rules != "" {
			prompt += "。" + rules
		}
		rawImg, err := llm.GenerateImage(ctx, deps.HTTP, *image, prompt, "1:1")
		if err != nil || len(rawImg) == 0 {
			continue
		}
		body, filename, _, prepErr := cover.PrepareCover(rawImg)
		if prepErr != nil {
			continue
		}
		url, err := client.UploadContentImage(ctx, body, filename)
		if err != nil || url == "" {
			if authFailed(err) {
				return used
			}
			continue
		}
		section["image_url"] = url
		used++
	}
	return used
}

func chooseCover(ctx context.Context, deps Deps, image *llm.Provider, title, direction, templateKey string, allowAI bool) ([]byte, string, map[string]any) {
	builtin := cover.CoverPNG(templateKey)
	raw := builtin
	source := "builtin"
	var errText string
	var generated []byte
	var genErr error
	if allowAI {
		generated, genErr = generatedCover(ctx, deps, image, title, direction)
	}
	if genErr != nil {
		errText = genErr.Error()
	} else if len(generated) > 0 {
		raw = generated
		source = "ai"
	}
	body, filename, contentType, prepErr := cover.PrepareCover(raw)
	if prepErr != nil && source == "ai" {
		if errText != "" {
			errText += "；"
		}
		errText += prepErr.Error()
		source = "builtin"
		body, filename, contentType, prepErr = cover.PrepareCover(builtin)
	}
	if prepErr != nil || len(body) == 0 {
		body, filename, contentType = builtin, "cover.png", "image/png"
		if errText == "" {
			errText = "封面处理失败"
		}
	}
	row := map[string]any{
		"stage": "cover", "source": source, "bytes": len(body),
		"filename": filename, "content_type": contentType, "ok": errText == "",
	}
	if errText != "" {
		row["error"] = clip(errText, 300)
	}
	return body, filename, row
}

func fetchResearch(ctx context.Context, gateway *llm.Gateway, providers []llm.Provider, payload map[string]any, policy content.SourcePolicy) (string, []any, llm.Completion, error) {
	topic := topicText(payload)
	messages := llm.StageMessages("资料检索", payload)
	var first llm.Completion
	var err1 error
	for try := 0; try < 2; try++ {
		if try > 0 {
			if err := waitResearch(ctx, gateway); err != nil {
				return "", nil, first, err
			}
		}
		first, err1 = gateway.CompleteResearch(ctx, providers, messages)
		if !retryResearch(first, err1) {
			break
		}
	}
	notes := ""
	var sources []map[string]any
	var dropped []map[string]any
	relaxed := false
	client := httpClient(gateway)
	if err1 == nil {
		notes, sources, dropped, relaxed = takeSources(ctx, client, first.Content, first.ResearchSources, policy, topic)
	}
	short := cloneMap(payload)
	short["query_mode"] = "short"
	backupModel := ""
	if gateway != nil {
		backupModel = gateway.ResearchBackupModel
	}
	failedModel := ""
	if len(providers) > 0 {
		failedModel = providers[0].Model
	}
	var second llm.Completion
	var err2 error
	merged := llm.Completion{
		Attempts:         append([]map[string]any{}, first.Attempts...),
		PromptTokens:     first.PromptTokens,
		CompletionTokens: first.CompletionTokens,
		ProviderID:       first.ProviderID,
		ProviderName:     first.ProviderName,
		Protocol:         first.Protocol,
		Model:            first.Model,
		Content:          first.Content,
	}
	if len(sources) == 0 {
		second, err2 = gateway.CompleteResearch(ctx, backupResearchProviders(providers, backupModel, failedModel), llm.StageMessages("资料检索", short))
		merged.Attempts = append(merged.Attempts, second.Attempts...)
		merged.PromptTokens += second.PromptTokens
		merged.CompletionTokens += second.CompletionTokens
		if err2 == nil {
			notes2, sources2, dropped2, relaxed2 := takeSources(ctx, client, second.Content, second.ResearchSources, policy, topic)
			dropped = append(dropped, dropped2...)
			merged.ProviderID = second.ProviderID
			merged.ProviderName = second.ProviderName
			merged.Protocol = second.Protocol
			merged.Model = second.Model
			merged.Content = second.Content
			if len(sources2) > 0 {
				if notes2 != "" {
					notes = notes2
				}
				sources = sources2
				relaxed = relaxed2
			} else {
				if notes == "" {
					notes = notes2
				}
				relaxed = relaxed || relaxed2
			}
		}
	}
	rawCount := len(first.ResearchSources)
	if len(second.ResearchSources) > 0 {
		rawCount += len(second.ResearchSources)
	}
	var engines []map[string]any
	if len(sources) < 2 {
		var found []map[string]any
		for _, query := range backupQueries(payload) {
			rows, logged, searchErr := backupHTMLSearch(ctx, gateway, query)
			engines = append(engines, logged...)
			if searchErr != nil && len(logged) == 0 {
				engines = append(engines, map[string]any{"engine": "backup", "query": query, "count": 0, "error": clip(searchErr.Error(), 180)})
			}
			found = append(found, rows...)
		}
		rawCount += len(found)
		if len(found) > 0 {
			combined := append(append([]map[string]any{}, sources...), found...)
			notes3, sources3, dropped3, relaxed3 := takeSources(ctx, client, notes, combined, policy, topic)
			dropped = append(dropped, dropped3...)
			if len(sources3) >= len(sources) {
				if notes == "" && notes3 != "" {
					notes = notes3
				}
				sources = sources3
				relaxed = relaxed || relaxed3
				if merged.Content == "" {
					merged.Content = notes
				}
			}
		}
	}
	if len(sources) > 0 {
		judged, extra, judgeCompletion := judgeAndKeep(ctx, gateway, providers, topic, sources)
		dropped = append(dropped, extra...)
		sources = judged
		merged.PromptTokens += judgeCompletion.PromptTokens
		merged.CompletionTokens += judgeCompletion.CompletionTokens
		if len(judgeCompletion.Attempts) > 0 {
			merged.Attempts = append(merged.Attempts, judgeCompletion.Attempts...)
		}
	}
	if sourcesHaveURL(sources) || err2 == nil || err1 == nil {
		if merged.Model == "" {
			merged.ProviderID = first.ProviderID
			merged.ProviderName = first.ProviderName
			merged.Protocol = first.Protocol
			merged.Model = first.Model
			merged.Content = first.Content
		}
		sources = preferMaps(sources, payload)
		stampSourceTrace(&merged, sources, dropped, relaxed, rawCount, engines)
		stripRankScores(sources)
		if sourcesHaveURL(sources) || err2 == nil || (err1 == nil && err2 == nil) {
			return notes, anySources(sources), merged, nil
		}
		if err1 == nil {
			return notes, anySources(sources), merged, nil
		}
	}
	if err2 != nil {
		stampSourceTrace(&merged, nil, dropped, relaxed, rawCount, engines)
		return "", nil, merged, err2
	}
	stripRankScores(sources)
	return notes, anySources(sources), merged, nil
}

func waitResearch(ctx context.Context, gateway *llm.Gateway) error {
	wait := 300 * time.Millisecond
	if gateway != nil && gateway.Backoff > 0 {
		wait = gateway.Backoff
		if wait > time.Second {
			wait = time.Second
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryResearch(completion llm.Completion, err error) bool {
	if err == nil {
		return strings.TrimSpace(completion.Content) == "" && len(completion.ResearchSources) == 0
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "502") || strings.Contains(msg, "503") || strings.Contains(msg, "没有返回文本")
}

func backupHTMLSearch(ctx context.Context, gateway *llm.Gateway, query string) ([]map[string]any, []map[string]any, error) {
	if gateway == nil {
		return nil, nil, nil
	}
	var targets []llm.SearchTarget
	if target, ok := llm.ConfiguredSearch(gateway.SearchAPIProvider, gateway.SearchAPIURL, gateway.SearchAPIKey); ok {
		targets = append(targets, target)
	}
	for _, raw := range []string{gateway.BackupSearchURL, gateway.BackupSearchAlt, gateway.BackupSearchBaidu} {
		if strings.TrimSpace(raw) != "" {
			targets = append(targets, llm.SearchTarget{URL: raw})
		}
	}
	if len(targets) == 0 {
		return nil, nil, nil
	}
	return llm.SearchChainLogged(ctx, gateway.Client, targets, query)
}

func backupQueries(payload map[string]any) []string {
	var out []string
	add := func(query string) {
		query = strings.TrimSpace(query)
		if query == "" || len(out) >= 3 {
			return
		}
		for _, prev := range out {
			if prev == query {
				return
			}
		}
		out = append(out, query)
	}
	add(shortSearchQuery(payload))
	add(focusedQuery(payload))
	title := strings.TrimSpace(str(payload["topic_title"]))
	if title == "" {
		title = strings.TrimSpace(str(payload["subtopic"]))
	}
	if utf8.RuneCountInString(title) > 12 {
		title = string([]rune(title)[:12])
	}
	extra := strings.TrimSpace(str(payload["keywords"]))
	if extra == "" {
		extra = strings.TrimSpace(str(payload["direction"]))
	}
	if utf8.RuneCountInString(extra) > 8 {
		extra = string([]rune(extra)[:8])
	}
	add(strings.TrimSpace(title + " " + extra))
	if len(out) < 2 {
		base := shortSearchQuery(payload)
		if utf8.RuneCountInString(base) > 8 {
			add(string([]rune(base)[:8]))
		}
	}
	return out
}

func focusedQuery(payload map[string]any) string {
	title := strings.TrimSpace(str(payload["topic_title"]))
	if title == "" {
		title = strings.TrimSpace(str(payload["subtopic"]))
	}
	if title == "" {
		return shortSearchQuery(payload)
	}
	title = strings.Map(func(r rune) rune {
		switch r {
		case '，', '。', '？', '?', '！', '、', '：', ':', '；', ';':
			return ' '
		default:
			return r
		}
	}, title)
	title = strings.Join(strings.Fields(title), " ")
	if utf8.RuneCountInString(title) > 24 {
		title = string([]rune(title)[:24])
	}
	return strings.TrimSpace(title)
}

func shortSearchQuery(payload map[string]any) string {
	title := strings.TrimSpace(str(payload["topic_title"]))
	if utf8.RuneCountInString(title) > 18 {
		title = string([]rune(title)[:18])
	}
	var extra []string
	seen := map[string]bool{}
	for _, term := range topicTerms(topicText(payload)) {
		if len(extra) >= 3 {
			break
		}
		if term == "" || seen[term] || strings.Contains(title, term) {
			continue
		}
		seen[term] = true
		extra = append(extra, term)
	}
	query := strings.TrimSpace(title + " " + strings.Join(extra, " "))
	if query == "" {
		query = strings.TrimSpace(topicText(payload))
	}
	if utf8.RuneCountInString(query) > 32 {
		query = string([]rune(query)[:32])
	}
	return strings.TrimSpace(query)
}

func httpClient(gateway *llm.Gateway) *http.Client {
	if gateway == nil {
		return nil
	}
	return gateway.Client
}

func sourcesDated(sources []any) bool {
	for _, raw := range sources {
		source, _ := raw.(map[string]any)
		if source == nil {
			continue
		}
		blob := str(source["title"]) + str(source["snippet"]) + str(source["note"]) + str(source["published_on"])
		if quality.DatedText(blob) {
			return true
		}
	}
	return false
}

func topicText(payload map[string]any) string {
	parts := []string{
		str(payload["direction"]), str(payload["subtopic"]), str(payload["column"]),
		str(payload["keywords"]), str(payload["topic_title"]), str(payload["column_positioning"]),
	}
	return strings.Join(parts, " ")
}

func backupResearchProviders(providers []llm.Provider, backupModel, failedModel string) []llm.Provider {
	if len(providers) == 0 {
		return nil
	}
	model := strings.TrimSpace(backupModel)
	failedModel = strings.TrimSpace(failedModel)
	if model == "" || strings.EqualFold(model, failedModel) || strings.Contains(strings.ToLower(model), "chat-fast") {
		model = "grok-4.7"
	}
	if strings.EqualFold(model, failedModel) {
		model = "grok-4"
	}
	var matched []llm.Provider
	for _, provider := range providers {
		if strings.EqualFold(provider.Model, model) && !strings.EqualFold(provider.Model, failedModel) {
			matched = append(matched, provider)
		}
	}
	if len(matched) == 0 {
		for _, provider := range providers {
			if strings.EqualFold(provider.Model, failedModel) || strings.Contains(strings.ToLower(provider.Model), "chat-fast") {
				continue
			}
			matched = append(matched, provider)
		}
	}
	if len(matched) > 0 {
		return matched
	}
	base := providers[0]
	for _, provider := range providers {
		if provider.Protocol == "" || provider.Protocol == "openai" {
			base = provider
			break
		}
	}
	clone := base
	if clone.Protocol != "" && clone.Protocol != "openai" {
		clone.Protocol = "openai"
	}
	clone.Model = model
	clone.Name = strings.TrimSpace(base.Name + " 补救")
	return []llm.Provider{clone}
}

func filterAnySources(ctx context.Context, client *http.Client, sources []any, topic string) []any {
	maps := make([]map[string]any, 0, len(sources))
	for _, raw := range sources {
		source, _ := raw.(map[string]any)
		if source != nil {
			maps = append(maps, source)
		}
	}
	enrichSources(ctx, client, maps)
	kept, _ := filterSourceQuality(maps, topic)
	stripRankScores(kept)
	out := make([]any, len(kept))
	for i := range kept {
		out[i] = kept[i]
	}
	return out
}

func preferMaps(sources []map[string]any, payload map[string]any) []map[string]any {
	if len(sources) == 0 {
		return sources
	}
	return content.PreferSources(sources, sourcePreferMode(payload))
}

func sourcePreferMode(payload map[string]any) string {
	if str(payload["vertical_key"]) == "elv" || strings.Contains(str(payload["vertical"]), "弱电") {
		return "elv"
	}
	return "zh"
}

func preferListed(sources []any, mode string) []any {
	maps := make([]map[string]any, 0, len(sources))
	for _, raw := range sources {
		source, _ := raw.(map[string]any)
		if source != nil {
			maps = append(maps, source)
		}
	}
	preferred := content.PreferSources(maps, mode)
	out := make([]any, len(preferred))
	for i := range preferred {
		out[i] = preferred[i]
	}
	return out
}

func takeSources(ctx context.Context, client *http.Client, content string, cited []map[string]any, policy content.SourcePolicy, topic string) (string, []map[string]any, []map[string]any, bool) {
	notes, _ := llm.InterpretResearch(content)
	policy = policy.ForTopic(topic)
	raw := cited
	enrichSources(ctx, client, raw)
	kept, dropped := policy.Split(raw)
	kept, extra := filterSourceQuality(kept, topic)
	dropped = append(dropped, extra...)
	if len(raw) == 0 {
		dropped = append(dropped, map[string]any{"reason": "没有解析到来源"})
	}
	if sourcesHaveURL(kept) {
		return notes, kept, dropped, false
	}
	softKept, _ := policy.Relaxed().Split(raw)
	softKept, extra = filterSourceQuality(softKept, topic)
	dropped = append(dropped, extra...)
	if sourcesHaveURL(softKept) {
		for _, row := range dropped {
			if row["reason"] == "不在允许名单" {
				row["kept_relaxed"] = true
			}
		}
		return notes, softKept, dropped, true
	}
	return notes, nil, dropped, false
}

func enrichSources(ctx context.Context, client *http.Client, sources []map[string]any) {
	if client == nil || len(sources) == 0 {
		return
	}
	fetched := 0
	for _, source := range sources {
		if fetched >= 8 {
			break
		}
		if source == nil || strings.TrimSpace(str(source["title"])) != "" {
			continue
		}
		raw := strings.TrimSpace(str(source["url"]))
		if raw == "" || content.PrivateURL(raw) {
			continue
		}
		fetched++
		title, desc := llm.FetchPageMeta(ctx, client, raw)
		if title != "" {
			source["title"] = title
			source["fetched_title"] = title
		}
		if desc != "" && strings.TrimSpace(str(source["snippet"])) == "" {
			source["snippet"] = desc
			source["fetched_meta"] = desc
		}
	}
}

const sourceKeepMax = 6

const judgeMinScore = 2

// filterSourceQuality hard-drops pages that cannot be a source. Relevance is the judge's job.
func filterSourceQuality(sources []map[string]any, _ string) (kept []map[string]any, dropped []map[string]any) {
	for _, source := range sources {
		if source == nil {
			continue
		}
		raw := str(source["url"])
		title := strings.TrimSpace(str(source["title"]))
		snippet := strings.TrimSpace(str(source["snippet"]) + " " + str(source["note"]) + " " + str(source["fetched_meta"]))
		if templatePage(raw, title+" "+str(source["name"])) {
			dropped = append(dropped, map[string]any{"url": raw, "title": title, "score": 0, "reason": "PPT模板或文库"})
			continue
		}
		if content.DictionaryHost(raw) || content.ReferenceEntry(title) {
			dropped = append(dropped, map[string]any{"url": raw, "title": title, "score": 0, "reason": "词典或百科"})
			continue
		}
		if content.SearchResultURL(raw) {
			dropped = append(dropped, map[string]any{"url": raw, "title": title, "score": 0, "reason": "搜索结果页"})
			continue
		}
		if reason := content.OffTopicPage(raw, title+" "+snippet); reason != "" {
			dropped = append(dropped, map[string]any{"url": raw, "title": title, "score": 0, "reason": reason})
			continue
		}
		if content.PrivateURL(raw) {
			dropped = append(dropped, map[string]any{"url": raw, "title": title, "score": 0, "reason": "内网地址"})
			continue
		}
		if title == "" && strings.TrimSpace(snippet) == "" && !content.PublicPageURL(raw) {
			dropped = append(dropped, map[string]any{"url": raw, "title": title, "score": 0, "reason": "没有标题"})
			continue
		}
		kept = append(kept, source)
	}
	return kept, dropped
}

func stripRankScores(sources []map[string]any) {
	for _, source := range sources {
		if source != nil {
			delete(source, "rank_score")
			delete(source, "_from_judge")
		}
	}
}

const judgeBatchSize = 8

const judgeRetryModel = "grok-4.20-0309-non-reasoning"

const sourceFloor = 2

func judgeAndKeep(ctx context.Context, gateway *llm.Gateway, providers []llm.Provider, topic string, sources []map[string]any) ([]map[string]any, []map[string]any, llm.Completion) {
	if len(sources) == 0 {
		return nil, nil, llm.Completion{}
	}
	if gateway == nil || len(providers) == 0 {
		kept, dropped := rankFallback(sources, topic, true)
		return kept, dropped, llm.Completion{Attempts: []map[string]any{{"ok": false, "error": "没有判断模型", "judge_raw": ""}}}
	}
	var completion llm.Completion
	var rawLog strings.Builder
	var kept, dropped, failed []map[string]any
	judgedOK := false
	for offset := 0; offset < len(sources); offset += judgeBatchSize {
		end := offset + judgeBatchSize
		if end > len(sources) {
			end = len(sources)
		}
		batch := sources[offset:end]
		items, raw, part := judgeBatch(ctx, gateway, providers, topic, batch, offset)
		completion = mergeCompletion(completion, part)
		if rawLog.Len() > 0 {
			rawLog.WriteString("\n")
		}
		rawLog.WriteString(raw)
		if items == nil {
			failed = append(failed, batch...)
			continue
		}
		judgedOK = true
		batchKept, batchDropped := applyJudgeScores(batch, alignJudgeItems(offset, batch, items))
		for _, source := range batchKept {
			source["_from_judge"] = true
		}
		kept = append(kept, batchKept...)
		dropped = append(dropped, batchDropped...)
	}
	if len(failed) > 0 {
		ranked, extra := rankFallback(failed, topic, !judgedOK)
		kept = append(kept, ranked...)
		dropped = append(dropped, extra...)
	}
	kept, dropped = capKept(kept, dropped)
	rawText := clip(rawLog.String(), 1500)
	if len(completion.Attempts) == 0 {
		completion.Attempts = []map[string]any{{"ok": judgedOK}}
	}
	completion.Attempts[len(completion.Attempts)-1]["judge_raw"] = rawText
	return kept, dropped, completion
}

func judgeBatch(ctx context.Context, gateway *llm.Gateway, providers []llm.Provider, topic string, batch []map[string]any, offset int) ([]any, string, llm.Completion) {
	candidates := make([]any, 0, len(batch))
	for i, source := range batch {
		title := strings.TrimSpace(str(source["title"]))
		snippet := sourceSnippet(source)
		candidates = append(candidates, map[string]any{
			"index": offset + i + 1, "title": title, "snippet": snippet, "url": str(source["url"]),
		})
	}
	payload := map[string]any{"topic": topic, "candidates": candidates}
	messages := llm.StageMessages("来源判断", payload)
	schema := llm.StageSchema("来源判断")
	first, err := gateway.CompleteFirst(ctx, providers, messages, schema)
	raw := strings.TrimSpace(first.Content)
	if err != nil && raw == "" {
		raw = err.Error()
	}
	if items, ok := parseJudgeItems(first.Content); ok {
		return items, raw, first
	}
	base := providers[0]
	if strings.EqualFold(strings.TrimSpace(base.Model), judgeRetryModel) {
		return nil, raw, first
	}
	retry := base
	retry.Model = judgeRetryModel
	retry.Name = strings.TrimSpace(base.Name + " 重试")
	second, err2 := gateway.CompleteFirst(ctx, []llm.Provider{retry}, messages, schema)
	combined := mergeCompletion(first, second)
	raw2 := strings.TrimSpace(second.Content)
	if err2 != nil && raw2 == "" {
		raw2 = err2.Error()
	}
	if raw2 != "" {
		if raw != "" {
			raw += "\n"
		}
		raw += raw2
	}
	if items, ok := parseJudgeItems(second.Content); ok {
		return items, raw, combined
	}
	return nil, raw, combined
}

func mergeCompletion(base, extra llm.Completion) llm.Completion {
	base.PromptTokens += extra.PromptTokens
	base.CompletionTokens += extra.CompletionTokens
	base.Attempts = append(base.Attempts, extra.Attempts...)
	if base.Model == "" {
		base.ProviderID = extra.ProviderID
		base.ProviderName = extra.ProviderName
		base.Protocol = extra.Protocol
		base.Model = extra.Model
	}
	if strings.TrimSpace(extra.Content) != "" {
		base.Content = extra.Content
	}
	return base
}

func alignJudgeItems(offset int, batch []map[string]any, items []any) []any {
	n := len(batch)
	if n == 0 || len(items) == 0 {
		return items
	}
	lo, hi := offset+1, offset+n
	global, local := 0, 0
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		index := asInt(item["index"])
		if index >= lo && index <= hi {
			global++
		} else if index >= 1 && index <= n {
			local++
		}
	}
	if global == 0 || global < local {
		return items
	}
	shifted := make([]any, 0, len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		clone := map[string]any{"score": item["score"], "facts": item["facts"]}
		index := asInt(item["index"])
		if index >= lo && index <= hi {
			clone["index"] = index - offset
		}
		shifted = append(shifted, clone)
	}
	return shifted
}

func parseJudgeItems(text string) ([]any, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, false
	}
	var candidates []string
	if fenced := extractFence(text); fenced != "" {
		candidates = append(candidates, fenced)
	}
	candidates = append(candidates, text)
	for _, raw := range candidates {
		if items, ok := itemsFromJudgeText(raw); ok {
			return items, true
		}
	}
	for _, raw := range candidates {
		if items := normalizeJudgeItems(salvageJudgeObjects(raw)); len(items) > 0 {
			return items, true
		}
	}
	return nil, false
}

func extractFence(text string) string {
	start := strings.Index(text, "```")
	if start < 0 {
		return ""
	}
	rest := strings.TrimSpace(text[start+3:])
	lower := strings.ToLower(rest)
	if strings.HasPrefix(lower, "json") {
		rest = strings.TrimSpace(rest[4:])
	}
	if end := strings.LastIndex(rest, "```"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

func itemsFromJudgeText(raw string) ([]any, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		if obj := outermost(raw, '{', '}'); obj != "" && json.Unmarshal([]byte(obj), &value) == nil {
		} else if arr := outermost(raw, '[', ']'); arr != "" && json.Unmarshal([]byte(arr), &value) == nil {
		} else {
			return nil, false
		}
	}
	return coerceJudgeItems(value)
}

func outermost(raw string, open, close rune) string {
	start := strings.IndexRune(raw, open)
	end := strings.LastIndex(raw, string(close))
	if start < 0 || end <= start {
		return ""
	}
	return raw[start : end+len(string(close))]
}

func coerceJudgeItems(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		items := normalizeJudgeItems(typed)
		return items, len(items) > 0
	case map[string]any:
		for _, key := range []string{"items", "sources", "results", "ratings", "candidates", "data"} {
			child, ok := typed[key]
			if !ok || child == nil {
				continue
			}
			if items, ok := coerceJudgeItems(child); ok {
				return items, true
			}
		}
	}
	return nil, false
}

func normalizeJudgeItems(list []any) []any {
	var out []any
	rated := false
	for _, raw := range list {
		item, _ := raw.(map[string]any)
		if item == nil || !judgeRating(item) {
			continue
		}
		rated = true
		index, present := judgeIndex(item)
		normalized := map[string]any{"score": judgeScore(item), "facts": judgeFacts(item)}
		if present {
			normalized["index"] = index
		}
		out = append(out, normalized)
	}
	if !rated {
		return nil
	}
	return out
}

func judgeRating(item map[string]any) bool {
	for _, key := range []string{"score", "rating", "relevance", "facts", "fact", "key_facts", "points"} {
		if _, ok := item[key]; ok {
			return true
		}
	}
	return false
}

func judgeIndex(item map[string]any) (int, bool) {
	for _, key := range []string{"index", "idx", "i", "id"} {
		if _, ok := item[key]; ok {
			return asInt(item[key]), true
		}
	}
	return 0, false
}

func judgeScore(item map[string]any) int {
	for _, key := range []string{"score", "rating", "relevance"} {
		if _, ok := item[key]; !ok {
			continue
		}
		score := asInt(item[key])
		if score < 0 {
			return 0
		}
		if score > 3 {
			return 3
		}
		return score
	}
	return 0
}

func judgeFacts(item map[string]any) []any {
	for _, key := range []string{"facts", "key_facts", "points", "fact"} {
		if _, ok := item[key]; !ok || item[key] == nil {
			continue
		}
		if text, ok := item[key].(string); ok {
			text = strings.TrimSpace(text)
			if text != "" {
				return []any{text}
			}
			continue
		}
		var facts []any
		for _, fact := range asList(item[key]) {
			text := strings.TrimSpace(str(fact))
			if text != "" {
				facts = append(facts, text)
			}
		}
		if len(facts) > 0 {
			return facts
		}
	}
	return []any{}
}

func salvageJudgeObjects(text string) []any {
	var items []any
	for _, obj := range scanJSONObjects(text) {
		var item map[string]any
		if json.Unmarshal([]byte(obj), &item) != nil || item == nil || !judgeRating(item) {
			continue
		}
		items = append(items, item)
	}
	return items
}

func scanJSONObjects(text string) []string {
	var out []string
	var starts []int
	inString := false
	escape := false
	for i, r := range text {
		if inString {
			if escape {
				escape = false
				continue
			}
			if r == '\\' {
				escape = true
				continue
			}
			if r == '"' {
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case '{':
			starts = append(starts, i)
		case '}':
			if len(starts) == 0 {
				continue
			}
			start := starts[len(starts)-1]
			starts = starts[:len(starts)-1]
			out = append(out, text[start:i+len(string(r))])
		}
	}
	return out
}

func capKept(kept, dropped []map[string]any) ([]map[string]any, []map[string]any) {
	if len(kept) <= sourceKeepMax {
		return kept, dropped
	}
	sort.SliceStable(kept, func(i, j int) bool {
		ij := kept[i]["_from_judge"] == true
		jj := kept[j]["_from_judge"] == true
		if ij != jj {
			return ij
		}
		return asInt(kept[i]["rank_score"]) > asInt(kept[j]["rank_score"])
	})
	for _, extra := range kept[sourceKeepMax:] {
		dropped = append(dropped, map[string]any{
			"url": str(extra["url"]), "title": str(extra["title"]),
			"score": extra["rank_score"], "reason": "超出保留条数",
		})
	}
	return kept[:sourceKeepMax], dropped
}

func rankFallback(sources []map[string]any, topic string, fill bool) (kept []map[string]any, dropped []map[string]any) {
	type scored struct {
		source  map[string]any
		score   int
		overlap int
	}
	var ranked []scored
	for _, source := range sources {
		if source == nil {
			continue
		}
		score, overlap := scoreSource(source, topic)
		source["rank_score"] = score
		ranked = append(ranked, scored{source: source, score: score, overlap: overlap})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})
	var above, below []scored
	for _, row := range ranked {
		if row.overlap >= sourceFloor && row.score >= sourceFloor {
			above = append(above, row)
		} else {
			below = append(below, row)
		}
	}
	chosen := above
	if len(chosen) > sourceKeepMax {
		for _, extra := range chosen[sourceKeepMax:] {
			dropped = append(dropped, map[string]any{
				"url": str(extra.source["url"]), "title": str(extra.source["title"]),
				"score": extra.score, "reason": "超出保留条数",
			})
		}
		chosen = chosen[:sourceKeepMax]
	}
	if fill && len(chosen) < 3 {
		need := 3 - len(chosen)
		if need > len(below) {
			need = len(below)
		}
		chosen = append(chosen, below[:need]...)
		below = below[need:]
	}
	for _, row := range below {
		dropped = append(dropped, map[string]any{
			"url": str(row.source["url"]), "title": str(row.source["title"]),
			"score": row.score, "reason": "低于分数线",
		})
	}
	for _, row := range chosen {
		kept = append(kept, row.source)
	}
	return kept, dropped
}

func scoreSource(source map[string]any, topic string) (score, overlap int) {
	title := strings.TrimSpace(str(source["title"]))
	meta := strings.TrimSpace(str(source["snippet"]) + " " + str(source["note"]) + " " + str(source["fetched_meta"]))
	blob := strings.TrimSpace(title + " " + meta + " " + str(source["url"]))
	phrases := 0
	for _, phrase := range topicPhrases(topic) {
		if !strings.Contains(blob, phrase) {
			continue
		}
		points := 3
		if strings.Contains(title, phrase) {
			points = 4
		}
		overlap += points
		phrases++
		if phrases >= 3 {
			break
		}
	}
	terms := 0
	seen := map[string]bool{}
	for _, term := range topicTerms(topic) {
		if seen[term] || !strings.Contains(blob, term) {
			continue
		}
		seen[term] = true
		overlap += 2
		terms++
		if terms >= 4 {
			break
		}
	}
	if content.SharesStandardNumber(topic, blob) {
		overlap += 6
	}
	score = overlap
	if overlap > 0 && content.OfficialHost(str(source["url"])) {
		score += 3
	}
	if strings.Contains(title, "百科") || strings.Contains(title, "词典") || strings.Contains(title, "搜索") {
		score -= 4
	}
	if score < 0 {
		score = 0
	}
	return score, overlap
}

func topicPhrases(topic string) []string {
	var out []string
	seen := map[string]bool{}
	for _, run := range hanRuns(topic) {
		if len(run) < 4 {
			continue
		}
		for i := 0; i+4 <= len(run); i++ {
			phrase := string(run[i : i+4])
			if seen[phrase] || genericPhrase(phrase) {
				continue
			}
			seen[phrase] = true
			out = append(out, phrase)
		}
	}
	return out
}

func hanRuns(text string) [][]rune {
	var out [][]rune
	var buf []rune
	flush := func() {
		if len(buf) > 0 {
			out = append(out, buf)
			buf = nil
		}
	}
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			buf = append(buf, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func genericPhrase(text string) bool {
	switch text {
	case "国家标准", "标准公告", "住房城乡", "城乡建设", "市场监管", "监督管理", "正式发布", "关于发布", "关于废止", "有限公司":
		return true
	default:
		return false
	}
}

func applyJudgeScores(sources []map[string]any, items []any) (kept []map[string]any, dropped []map[string]any) {
	type rating struct {
		score int
		facts []any
	}
	byIndex := map[int]rating{}
	for order, raw := range items {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		index := asInt(item["index"])
		if _, present := item["index"]; !present || index <= 0 {
			index = order + 1
		}
		score := asInt(item["score"])
		if score < 0 {
			score = 0
		}
		if score > 3 {
			score = 3
		}
		var facts []any
		for _, fact := range asList(item["facts"]) {
			text := strings.TrimSpace(str(fact))
			if text != "" {
				facts = append(facts, text)
			}
		}
		byIndex[index] = rating{score: score, facts: facts}
	}
	type scored struct {
		source map[string]any
		score  int
	}
	var ranked []scored
	for i, source := range sources {
		if source == nil {
			continue
		}
		rate, ok := byIndex[i+1]
		if !ok || rate.score < judgeMinScore {
			score := 0
			if ok {
				score = rate.score
			}
			dropped = append(dropped, map[string]any{
				"url": str(source["url"]), "title": str(source["title"]), "score": score, "reason": "相关性不足",
			})
			continue
		}
		if len(rate.facts) > 0 {
			source["facts"] = rate.facts
		}
		source["rank_score"] = rate.score
		ranked = append(ranked, scored{source: source, score: rate.score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})
	if len(ranked) > sourceKeepMax {
		for _, extra := range ranked[sourceKeepMax:] {
			dropped = append(dropped, map[string]any{
				"url": str(extra.source["url"]), "title": str(extra.source["title"]),
				"score": extra.score, "reason": "超出保留条数",
			})
		}
		ranked = ranked[:sourceKeepMax]
	}
	for _, row := range ranked {
		kept = append(kept, row.source)
	}
	return kept, dropped
}

func sourceSnippet(source map[string]any) string {
	text := strings.TrimSpace(str(source["snippet"]))
	if text == "" {
		text = strings.TrimSpace(str(source["note"]))
	}
	if text == "" {
		text = strings.TrimSpace(str(source["fetched_meta"]))
	}
	return text
}

func factSheet(sources []any) string {
	var b strings.Builder
	n := 0
	for _, raw := range sources {
		source, _ := raw.(map[string]any)
		if source == nil || strings.TrimSpace(str(source["url"])) == "" {
			continue
		}
		n++
		title := strings.TrimSpace(str(source["title"]))
		if title == "" {
			title = strings.TrimSpace(str(source["name"]))
		}
		if title == "" {
			title = str(source["url"])
		}
		b.WriteString(itoa(n))
		b.WriteString(". ")
		b.WriteString(title)
		b.WriteString("\n")
		facts := factLines(source)
		if len(facts) == 0 {
			if text := sourceSnippet(source); text != "" {
				facts = []string{text}
			}
		}
		for _, fact := range facts {
			b.WriteString("- ")
			b.WriteString(fact)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func factLines(source map[string]any) []string {
	var out []string
	for _, raw := range asList(source["facts"]) {
		if text := strings.TrimSpace(str(raw)); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func urlCount(sources []any) int {
	n := 0
	for _, raw := range sources {
		source, _ := raw.(map[string]any)
		if source != nil && strings.TrimSpace(str(source["url"])) != "" {
			n++
		}
	}
	return n
}

func settleExperience(draft map[string]any, structure string, target int) map[string]any {
	if draft == nil {
		return nil
	}
	draft = stripCiteMarks(draft)
	draft = editorial.LimitParagraphs(draft, 4)
	draft = dedupeExperience(draft, target)
	if columns.NormalizeStructure(structure) != "custom" {
		draft = quality.AlignStructure(structure, draft)
	}
	return draft
}

// dedupeExperience drops near-duplicate paragraphs. When that would push a draft
// that already cleared the length floor back under it, the longer text stays.
func dedupeExperience(draft map[string]any, target int) map[string]any {
	before := content.BodyRunes(draft)
	next := cloneDraft(draft)
	next = editorial.RepairNearDuplicates(next)
	next = editorial.DedupeBlocks(next)
	after := content.BodyRunes(next)
	floor := 0
	if target > 0 {
		floor = target * 85 / 100
	}
	if floor > 0 && before >= floor && after < floor {
		return draft
	}
	return next
}

func cloneDraft(draft map[string]any) map[string]any {
	raw, err := json.Marshal(draft)
	if err != nil {
		return draft
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil || out == nil {
		return draft
	}
	return out
}

func stripCiteMarks(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	for _, key := range []string{"title", "summary", "closing"} {
		if text := str(draft[key]); text != "" {
			draft[key] = citationMarks.ReplaceAllString(text, "")
		}
	}
	for _, raw := range asList(draft["sections"]) {
		section, _ := raw.(map[string]any)
		if section == nil {
			continue
		}
		if text := str(section["heading"]); text != "" {
			section["heading"] = citationMarks.ReplaceAllString(text, "")
		}
		var paras []any
		for _, item := range asList(section["paragraphs"]) {
			paras = append(paras, citationMarks.ReplaceAllString(str(item), ""))
		}
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
		if len(asList(section["list"])) > 0 {
			var list []any
			for _, item := range asList(section["list"]) {
				list = append(list, citationMarks.ReplaceAllString(str(item), ""))
			}
			section["list"] = list
		}
	}
	return draft
}

func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case string:
		n = strings.TrimSpace(n)
		if n == "" {
			return 0
		}
		sign := 1
		if n[0] == '-' {
			sign = -1
			n = n[1:]
		}
		i := 0
		for _, r := range n {
			if r < '0' || r > '9' {
				break
			}
			i = i*10 + int(r-'0')
		}
		return sign * i
	default:
		return 0
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func topicTerms(topic string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(text string) {
		text = strings.TrimSpace(text)
		if utf8.RuneCountInString(text) != 2 || seen[text] || noisyTerm(text) {
			return
		}
		seen[text] = true
		out = append(out, text)
	}
	for _, part := range strings.FieldsFunc(topic, func(r rune) bool {
		return r == ' ' || r == ',' || r == '，' || r == '、' || r == '\n' || r == '；' || r == ';' || r == '。' || r == '：' || r == ':'
	}) {
		runes := []rune(strings.TrimSpace(part))
		if len(runes) == 2 {
			add(string(runes))
		}
		for i := 0; i+2 <= len(runes); i++ {
			add(string(runes[i : i+2]))
		}
	}
	return out
}

func noisyTerm(text string) bool {
	for _, r := range text {
		switch r {
		case '的', '了', '在', '是', '和', '与', '及', '把', '用', '就', '都', '而', '要', '对', '从', '给', '让', '这', '那', '一', '个', '不', '也', '又', '很', '最', '会', '能', '可', '被', '向', '中', '上', '下', '来', '去', '或', '并', '其', '为', '以', '于', '到', '等', '吧', '吗', '呢':
			return true
		}
	}
	switch text {
	case "一个", "这个", "可以", "不要", "我们", "读者", "自己", "什么", "怎么", "如何", "进行", "通过", "为了", "栏目", "定位", "全文", "已经", "因为", "所以", "如果", "但是":
		return true
	}
	return false
}

func templatePage(raw, title string) bool {
	blob := strings.ToLower(raw + " " + title)
	for _, word := range []string{".ppt", "pptx", "幻灯片", "ppt模板", "课件下载", "模板下载"} {
		if strings.Contains(blob, word) {
			return true
		}
	}
	host := strings.ToLower(raw)
	for _, domain := range []string{"1ppt.com", "51ppt.com", "pptplus.cn", "doc88.com", "docin.com", "book118.com", "wenku.baidu.com", "renrendoc.com", "taodocs.com", "doczj.com", "jinchutou.com"} {
		if strings.Contains(host, domain) {
			return true
		}
	}
	return false
}

func stampSourceTrace(completion *llm.Completion, sources []map[string]any, dropped []map[string]any, relaxed bool, raw int, engines []map[string]any) {
	if completion == nil {
		return
	}
	if len(completion.Attempts) == 0 {
		completion.Attempts = []map[string]any{{
			"provider_id": completion.ProviderID, "provider_name": completion.ProviderName,
			"protocol": completion.Protocol, "model": completion.Model, "ok": true,
		}}
	}
	last := completion.Attempts[len(completion.Attempts)-1]
	last["raw"] = raw
	last["kept"] = len(sources)
	last["relaxed"] = relaxed
	if len(engines) > 0 {
		last["engines"] = engines
	}
	var scored []any
	for _, source := range sources {
		if source == nil {
			continue
		}
		scored = append(scored, map[string]any{
			"url": str(source["url"]), "title": str(source["title"]),
			"score": source["rank_score"], "reason": "保留",
		})
	}
	for _, row := range dropped {
		scored = append(scored, row)
	}
	if len(scored) > 0 {
		last["scored"] = scored
	}
	if len(dropped) > 0 {
		last["dropped"] = dropped
	}
}

func rewriteColumnStructure(ctx context.Context, gateway *llm.Gateway, providers map[string][]llm.Provider, draft, base map[string]any, structure, costNote string) (map[string]any, int, []map[string]any, bool) {
	kind := columns.NormalizeStructure(structure)
	if draft == nil || (kind != "news" && kind != "qa" && kind != "steps") {
		return draft, 0, nil, false
	}
	if quality.StructureOK(kind, draftParagraphs(draft)) {
		return draft, 0, nil, false
	}
	if strings.TrimSpace(costNote) != "" {
		return draft, 0, nil, true
	}
	payload := cloneMap(base)
	payload["draft"] = draft
	payload["column_structure"] = kind
	model, completion, err := gateway.CompleteJSON(ctx, pieceProviders(providers), llm.StageMessages("栏目结构", payload), llm.StageSchema("正文"), validateDraft)
	trace := appendStage(nil, "structure", completion, err)
	if err != nil || model == nil || !quality.StructureOK(kind, draftParagraphs(model)) {
		shaped := quality.AlignStructure(kind, draft)
		if quality.StructureOK(kind, draftParagraphs(shaped)) {
			return shaped, tokensOf(completion), trace, false
		}
		return draft, tokensOf(completion), trace, true
	}
	if content.BodyRunes(model)*2 < content.BodyRunes(draft) {
		return draft, tokensOf(completion), trace, true
	}
	copySources(model, draft)
	return model, tokensOf(completion), trace, false
}

func draftParagraphs(draft map[string]any) []string {
	var out []string
	for _, raw := range asList(draft["sections"]) {
		section, _ := raw.(map[string]any)
		if section == nil {
			continue
		}
		for _, item := range asList(section["paragraphs"]) {
			if text := strings.TrimSpace(str(item)); text != "" {
				out = append(out, text)
			}
		}
	}
	return out
}

func polishDraft(draft map[string]any, w Input) map[string]any {
	draft = editorial.DropGlossary(draft, w.Pack.Key, w.Direction)
	draft = editorial.RepairNearDuplicates(draft)
	draft = editorial.DedupeBlocks(draft)
	draft = editorial.DedupeTables(draft)
	draft = editorial.StripAudience(draft, w.Audience)
	draft = editorial.DropSummaryEcho(draft)
	draft = editorial.RewriteTaboos(draft, w.Taboos)
	draft = editorial.StripMeta(draft)
	draft = editorial.StripForeignScenes(draft, w.ForeignTitles)
	return editorial.StripUnsourcedNames(draft)
}

func bodyHasAny(draft map[string]any, words []string) bool {
	if draft == nil || len(words) == 0 {
		return false
	}
	text := bodyPlain(draft) + "\n" + str(draft["title"]) + "\n" + str(draft["summary"]) + "\n" + str(draft["closing"])
	for _, word := range words {
		if word != "" && strings.Contains(text, word) {
			return true
		}
	}
	return false
}

func softenTaboos(ctx context.Context, gateway *llm.Gateway, providers map[string][]llm.Provider, draft, base map[string]any, taboos, costNote string) (map[string]any, int, []map[string]any) {
	if !editorial.HasTaboo(draft, taboos) {
		return draft, 0, nil
	}
	if strings.TrimSpace(costNote) == "" && gateway != nil {
		payload := cloneMap(base)
		payload["draft"] = draft
		payload["taboos"] = taboos
		model, completion, err := gateway.CompleteJSON(ctx, pieceProviders(providers), llm.StageMessages("禁忌改写", payload), llm.StageSchema("正文"), validateDraft)
		trace := appendStage(nil, "taboo", completion, err)
		if err == nil && model != nil && !editorial.HasTaboo(model, taboos) && !strings.Contains(bodyPlain(model), "这一步按现场记录来写") && content.BodyRunes(model)*2 >= content.BodyRunes(draft) {
			copySources(model, draft)
			return model, tokensOf(completion), trace
		}
		return editorial.RewriteTaboos(draft, taboos), tokensOf(completion), trace
	}
	return editorial.RewriteTaboos(draft, taboos), 0, nil
}

func topUpLength(ctx context.Context, gateway *llm.Gateway, providers map[string][]llm.Provider, draft, base map[string]any, target int, costNote string) (map[string]any, int, []map[string]any) {
	if gateway == nil || target <= 0 || strings.TrimSpace(costNote) != "" || draft == nil {
		return draft, 0, nil
	}
	goal := target * 110 / 100
	high := target * 130 / 100
	if content.BodyRunes(draft) >= goal {
		return draft, 0, nil
	}
	limit := 2
	if target >= 1200 {
		limit = 4
	}
	var tokens int
	var trace []map[string]any
	for pass := 0; pass < limit && content.BodyRunes(draft) < goal; pass++ {
		before := content.BodyRunes(draft)
		shortfall := goal - before
		if shortfall < 1 {
			shortfall = 1
		}
		next, added, expandTrace := expandBody(ctx, gateway, pieceProviders(providers), base, draft, goal, shortfall, 0, nil)
		tokens += added
		trace = append(trace, expandTrace...)
		nextRunes := content.BodyRunes(next)
		if nextRunes > high && before < target*85/100 {
			next = content.TrimToMax(next, target*120/100)
			nextRunes = content.BodyRunes(next)
		}
		if nextRunes > before && nextRunes <= high {
			copySources(next, draft)
			draft = next
		}
	}
	return draft, tokens, trace
}

func rewriteCliches(ctx context.Context, gateway *llm.Gateway, providers map[string][]llm.Provider, draft, base map[string]any, costNote string) (map[string]any, int, []map[string]any) {
	if draft == nil || !bodyHasAny(draft, catalog.Cliches) {
		return draft, 0, nil
	}
	fallback := func() map[string]any {
		cleaned, _ := content.StripCliches(draft).(map[string]any)
		if cleaned == nil {
			return draft
		}
		return cleaned
	}
	if strings.TrimSpace(costNote) != "" || gateway == nil {
		return fallback(), 0, nil
	}
	payload := cloneMap(base)
	payload["draft"] = draft
	model, completion, err := gateway.CompleteJSON(ctx, pieceProviders(providers), llm.StageMessages("套话改写", payload), llm.StageSchema("正文"), validateDraft)
	trace := appendStage(nil, "cliche", completion, err)
	if err == nil && model != nil && !bodyHasAny(model, catalog.Cliches) && content.BodyRunes(model)*2 >= content.BodyRunes(draft) {
		copySources(model, draft)
		return model, tokensOf(completion), trace
	}
	return fallback(), tokensOf(completion), trace
}

func rewriteCopiedSpans(ctx context.Context, gateway *llm.Gateway, providers map[string][]llm.Provider, draft, base map[string]any, costNote string) (map[string]any, int, []map[string]any) {
	windows := content.CopiedWindows(content.DraftText(draft), content.SourceTexts(draft))
	if len(windows) == 0 || draft == nil {
		return draft, 0, nil
	}
	if strings.TrimSpace(costNote) != "" || gateway == nil {
		return draft, 0, nil
	}
	payload := cloneMap(base)
	payload["draft"] = draft
	listed := make([]any, len(windows))
	for i, window := range windows {
		listed[i] = window
	}
	payload["copied_spans"] = listed
	model, completion, err := gateway.CompleteJSON(ctx, pieceProviders(providers), llm.StageMessages("表述改写", payload), llm.StageSchema("正文"), validateDraft)
	trace := appendStage(nil, "copy", completion, err)
	if err != nil || model == nil || content.BodyRunes(model)*2 < content.BodyRunes(draft) {
		return draft, tokensOf(completion), trace
	}
	if content.CopiedSpan(content.DraftText(model), content.SourceTexts(draft)) {
		return draft, tokensOf(completion), trace
	}
	copySources(model, draft)
	return model, tokensOf(completion), trace
}

func addCitations(ctx context.Context, gateway *llm.Gateway, providers map[string][]llm.Provider, draft, base map[string]any) (map[string]any, int, []map[string]any) {
	payload := cloneMap(base)
	payload["draft"] = draft
	model, completion, err := gateway.CompleteJSON(ctx, pieceProviders(providers), llm.StageMessages("补引用", payload), llm.StageSchema("正文"), validateDraft)
	trace := appendStage(nil, "cite", completion, err)
	if err != nil || model == nil {
		return draft, tokensOf(completion), trace
	}
	if !citationMarks.MatchString(bodyPlain(model)) {
		return draft, tokensOf(completion), trace
	}
	if content.BodyRunes(model)*2 < content.BodyRunes(draft) {
		return draft, tokensOf(completion), trace
	}
	if newClaims(draft, model) {
		return draft, tokensOf(completion), trace
	}
	copySources(model, draft)
	return model, tokensOf(completion), trace
}

func newClaims(original, next map[string]any) bool {
	seen := map[string]int{}
	for _, token := range claimNumbers.FindAllString(citationMarks.ReplaceAllString(bodyPlain(original), ""), -1) {
		seen[token]++
	}
	for _, token := range claimNumbers.FindAllString(citationMarks.ReplaceAllString(bodyPlain(next), ""), -1) {
		if seen[token] == 0 {
			return true
		}
		seen[token]--
	}
	return false
}

func applyCitations(draft map[string]any) bool {
	marks := map[int]bool{}
	for _, mark := range citationMarks.FindAllString(bodyPlain(draft), -1) {
		n := 0
		for _, r := range mark {
			if r >= '0' && r <= '9' {
				n = n*10 + int(r-'0')
			}
		}
		if n > 0 {
			marks[n] = true
		}
	}
	if len(marks) == 0 {
		draft["sources"] = []any{}
		return false
	}
	var kept []any
	for i, raw := range asList(draft["sources"]) {
		source, _ := raw.(map[string]any)
		if source == nil || !marks[i+1] {
			continue
		}
		source["cite"] = i + 1
		kept = append(kept, source)
	}
	if kept == nil {
		kept = []any{}
	}
	draft["sources"] = kept
	return len(kept) > 0
}

func listedHaveURL(sources []any) bool {
	for _, raw := range sources {
		source, _ := raw.(map[string]any)
		if source != nil && str(source["url"]) != "" {
			return true
		}
	}
	return false
}

func sourcesHaveURL(sources []map[string]any) bool {
	for _, source := range sources {
		if str(source["url"]) != "" {
			return true
		}
	}
	return false
}

func anySources(sources []map[string]any) []any {
	if len(sources) == 0 {
		return nil
	}
	listed := make([]any, len(sources))
	for i := range sources {
		listed[i] = sources[i]
	}
	return listed
}

func factCheck(ctx context.Context, gateway *llm.Gateway, providers []llm.Provider, payload, draft map[string]any) (map[string]any, int, []map[string]any) {
	checkPayload := cloneMap(payload)
	checkPayload["draft"] = draft
	checked, completion, err := gateway.CompleteJSON(ctx, providers, llm.StageMessages("事实核对", checkPayload), llm.StageSchema("正文"), validateDraft)
	trace := appendStage(nil, "factcheck", completion, err)
	if err != nil {
		return draft, 0, trace
	}
	return checked, completion.PromptTokens + completion.CompletionTokens, trace
}

func fitBody(ctx context.Context, gateway *llm.Gateway, providers []llm.Provider, payload, draft map[string]any, target int) (map[string]any, int, []map[string]any, bool) {
	if target <= 0 {
		return draft, 0, nil, false
	}
	low := target * 85 / 100
	floor := target * 95 / 100
	high := target * 130 / 100
	goal := target * 105 / 100
	var trace []map[string]any
	tokens := 0
	stillShort := false
	if content.BodyRunes(draft) < floor {
		for pass := 0; pass < 3 && content.BodyRunes(draft) < floor; pass++ {
			before := content.BodyRunes(draft)
			shortfall := goal - before
			if shortfall < 1 {
				shortfall = 1
			}
			next, added, expandTrace := expandBody(ctx, gateway, providers, payload, draft, goal, shortfall, 0, nil)
			tokens += added
			trace = append(trace, expandTrace...)
			if content.BodyRunes(next) > before {
				draft = next
			}
		}
		stillShort = content.BodyRunes(draft) < low
	}
	if content.BodyRunes(draft) > high {
		before := content.BodyRunes(draft)
		condensePayload := cloneMap(payload)
		condensePayload["draft"] = draft
		condensePayload["current_chars"] = before
		condensed, completion, err := gateway.CompleteJSON(ctx, providers, llm.StageMessages("缩写", condensePayload), llm.StageSchema("正文"), validateDraft)
		trace = appendStage(trace, "condense", completion, err)
		if err == nil && condensed != nil {
			tokens += completion.PromptTokens + completion.CompletionTokens
			if kept, ok := keepCondensed(draft, condensed, target); ok {
				draft = kept
			}
		}
	}
	return draft, tokens, trace, stillShort
}

func expandBody(ctx context.Context, gateway *llm.Gateway, providers []llm.Provider, payload, draft map[string]any, goal, shortfall, tokens int, trace []map[string]any) (map[string]any, int, []map[string]any) {
	expandPayload := cloneMap(payload)
	expandPayload["draft"] = draft
	expandPayload["current_chars"] = content.BodyRunes(draft)
	expandPayload["expand_goal"] = goal
	if shortfall > 0 {
		expandPayload["shortfall"] = shortfall
	}
	expanded, completion, err := gateway.CompleteJSON(ctx, providers, llm.StageMessages("扩写", expandPayload), llm.StageSchema("正文"), validateDraft)
	trace = appendStage(trace, "expand", completion, err)
	if err != nil {
		return draft, tokens, trace
	}
	return expanded, tokens + completion.PromptTokens + completion.CompletionTokens, trace
}

func loadSourcePolicy(ctx context.Context, st *store.Store, q db.Q) (content.SourcePolicy, error) {
	row, err := st.FindService(ctx, q, "audit", "source_domains")
	if err != nil {
		return content.SourcePolicy{}, err
	}
	if row == nil {
		return content.DefaultSourcePolicy(), nil
	}
	return content.PolicyFromParams(row.ExtraParams, true), nil
}

func rememberColumn(ctx context.Context, deps Deps, w *work, topic topics.Choice) {
	if w == nil || deps.Store == nil || strings.TrimSpace(topic.Column) == "" {
		return
	}
	_ = deps.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		article, err := deps.Store.GetArticleByTask(ctx, tx, w.TaskID)
		if err != nil || article == nil {
			return err
		}
		article.ColumnType = topic.Column
		article.Subtopic = topic.Subtopic
		article.TopicTag = topic.TopicTag
		return deps.Store.UpdateArticle(ctx, tx, article)
	})
}

func stageFail(trace []map[string]any, stage string, completion llm.Completion, err error) (Built, error) {
	return Built{Trace: appendStage(trace, stage, completion, err)}, err
}

func appendStage(trace []map[string]any, stage string, completion llm.Completion, err error) []map[string]any {
	if err != nil && len(completion.Attempts) == 0 {
		return append(trace, map[string]any{
			"stage": stage, "provider_id": completion.ProviderID, "provider_name": completion.ProviderName,
			"protocol": completion.Protocol, "model": completion.Model, "ok": false, "error": clip(err.Error(), 300),
		})
	}
	return append(trace, traceRows(stage, completion)...)
}

func traceOf(err error) []map[string]any {
	var traced *tracedError
	if errors.As(err, &traced) && traced != nil {
		return traced.trace
	}
	return nil
}

func filterListed(sources []any, policy content.SourcePolicy) []any {
	maps := make([]map[string]any, 0, len(sources))
	for _, raw := range sources {
		source, _ := raw.(map[string]any)
		if source != nil {
			maps = append(maps, source)
		}
	}
	filtered := policy.Filter(maps)
	out := make([]any, len(filtered))
	for i := range filtered {
		out[i] = filtered[i]
	}
	return out
}

func mergeSources(existing, extra []any) []any {
	index := map[string]int{}
	for i, raw := range existing {
		source, _ := raw.(map[string]any)
		if source == nil {
			continue
		}
		if url := str(source["url"]); url != "" {
			index[url] = i
		}
	}
	for _, raw := range extra {
		source, _ := raw.(map[string]any)
		if source == nil {
			continue
		}
		url := str(source["url"])
		if url != "" {
			if i, ok := index[url]; ok {
				prev, _ := existing[i].(map[string]any)
				if prev != nil && str(prev["note"]) == "" && str(source["note"]) != "" {
					prev["note"] = source["note"]
				}
				continue
			}
		}
		existing = append(existing, source)
		if url != "" {
			index[url] = len(existing) - 1
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
		return []map[string]any{{
			"stage": stage, "provider_id": completion.ProviderID, "provider_name": completion.ProviderName,
			"protocol": completion.Protocol, "model": completion.Model, "ok": true,
		}}
	}
	out := make([]map[string]any, 0, len(completion.Attempts))
	chars := utf8.RuneCountInString(completion.Content)
	for _, attempt := range completion.Attempts {
		row := map[string]any{"stage": stage, "chars": chars}
		for k, v := range attempt {
			row[k] = v
		}
		if _, ok := row["chars"]; !ok {
			row["chars"] = chars
		}
		out = append(out, row)
	}
	return out
}

func stageCharList(trace []map[string]any) []any {
	var out []any
	for _, row := range trace {
		out = append(out, map[string]any{"stage": row["stage"], "chars": row["chars"]})
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
		pointOut := map[string]any{"heading": clip(heading, 40), "intent": clip(str(point["intent"]), 120)}
		if need, ok := point["need_table"].(bool); ok && need {
			pointOut["need_table"] = true
		}
		cleaned = append(cleaned, pointOut)
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
		row := map[string]any{"heading": clip(heading, 40), "paragraphs": paragraphs, "list": items}
		if table := normalizeTable(section["table"]); table != nil {
			row["table"] = table
		}
		normalized = append(normalized, row)
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
		if name == "" && sourceTitle == "" && url == "" {
			continue
		}
		row := map[string]any{"name": clip(name, 80), "title": clip(sourceTitle, 120), "published_on": clip(str(source["published_on"]), 32), "url": clip(url, 300), "source_type": clip(strOr(source["source_type"], "other"), 32)}
		if note := strings.TrimSpace(str(source["note"])); note != "" {
			row["note"] = clip(note, 200)
		}
		sources = append(sources, row)
	}
	alts := []any{}
	for _, item := range asList(data["alt_titles"]) {
		if len(alts) >= 12 {
			break
		}
		switch row := item.(type) {
		case string:
			if text := strings.TrimSpace(row); text != "" {
				alts = append(alts, clip(text, 64))
			}
		case map[string]any:
			text := strings.TrimSpace(str(row["title"]))
			if text == "" {
				continue
			}
			alt := map[string]any{"title": clip(text, 64)}
			if _, ok := row["hook"]; ok {
				alt["hook"] = intOf(row["hook"])
				alt["concrete"] = intOf(row["concrete"])
				alt["consistent"] = intOf(row["consistent"])
				alt["persona"] = intOf(row["persona"])
			}
			alts = append(alts, alt)
		}
	}
	out := map[string]any{"title": clip(title, 64), "alt_titles": alts, "summary": clip(str(data["summary"]), 120), "sections": normalized, "closing": clip(str(data["closing"]), 300), "sources": sources}
	if quality, ok := data["_quality"].(map[string]any); ok {
		out["_quality"] = quality
	}
	if quality, ok := data["quality"].(map[string]any); ok && out["_quality"] == nil {
		out["_quality"] = quality
	}
	return out, nil
}

func validateReview(data map[string]any) (map[string]any, error) {
	draft, err := validateDraft(data)
	if err != nil {
		return nil, err
	}
	if quality, ok := data["quality"].(map[string]any); ok {
		draft["_quality"] = map[string]any{
			"hook": intOf(quality["hook"]), "headings": quality["headings"] == true,
			"closing": quality["closing"] == true, "motif": quality["motif"] == true,
		}
	}
	return draft, nil
}

func validateChoice(data map[string]any) (map[string]any, error) {
	choice := strings.TrimSpace(str(data["choice"]))
	if choice != "original" && choice != "rewrite" {
		return nil, errStr("比较结果无效")
	}
	return map[string]any{"choice": choice}, nil
}

func validateWinner(data map[string]any) (map[string]any, error) {
	if data == nil {
		return nil, errStr("没有选出标题")
	}
	winner := strings.TrimSpace(str(data["winner"]))
	if winner == "" {
		return nil, errStr("没有选出标题")
	}
	return map[string]any{"winner": winner}, nil
}

func validatePolish(data map[string]any) (map[string]any, error) {
	if data == nil {
		return nil, errStr("润色结果为空")
	}
	var headings []any
	for _, item := range asList(data["headings"]) {
		if text := strings.TrimSpace(str(item)); text != "" {
			headings = append(headings, text)
		}
	}
	if headings == nil {
		headings = []any{}
	}
	return map[string]any{
		"hook":     strings.TrimSpace(str(data["hook"])),
		"headings": headings,
		"closing":  strings.TrimSpace(str(data["closing"])),
	}, nil
}

func validateTitles(data map[string]any) (map[string]any, error) {
	var titles []any
	for _, item := range asList(data["titles"]) {
		if len(titles) >= 12 {
			break
		}
		switch row := item.(type) {
		case string:
			if text := strings.TrimSpace(row); text != "" {
				titles = append(titles, map[string]any{"title": text})
			}
		case map[string]any:
			text := strings.TrimSpace(str(row["title"]))
			if text == "" {
				text = strings.TrimSpace(str(row["text"]))
			}
			if text == "" {
				continue
			}
			titles = append(titles, map[string]any{
				"title": text, "hook": intOf(row["hook"]), "concrete": intOf(row["concrete"]),
				"consistent": intOf(row["consistent"]), "persona": intOf(row["persona"]), "scored": true,
			})
		}
	}
	if len(titles) == 0 {
		return nil, errStr("没有可用标题")
	}
	return map[string]any{"titles": titles}, nil
}

type strErr string

func (e strErr) Error() string { return string(e) }
func errStr(s string) error    { return strErr(s) }

func normalizeTable(raw any) map[string]any {
	table, _ := raw.(map[string]any)
	if table == nil {
		return nil
	}
	var headers []any
	for _, item := range asList(table["headers"]) {
		text := strings.TrimSpace(str(item))
		if text == "" {
			continue
		}
		if len(headers) >= 4 {
			break
		}
		headers = append(headers, clip(text, 20))
	}
	if len(headers) < 2 {
		return nil
	}
	var rows []any
	for _, rawRow := range asList(table["rows"]) {
		if len(rows) >= 8 {
			break
		}
		cells, _ := rawRow.([]any)
		if cells == nil {
			continue
		}
		line := make([]any, len(headers))
		empty := true
		for i := range headers {
			text := ""
			if i < len(cells) {
				text = strings.TrimSpace(str(cells[i]))
			}
			if text != "" {
				empty = false
			}
			line[i] = clip(text, 40)
		}
		if !empty {
			rows = append(rows, line)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return map[string]any{"headers": headers, "rows": rows}
}

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
	case int64:
		return int(n)
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
