package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// upstream scripts one behaviour per call.
type upstream struct {
	mu     sync.Mutex
	steps  []func(w http.ResponseWriter, r *http.Request)
	calls  int32
	bodies []map[string]any
}

func (u *upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := int(atomic.AddInt32(&u.calls, 1)) - 1
	b, _ := io.ReadAll(r.Body)
	var m map[string]any
	json.Unmarshal(b, &m)
	u.mu.Lock()
	u.bodies = append(u.bodies, m)
	step := u.steps[len(u.steps)-1]
	if n < len(u.steps) {
		step = u.steps[n]
	}
	u.mu.Unlock()
	step(w, r)
}

func sse(w http.ResponseWriter, lines ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	f := w.(http.Flusher)
	for _, l := range lines {
		if strings.HasPrefix(l, "sleep:") {
			d, _ := time.ParseDuration(l[6:])
			time.Sleep(d)
			continue
		}
		fmt.Fprint(w, l+"\n\n")
		f.Flush()
	}
}

func content(s string) string {
	b, _ := json.Marshal(map[string]any{"model": "upstream-model-x", "choices": []any{map[string]any{"delta": map[string]string{"content": s}}}})
	return "data: " + string(b)
}
func reasoning(s string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"reasoning_content": s}}}})
	return "data: " + string(b)
}

const finish = `data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":22}}`
const done = "data: [DONE]"

func okStep(text string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		r2 := []rune(text)
		sse(w, content(string(r2[:len(r2)/2])), content(string(r2[len(r2)/2:])), finish, done)
	}
}
func emptyStep(w http.ResponseWriter, r *http.Request) { sse(w, finish, done) }
func statusStep(code int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", code) }
}

type rec struct {
	mu sync.Mutex
	as []Attempt
}

func (r *rec) Record(_ context.Context, _ string, _ int64, a Attempt) {
	r.mu.Lock()
	r.as = append(r.as, a)
	r.mu.Unlock()
}

