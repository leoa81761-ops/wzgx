package pipeline

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/chart"
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
}

type Built struct {
	Draft          map[string]any
	HTML           string
	Topic          topics.Choice
	Tokens         int
	Trace          []map[string]any
	MissingSources bool
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
		finishFailure(ctx, deps, taskID, clip(err.Error(), 500), now, authFailed(err), traceOf(err))
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
		titles, err := deps.Store.RecentTitles(ctx, tx, plan.ID, 20)
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
		for _, title := range siblingTitles {
			if title == "" || seenTitle[title] {
				continue
			}
			seenTitle[title] = true
			titles = append(titles, title)
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
			History: history, RecentTitles: titles, Vetoed: vetoed, SpecifiedTitle: pinned, Pack: pack,
			StyleName: styleName, StyleHint: styleHint, Weights: weights,
			Audience: voice.Audience, Keywords: strings.TrimSpace(extra.Keywords),
			BannedWords: strings.TrimSpace(extra.BannedWords), TopicMode: strings.TrimSpace(extra.TopicMode),
			ImageMode: strings.TrimSpace(extra.ImageMode), Tone: voice.Tone,
			PersonaColumns: voice.Columns, Signatures: voice.Signatures,
			Taboos: voice.Taboos, TitlePrefer: strings.TrimSpace(prefer),
		}
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
		sensitive = mergeBanned(sensitive, w.Taboos)
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
		return nil, &tracedError{err: err, trace: built.Trace}
	}
	if content.CopiedSpan(content.DraftText(built.Draft), content.SourceTexts(built.Draft)) {
		return nil, &llm.Error{Message: "正文与参考来源有整段相同的表述"}
	}
	if deps.Gateway != nil && !deps.Gateway.Mock {
		content.AnnotateAccess(ctx, deps.HTTP, built.Draft)
	}
	push, err := pushDraft(ctx, deps, w, built, wx, image)
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
	if built.MissingSources {
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
	return Input{Direction: w.Direction, StyleKey: w.StyleKey, TemplateKey: w.TemplateKey, WordTarget: w.WordTarget, Tier: w.Tier, History: w.History, RecentTitles: w.RecentTitles, Locked: w.Locked, Vetoed: w.Vetoed, SpecifiedTitle: w.SpecifiedTitle, Pack: w.Pack, StyleName: w.StyleName, StyleHint: w.StyleHint, Weights: w.Weights, Opening: w.Opening, Ending: w.Ending, ColumnName: w.ColumnName, ColumnStyle: w.ColumnStyle, AssetNote: w.AssetNote, PromptExtra: w.PromptExtra, Audience: w.Audience, Keywords: w.Keywords, BannedWords: w.BannedWords, TopicMode: w.TopicMode, ImageMode: w.ImageMode, Tone: w.Tone, PersonaColumns: w.PersonaColumns, Signatures: w.Signatures, Taboos: w.Taboos, TitlePrefer: w.TitlePrefer}
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

func voiceFields(in Input) map[string]any {
	out := map[string]any{}
	if text := strings.TrimSpace(in.Audience); text != "" {
		out["audience"] = text
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

func finishPiece(ctx context.Context, gateway *llm.Gateway, providers map[string][]llm.Provider, draft map[string]any, w Input, fallbackTitle string, base map[string]any) (map[string]any, int, []map[string]any) {
	if draft == nil {
		return draft, 0, nil
	}
	rewritten, tokens, trace := rewriteWeak(ctx, gateway, providers, draft, base)
	draft = rewritten
	if w.WordTarget > 0 && content.BodyRunes(draft) > w.WordTarget*130/100 {
		shortened, condenseTokens, condenseTrace := condenseOnce(ctx, gateway, providers, draft, base)
		draft = shortened
		tokens += condenseTokens
		trace = append(trace, condenseTrace...)
	}
	ranked, titleTokens, titleTrace := rankTitles(ctx, gateway, providers, draft, w, fallbackTitle, base)
	return ranked, tokens + titleTokens, append(trace, titleTrace...)
}

func rewriteWeak(ctx context.Context, gateway *llm.Gateway, providers map[string][]llm.Provider, draft, base map[string]any) (map[string]any, int, []map[string]any) {
	need := editorial.NeedsRewrite(draft)
	if !need.Any() {
		return draft, 0, nil
	}
	payload := cloneMap(base)
	payload["draft"] = draft
	payload["rewrite_hook"] = need.Hook
	payload["rewrite_headings"] = need.Headings
	payload["rewrite_closing"] = need.Closing
	if need.Hook {
		payload["current_hook"] = draftHook(draft)
	}
	if need.Headings {
		payload["current_headings"] = draftHeadings(draft)
	}
	if need.Closing {
		payload["current_closing"] = strings.TrimSpace(str(draft["closing"]))
	}
	model, completion, err := gateway.CompleteJSON(ctx, pieceProviders(providers), llm.StageMessages("结构润色", payload), llm.StageSchema("结构润色"), validatePolish)
	trace := appendStage(nil, "polish", completion, err)
	if err != nil {
		return draft, tokensOf(completion), trace
	}
	return editorial.Apply(draft, model, nil), tokensOf(completion), trace
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
	if next := content.BodyRunes(condensed); next == 0 || next >= before {
		return draft, tokensOf(completion), trace
	}
	return condensed, tokensOf(completion), trace
}

func rankTitles(ctx context.Context, gateway *llm.Gateway, providers map[string][]llm.Provider, draft map[string]any, w Input, fallbackTitle string, base map[string]any) (map[string]any, int, []map[string]any) {
	payload := cloneMap(base)
	payload["draft_title"] = strings.TrimSpace(str(draft["title"]))
	payload["alt_titles"] = asList(draft["alt_titles"])
	model, completion, err := gateway.CompleteJSON(ctx, pieceProviders(providers), llm.StageMessages("标题", payload), llm.StageSchema("标题"), validateTitles)
	trace := appendStage(nil, "titles", completion, err)
	var candidates []string
	if err == nil && model != nil {
		candidates = append(candidates, stringList(asList(model["titles"]))...)
	}
	if title := strings.TrimSpace(str(draft["title"])); title != "" {
		candidates = append(candidates, title)
	}
	candidates = append(candidates, stringList(asList(draft["alt_titles"]))...)
	best, alts := headline.Rank(candidates, splitWords(w.Pack.RedLines, w.BannedWords, w.Taboos), w.RecentTitles, w.TitlePrefer)
	title, altAny := chosenTitle(best, alts, w.SpecifiedTitle, str(draft["title"]), fallbackTitle)
	draft["title"] = title
	draft["alt_titles"] = altAny
	return draft, tokensOf(completion), trace
}

func chosenTitle(best string, alts []string, specified, current, fallback string) (string, []any) {
	title := strings.TrimSpace(best)
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
	add := func(item string) {
		item = strings.TrimSpace(item)
		if item == "" || item == title || seen[item] {
			return
		}
		seen[item] = true
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

func Build(ctx context.Context, gateway *llm.Gateway, w Input, sensitive []string, providers map[string][]llm.Provider, policy content.SourcePolicy, now time.Time) (Built, error) {
	w = fillPersona(w)
	topic := topics.PickTree(topics.ExpandTree(vertical.Subtopics(w.Pack), w.Direction, w.Tier, w.Weights), w.Direction, w.Tier, w.History, now, w.Vetoed...)
	if w.Locked != nil {
		topic = *w.Locked
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
		"word_target": w.WordTarget, "recent_titles": w.RecentTitles, "tier": w.Tier,
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
	base["structure"] = editorial.Structure(w.Pack.Key, w.Direction)
	var trace []map[string]any
	tokens := 0
	topicData, completion, err := gateway.CompleteJSON(ctx, providers["topic"], llm.StageMessages("选题", base), llm.StageSchema("选题"), validateTopic)
	if err != nil {
		return stageFail(trace, "topic", completion, err)
	}
	tokens += completion.PromptTokens + completion.CompletionTokens
	trace = append(trace, traceRows("topic", completion)...)
	topicTitle := clip(str(topicData["title"]), 64)
	if topicTitle == "" {
		topicTitle = clip(topic.Subtopic+"："+topic.Angle, 64)
	}
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
	}
	outlinePayload := cloneMap(base)
	outlinePayload["topic_title"] = topicTitle
	outline, completion, err := gateway.CompleteJSON(ctx, providers["outline"], llm.StageMessages("大纲", outlinePayload), llm.StageSchema("大纲"), validateOutline)
	if err != nil {
		return stageFail(trace, "outline", completion, err)
	}
	tokens += completion.PromptTokens + completion.CompletionTokens
	trace = append(trace, traceRows("outline", completion)...)
	draftPayload := cloneMap(outlinePayload)
	draftPayload["outline"] = outline
	draft, completion, err := gateway.CompleteJSON(ctx, providers["draft"], llm.StageMessages("正文", draftPayload), llm.StageSchema("正文"), validateDraft)
	if err != nil {
		return stageFail(trace, "draft", completion, err)
	}
	tokens += completion.PromptTokens + completion.CompletionTokens
	trace = append(trace, traceRows("draft", completion)...)
	fitted, fitTokens, fitTrace := fitBody(ctx, gateway, providers["draft"], draftPayload, draft, w.WordTarget)
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
	reviewed, completion, err := gateway.CompleteJSON(ctx, providers["review"], llm.StageMessages("审核", reviewPayload), llm.StageSchema("审核"), validateDraft)
	if err != nil {
		return stageFail(trace, "review", completion, err)
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
			return stageFail(trace, "review", completion, err)
		}
		tokens += completion.PromptTokens + completion.CompletionTokens
		trace = append(trace, traceRows("review", completion)...)
		cleaned, _ = content.StripCliches(reviewed).(map[string]any)
		draft = cleaned
		hits = content.FindSensitive(draft, sensitive)
		if len(hits) > 0 {
			message := "审核未通过，命中敏感词：" + strings.Join(hits, "、")
			trace = append(trace, map[string]any{"stage": "audit", "ok": false, "error": clip(message, 300)})
			return Built{Trace: trace}, &llm.Error{Message: message}
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
		draft["sources"] = preferListed(filtered, sourcePreferMode(base))
	}
	if w.Tier == "free" {
		draft["sources"] = []any{}
	}
	missingSources := w.Tier != "free" && len(providers["research"]) > 0 && !listedHaveURL(asList(draft["sources"]))
	finished, pieceTokens, pieceTrace := finishPiece(ctx, gateway, providers, draft, w, topicTitle, base)
	draft = finished
	tokens += pieceTokens
	trace = append(trace, pieceTrace...)
	footnotes := []any{"请发布前审阅。"}
	if w.Pack.Disclaimer != "" {
		footnotes = append(footnotes, w.Pack.Disclaimer)
	} else if strings.Contains(w.Direction, "母婴") {
		footnotes = append(footnotes, "本文仅供科普，不能替代医生诊疗意见。")
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
	html, err := render.RenderWithinLimit(draft, w.TemplateKey, false)
	if err != nil {
		return Built{Trace: trace}, err
	}
	return Built{Draft: draft, HTML: html, Topic: topic, Tokens: tokens, Trace: trace, MissingSources: missingSources, Searched: searched}, nil
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
		png, filename, coverTrace = chooseCover(ctx, deps, image, str(article.Draft["title"]), w.Direction, w.TemplateKey, aiCover)
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
		if n := uploadInlineImages(ctx, deps, client, image, article.Draft, inlineN, w.Pack.ImageRules, w.Direction); n > 0 {
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
	if strings.Contains(str(result["delivery_note"]), "无参考来源") {
		emitNotice(ctx, deps, taskUserID(ctx, deps, taskID), "research", "无参考来源", "这篇稿已经在草稿箱，但没有可用的参考来源。发布前请自行核对事实。")
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
	messages := llm.StageMessages("资料检索", payload)
	first, err1 := gateway.CompleteResearch(ctx, providers, messages)
	notes := ""
	var sources []map[string]any
	var dropped []map[string]any
	relaxed := false
	if err1 == nil {
		notes, sources, dropped, relaxed = takeSources(first.Content, first.ResearchSources, policy)
		if sourcesHaveURL(sources) {
			sources = preferMaps(sources, payload)
			stampSourceTrace(&first, sources, dropped, relaxed)
			return notes, anySources(sources), first, nil
		}
	}
	short := cloneMap(payload)
	short["query_mode"] = "short"
	backupModel := ""
	if gateway != nil {
		backupModel = gateway.ResearchBackupModel
	}
	second, err2 := gateway.CompleteResearch(ctx, backupResearchProviders(providers, backupModel), llm.StageMessages("资料检索", short))
	merged := llm.Completion{
		Attempts:         append(append([]map[string]any{}, first.Attempts...), second.Attempts...),
		PromptTokens:     first.PromptTokens + second.PromptTokens,
		CompletionTokens: first.CompletionTokens + second.CompletionTokens,
	}
	if err2 == nil {
		notes2, sources2, dropped2, relaxed2 := takeSources(second.Content, second.ResearchSources, policy)
		dropped = append(dropped, dropped2...)
		merged.ProviderID = second.ProviderID
		merged.ProviderName = second.ProviderName
		merged.Protocol = second.Protocol
		merged.Model = second.Model
		merged.Content = second.Content
		if sourcesHaveURL(sources2) {
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
		sources = preferMaps(sources, payload)
		stampSourceTrace(&merged, sources, dropped, relaxed)
		return notes, anySources(sources), merged, nil
	}
	if err1 != nil {
		return "", nil, merged, err2
	}
	merged.ProviderID = first.ProviderID
	merged.ProviderName = first.ProviderName
	merged.Protocol = first.Protocol
	merged.Model = first.Model
	merged.Content = first.Content
	sources = preferMaps(sources, payload)
	stampSourceTrace(&merged, sources, dropped, relaxed)
	return notes, anySources(sources), merged, nil
}

func backupResearchProviders(providers []llm.Provider, backupModel string) []llm.Provider {
	if len(providers) == 0 {
		return nil
	}
	model := strings.TrimSpace(backupModel)
	if model == "" {
		model = "grok-chat-fast"
	}
	var matched []llm.Provider
	for _, provider := range providers {
		if strings.EqualFold(provider.Model, model) {
			matched = append(matched, provider)
		}
	}
	if len(matched) == 0 && strings.Contains(strings.ToLower(model), "chat-fast") {
		for _, provider := range providers {
			if strings.Contains(strings.ToLower(provider.Model), "chat-fast") {
				matched = append(matched, provider)
			}
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

func takeSources(content string, cited []map[string]any, policy content.SourcePolicy) (string, []map[string]any, []map[string]any, bool) {
	notes, _ := llm.InterpretResearch(content)
	raw := cited
	kept, dropped := policy.Split(raw)
	if len(raw) == 0 {
		dropped = append(dropped, map[string]any{"reason": "没有解析到来源"})
	}
	if sourcesHaveURL(kept) {
		return notes, kept, dropped, false
	}
	softKept, _ := policy.Relaxed().Split(raw)
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

func stampSourceTrace(completion *llm.Completion, sources []map[string]any, dropped []map[string]any, relaxed bool) {
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
	last["kept"] = len(sources)
	last["relaxed"] = relaxed
	if len(dropped) > 0 {
		last["dropped"] = dropped
	}
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

func fitBody(ctx context.Context, gateway *llm.Gateway, providers []llm.Provider, payload, draft map[string]any, target int) (map[string]any, int, []map[string]any) {
	if target <= 0 {
		return draft, 0, nil
	}
	low := target * 85 / 100
	high := target * 130 / 100
	goal := target * 105 / 100
	var trace []map[string]any
	tokens := 0
	if content.BodyRunes(draft) < low {
		expandPayload := cloneMap(payload)
		expandPayload["draft"] = draft
		expandPayload["current_chars"] = content.BodyRunes(draft)
		expandPayload["expand_goal"] = goal
		expanded, completion, err := gateway.CompleteJSON(ctx, providers, llm.StageMessages("扩写", expandPayload), llm.StageSchema("正文"), validateDraft)
		trace = appendStage(trace, "expand", completion, err)
		if err == nil {
			draft = expanded
			tokens += completion.PromptTokens + completion.CompletionTokens
		}
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
			if next := content.BodyRunes(condensed); next > 0 && next < before {
				draft = condensed
			}
		}
	}
	return draft, tokens, trace
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
		if text := strings.TrimSpace(str(item)); text != "" {
			titles = append(titles, text)
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
