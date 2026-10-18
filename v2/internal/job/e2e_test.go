package job_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"gonghao/internal/account"
	"gonghao/internal/audit"
	"gonghao/internal/auth"
	"gonghao/internal/crypto"
	"gonghao/internal/gateway"
	"gonghao/internal/gen"
	"gonghao/internal/job"
	"gonghao/internal/publish"
	"gonghao/internal/search"
	"gonghao/internal/server"
	"gonghao/internal/style"
	"gonghao/internal/svccfg"
	"gonghao/internal/testdb"
	"gonghao/internal/wechat"
)

// fakeModel serves /{stage}/v1/chat/completions, streaming scripted replies in small chunks.
type fakeModel struct {
	mu      sync.Mutex
	replies map[string][]string // stage -> queue; last one repeats
	finish  map[string]string
	reqs    map[string][]map[string]any
	url     string
}

func (f *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	stage := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.reqs[stage] = append(f.reqs[stage], body)
	q := f.replies[stage]
	text := ""
	if len(q) > 0 {
		text = q[0]
		if len(q) > 1 {
			f.replies[stage] = q[1:]
		}
	}
	fin := f.finish[stage]
	f.mu.Unlock()
	if fin == "" {
		fin = "stop"
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fl := w.(http.Flusher)
	fmt.Fprint(w, `data: {"choices":[{"delta":{"reasoning_content":"思考中"}}]}`+"\n\n")
	fl.Flush()
	rs := []rune(text)
	for i := 0; i < len(rs); i += 40 {
		end := min(i+40, len(rs))
		b, _ := json.Marshal(map[string]any{"model": "fake-" + stage, "choices": []any{map[string]any{"delta": map[string]string{"content": string(rs[i:end])}}}})
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":%q}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":2000},\"citations\":[\"https://www.douyin.com/video/1\"]}\n\ndata: [DONE]\n\n", fin)
}

func (f *fakeModel) set(stage string, finish string, replies ...string) {
	f.mu.Lock()
	f.replies[stage], f.finish[stage] = replies, finish
	f.mu.Unlock()
}

func (f *fakeModel) last(stage string) (system, user string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rs := f.reqs[stage]
	if len(rs) == 0 {
		return "", ""
	}
	ms := rs[len(rs)-1]["messages"].([]any)
	return ms[0].(map[string]any)["content"].(string), ms[1].(map[string]any)["content"].(string)
}

type env struct {
	t    *testing.T
	srv  *server.Server
	auth *auth.Service
	jobs *job.Service
	fm   *fakeModel
	pool *pgxpool.Pool
	wx   *fakeWX
	pub  *publish.Service
}

func do(t *testing.T, srv http.Handler, method, path, tok string, body any) (int, map[string]any, string) {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var m map[string]any
	json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m, rec.Body.String()
}

const article1 = `{"title":"孙子写作业磨蹭，我用三个小办法","alt_titles":["备选"],"summary":"摘要",
"blocks":[{"type":"p","text":"我是奶奶。学习桌照明要符合 GB 50034 的要求[1]，网上说法[3]不算数。"},
{"type":"h2","text":"先看灯光"},{"type":"list","items":["照度：不低于 300 lx","色温：4000K 左右","位置：放在左前方"]},
{"type":"h2","text":"再看时间"},{"type":"p","text":"每学 25 分钟歇 5 分钟。"},{"type":"p","text":"慢慢来，孩子会好的。"}]}`

const revised = `{"title":"孙子写作业磨蹭，我用三个小办法","summary":"修订摘要",
"blocks":[{"type":"p","text":"我是奶奶。学习桌照明要符合 GB 50034 的要求[1]。"},{"type":"h2","text":"先看灯光"},
{"type":"list","items":["照度：不低于 300 lx","色温：4000K 左右","位置：放在左前方"]},{"type":"p","text":"修订后的结尾。"}]}`

const research = `{"facts":[
{"claim":"建筑照明标准值","title":"建筑照明设计标准","url":"https://www.mohurd.gov.cn/x","date":"2024-03-01","kind":"national_standard","doc_number":"GB 50034-2024","issuer":"住房和城乡建设部"},
{"claim":"短视频","title":"护眼神器","url":"https://www.douyin.com/video/2","kind":"other"},
{"claim":"自称官方","title":"某博客说法","url":"https://blog.example.com/a","kind":"gov_document"}]}`

