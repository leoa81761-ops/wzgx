package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestParseSearchBodyReadsJSONAndHTML(t *testing.T) {
	jsonRows := ParseSearchBody([]byte(`{"results":[{"title":"弱电智能化验收","url":"https://www.mohurd.gov.cn/a"},{"title":"跳过","url":"https://duckduckgo.com/skip"}]}`))
	if len(jsonRows) != 1 || jsonRows[0]["url"] != "https://www.mohurd.gov.cn/a" {
		t.Fatal(jsonRows)
	}
	htmlRows := ParseSearchBody([]byte(`<a href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fwww.gov.cn%2Fzhengce%2F1&amp;rut=1">`))
	if len(htmlRows) != 1 || htmlRows[0]["url"] != "https://www.gov.cn/zhengce/1" {
		t.Fatal(htmlRows)
	}
	bing := ParseSearchBody([]byte(`<li class="b_algo"><h2><a href="https://www.mohurd.gov.cn/a">综合布线验收</a></h2><p>验收要核对回路。</p></li>`))
	if len(bing) != 1 || bing[0]["title"] != "综合布线验收" || bing[0]["snippet"] != "验收要核对回路。" {
		t.Fatal(bing)
	}
	sogou := ParseSearchBody([]byte(`<h3 class="vr-title"><a href="https://www.gov.cn/a">职场成长通知</a></h3><p>公开说明。</p>`))
	if len(sogou) != 1 || sogou[0]["url"] != "https://www.gov.cn/a" || sogou[0]["title"] != "职场成长通知" {
		t.Fatal(sogou)
	}
	baidu := ParseSearchBody([]byte(`<div mu="https://www.mohurd.gov.cn/b"><h3><a href="https://www.baidu.com/link?url=abc">布线标准</a></h3>验收摘要</div>`))
	if len(baidu) != 1 || baidu[0]["url"] != "https://www.mohurd.gov.cn/b" || baidu[0]["title"] != "布线标准" {
		t.Fatal(baidu)
	}
	bocha := ParseSearchBody([]byte(`{"data":{"webPages":{"value":[{"name":"弱电验收","url":"https://www.gov.cn/b","snippet":"公开说明"}]}}}`))
	if len(bocha) != 1 || bocha[0]["url"] != "https://www.gov.cn/b" || bocha[0]["snippet"] != "公开说明" {
		t.Fatal(bocha)
	}
}

func TestSearchChainFallsBackFromBing(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		switch r.URL.Path {
		case "/bing":
			http.Error(w, "down", 502)
		case "/sogou":
			if r.URL.Query().Get("q") == "" && r.URL.Query().Get("query") == "" {
				t.Errorf("missing query %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`<h3 class="vr-title"><a href="https://www.gov.cn/a">综合布线验收</a></h3>`))
		case "/bocha":
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Errorf("auth %s", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"data":{"webPages":{"value":[{"name":"弱电验收","url":"https://www.mohurd.gov.cn/a","snippet":"验收"}]}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	rows, err := SearchChain(context.Background(), srv.Client(), []SearchTarget{
		{URL: srv.URL + "/bing"},
		{URL: srv.URL + "/sogou"},
	}, "综合布线验收")
	if err != nil || len(rows) != 1 || rows[0]["url"] != "https://www.gov.cn/a" {
		t.Fatal(rows, err, hits)
	}
	if !strings.Contains(strings.Join(hits, ","), "/bing") || !strings.Contains(strings.Join(hits, ","), "/sogou") {
		t.Fatal(hits)
	}
	api, err := SearchChain(context.Background(), srv.Client(), []SearchTarget{{URL: srv.URL + "/bocha", APIKey: "secret"}}, "综合布线")
	if err != nil || len(api) != 1 || api[0]["url"] != "https://www.mohurd.gov.cn/a" {
		t.Fatal(api, err)
	}
}

func TestSearchQueryParamFollowsTheEngine(t *testing.T) {
	sogou := url.Values{}
	setSearchQuery("www.sogou.com", sogou, "验收")
	if sogou.Get("query") != "验收" {
		t.Fatal(sogou)
	}
	baidu := url.Values{}
	setSearchQuery("www.baidu.com", baidu, "验收")
	if baidu.Get("wd") != "验收" {
		t.Fatal(baidu)
	}
	bing := url.Values{}
	setSearchQuery("cn.bing.com", bing, "验收")
	if bing.Get("q") != "验收" {
		t.Fatal(bing)
	}
}
