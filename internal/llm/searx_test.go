package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/content"
)

func TestPlanTargetsPutsUserTavilyThenSearxng(t *testing.T) {
	user := PlanTargets(ChainOptions{
		UserTavilyKey: "user-key-aaaa", UserTavilyURL: "http://user.example/search",
		SearxngURL: "http://127.0.0.1:8080", PlatformTavilyKey: "platform-key",
		Provider: "bocha", APIKey: "bocha-key", APIURL: "https://api.bochaai.com/v1/web-search",
		Bing: "https://cn.bing.com/search", Sogou: "https://www.sogou.com/web", Baidu: "https://www.baidu.com/s",
	})
	got := make([]string, len(user))
	for i, target := range user {
		got[i] = target.Provider
		if target.APIKey != "" && target.APIKey != "user-key-aaaa" {
			t.Fatal("platform key leaked into a user chain", target.Provider, target.APIKey)
		}
		if target.Owner == "platform" {
			t.Fatal(target)
		}
	}
	if strings.Join(got, ",") != "tavily,searxng,bing,sogou,baidu" || user[0].Owner != "user" {
		t.Fatal(got, user[0].Owner)
	}
	other := PlanTargets(ChainOptions{UserTavilyKey: "user-key-bbbb", SearxngURL: "http://127.0.0.1:8080"})
	if other[0].APIKey != "user-key-bbbb" || user[0].APIKey == other[0].APIKey {
		t.Fatal(user[0].APIKey, other[0].APIKey)
	}
	plain := PlanTargets(ChainOptions{
		SearxngURL: "http://127.0.0.1:8080", PlatformTavilyKey: "platform-key",
		Provider: "tavily", APIKey: "platform-key",
		Bing: "https://cn.bing.com/search",
	})
	names := make([]string, len(plain))
	for i, target := range plain {
		names[i] = target.Provider + ":" + target.Owner
	}
	if strings.Join(names, ",") != "searxng:,tavily:platform,bing:" {
		t.Fatal(names)
	}
}

func TestSearxngJSONCacheCircuitAndPrivateResults(t *testing.T) {
	var hits []string
	secret := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, "secret")
		w.WriteHeader(http.StatusOK)
	}))
	defer secret.Close()
	var searches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searches++
		hits = append(hits, r.URL.Path)
		if r.URL.Query().Get("format") != "json" || r.URL.Query().Get("q") == "" {
			t.Errorf("query %s", r.URL.RawQuery)
		}
		if !strings.Contains(r.URL.RawQuery, "engines=360search,sogou,sogou%20wechat") {
			t.Errorf("engines %s", r.URL.RawQuery)
		}
		if strings.Contains(r.URL.RawQuery, "bing") || strings.Contains(r.URL.RawQuery, "baidu") {
			t.Errorf("engines %s", r.URL.RawQuery)
		}
		if searches <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
			{"title": "内部", "url": secret.URL + "/secret", "content": "不要抓"},
			{"title": "综合布线验收说明", "url": "https://www.gov.cn/zhengce/a", "content": "公开说明"},
		}})
	}))
	defer srv.Close()
	guard := NewSearxGuard()
	guard.breaker = 2
	guard.maxWait = time.Millisecond
	guard.rate = 100
	guard.burst = 5
	guard.tokens = 5
	rows, meta, err := guard.Search(context.Background(), srv.Client(), srv.URL, "综合布线", 1)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatal(rows, err)
	}
	_, _, err = guard.Search(context.Background(), srv.Client(), srv.URL, "弱电验收", 1)
	if err == nil {
		t.Fatal("second failure should still be attempted")
	}
	_, metaOpen, err := guard.Search(context.Background(), srv.Client(), srv.URL, "线槽", 1)
	if err == nil || !strings.Contains(err.Error(), "熔断") || metaOpen["provider"] != "searxng" {
		t.Fatal(err, metaOpen)
	}
	if searches != 2 {
		t.Fatal(searches, hits)
	}
	guard.openUntil = time.Time{}
	guard.fails = 0
	rows, meta, err = guard.Search(context.Background(), srv.Client(), srv.URL, "综合布线验收", 1)
	if err != nil || len(rows) != 1 || rows[0]["url"] != "https://www.gov.cn/zhengce/a" {
		t.Fatal(rows, err)
	}
	if meta["cached"] == true {
		t.Fatal(meta)
	}
	before := searches
	rows, meta, err = guard.Search(context.Background(), srv.Client(), srv.URL, "综合布线验收", 2)
	if err != nil || len(rows) != 1 || meta["cached"] != true || searches != before {
		t.Fatal(rows, meta, err, searches, before)
	}
	for _, hit := range hits {
		if hit == "secret" || strings.Contains(hit, "/secret") {
			t.Fatal(hits)
		}
	}
}

