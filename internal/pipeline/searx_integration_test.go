package pipeline

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/content"
	"github.com/leoa81761-ops/wzgx/internal/llm"
)

func TestFetchResearchCallsSearxngWhenTheModelAlreadyHasSources(t *testing.T) {
	var searches int
	var rawQuery string
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searches++
		rawQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"results":[
			{"title":"综合布线验收公开说明","url":"https://www.gov.cn/zhengce/searx","content":"综合布线验收要先核对线槽"},
			{"title":"综合布线验收电视剧","url":"https://www.iqiyi.com/v/1","content":"在线观看"},
			{"title":"综合布线验收视频","url":"https://www.youtube.com/watch?v=1","content":"视频"},
			{"title":"综合布线验收工具","url":"https://item.taobao.com/item.htm?id=1","content":"网店"},
			{"title":"综合布线标准下载","url":"https://std.samr.gov.cn/gb/download/1","content":"下载页"}
		]}`)
	}))
	defer searx.Close()
	research := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		if stageOf(req) == "来源判断" {
			writeSourceJudge(w, req)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"检索到两条公开说明。"}}],"citations":[
			{"url":"https://www.mohurd.gov.cn/gongkai/1","title":"综合布线验收说明","snippet":"综合布线验收要核对材料"},
			{"url":"https://www.gov.cn/zhengce/2","title":"综合布线验收记录","snippet":"综合布线验收的现场记录"}
		]}`))
	}))
	defer research.Close()
	gateway := llm.NewGateway(false, research.Client())
	gateway.SearxngURL = searx.URL
	gateway.ResearchTimeout = 5 * time.Second
	gateway.MaxAttempts = 1
	guard := gateway.EnsureSearx()
	guard.SetLimits(0, 5)
	guard.SetPace(100, 10)
	_, sources, completion, err := fetchResearch(t.Context(), gateway, []llm.Provider{{
		Name: "检索", Protocol: "openai", BaseURL: research.URL + "/v1", Model: "grok-4", Timeout: 5 * time.Second,
	}}, map[string]any{
		"topic_title": "综合布线验收", "direction": "弱电智能化", "keywords": "验收",
	}, content.DefaultSourcePolicy(), searchActor{})
	if err != nil {
		t.Fatal(err)
	}
	if searches == 0 || !strings.Contains(rawQuery, "engines=sogou,bing,360search,sogou%20wechat") {
		t.Fatalf("searx searches %d query %s", searches, rawQuery)
	}
	seen := map[string]bool{}
	for _, raw := range sources {
		source, _ := raw.(map[string]any)
		seen[str(source["url"])] = true
	}
	if !seen["https://www.mohurd.gov.cn/gongkai/1"] || !seen["https://www.gov.cn/zhengce/searx"] {
		t.Fatal(seen)
	}
	for _, url := range []string{
		"https://www.iqiyi.com/v/1",
		"https://www.youtube.com/watch?v=1",
		"https://item.taobao.com/item.htm?id=1",
		"https://std.samr.gov.cn/gb/download/1",
	} {
		if seen[url] {
			t.Fatal("kept junk", url, seen)
		}
	}
	found := false
	for _, attempt := range completion.Attempts {
		list, _ := attempt["engines"].([]map[string]any)
		for _, engine := range list {
			if str(engine["provider"]) != "searxng" {
				continue
			}
			if str(engine["engine"]) == "" || str(engine["query"]) == "" || !strings.Contains(str(engine["engines"]), "360search") {
				t.Fatal(engine)
			}
			found = true
		}
	}
	if !found {
		t.Fatal(completion.Attempts)
	}
}
