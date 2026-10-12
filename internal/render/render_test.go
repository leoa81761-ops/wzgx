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
	if !strings.Contains(html, "参考来源") || !strings.Contains(html, "示例媒体") || !strings.Contains(html, "一条报道") {
		t.Fatalf("source line mismatch: %s", html)
	}
	if strings.Contains(html, "www.example.com") || strings.Contains(html, "www点example点com") || strings.Contains(html, "点") {
		t.Fatal(html)
	}
	if !strings.Contains(html, "#1f6a4d") {
		t.Fatal("fresh color missing")
	}
	tabled := RenderHTML(map[string]any{
		"sections": []any{map[string]any{
			"heading": "对照",
			"table": map[string]any{
				"headers": []any{"做法", "说明"},
				"rows":    []any{[]any{"先做 <b>", "记下"}},
			},
			"image_url": "https://mmbiz.qpic.cn/a.png",
		}},
	}, "card", false, false)
	if !strings.Contains(tabled, "<table") || strings.Contains(tabled, "<b>") || strings.Contains(tabled, "class=") {
		t.Fatal(tabled)
	}
	if !strings.Contains(tabled, "https://mmbiz.qpic.cn/a.png") {
		t.Fatal(tabled)
	}
	wide := RenderHTML(map[string]any{
		"sections": []any{map[string]any{
			"heading": "宽表",
			"table": map[string]any{
				"headers": []any{"甲", "乙", "丙", "丁", "戊"},
				"rows":    []any{[]any{"1", "2", "3", "4", "5"}},
			},
		}},
	}, "simple", false, false)
	if strings.Contains(wide, "<table") || !strings.Contains(wide, "甲：1") {
		t.Fatal(wide)
	}
	if DisplayURL("a.b.c") != "a点b点c" {
		t.Fatal(DisplayURL("a.b.c"))
	}
	labeled := RenderHTML(map[string]any{"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"乙"}}}, "ai_label": true}, "simple", false, false)
	if !strings.Contains(labeled, "本文由人工智能辅助生成") {
		t.Fatal(labeled)
	}
}
