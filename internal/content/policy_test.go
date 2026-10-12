package content

import (
	"fmt"
	"strings"
	"testing"
)

func TestTrimToMaxCutsBodyOnly(t *testing.T) {
	paragraph := strings.Repeat("甲", 500)
	draft := map[string]any{
		"title":   "标题不动",
		"summary": "摘要不动",
		"sections": []any{map[string]any{
			"heading":    "小节",
			"paragraphs": []any{paragraph},
			"list":       []any{"乙乙"},
		}},
		"closing": strings.Repeat("丙", 100),
	}
	if BodyRunes(draft) != 604 {
		t.Fatalf("runes %d", BodyRunes(draft))
	}
	out := TrimToMax(draft, 400)
	if BodyRunes(out) > 400 || BodyRunes(out) == 0 {
		t.Fatalf("trimmed %d", BodyRunes(out))
	}
	if out["title"] != "标题不动" || out["summary"] != "摘要不动" {
		t.Fatalf("%#v", out)
	}
	if BodyRunes(draft) != 604 {
		t.Fatal("original draft was mutated")
	}
}

func TestTrimToMaxCountsTableCells(t *testing.T) {
	cell := strings.Repeat("格", 500)
	draft := map[string]any{
		"title":   "标题",
		"summary": "摘要不计入",
		"sections": []any{map[string]any{
			"heading":    "小标题",
			"paragraphs": []any{"段落"},
			"list":       []any{},
			"table": map[string]any{
				"headers": []any{"项"},
				"rows":    []any{[]any{cell}},
			},
		}},
		"closing": "结尾",
	}
	if BodyRunes(draft) != 3+2+1+500+2 {
		t.Fatalf("runes %d", BodyRunes(draft))
	}
	out := TrimToMax(draft, 40)
	if BodyRunes(out) > 40 || BodyRunes(out) == 0 {
		t.Fatalf("trimmed %d", BodyRunes(out))
	}
	if out["summary"] != "摘要不计入" || out["title"] != "标题" {
		t.Fatalf("%#v", out)
	}
	if strings.Contains(flatten(out), cell) {
		t.Fatal("full table cell survived the trim")
	}
}

func TestSourcePolicy(t *testing.T) {
	policy := DefaultSourcePolicy()
	for _, raw := range []string{"https://www.xilu.com/a", "https://news.fyun.com/x", "http://huanancj.com/p"} {
		if policy.AllowsURL(raw) {
			t.Fatalf("blocked %s", raw)
		}
	}
	if !policy.AllowsURL("https://www.gov.cn/zhengce") || !policy.AllowsURL("") {
		t.Fatal("official and empty urls should stay")
	}
	allow := SourcePolicy{Allow: []string{"https://www.gov.cn/path"}}
	if allow.AllowsURL("https://news.example.com/a") || !allow.AllowsURL("https://www.gov.cn/a") {
		t.Fatal("allowlist")
	}
	filtered := policy.Filter([]map[string]any{
		{"url": "https://www.xilu.com/a", "title": "转载"},
		{"url": "https://www.gov.cn/zhengce", "title": "原文"},
	})
	if len(filtered) != 1 || filtered[0]["title"] != "原文" {
		t.Fatalf("%#v", filtered)
	}
	cleared := PolicyFromParams(map[string]any{"block": []any{}, "allow": []any{}}, true)
	if !cleared.AllowsURL("https://xilu.com/a") {
		t.Fatal("saved empty blocklist should not use defaults")
	}
	defaults := PolicyFromParams(nil, false)
	if defaults.AllowsURL("https://xilu.com/a") {
		t.Fatal("missing config uses the default blocklist")
	}
	strict := SourcePolicy{Block: []string{"xilu.com"}, Allow: []string{"gov.cn"}}
	kept, dropped := strict.Split([]map[string]any{
		{"url": "https://www.xilu.com/a", "title": "转载"},
		{"url": "https://news.example.com/a", "title": "媒体"},
		{"url": "https://www.gov.cn/zhengce", "title": "原文"},
	})
	if len(kept) != 1 || kept[0]["title"] != "原文" || len(dropped) != 2 {
		t.Fatalf("kept %#v dropped %#v", kept, dropped)
	}
	softKept, softDropped := strict.Relaxed().Split([]map[string]any{
		{"url": "https://www.xilu.com/a", "title": "转载"},
		{"url": "https://news.example.com/a", "title": "媒体"},
	})
	if len(softKept) != 1 || softKept[0]["title"] != "媒体" || len(softDropped) != 1 || softDropped[0]["reason"] != "屏蔽域名" {
		t.Fatalf("relaxed %#v %#v", softKept, softDropped)
	}
}

