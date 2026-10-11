package content

import (
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
	if BodyRunes(draft) != 602 {
		t.Fatalf("runes %d", BodyRunes(draft))
	}
	out := TrimToMax(draft, 400)
	if BodyRunes(out) > 400 || BodyRunes(out) == 0 {
		t.Fatalf("trimmed %d", BodyRunes(out))
	}
	if out["title"] != "标题不动" || out["summary"] != "摘要不动" {
		t.Fatalf("%#v", out)
	}
	if BodyRunes(draft) != 602 {
		t.Fatal("original draft was mutated")
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
}