func setup(t *testing.T) (*env, string, string, int64, int64) {
	pool := testdb.Open(t)
	auth.SetTrustedProxies("192.0.2.0/24,127.0.0.0/8") // httptest requests come from 192.0.2.1
	ctx := context.Background()
	box, _ := crypto.NewBox("0123456789abcdef0123456789abcdef")
	as := auth.NewService(auth.NewPGStore(pool), time.Hour)
	h := auth.NewHandlers(as, false)
	srv := server.New(server.Options{})
	h.Register(srv)
	aud := audit.PG{DB: pool}
	account.New(pool, box, aud).Register(srv, h)
	gw := gateway.New(gateway.PGRecorder{DB: pool, Log: slog.Default()})
	gw.Sleep = func(context.Context, time.Duration) error { return nil }
	gw.MinCall = time.Millisecond
	(&svccfg.Service{DB: pool, Box: box, Audit: aud, GW: gw}).Register(srv, h)

	fm := &fakeModel{replies: map[string][]string{}, finish: map[string]string{}, reqs: map[string][]map[string]any{}}
	ms := httptest.NewServer(fm)
	fm.url = ms.URL
	t.Cleanup(ms.Close)
	var tavilyKey string
	tv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tavilyKey = r.Header.Get("Authorization")
		io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, `{"results":[{"title":"关于做好儿童青少年近视防控工作的通知","url":"https://www.nhc.gov.cn/a.html","content":"……","published_date":"2026-09-01"}]}`)
	}))
	t.Cleanup(tv.Close)
	t.Cleanup(func() {
		if tavilyKey != "" && tavilyKey != "Bearer tvly-user-key-123456" {
			t.Errorf("tavily got wrong key")
		}
	})

	(&style.Service{DB: pool, GW: gw, Box: box, Audit: aud}).Register(srv, h)
	pipe := &gen.Pipeline{DB: pool, GW: gw, Box: box, Tavily: search.Tavily{BaseURL: tv.URL}, DefaultImageURL: "https://cdn.example/cover.jpg"}
	js := &job.Service{DB: pool, Runner: pipe, Render: pipe, Auth: as, Audit: aud}
	js.Register(srv, h)
	wx := newFakeWX(t)
	pub := &publish.Service{DB: pool, Box: box, Audit: aud, WX: &wechat.Client{Base: wx.srv.URL}, Fetch: &http.Client{Transport: imageTransport{}}}
	pub.Register(srv, h)

	as.Bootstrap(ctx, "root@example.com", "rootpass1")
	_, root, _ := as.Login(ctx, "root@example.com", "rootpass1", "", "")
	as.Register(ctx, "u@example.com", "password1", "", auth.Consent{TermsVersion: "1", PrivacyVersion: "1"}, "", "")
	_, user, _ := as.Login(ctx, "u@example.com", "password1", "", "")

	// admin: one config per stage on the fake server, routes, an article type
	for _, st := range svccfg.Stages {
		_, c, b := do(t, srv, "POST", "/api/admin/configs", root, map[string]any{"kind": "text", "name": st, "base_url": ms.URL + "/" + st + "/v1",
			"api_key": "sk-fake-0123456789", "model": "m-" + st, "reasoning_effort": "high", "enabled": true})
		if c["id"] == nil {
			t.Fatal(b)
		}
		if code, _, b := do(t, srv, "PUT", "/api/admin/routes/"+st, root, map[string]any{"config_ids": []any{c["id"]}}); code != 200 {
			t.Fatal(b)
		}
	}
	_, ty, _ := do(t, srv, "POST", "/api/admin/article-types", root, map[string]any{"key": "life_tip", "name": "生活妙招", "description": "一个能马上照做的生活小办法", "enabled": true})

	// user: bind, profile, column, own Tavily key
	_, a, b := do(t, srv, "POST", "/api/accounts", user, map[string]any{"name": "奶奶不当家", "app_id": "wx0123456789abcdef", "app_secret": "0123456789abcdef0123456789abcdef"})
	if a["id"] == nil {
		t.Fatal(b)
	}
	accID := int64(a["id"].(float64))
	do(t, srv, "PUT", fmt.Sprintf("/api/accounts/%d/profile", accID), user, map[string]any{
		"persona": "我是退休的奶奶，用第一人称讲家里的事", "audience": "带孙辈的老人", "signature": "奶奶不当家，天天有妙招", "self_review": true})
	_, c, _ := do(t, srv, "POST", fmt.Sprintf("/api/accounts/%d/columns", accID), user, map[string]any{"name": "妙招", "article_type_id": ty["id"], "enabled": true})
	colID := int64(c["id"].(float64))
	if code, m, _ := do(t, srv, "PUT", "/api/me/tavily", user, map[string]any{"key": "tvly-user-key-123456"}); code != 200 || m["mask"] == "" {
		t.Fatal("tavily key")
	}
	e := &env{t: t, srv: srv, auth: as, jobs: js, fm: fm, pool: pool, wx: wx, pub: pub}
	return e, root, user, accID, colID
}