func testGW() (*Gateway, *rec, *[]time.Duration) {
	r := &rec{}
	g := New(r)
	g.MinCall = time.Millisecond
	var waits []time.Duration
	g.Sleep = func(ctx context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	return g, r, &waits
}

func cfg(id int64, url string) Config {
	return Config{ID: id, BaseURL: url, Model: fmt.Sprintf("model-%d", id), FirstByte: time.Second, Idle: time.Second, Total: 5 * time.Second}
}

var req = Request{Stage: "write", Messages: []Message{{Role: "user", Content: "hi"}}}

func TestStreamsAndReportsUpstreamModel(t *testing.T) {
	u := &upstream{steps: []func(http.ResponseWriter, *http.Request){okStep("你好世界，全文")}}
	s := httptest.NewServer(u)
	defer s.Close()
	g, r, _ := testGW()
	c := cfg(1, s.URL)
	c.ReasoningEffort = "high"
	c.Params = map[string]any{"temperature": 0.7, "model": "evil-override", "stream": false}
	res, err := g.Chat(context.Background(), []Config{c}, req, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "你好世界，全文" || res.FinishReason != "stop" || res.Model != "upstream-model-x" || res.TokensOut != 22 {
		t.Fatalf("bad result %+v", res)
	}
	b := u.bodies[0]
	if b["stream"] != true || b["model"] != "model-1" || b["reasoning_effort"] != "high" || b["temperature"] != 0.7 {
		t.Fatalf("bad request body %v", b)
	}
	if res.Attempts[0].TotalMs < 0 || res.Attempts[0].FirstByteMs < 0 {
		t.Fatal("timings")
	}
	if len(r.as) != 1 || r.as[0].Outcome != OK || r.as[0].Effort != "high" {
		t.Fatalf("recorder %+v", r.as)
	}
}

func TestNoReasoningEffortWhenUnset(t *testing.T) {
	u := &upstream{steps: []func(http.ResponseWriter, *http.Request){okStep("ok text")}}
	s := httptest.NewServer(u)
	defer s.Close()
	g, _, _ := testGW()
	if _, err := g.Chat(context.Background(), []Config{cfg(1, s.URL)}, req, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, has := u.bodies[0]["reasoning_effort"]; has {
		t.Fatal("gateway invented a reasoning effort")
	}
}

func TestEmptyRetriesFourTimesWithBackoffThenBackup(t *testing.T) {
	pri := &upstream{steps: []func(http.ResponseWriter, *http.Request){emptyStep}}
	bak := &upstream{steps: []func(http.ResponseWriter, *http.Request){okStep("backup text")}}
	s1, s2 := httptest.NewServer(pri), httptest.NewServer(bak)
	defer s1.Close()
	defer s2.Close()
	g, _, waits := testGW()
	res, err := g.Chat(context.Background(), []Config{cfg(1, s1.URL), cfg(2, s2.URL)}, req, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if pri.calls != 4 || bak.calls != 1 || res.ConfigID != 2 || res.Text != "backup text" {
		t.Fatalf("primary=%d backup=%d res=%+v", pri.calls, bak.calls, res)
	}
	want := []time.Duration{3 * time.Second, 6 * time.Second, 12 * time.Second}
	if fmt.Sprint(*waits) != fmt.Sprint(want) {
		t.Fatalf("waits %v want %v", *waits, want)
	}
	if len(res.Attempts) != 5 || res.Attempts[3].N != 4 || res.Attempts[3].Outcome != Empty || res.Attempts[4].N != 1 {
		t.Fatalf("attempts %+v", res.Attempts)
	}
}

func TestEmptyThenSuccessOnSameConfig(t *testing.T) {
	u := &upstream{steps: []func(http.ResponseWriter, *http.Request){emptyStep, emptyStep, okStep("third time")}}
	s := httptest.NewServer(u)
	defer s.Close()
	g, _, waits := testGW()
	res, err := g.Chat(context.Background(), []Config{cfg(1, s.URL)}, req, time.Minute)
	if err != nil || res.Text != "third time" || u.calls != 3 || len(*waits) != 2 {
		t.Fatalf("err=%v res=%+v calls=%d waits=%v", err, res, u.calls, *waits)
	}
}

func TestWhitespaceOnlyIsEmpty(t *testing.T) {
	u := &upstream{steps: []func(http.ResponseWriter, *http.Request){
		func(w http.ResponseWriter, r *http.Request) { sse(w, content(" \n "), finish, done) }, okStep("real")}}
	s := httptest.NewServer(u)
	defer s.Close()
	g, _, _ := testGW()
	res, err := g.Chat(context.Background(), []Config{cfg(1, s.URL)}, req, time.Minute)
	if err != nil || res.Text != "real" || res.Attempts[0].Outcome != Empty {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestHTTP429RetriesOnceThenBackup(t *testing.T) {
	pri := &upstream{steps: []func(http.ResponseWriter, *http.Request){statusStep(429)}}
	bak := &upstream{steps: []func(http.ResponseWriter, *http.Request){okStep("from backup")}}
	s1, s2 := httptest.NewServer(pri), httptest.NewServer(bak)
	defer s1.Close()
	defer s2.Close()
	g, _, _ := testGW()
	res, err := g.Chat(context.Background(), []Config{cfg(1, s1.URL), cfg(2, s2.URL)}, req, time.Minute)
	if err != nil || pri.calls != 2 || res.ConfigID != 2 {
		t.Fatalf("err=%v calls=%d res=%+v", err, pri.calls, res)
	}
	if res.Attempts[0].HTTPStatus != 429 || res.Attempts[0].Outcome != HTTPError {
		t.Fatalf("%+v", res.Attempts[0])
	}
}

func TestHTTP401GoesStraightToBackup(t *testing.T) {
	pri := &upstream{steps: []func(http.ResponseWriter, *http.Request){statusStep(401)}}
	bak := &upstream{steps: []func(http.ResponseWriter, *http.Request){okStep("b")}}
	s1, s2 := httptest.NewServer(pri), httptest.NewServer(bak)
	defer s1.Close()
	defer s2.Close()
	g, _, _ := testGW()
	if _, err := g.Chat(context.Background(), []Config{cfg(1, s1.URL), cfg(2, s2.URL)}, req, time.Minute); err != nil || pri.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, pri.calls)
	}
}

func TestLongReasoningSilenceSurvivesWhileBytesFlow(t *testing.T) {
	// 600ms of reasoning deltas, each gap 100ms, idle timeout 300ms: must not time out.
	steps := []string{}
	for i := 0; i < 6; i++ {
		steps = append(steps, reasoning("thinking"), "sleep:100ms")
	}
	steps = append(steps, ": keep-alive", "sleep:100ms", content("final"), finish, done)
	u := &upstream{steps: []func(http.ResponseWriter, *http.Request){func(w http.ResponseWriter, r *http.Request) { sse(w, steps...) }}}
	s := httptest.NewServer(u)
	defer s.Close()
	g, _, _ := testGW()
	c := cfg(1, s.URL)
	c.Idle = 300 * time.Millisecond
	res, err := g.Chat(context.Background(), []Config{c}, req, time.Minute)
	if err != nil || res.Text != "final" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestIdleTimeoutDiscardsPartialAndFailsOver(t *testing.T) {
	pri := &upstream{steps: []func(http.ResponseWriter, *http.Request){func(w http.ResponseWriter, r *http.Request) {
		sse(w, content("half "), "sleep:500ms", content("rest"), finish, done)
	}}}
	bak := &upstream{steps: []func(http.ResponseWriter, *http.Request){okStep("whole article")}}
	s1, s2 := httptest.NewServer(pri), httptest.NewServer(bak)
	defer s1.Close()
	defer s2.Close()
	g, _, _ := testGW()
	c := cfg(1, s1.URL)
	c.Idle = 150 * time.Millisecond
	res, err := g.Chat(context.Background(), []Config{c, cfg(2, s2.URL)}, req, time.Minute)
	if err != nil || res.Text != "whole article" || pri.calls != 1 {
		t.Fatalf("%v %+v calls=%d", err, res, pri.calls)
	}
	if res.Attempts[0].Outcome != TimeoutIdle {
		t.Fatalf("outcome %s", res.Attempts[0].Outcome)
	}
}

func TestFirstByteTimeout(t *testing.T) {
	u := &upstream{steps: []func(http.ResponseWriter, *http.Request){func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(time.Second):
		case <-r.Context().Done():
		}
	}}}
	s := httptest.NewServer(u)
	defer s.Close()
	g, _, _ := testGW()
	c := cfg(1, s.URL)
	c.FirstByte = 100 * time.Millisecond
	_, err := g.Chat(context.Background(), []Config{c}, req, time.Minute)
	var fe *FailedError
	if !errors.As(err, &fe) || fe.Attempts[0].Outcome != TimeoutFirstByte || len(fe.Attempts) != 1 {
		t.Fatalf("%v", err)
	}
}

func TestTotalTimeout(t *testing.T) {
	steps := []string{}
	for i := 0; i < 20; i++ {
		steps = append(steps, content("x"), "sleep:50ms")
	}
	u := &upstream{steps: []func(http.ResponseWriter, *http.Request){func(w http.ResponseWriter, r *http.Request) { sse(w, append(steps, finish, done)...) }}}
	s := httptest.NewServer(u)
	defer s.Close()
	g, _, _ := testGW()
	c := cfg(1, s.URL)
	c.Total = 300 * time.Millisecond
	_, err := g.Chat(context.Background(), []Config{c}, req, time.Minute)
	var fe *FailedError
	if !errors.As(err, &fe) || fe.Attempts[0].Outcome != TimeoutTotal {
		t.Fatalf("%v", err)
	}
}

func TestBrokenStreamWithoutFinish(t *testing.T) {
	u := &upstream{steps: []func(http.ResponseWriter, *http.Request){func(w http.ResponseWriter, r *http.Request) { sse(w, content("cut off")) }}}
	s := httptest.NewServer(u)
	defer s.Close()
	g, _, _ := testGW()
	_, err := g.Chat(context.Background(), []Config{cfg(1, s.URL)}, req, time.Minute)
	var fe *FailedError
	if !errors.As(err, &fe) || fe.Attempts[0].Outcome != StreamBroken || u.calls != 1 {
		t.Fatalf("%v calls=%d", err, u.calls)
	}
}

func TestLengthFinishIsReturnedForGates(t *testing.T) {
	u := &upstream{steps: []func(http.ResponseWriter, *http.Request){func(w http.ResponseWriter, r *http.Request) {
		sse(w, content("long text"), `data: {"choices":[{"delta":{},"finish_reason":"length"}]}`, done)
	}}}
	s := httptest.NewServer(u)
	defer s.Close()
	g, _, _ := testGW()
	res, err := g.Chat(context.Background(), []Config{cfg(1, s.URL)}, req, time.Minute)
	if err != nil || res.FinishReason != "length" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestBudgetStopsWaiting(t *testing.T) {
	u := &upstream{steps: []func(http.ResponseWriter, *http.Request){emptyStep}}
	s := httptest.NewServer(u)
	defer s.Close()
	g, _, waits := testGW()
	g.MinCall = time.Second
	// 3s wait + 1s min call > 2s budget: only one attempt, no sleep.
	_, err := g.Chat(context.Background(), []Config{cfg(1, s.URL)}, req, 2*time.Second)
	if err == nil || u.calls != 1 || len(*waits) != 0 {
		t.Fatalf("err=%v calls=%d waits=%v", err, u.calls, *waits)
	}
}

func TestCitations(t *testing.T) {
	u := &upstream{steps: []func(http.ResponseWriter, *http.Request){func(w http.ResponseWriter, r *http.Request) {
		sse(w, content("found"), `data: {"choices":[],"citations":["https://a.gov.cn/x",{"url":"https://b.cn/y"},"https://a.gov.cn/x"]}`, finish, done)
	}}}
	s := httptest.NewServer(u)
	defer s.Close()
	g, _, _ := testGW()
	res, err := g.Chat(context.Background(), []Config{cfg(1, s.URL)}, req, time.Minute)
	if err != nil || len(res.Citations) != 2 {
		t.Fatalf("%v %+v", err, res.Citations)
	}
}

func TestNoConfigs(t *testing.T) {
	g, _, _ := testGW()
	if _, err := g.Chat(context.Background(), nil, req, 0); !errors.Is(err, ErrNoConfig) {
		t.Fatal(err)
	}
}

// Model names must come from config only.
func TestNoHardcodedModelNames(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		low := strings.ToLower(string(b))
		for _, banned := range []string{"grok", "gpt-", "claude", "deepseek", "qwen"} {
			if strings.Contains(low, banned) {
				t.Errorf("%s mentions %q", f, banned)
			}
		}
	}
}