func TestSearxngRateLimitAndDailyCap(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = io.WriteString(w, `{"results":[{"title":"综合布线","url":"https://www.gov.cn/a","content":"说明"}]}`)
	}))
	defer srv.Close()
	guard := NewSearxGuard()
	guard.tokens = 1
	guard.burst = 1
	guard.rate = 0.001
	guard.maxWait = time.Millisecond
	guard.updated = time.Now()
	if _, _, err := guard.Search(context.Background(), srv.Client(), srv.URL, "第一次", 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := guard.Search(context.Background(), srv.Client(), srv.URL, "第二次", 0); err == nil || !strings.Contains(err.Error(), "频繁") {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatal(hits)
	}
	capped := NewSearxGuard()
	capped.SetLimits(1, 5)
	capped.rate = 100
	capped.tokens = 5
	capped.burst = 5
	if _, _, err := capped.Search(context.Background(), srv.Client(), srv.URL, "甲", 9); err != nil {
		t.Fatal(err)
	}
	if _, _, err := capped.Search(context.Background(), srv.Client(), srv.URL, "乙", 9); err == nil || !strings.Contains(err.Error(), "今日次数") {
		t.Fatal(err)
	}
	if _, _, err := capped.Search(context.Background(), srv.Client(), srv.URL, "丙", 10); err != nil {
		t.Fatal(err)
	}
}

func TestSearxngRedirectStaysOnItsHost(t *testing.T) {
	secret := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("result host was fetched")
	}))
	defer secret.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secret.URL+"/secret", http.StatusFound)
	}))
	defer srv.Close()
	guard := NewSearxGuard()
	guard.rate = 100
	guard.tokens = 3
	_, _, err := guard.Search(context.Background(), srv.Client(), srv.URL, "综合布线", 0)
	if err == nil || !strings.Contains(err.Error(), "跳转") {
		t.Fatal(err)
	}
}

func TestSearxngAllowlistIgnoresThePublicClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "engines=360search,sogou,sogou%20wechat") {
			t.Errorf("engines %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"results":[{"title":"综合布线验收","url":"https://www.gov.cn/a","content":"公开说明"}]}`)
	}))
	defer srv.Close()
	guard := NewSearxGuard()
	guard.rate = 100
	guard.tokens = 3
	guard.burst = 3
	rows, meta, err := guard.Search(context.Background(), content.PublicClient(), srv.URL, "综合布线", 0)
	if err != nil || len(rows) != 1 || rows[0]["url"] != "https://www.gov.cn/a" {
		t.Fatal(rows, err)
	}
	if meta["provider"] != "searxng" || meta["engines"] != "360search,sogou,sogou wechat" {
		t.Fatal(meta)
	}
}

func TestUserTavilyFailureFallsBackAndRecordsProvider(t *testing.T) {
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tavily":
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			key, _ := body["api_key"].(string)
			keys = append(keys, key)
			if key == "user-key-aaaa" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"error":"invalid api key"}`)
				return
			}
			t.Errorf("unexpected tavily key %s", key)
		case "/search":
			_, _ = io.WriteString(w, `{"results":[{"title":"综合布线验收","url":"https://www.gov.cn/a","content":"公开说明"}]}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	guard := NewSearxGuard()
	guard.rate = 100
	guard.tokens = 5
	guard.burst = 5
	targets := PlanTargets(ChainOptions{
		UserTavilyKey: "user-key-aaaa", UserTavilyURL: srv.URL + "/tavily",
		SearxngURL: srv.URL, PlatformTavilyKey: "platform-key",
		Bing: srv.URL + "/bing",
	})
	for i := range targets {
		if targets[i].Provider == "searxng" {
			targets[i].Guard = guard
			targets[i].UserID = 4
		}
	}
	rows, engines, err := SearchChainLogged(context.Background(), srv.Client(), targets, "综合布线验收")
	if err != nil || len(rows) != 1 || rows[0]["url"] != "https://www.gov.cn/a" {
		t.Fatal(rows, err)
	}
	if len(engines) != 2 || engines[0]["provider"] != "tavily" || engines[0]["owner"] != "user" || engines[0]["key_error"] != "invalid" {
		t.Fatal(engines)
	}
	if engines[1]["provider"] != "searxng" || engines[1]["count"] != 1 {
		t.Fatal(engines)
	}
	blob := strings.Join(keys, ",")
	if blob != "user-key-aaaa" || strings.Contains(blob, "platform-key") {
		t.Fatal(keys)
	}
	raw, _ := json.Marshal(engines)
	if strings.Contains(string(raw), "user-key-aaaa") || strings.Contains(string(raw), "platform-key") {
		t.Fatal(string(raw))
	}
}
