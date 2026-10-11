package render

import (
	"strings"
	"testing"
)

func TestRendererInlinesStylesAndRewritesURLs(t *testing.T) {
	html := RenderHTML(map[string]any{
		"summary": "导语",
		"sections": []any{map[string]any{
			"heading": "小节 <b>", "paragraphs": []any{"正文"}, "list": []any{"一条"},
		}},
		"closing": "结尾",
		"sources": []any{map[string]any{
			"name": "示例媒体", "title": "一条报道", "published_on": "2026-01-01", "url": "https://www.example.com/a.b",
		}},
		"footnotes": []any{"请发布前审阅。"},
	}, "fresh", false, false)
	if !strings.Contains(html, "style=") || strings.Contains(html, "class=") || strings.Contains(html, "<b>") {
		t.Fatalf("style/escape mismatch: %s", html)
	}
	if !strings.Contains(html, "参考来源") || strings.Contains(html, "www.example.com") {
		t.Fatalf("source rewrite mismatch: %s", html)
	}
	if !strings.Contains(html, "www点example点com/a点b") {
		t.Fatal(html)
	}
	if !strings.Contains(html, "#1f6a4d") {
		t.Fatal("fresh color missing")
	}
	if DisplayURL("a.b.c") != "a点b点c" {
		t.Fatal(DisplayURL("a.b.c"))
	}
}
