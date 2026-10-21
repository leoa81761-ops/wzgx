// Package gen is the writing pipeline: topic -> research -> one streamed full write ->
// optional self-review (structured JSON) -> hard gates -> render. 2–4 model calls per article.
// The program never edits the model's text; gates only block.
package gen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"gonghao/internal/gateway"
	"gonghao/internal/render"
	"gonghao/internal/search"
	"gonghao/internal/stats"
	"gonghao/internal/style"
)

type Chatter interface {
	Chat(ctx context.Context, cfgs []gateway.Config, req gateway.Request, budget time.Duration) (gateway.Result, error)
}

type Searcher interface {
	Search(ctx context.Context, key, query string, max int) ([]search.Hit, error)
}

type Pipeline struct {
	DB              *pgxpool.Pool
	GW              Chatter
	Box             gateway.Opener
	Tavily          Searcher // nil disables user Tavily
	DefaultImageURL string
	Budget          time.Duration // whole article, default 20 min
	Now             func() time.Time
	Log             *slog.Logger
}

type Job struct {
	ID, UserID, AccountID int64
	ColumnID              *int64
	TopicHint             string
	DryRun                bool
	// Topic is fixed (given by the user, or a proposal the user approved): no topic stage, no scan.
	Topic     string
	TopicKind string     // news | practical, from an approved proposal; "" for a user-given topic
	Seed      []Evidence // the scan's events behind an approved news proposal
	Rejected  []string   // topics the user turned down for this job
	Confirm   bool       // stop after the topic stage and return a Proposal for the user to decide
}

// Proposal is the topic a confirm-mode job waits on.
type Proposal struct {
	Topic       string     `json:"topic"`
	Angle       string     `json:"angle,omitempty"`
	SearchQuery string     `json:"search_query,omitempty"`
	Kind        string     `json:"kind,omitempty"`
	ColumnID    int64      `json:"column_id"` // the column picked for this run, kept when the job resumes
	Events      []Evidence `json:"events,omitempty"`
}

type Evidence struct {
	N         int    `json:"n"`
	Title     string `json:"title"`
	URL       string `json:"url,omitempty"`
	Snippet   string `json:"snippet,omitempty"`
	Date      string `json:"date,omitempty"`
	Kind      string `json:"kind"`
	Issuer    string `json:"issuer,omitempty"`
	DocNumber string `json:"doc_number,omitempty"`
	From      string `json:"from"`
}

var stdRe = regexp.MustCompile(`(?i)\bGB(/T|/Z)?\s?\d{3,6}`)
var govDocRe = regexp.MustCompile(`[〔\[]\d{4}[〕\]]\s*\d+\s*号|通知|办法|规定|意见|决定|条例|公告`)

// Official: national standards and government / ministry red-header documents only.
func (e Evidence) Official() bool { return e.Kind == "national_standard" || e.Kind == "gov_document" }

// classify trusts neither the model nor the site alone: a standard needs a GB number; a government
// document needs a gov.cn page or a 〔year〕N号 document number, and a document-like title.
func classify(e *Evidence) {
	switch {
	case stdRe.MatchString(e.DocNumber) || stdRe.MatchString(e.Title):
		e.Kind = "national_standard"
	case (govHost(e.URL) || docNoRe.MatchString(e.DocNumber)) && govDocRe.MatchString(e.Title+" "+e.DocNumber):
		e.Kind = "gov_document"
	default:
		e.Kind = "other"
	}
}

var docNoRe = regexp.MustCompile(`[〔\[]\d{4}[〕\]]\s*\d+\s*号`)

func govHost(u string) bool {
	p, err := url.Parse(u)
	return err == nil && (strings.HasSuffix(p.Hostname(), ".gov.cn") || p.Hostname() == "gov.cn")
}

// junk sources: video/social/encyclopedia/download sites
var junkHosts = []string{"douyin.com", "tiktok.com", "facebook.com", "x.com", "twitter.com", "baike.baidu.com", "wikipedia.org",
	"bilibili.com", "youtube.com", "kuaishou.com", "weibo.com", "zhihu.com/zvideo", "v.qq.com", "iqiyi.com", "youku.com"}