func TestSourceQuality(t *testing.T) {
	policy := DefaultSourcePolicy()
	junk := []string{
		"https://webstore.example.com/a",
		"https://helpdesk-a.example.com/ticket",
		"https://shop.example.com/item",
		"https://example.com/store/sku",
		"https://example.com/shop/1",
		"https://example.com/cart",
		"https://example.com/login",
		"https://example.com/signin",
		"https://example.com/checkout",
	}
	for _, raw := range junk {
		kept, dropped := policy.Split([]map[string]any{{"url": raw, "title": "店"}})
		if len(kept) != 0 || len(dropped) != 1 || dropped[0]["reason"] != "网店或登录页" {
			t.Fatalf("%s kept %#v dropped %#v", raw, kept, dropped)
		}
	}
	if reason := policy.dropReason("https://example.com/storage/manual"); reason != "" {
		t.Fatalf("storage page dropped: %s", reason)
	}
	filtered := policy.Filter([]map[string]any{
		{"title": "无网址"},
		{"url": "https://webstore.example.com/a", "title": "店"},
		{"url": "https://www.gov.cn/zhengce", "title": "原文"},
	})
	if len(filtered) != 2 || filtered[0]["title"] != "无网址" || filtered[1]["title"] != "原文" {
		t.Fatalf("%#v", filtered)
	}
	sources := []map[string]any{
		{"url": "https://news.example.com/1", "title": "一"},
		{"url": "https://news.example.com/2", "title": "二"},
		{"url": "https://www.news.example.com/3", "title": "三"},
		{"url": "https://english.example.org/d", "title": "en"},
		{"url": "https://www.gov.cn/a", "title": "国务院"},
		{"url": "https://www.mohurd.gov.cn/b", "title": "GB 50311 综合布线"},
		{"url": "https://openstd.samr.gov.cn/c", "title": "国家标准"},
	}
	out := PreferSources(sources, "elv")
	hosts := map[string]int{}
	for _, source := range out {
		hosts[sourceHost(asText(source["url"]))]++
	}
	if hosts["news.example.com"] != 2 || hosts["gov.cn"] != 1 || hosts["mohurd.gov.cn"] != 1 || hosts["openstd.samr.gov.cn"] != 1 {
		t.Fatalf("hosts %#v out %#v", hosts, out)
	}
	if !strings.Contains(asText(out[0]["url"]), "mohurd.gov.cn") {
		t.Fatalf("first %#v", out[0])
	}
	for _, domain := range []string{"bzko.com", "yuanlin8.com", "bbs.co188.com", "ireader.com", "abook.qq.com", "gcs66.com", "bzfyw.com", "baijiantest.com"} {
		if policy.AllowsURL("https://" + domain + "/a") {
			t.Fatalf("default blocklist should drop %s", domain)
		}
	}
	cleared := PolicyFromParams(map[string]any{"block": []any{}, "allow": []any{}}, true)
	if !cleared.AllowsURL("https://bzko.com/a") {
		t.Fatal("saved empty blocklist stays editable")
	}
	var many []map[string]any
	for i := 0; i < 10; i++ {
		many = append(many, map[string]any{"url": fmt.Sprintf("https://bzko.com/%d", i), "title": "镜像"})
	}
	many = append(many,
		map[string]any{"url": "https://www.example.com/shop/1", "title": "网店"},
		map[string]any{"url": "https://www.mohurd.gov.cn/gongkai/gb", "title": "GB 50311 综合布线"},
		map[string]any{"url": "https://openstd.samr.gov.cn/bzgk/std", "title": "国家标准"},
		map[string]any{"url": "https://www.people.com.cn/n", "title": "人民日报"},
	)
	kept, dropped := policy.Split(many)
	if len(dropped) < 10 {
		t.Fatalf("dropped %#v", dropped)
	}
	ranked := PreferSources(kept, "zh")
	if len(ranked) == 0 || !strings.Contains(asText(ranked[0]["url"]), "mohurd.gov.cn") {
		t.Fatalf("ranked %#v", ranked)
	}
	for _, source := range ranked {
		if strings.Contains(asText(source["url"]), "bzko.com") || strings.Contains(asText(source["url"]), "/shop/") {
			t.Fatalf("junk survived %#v", ranked)
		}
	}
	if len(ranked) > 8 {
		t.Fatalf("capped %d", len(ranked))
	}
	var crowded []map[string]any
	for i := 0; i < 10; i++ {
		crowded = append(crowded, map[string]any{"url": fmt.Sprintf("https://site%d.example.com/a", i), "title": "普通"})
	}
	crowded = append(crowded, map[string]any{"url": "https://std.samr.gov.cn/gb", "title": "国家标准"})
	rankedAll := PreferSources(crowded, "zh")
	if len(rankedAll) != 8 || !strings.Contains(asText(rankedAll[0]["url"]), "std.samr.gov.cn") {
		t.Fatalf("rank every citation then keep 8: %#v", rankedAll)
	}
}
