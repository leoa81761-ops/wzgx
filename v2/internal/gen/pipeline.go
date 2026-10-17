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
	if err != nil {
		return res, err
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

	// ⓪ for news-type columns, look for recent concrete events first so the topic is a real one
	if wc.NeedsNews {
		if scfgs, err := load("research"); err == nil {
			q := wc.ColumnName + " " + wc.ColumnNote + " " + wc.Profile["账号定位"]
			if r, err := chat("scan", scfgs, scanSystem, "范围："+q+"\n今天："+wc.Today); err == nil {
				wc.Recentnews = strings.TrimSpace(r.Text)
				if len(r.Citations) > 0 {
					wc.Recentnews += "\n来源：" + strings.Join(r.Citations[:min(len(r.Citations), 8)], " ")
				}
			}
		}
	}

	// ① topic
	cfgs, err := load("topic")
	if err != nil {
		return res, fmt.Errorf("选题：%w", err)
	}
	var topic struct{ Topic, Angle, SearchQuery string }
	var rejected []string
	for try := 0; try < 3; try++ {
		r, err := chat("topic", cfgs, topicSystem, topicUser(wc, j.TopicHint, rejected))
		if err != nil {
			return res, fmt.Errorf("选题：%w", err)
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
		return res, errors.New("选题：三次都没有选出具体题目")
	}
	if topic.SearchQuery == "" {
		topic.SearchQuery = topic.Topic
	}

	// ② research (optional: only when a research route exists / the user has Tavily)
	ev := p.research(ctx, j, topic.Topic, topic.SearchQuery, load, chat, trace)

	// ③ write
	wcfgs, err := load("write")
	if err != nil {
		return res, fmt.Errorf("写作：%w", err)
	}
	wr, err := chat("write", wcfgs, writeSystem, writeUser(wc, topic.Topic, topic.Angle, ev))
	if err != nil {
		return res, fmt.Errorf("写作：%w", err)
	}
	var art render.Article
	block := ""
	if decodeJSON(wr.Text, &art) != nil {
		block = "空：模型没有返回可解析的文章"
	} else {
		block = Gate(art, wr.FinishReason, p.sensitive(ctx))
	}

	// ④ optional self-review: a revision replaces the draft only if it is a complete article that passes the gates
	if block == "" && selfReview {
		if rcfgs, err := load("review"); err == nil {
			orig, _ := json.Marshal(art)
			rr, err := chat("review", rcfgs, reviewSystem, wc.settings()+"\n文章：\n"+string(orig))
			var rv struct {
				Pass     *bool          `json:"pass"`
				Problems []string       `json:"problems"`
				Article  render.Article `json:"article"`
			}
			switch {
			case err != nil:
				trace(Step{Stage: "review", Note: "review failed, keeping draft"})
			case decodeJSON(rr.Text, &rv) != nil || rv.Pass == nil:
				trace(Step{Stage: "review", Note: "review reply not structured, keeping draft"})
			case *rv.Pass:
				trace(Step{Stage: "review", Note: "pass"})
			case Gate(rv.Article, rr.FinishReason, p.sensitive(ctx)) == "" && len(rv.Article.Blocks) > 0:
				art = rv.Article
				trace(Step{Stage: "review", Note: "revised: " + strings.Join(rv.Problems, "；")})
			default:
				trace(Step{Stage: "review", Note: "revision rejected by gates, keeping draft"})
			}
		}
	}

	// ⑤ render
	srcs := map[int]render.Source{}
	for _, e := range ev {
		if e.Official() {
			srcs[e.N] = render.Source{N: e.N, Kind: e.Kind, Title: e.Title, Issuer: e.Issuer, DocNumber: e.DocNumber, Date: e.Date, URL: e.URL}
		}
	}
	prof := wc.Profile
	out := render.Render(art, render.Options{Opening: prof["开头"], Ending: prof["结尾"], Signature: prof["签名"],
		AILabel: prof["AI标识"] == "on", DefaultImageURL: p.DefaultImageURL, Sources: srcs})

	res.Status, res.BlockReason = "ready", block
	if block != "" {
		res.Status = "blocked"
	}
	res.ArticleID, err = p.save(ctx, j, colID, topic.Topic, art, out, res)
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

func (p *Pipeline) research(ctx context.Context, j Job, topic, query string,
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
	err := p.DB.QueryRow(ctx, `SELECT u.tier, a.name FROM wx_account a JOIN users u ON u.id=a.user_id WHERE a.id=$1 AND a.user_id=$2`,
		j.AccountID, j.UserID).Scan(&tier, &w.AccountName)
	if err != nil {
		return w, "", 0, false, fmt.Errorf("读取公众号：%w", err)
	}
	var pos, persona, aud, tone, taboos, sig, opening, ending string
	var ai, review bool
	err = p.DB.QueryRow(ctx, `SELECT positioning, persona, audience, tone, taboos, signature, opening, ending, ai_label, self_review
		FROM account_profile WHERE account_id=$1`, j.AccountID).Scan(&pos, &persona, &aud, &tone, &taboos, &sig, &opening, &ending, &ai, &review)
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
		rows, err := p.DB.Query(ctx, `SELECT c.id, c.weight FROM account_column c JOIN article_type t ON t.id=c.article_type_id
			WHERE c.account_id=$1 AND c.enabled AND t.enabled`, j.AccountID)
		if err != nil {
			return w, "", 0, false, err
		}
		type cw struct{ id, w int64 }
		var cs []cw
		var total int64
		for rows.Next() {
			var c cw
			rows.Scan(&c.id, &c.w)
			cs = append(cs, c)
			total += c.w
		}
		rows.Close()
		if len(cs) == 0 {
			return w, "", 0, false, ErrNoColumn
		}
		pick := rand.Int64N(total)
		for _, c := range cs {
			if pick < c.w {
				colID = c.id
				break
			}
			pick -= c.w
		}
	}
	err = p.DB.QueryRow(ctx, `SELECT c.name, c.note, c.length_hint, t.name, t.description, t.needs_news
		FROM account_column c JOIN article_type t ON t.id=c.article_type_id WHERE c.id=$1 AND c.account_id=$2`, colID, j.AccountID).
		Scan(&w.ColumnName, &w.ColumnNote, &w.LengthHint, &w.TypeName, &w.TypeDesc, &w.NeedsNews)
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
	return w, tier, colID, review, nil
}

func (p *Pipeline) save(ctx context.Context, j Job, colID int64, topic string, a render.Article, out render.Output, res Result) (int64, error) {
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
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO article (job_id, user_id, account_id, column_id, title, alt_titles, summary, body_json, html, status, block_reason, topic)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
		jobID, j.UserID, j.AccountID, colID, a.Title, alts, a.Summary, body, out.HTML, res.Status, res.BlockReason, topic).Scan(&id)
	if err != nil {
		return 0, err
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