func junkURL(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return u != ""
	}
	h := strings.TrimPrefix(p.Hostname(), "www.")
	for _, j := range junkHosts {
		if h == j || strings.HasSuffix(h, "."+j) || strings.HasPrefix(h+p.Path, j) {
			return true
		}
	}
	return false
}

type Step struct {
	Stage    string            `json:"stage"`
	Note     string            `json:"note,omitempty"`
	Attempts []gateway.Attempt `json:"attempts,omitempty"`
}

type Result struct {
	ArticleID   int64
	Status      string // ready | blocked
	BlockReason string
	Trace       []Step
	Proposal    *Proposal // set when the job stops for topic confirmation
	Layout      string
	AIImages    bool // the article was rendered with generated pictures
	FactCheck   *FactCheck
}

var ErrNoColumn = errors.New("这个公众号还没有可用的栏目")

func (p *Pipeline) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Run generates one article for the job and stores it (never pushes; delivery is separate).
func (p *Pipeline) Run(ctx context.Context, j Job) (Result, error) {
	budget := p.Budget
	if budget <= 0 {
		budget = 20 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	left := func() time.Duration { d, _ := ctx.Deadline(); return time.Until(d) }
	var res Result
	trace := func(s Step) { res.Trace = append(res.Trace, s) }

	wc, tier, colID, selfReview, err := p.load(ctx, j)
	agent := wc.Agent
	if err != nil {
		return res, err
	}
	if wc.Rotation != "" {
		trace(Step{Stage: "column", Note: wc.Rotation})
	}
	load := func(stage string) ([]gateway.Config, error) { return gateway.LoadStage(ctx, p.DB, p.Box, stage, tier) }
	chat := func(stage string, cfgs []gateway.Config, sys, user string) (gateway.Result, error) {
		// Search models answer from live search; JSON mode makes them skip searching, so research
		// and scan ask for plain-text lines (see ParseFacts) without JSON mode.
		r, err := p.GW.Chat(ctx, cfgs, gateway.Request{Stage: stage, JobID: j.ID, JSON: stage != "research" && stage != "scan",
			Messages: []gateway.Message{{Role: "system", Content: sys}, {Role: "user", Content: user}}}, left())
		var fe *gateway.FailedError
		if errors.As(err, &fe) {
			r.Attempts = fe.Attempts
		}
		trace(Step{Stage: stage, Attempts: r.Attempts})
		return r, err
	}

	// ⓪ for news-type columns, look for recent concrete events first so the topic is a real one.
	// If none turn up, the topic falls back to a practical non-news one instead of inventing news.
	topicKind := ""
	var scanEvents []Evidence // become facts for a news topic
	fixed := strings.TrimSpace(j.Topic) != ""
	if fixed {
		topicKind, scanEvents = j.TopicKind, j.Seed
		if topicKind == "news" {
			for _, e := range scanEvents {
				wc.Recentnews = joinNonEmpty("\n", wc.Recentnews, "- "+e.Snippet)
			}
		}
		trace(Step{Stage: "topic", Note: "fixed topic: " + j.Topic})
	} else if wc.NeedsNews {
		topicKind = "practical"
		if scfgs, err := load("research"); err == nil {
			// the sharp query first; if it brings back no event, one short query (month + keywords + 新闻)
			for _, q := range scanQueries(wc, p.now()) {
				r, err := chat("scan", scfgs, scanSystem, q+"\n今天："+wc.Today)
				if err != nil {
					continue
				}
				events := ParseFacts(r.Text)
				var lines []string
				for _, e := range events {
					if e.Title == "检索摘要" { // unstructured prose is not a concrete event
						continue
					}
					l := "- " + e.Snippet
					if src := joinNonEmpty("｜", e.Title, e.DocNumber, e.Date); src != "" && !strings.HasPrefix(e.Snippet, e.Title) {
						l += "（" + src + "）"
					} else if e.Date != "" {
						l += "（" + e.Date + "）"
					}
					lines = append(lines, l)
					scanEvents = append(scanEvents, e)
				}
				trace(Step{Stage: "scan", Note: fmt.Sprintf("%d events", len(lines))})
				if len(lines) > 0 {
					wc.Recentnews = strings.Join(lines, "\n")
					break
				}
			}
		}
		if wc.Recentnews != "" {
			topicKind = "news"
		} else {
			trace(Step{Stage: "scan", Note: "no recent event found; practical non-news topic"})
		}
	}

	// ① topic, ② research. A news topic with no facts at all is not written: the run falls back
	// to a practical non-news topic.
	var topic struct{ Topic, Angle, SearchQuery string }
	var ev []Evidence
	for round := 0; ; round++ {
		var err error
		if fixed && round == 0 {
			topic.Topic, topic.SearchQuery = strings.TrimSpace(j.Topic), strings.TrimSpace(j.Topic)
			if j.TopicKind != "" && len(j.Seed) == 0 && topicKind == "news" {
				topicKind = "practical"
			}
		} else if topic, err = p.pickTopic(wc, j.TopicHint, j.Rejected, load, chat, trace); err != nil {
			return res, err
		}
		if j.Confirm && !fixed {
			pr := &Proposal{Topic: topic.Topic, Angle: topic.Angle, SearchQuery: topic.SearchQuery, Kind: topicKind, ColumnID: colID}
			if topicKind == "news" {
				pr.Events = scanEvents
			}
			res.Proposal = pr
			trace(Step{Stage: "topic", Note: "waiting for the user to confirm the topic"})
			return res, nil
		}
		wc.Practical = topicKind == "practical"
		ev = nil
		var seed []Evidence
		if topicKind == "news" {
			seed = scanEvents // the scan's event lines (source, title, date) count as facts
		}
		if wc.Research {
			ev = p.research(ctx, j, topic.Topic, topic.SearchQuery, seed, load, chat, trace)
		} else {
			ev = p.research(ctx, j, "", "", seed, nil, nil, trace)
			trace(Step{Stage: "research", Note: "off for this article type"})
		}
		if topicKind != "news" || len(ev) > 0 || round > 0 {
			break
		}
		if fixed { // the user chose this topic: keep it, write it as a practical piece
			topicKind, wc.Recentnews, scanEvents = "practical", "", nil
			wc.Practical = true
			break
		}
		trace(Step{Stage: "research", Note: "news topic with no facts; practical non-news topic instead"})
		topicKind, wc.Recentnews, scanEvents = "practical", "", nil
	}

	// ③ write
	wcfgs, err := load("write")
	if err != nil {
		return res, fmt.Errorf("写作：%w", err)
	}
	brief := writeUser(wc, topic.Topic, topic.Angle, ev)
	// agent mode ③a: plan first (reader pain, angle, takeaway, outline). A failed plan just means no plan.
	if agent {
		if pr, err := chat("plan", wcfgs, planSystem, brief); err == nil {
			if plan := planText(pr.Text); plan != "" {
				brief += "\n写作计划（你自己先做的，照着写，发现更好的写法可以调整）：\n" + plan + "\n"
				trace(Step{Stage: "plan", Note: "ok"})
			} else {
				trace(Step{Stage: "plan", Note: "plan reply not structured, writing without it"})
			}
		} else {
			trace(Step{Stage: "plan", Note: "plan failed, writing without it"})
		}
	}
	wr, err := chat("write", wcfgs, writeSystem, brief)
	if err != nil {
		return res, fmt.Errorf("写作：%w", err)
	}
	art, perr := parseArticle(wr.Text)
	block := ""
	if perr != nil {
		block = "空：模型没有返回可解析的文章"
		trace(Step{Stage: "write", Note: rawNote("write reply not readable", wr.Text)})
	} else {
		block = Gate(art, wr.FinishReason, p.sensitive(ctx))
	}

	// agent mode ③b: compare the draft with the user's own settings and excerpts, revise once.
	// The revision replaces the draft only if it is a complete article that passes the gates.
	if block == "" && agent {
		func() {
			rr, err := chat("revise", wcfgs, reviseSystem, brief+"\n初稿：\n"+articleMarkdown(art))
			if err != nil {
				trace(Step{Stage: "revise", Note: "revise failed, keeping draft"})
			} else if gaps, head, body := splitNotes(rr.Text); body == "" && (strings.Contains(head, "无差距") || strings.HasPrefix(strings.TrimSpace(head), "{")) {
				var rv reviseReply
				if strings.HasPrefix(strings.TrimSpace(head), "{") && parseRevise(head, &rv) == nil && len(rv.Article.Blocks) > 0 && Gate(rv.Article, rr.FinishReason, p.sensitive(ctx)) == "" {
					art = rv.Article
					trace(Step{Stage: "revise", Note: "revised: " + strings.Join(rv.Gaps, "；")})
					return
				}
				trace(Step{Stage: "revise", Note: "no gaps"})
			} else if ra, err := parseArticle(body); body == "" || err != nil {
				trace(Step{Stage: "revise", Note: rawNote("revise reply not structured, keeping draft", rr.Text)})
			} else if why := Gate(ra, rr.FinishReason, p.sensitive(ctx)); why == "" {
				art = ra
				trace(Step{Stage: "revise", Note: "revised: " + strings.Join(gaps, "；")})
			} else {
				trace(Step{Stage: "revise", Note: "revision rejected by gates (" + why + "), keeping draft"})
			}
		}()
	}

	// ④ optional self-review: a revision replaces the draft only if it is a complete article that passes the gates
	if block == "" && selfReview {
		if rcfgs, err := load("review"); err == nil {
			rr, err := chat("review", rcfgs, reviewSystem, wc.settings()+"\n文章：\n"+articleMarkdown(art))
			if err != nil {
				trace(Step{Stage: "review", Note: "review failed, keeping draft"})
			} else if probs, head, body := splitNotes(rr.Text); body == "" && ((strings.Contains(head, "通过") && !strings.Contains(head, "需修改")) || jsonPass(head)) {
				trace(Step{Stage: "review", Note: "pass"})
			} else if ra, err := parseArticle(body); body == "" || err != nil {
				trace(Step{Stage: "review", Note: rawNote("review reply not structured, keeping draft; problems: "+strings.Join(probs, "；"), rr.Text)})
			} else if why := Gate(ra, rr.FinishReason, p.sensitive(ctx)); why == "" {
				art = ra
				trace(Step{Stage: "review", Note: "revised: " + strings.Join(probs, "；")})
			} else {
				trace(Step{Stage: "review", Note: "revision rejected by gates (" + why + "), keeping draft; problems: " + strings.Join(probs, "；")})
			}
		}
	}

	// ⑥ render (official sources only are cited)
	srcs := map[int]render.Source{}
	for _, e := range ev {
		if e.Official() {
			srcs[e.N] = render.Source{N: e.N, Kind: e.Kind, Title: e.Title, Issuer: e.Issuer, DocNumber: e.DocNumber, Date: e.Date, URL: e.URL}
		}
	}
	// ⑤ optional fact check: flags only, for the user; never blocks or rewrites
	if wc.FactCheck {
		fc := CheckFacts(art, ev)
		res.FactCheck = &fc
		trace(Step{Stage: "fact_check", Note: fmt.Sprintf("%d claims, %d without support in the research", len(fc.Claims), fc.Unsupported)})
	}

	// ⑤′ pictures (only for an article that passed; never blocks it)
	var pics, charts map[int]string
	if block == "" {
		pics, res.AIImages = p.pictures(ctx, j, tier, art, load, chat, trace)
		charts = p.charts(ctx, j, art, trace) // the system decides; a failed chart is skipped
		if pics[0] == "" {
			if k := p.refCover(ctx, j, trace); k != "" {
				if pics == nil {
					pics = map[int]string{}
				}
				pics[0] = k
			}
		}
	}
	prof := wc.Profile
	out := render.Render(art, render.Options{Opening: prof["开头"], Ending: prof["结尾"], Signature: prof["签名"],
		AILabel: prof["AI标识"] == "on", DefaultImageURL: p.DefaultImageURL, Sources: srcs, Layout: wc.Layout,
		AIImages: res.AIImages, Images: mediaURLs(pics), Charts: chartURLs(charts)})
	res.Layout = render.LayoutByKey(wc.Layout).Key

	res.Status, res.BlockReason = "ready", block
	if block != "" {
		res.Status = "blocked"
	}
	keys := map[string]bool{}
	for _, m := range []map[int]string{pics, charts} {
		for _, k := range m {
			keys[k] = true
		}
	}
	res.ArticleID, err = p.save(ctx, j, colID, topic.Topic, topicKind, art, out, res, keys)
	return res, err
}

func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// badTopic rejects names-only or vague topics and repeats.
func badTopic(t string, w writingContext) string {
	n := norm(t)
	if len([]rune(n)) < 6 {
		return "太短，不是具体题目"
	}
	for _, name := range []string{w.AccountName, w.ColumnName, w.TypeName, w.Profile["账号定位"]} {
		nn := norm(name)
		if nn != "" && (n == nn || strings.Contains(nn, n)) {
			return "只是账号或栏目名"
		}
		if nn != "" && strings.HasPrefix(n, nn) && len([]rune(n))-len([]rune(nn)) < 4 {
			return "只是账号或栏目名"
		}
	}
	for _, r := range w.Recent {
		if norm(r) == n {
			return "和最近的题目重复"
		}
	}
	return ""
}

func (p *Pipeline) research(ctx context.Context, j Job, topic, query string, seed []Evidence,
	load func(string) ([]gateway.Config, error),
	chat func(string, []gateway.Config, string, string) (gateway.Result, error),
	trace func(Step)) []Evidence {
	var ev []Evidence
	seen := map[string]bool{}
	add := func(e Evidence) {
		e.Title, e.URL = strings.TrimSpace(e.Title), strings.TrimSpace(e.URL)
		if e.Title == "" && e.URL == "" || junkURL(e.URL) || (e.URL != "" && seen[e.URL]) || len(ev) >= 10 {
			return
		}
		if e.Title == "" {
			e.Title = e.URL
		}
		seen[e.URL] = true
		classify(&e)
		e.N = len(ev) + 1
		ev = append(ev, e)
	}
	for _, e := range seed {
		add(e)
	}
	if load == nil { // research off: only the seed
		return ev
	}
	if cfgs, err := load("research"); err == nil {
		// one normal search, then one shorter rescue search if nothing usable came back
		for _, q := range []string{query, topic} {
			r, err := chat("research", cfgs, researchSystem, "题目："+topic+"\n检索词："+q+"\n今天："+p.now().Format("2006-01-02"))
			if err != nil {
				continue
			}
			facts := ParseFacts(r.Text)
			for _, f := range facts {
				add(f)
			}
			note := fmt.Sprintf("%d facts, %d citations", len(facts), len(r.Citations))
			if len(facts) == 0 {
				rs := []rune(strings.TrimSpace(r.Text))
				note += "; reply: " + string(rs[:min(len(rs), 120)])
			}
			trace(Step{Stage: "research", Note: note})
			if len(facts) > 0 {
				break
			}
		}
	} else {
		trace(Step{Stage: "research", Note: "no search route"})
	}
	if p.Tavily != nil {
		var sealed string
		err := p.DB.QueryRow(ctx, `SELECT cipher FROM user_secret WHERE user_id=$1 AND kind='tavily'`, j.UserID).Scan(&sealed)
		if err == nil {
			if key, err := p.Box.Open(sealed); err == nil {
				hits, err := p.Tavily.Search(ctx, key, query, 5)
				note := fmt.Sprintf("tavily %d hits", len(hits))
				if err != nil {
					note = "tavily failed: " + err.Error()
				}
				trace(Step{Stage: "research", Note: note})
				for _, h := range hits {
					sn := []rune(h.Content)
					if len(sn) > 300 {
						sn = sn[:300]
					}
					add(Evidence{Title: h.Title, URL: h.URL, Snippet: string(sn), Date: h.Date, From: "tavily"})
				}
			}
		}
	}
	return ev
}

func (p *Pipeline) sensitive(ctx context.Context) []string {
	rows, err := p.DB.Query(ctx, `SELECT word FROM sensitive_word`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ws []string
	for rows.Next() {
		var w string
		if rows.Scan(&w) == nil {
			ws = append(ws, w)
		}
	}
	return ws
}

func (p *Pipeline) load(ctx context.Context, j Job) (writingContext, string, int64, bool, error) {
	var w writingContext
	var tier string
	err := p.DB.QueryRow(ctx, `SELECT u.tier, a.name, a.layout FROM wx_account a JOIN users u ON u.id=a.user_id WHERE a.id=$1 AND a.user_id=$2`,
		j.AccountID, j.UserID).Scan(&tier, &w.AccountName, &w.Layout)
	if err != nil {
		return w, "", 0, false, fmt.Errorf("读取公众号：%w", err)
	}
	var pos, persona, aud, tone, taboos, sig, opening, ending string
	var ai, review bool
	err = p.DB.QueryRow(ctx, `SELECT positioning, persona, audience, tone, taboos, signature, opening, ending, ai_label, self_review, fact_check, agent_mode
		FROM account_profile WHERE account_id=$1`, j.AccountID).Scan(&pos, &persona, &aud, &tone, &taboos, &sig, &opening, &ending, &ai, &review, &w.FactCheck, &w.Agent)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return w, "", 0, false, err
	}
	w.Profile = map[string]string{"账号定位": pos, "人设": persona, "读者": aud, "语气": tone, "忌讳": taboos,
		"签名": sig, "开头": opening, "结尾": ending}
	if ai {
		w.Profile["AI标识"] = "on"
	}

	var colID int64
	if j.ColumnID != nil {
		colID = *j.ColumnID
	} else {
		rows, err := p.DB.Query(ctx, `SELECT c.id, c.name, c.weight FROM account_column c JOIN article_type t ON t.id=c.article_type_id
			WHERE c.account_id=$1 AND c.enabled AND t.enabled ORDER BY c.id`, j.AccountID)
		if err != nil {
			return w, "", 0, false, err
		}
		type cw struct {
			id   int64
			name string
			w    float64
		}
		var cs []cw
		for rows.Next() {
			var c cw
			var wt int64
			rows.Scan(&c.id, &c.name, &wt)
			c.w = float64(wt)
			cs = append(cs, c)
		}
		rows.Close()
		if len(cs) == 0 {
			return w, "", 0, false, ErrNoColumn
		}
		// optional (off by default): reads gently weight the rotation; what it did is recorded
		var weighting bool
		p.DB.QueryRow(ctx, `SELECT stats_weighting FROM wx_account WHERE id=$1`, j.AccountID).Scan(&weighting)
		var parts []string
		if weighting && len(cs) > 1 {
			if fs, _, err := stats.Factors(ctx, p.DB, j.AccountID); err == nil {
				by := map[int64]float64{}
				for _, f := range fs {
					by[f.ColumnID] = f.Factor
				}
				for i := range cs {
					if f, ok := by[cs[i].id]; ok && f != 1 {
						parts = append(parts, fmt.Sprintf("「%s」%g×%.1f", cs[i].name, cs[i].w, f))
						cs[i].w *= f
					}
				}
			}
		}
		var total float64
		for _, c := range cs {
			total += c.w
		}
		pick := rand.Float64() * total
		colID = cs[len(cs)-1].id
		for _, c := range cs {
			if pick < c.w {
				colID = c.id
				break
			}
			pick -= c.w
		}
		if weighting && len(cs) > 1 {
			name := ""
			for _, c := range cs {
				if c.id == colID {
					name = c.name
				}
			}
			note := "按阅读数据调整栏目权重：数据还不够，各栏目权重不变"
			if len(parts) > 0 {
				note = "按阅读数据调整栏目权重：" + strings.Join(parts, "，") + "（其余不变）"
			}
			note += "；这次选了「" + name + "」"
			w.Rotation = note
			p.DB.Exec(ctx, `UPDATE wx_account SET rotation_note=$2, rotation_at=now() WHERE id=$1`, j.AccountID, note)
		}
	}
	err = p.DB.QueryRow(ctx, `SELECT c.name, c.note, c.length_hint, t.name, t.description, t.needs_news, t.research
		FROM account_column c JOIN article_type t ON t.id=c.article_type_id WHERE c.id=$1 AND c.account_id=$2`, colID, j.AccountID).
		Scan(&w.ColumnName, &w.ColumnNote, &w.LengthHint, &w.TypeName, &w.TypeDesc, &w.NeedsNews, &w.Research)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, "", 0, false, ErrNoColumn
	}
	if err != nil {
		return w, "", 0, false, err
	}
	rows, err := p.DB.Query(ctx, `SELECT COALESCE(NULLIF(topic,''), title) FROM article WHERE account_id=$1 ORDER BY id DESC LIMIT 20`, j.AccountID)
	if err != nil {
		return w, "", 0, false, err
	}
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil && t != "" {
			w.Recent = append(w.Recent, t)
		}
	}
	rows.Close()
	w.Today = p.now().Format("2006-01-02")
	w.Style, w.Excerpts = style.ForWriter(ctx, p.DB, j.AccountID, colID)
	return w, tier, colID, review, nil
}

