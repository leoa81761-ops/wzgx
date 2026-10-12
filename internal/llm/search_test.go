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

func TestPageMetaReadsHTMLTitleAndDescription(t *testing.T) {
	title, desc := pageMeta(`<html><head><title>布线施工记录</title><meta name="description" content="弱电验收要看的公开说明"></head><body>page</body></html>`)
	if title != "布线施工记录" || desc != "弱电验收要看的公开说明" {
		t.Fatal(title, desc)
	}
	title, desc = pageMeta(`<html><head><meta property="og:description" content="综合布线的条文说明"><title></title></head></html>`)
	if title != "" || desc != "综合布线的条文说明" {
		t.Fatal(title, desc)
	}
	title, desc = pageMeta(`<html><head><title>奶奶养生茶</title><meta name="description" content="周末喝茶转圈"></head></html>`)
	if title != "奶奶养生茶" || desc != "周末喝茶转圈" {
		t.Fatal(title, desc)
	}
	if !htmlContent("text/html; charset=utf-8") || htmlContent("application/json") || htmlContent("text/plain") || htmlContent("application/xhtml+xml") || htmlContent("") {
		t.Fatal("content type")
	}
}

func TestFetchPageMetaRefusesLoopback(t *testing.T) {
	var hits int
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>布线施工记录</title></head></html>`))
	}))
	defer page.Close()
	title, desc := FetchPageMeta(t.Context(), page.Client(), page.URL+"/std")
	if title != "" || desc != "" || hits != 0 {
		t.Fatalf("title %q desc %q hits %d", title, desc, hits)
	}
	title, desc = FetchPageMeta(t.Context(), http.DefaultClient, "http://127.0.0.1/secret")
	if title != "" || desc != "" {
		t.Fatal(title, desc)
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