func (e *env) run() {
	if ran, err := e.jobs.RunOnce(context.Background()); !ran || err != nil {
		e.t.Fatalf("RunOnce ran=%v err=%v", ran, err)
	}
}

func TestEndToEnd(t *testing.T) {
	e, root, user, accID, colID := setup(t)
	// topic: first candidate is just the account name -> rejected; second is concrete
	e.fm.set("topic", "", `{"topic":"奶奶不当家"}`, `{"topic":"孙子写作业磨蹭，台灯和计时器怎么用","angle":"奶奶亲自试过","search_query":"儿童 写作业 台灯 照度 标准"}`)
	e.fm.set("research", "", research)
	e.fm.set("write", "", article1)
	e.fm.set("review", "", `{"pass":false,"problems":["结尾重复"],"article":`+revised+`}`)

	code, m, b := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/generate", accID), user, map[string]any{"column_id": colID})
	if code != 202 || m["dry_run"] != true {
		t.Fatal(code, b)
	}
	jobID := int64(m["job_id"].(float64))
	e.run()

	_, j, jb := do(t, e.srv, "GET", fmt.Sprintf("/api/jobs/%d", jobID), user, nil)
	if j["status"] != "done" || j["article_id"] == nil {
		t.Fatalf("job: %s", jb)
	}
	if !strings.Contains(jb, "rejected: 奶奶不当家") || !strings.Contains(jb, "revised: 结尾重复") || !strings.Contains(jb, "tavily 1 hits") {
		t.Fatalf("trace missing steps: %s", jb)
	}
	artID := int64(j["article_id"].(float64))
	_, a, ab := do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%d", artID), user, nil)
	if a["status"] != "ready" || a["topic"] != "孙子写作业磨蹭，台灯和计时器怎么用" || a["summary"] != "修订摘要" {
		t.Fatalf("article: %s", ab)
	}
	h := a["html"].(string)
	if !strings.Contains(h, "<table") || !strings.Contains(h, "300 lx") {
		t.Fatal("kv list should render as a table")
	}
	if !strings.Contains(h, "奶奶不当家，天天有妙招") || strings.Index(h, "天天有妙招") < strings.Index(h, "修订后的结尾") {
		t.Fatal("signature appended at the end by the renderer")
	}
	if !strings.Contains(h, "GB 50034-2024") || strings.Count(h, "<sup") != 1 || !strings.Contains(h, "依据") {
		t.Fatal("only the national standard is cited")
	}
	if strings.Contains(h, "blog.example.com") || strings.Contains(h, "douyin") {
		t.Fatal("non-official / junk source shown")
	}

	// prompts: system holds platform rules only; user settings passed verbatim; no length hint when unset
	sys, usr := e.fm.last("write")
	if strings.Contains(sys, "奶奶") || !strings.Contains(usr, "我是退休的奶奶，用第一人称讲家里的事") || !strings.Contains(usr, "生活妙招") {
		t.Fatal("write prompt composition")
	}
	if strings.Contains(usr, "篇幅") || strings.Contains(usr+sys, "字数") {
		t.Fatal("length hint without a setting")
	}
	if !strings.Contains(usr, "官方依据（用到时句末标注编号）") || !strings.Contains(usr, "【官方依据】[1] 建筑照明设计标准") || !strings.Contains(usr, "nhc.gov.cn") && !strings.Contains(usr, "近视防控") {
		t.Fatalf("evidence in prompt: %s", usr)
	}
	if strings.Contains(usr, "douyin") || strings.Contains(usr, "【官方依据】[3]") {
		t.Fatal("junk or fake-official evidence")
	}
	_, tu := e.fm.last("topic")
	if !strings.Contains(tu, "不合格") {
		t.Fatal("rejected topic fed back")
	}
	e.fm.mu.Lock()
	effort := e.fm.reqs["write"][0]["reasoning_effort"]
	e.fm.mu.Unlock()
	if effort != "high" {
		t.Fatal("reasoning effort from config")
	}
	e.fm.mu.Lock()
	_, rf := e.fm.reqs["research"][0]["response_format"]
	_, wf := e.fm.reqs["write"][0]["response_format"]
	e.fm.mu.Unlock()
	if rf || !wf {
		t.Fatal("search model must not get JSON mode (it skips searching); writer must")
	}

	// preview
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/articles/%d/preview", artID), nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: user})
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") || !strings.Contains(rec.Body.String(), "max-width:420px") {
		t.Fatal("preview")
	}
	if code, _, _ := do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%d/preview", artID), root, nil); code != 200 {
		t.Fatal("staff preview")
	}

	// daily quota = 1: the second request today is refused
	if code, _, _ := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/generate", accID), user, nil); code != 429 {
		t.Fatalf("quota: %d", code)
	}
	// staff can't generate (no accounts)
	if code, _, _ := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/generate", accID), root, nil); code != 403 {
		t.Fatal("staff generate")
	}
}