func (p *Pipeline) save(ctx context.Context, j Job, colID int64, topic, topicKind string, a render.Article, out render.Output, res Result, pics map[string]bool) (int64, error) {
	body, _ := json.Marshal(a)
	alts := a.AltTitles
	if alts == nil {
		alts = []string{}
	}
	tx, err := p.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var jobID any
	if j.ID > 0 {
		jobID = j.ID
	}
	var fc []byte // NULL = not checked
	if res.FactCheck != nil {
		fc, _ = json.Marshal(res.FactCheck)
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO article (job_id, user_id, account_id, column_id, title, alt_titles, summary, body_json, html, status, block_reason, topic, topic_kind, layout, ai_images, fact_check)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULLIF($13,''),$14,$15,$16) RETURNING id`,
		jobID, j.UserID, j.AccountID, colID, a.Title, alts, a.Summary, body, out.HTML, res.Status, res.BlockReason, topic, topicKind, res.Layout, res.AIImages, fc).Scan(&id)
	if err != nil {
		return 0, err
	}
	if len(pics) > 0 {
		keys := make([]string, 0, len(pics))
		for k := range pics {
			keys = append(keys, k)
		}
		if _, err := tx.Exec(ctx, `UPDATE article_image SET article_id=$1 WHERE key = ANY($2) AND article_id IS NULL`, id, keys); err != nil {
			return 0, err
		}
	}
	for _, s := range out.Cited {
		var d any
		if t, err := time.Parse("2006-01-02", s.Date); err == nil {
			d = t
		}
		if _, err := tx.Exec(ctx, `INSERT INTO article_source (article_id, n, kind, title, issuer, doc_number, published_on, url)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, id, s.N, s.Kind, s.Title, s.Issuer, s.DocNumber, d, s.URL); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit(ctx)
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

// scanQueries builds the news pre-scan requests in Chinese. The first: month and year, the account's
// industry keywords ("关键词：…" in the positioning text, else the whole positioning), and the column
// note. The second, used only if the first finds nothing: month and year, keywords, 新闻.
func scanQueries(w writingContext, now time.Time) []string {
	kw := strings.TrimSpace(w.Profile["账号定位"])
	if _, after, ok := strings.Cut(kw, "关键词"); ok {
		kw = strings.Trim(after, "：: ，,。")
	}
	kw = strings.NewReplacer("、", " ", "，", " ", ",", " ").Replace(kw)
	month := fmt.Sprintf("%d年%d月", now.Year(), int(now.Month()))
	return []string{joinNonEmpty(" ", month, kw, w.ColumnNote), joinNonEmpty(" ", month, kw, "新闻")}
}

func (p *Pipeline) pickTopic(wc writingContext, hint string, prior []string,
	load func(string) ([]gateway.Config, error),
	chat func(string, []gateway.Config, string, string) (gateway.Result, error),
	trace func(Step)) (topic struct{ Topic, Angle, SearchQuery string }, _ error) {
	cfgs, err := load("topic")
	if err != nil {
		return topic, fmt.Errorf("选题：%w", err)
	}
	rejected := []string{}
	for _, t := range prior {
		rejected = append(rejected, t+"（用户没有采用）")
	}
	for try := 0; try < 3; try++ {
		r, err := chat("topic", cfgs, topicSystem, topicUser(wc, hint, rejected))
		if err != nil {
			return topic, fmt.Errorf("选题：%w", err)
		}
		var t struct {
			Topic       string `json:"topic"`
			Angle       string `json:"angle"`
			SearchQuery string `json:"search_query"`
		}
		if decodeJSON(r.Text, &t) != nil {
			rejected = append(rejected, "（上次回复不是 JSON）")
			continue
		}
		if why := badTopic(t.Topic, wc); why != "" {
			rejected = append(rejected, t.Topic+"（"+why+"）")
			trace(Step{Stage: "topic", Note: "rejected: " + t.Topic + " — " + why})
			continue
		}
		topic.Topic, topic.Angle, topic.SearchQuery = strings.TrimSpace(t.Topic), t.Angle, t.SearchQuery
		break
	}
	if topic.Topic == "" {
		return topic, errors.New("选题：三次都没有选出具体题目")
	}
	if topic.SearchQuery == "" {
		topic.SearchQuery = topic.Topic
	}

	return topic, nil
}