func TestGatesAndQuotaExemption(t *testing.T) {
	e, root, user, accID, colID := setup(t)
	var uid int64 = 2
	do(t, e.srv, "PUT", fmt.Sprintf("/api/admin/users/%d/allowance", uid), root, map[string]any{"max_accounts": 1, "tier": "free", "quota_exempt": true})
	gen1 := func() map[string]any {
		code, m, b := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/generate", accID), user, map[string]any{"column_id": colID})
		if code != 202 {
			t.Fatalf("exempt user refused: %d %s", code, b)
		}
		e.run()
		_, j, _ := do(t, e.srv, "GET", fmt.Sprintf("/api/jobs/%d", int64(m["job_id"].(float64))), user, nil)
		if j["article_id"] == nil {
			return j
		}
		_, a, _ := do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%d", int64(j["article_id"].(float64))), user, nil)
		return a
	}
	n := 0
	topic := func() string { n++; return fmt.Sprintf(`{"topic":"第%d个具体的家务小办法怎么做"}`, n) }
	e.fm.set("research", "", `{"facts":[]}`)

	// truncated by length
	e.fm.set("topic", "", topic())
	e.fm.set("write", "length", article1)
	if a := gen1(); a["status"] != "blocked" || !strings.Contains(a["block_reason"].(string), "截断") {
		t.Fatalf("length gate: %v", a)
	}
	// last paragraph cut mid-sentence
	e.fm.set("topic", "", topic())
	e.fm.set("write", "", `{"title":"t","blocks":[{"type":"p","text":"开头。"},{"type":"p","text":"然后我们就"}]}`)
	if a := gen1(); a["status"] != "blocked" {
		t.Fatalf("cut gate: %v", a)
	}
	// model tokens
	e.fm.set("topic", "", topic())
	e.fm.set("write", "", `{"title":"t","blocks":[{"type":"p","text":"正文<|im_end|>结束。"}]}`)
	if a := gen1(); a["status"] != "blocked" || !strings.Contains(a["block_reason"].(string), "异常") {
		t.Fatalf("junk gate: %v", a)
	}
	// review that answers with a bare word never replaces the article
	e.fm.set("topic", "", topic())
	e.fm.set("write", "", article1)
	e.fm.set("review", "", "PASS")
	if a := gen1(); a["status"] != "ready" || a["title"] != "孙子写作业磨蹭，我用三个小办法" {
		t.Fatalf("unstructured review: %v", a)
	}
	// sensitive word (platform list)
	e.fm.set("topic", "", topic())
	e.fm.set("review", "", `{"pass":true}`)
	pool := e.pool
	pool.Exec(context.Background(), `INSERT INTO sensitive_word (word) VALUES ('计时器') , ('磨蹭')`)
	if a := gen1(); a["status"] != "blocked" || !strings.Contains(a["block_reason"].(string), "敏感词") {
		t.Fatalf("sensitive gate: %v", a)
	}
	// write model returns nothing at all: 4 empty attempts, job fails, quota released
	e.fm.set("topic", "", topic())
	e.fm.set("write", "", "")
	if j := gen1(); j["status"] != "failed" || !strings.Contains(j["failure"].(string), "empty") {
		t.Fatalf("empty write: %v", j)
	}
	e.fm.mu.Lock()
	nw := len(e.fm.reqs["write"])
	e.fm.mu.Unlock()
	if nw != 5+4 {
		t.Fatalf("write calls=%d (empty reply should be tried 4 times)", nw)
	}
}

func TestNewsFallbackAndResearchOff(t *testing.T) {
	e, root, user, accID, _ := setup(t)
	// a news type with research switched off
	_, ty, tb := do(t, e.srv, "POST", "/api/admin/article-types", root, map[string]any{"key": "news_story", "name": "快报故事",
		"needs_news": true, "research": false, "enabled": true})
	if ty["research"] != false {
		t.Fatalf("research flag: %s", tb)
	}
	// editing without the field keeps it off
	_, ty2, _ := do(t, e.srv, "PUT", fmt.Sprintf("/api/admin/article-types/%v", ty["id"]), root, map[string]any{"key": "news_story", "name": "快报故事", "needs_news": true, "enabled": true})
	if ty2["research"] != false {
		t.Fatalf("edit reset research: %v", ty2)
	}
	_, c, _ := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/columns", accID), user, map[string]any{"name": "快报", "article_type_id": ty["id"], "enabled": true})
	e.fm.set("research", "", "**未检索到可靠来源**") // the pre-scan finds nothing
	e.fm.set("topic", "", `{"topic":"冬天给老人房间加湿器怎么摆","angle":"实用","search_query":"加湿器 摆放"}`)
	e.fm.set("write", "", article1)
	e.fm.set("review", "", `{"pass":true}`)
	_, m, b := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/generate", accID), user, map[string]any{"column_id": c["id"]})
	if m["job_id"] == nil {
		t.Fatal(b)
	}
	e.run()
	_, tu := e.fm.last("topic")
	if !strings.Contains(tu, "这一篇不写新闻") || strings.Contains(tu, "近期检索到的动态") {
		t.Fatalf("topic prompt without events: %s", tu)
	}
	e.fm.mu.Lock()
	n := len(e.fm.reqs["research"])
	e.fm.mu.Unlock()
	if n != 2 {
		t.Fatalf("only the two pre-scan queries should search (research off), got %d calls", n)
	}
	_, j, jb := do(t, e.srv, "GET", fmt.Sprintf("/api/jobs/%v", m["job_id"]), user, nil)
	if !strings.Contains(jb, "off for this article type") || !strings.Contains(jb, "practical non-news topic") {
		t.Fatalf("trace: %s", jb)
	}
	_, a, ab := do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%v", j["article_id"]), user, nil)
	if a["topic_kind"] != "practical" {
		t.Fatalf("article kind: %s", ab)
	}
	_, wu := e.fm.last("write")
	if !strings.Contains(wu, "不按新闻写") {
		t.Fatal("writer told the topic is a practical fallback")
	}
	if strings.Contains(wu, "资料") {
		t.Fatal("no material without research")
	}

	// with events found, the topic picks from them and the article is marked news
	e.fm.set("research", "", "- 某市发布智慧社区建设通知（来源：某市住建局｜2026-10-10）")
	e.fm.set("topic", "", `{"topic":"某市智慧社区建设通知对老旧小区意味着什么","angle":"解读","search_query":"智慧社区 通知"}`)
	_, m, _ = do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/generate", accID), user, map[string]any{"column_id": c["id"]})
	if m["job_id"] == nil {
		// daily quota of 1 — lift it for this check
		e.pool.Exec(context.Background(), `UPDATE users SET daily_quota=10`)
		_, m, b = do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/generate", accID), user, map[string]any{"column_id": c["id"]})
		if m["job_id"] == nil {
			t.Fatal(b)
		}
	}
	e.run()
	_, tu = e.fm.last("topic")
	if !strings.Contains(tu, "某市发布智慧社区建设通知（某市住建局｜2026-10-10）") || strings.Contains(tu, "不写新闻") {
		t.Fatalf("topic prompt with events: %s", tu)
	}
	if _, wu := e.fm.last("write"); !strings.Contains(wu, "某市发布智慧社区建设通知") {
		t.Fatalf("scan event must reach the writer as a fact: %s", wu)
	}
	_, j, _ = do(t, e.srv, "GET", fmt.Sprintf("/api/jobs/%v", m["job_id"]), user, nil)
	_, a, _ = do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%v", j["article_id"]), user, nil)
	if a["topic_kind"] != "news" {
		t.Fatalf("kind %v", a["topic_kind"])
	}
}
