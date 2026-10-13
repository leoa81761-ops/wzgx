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
	if strings.Contains(html, "导语") {
		t.Fatal("summary is not rendered as the lead paragraph")
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
	if !strings.Contains(wide, "<table") || !strings.Contains(wide, "甲") || strings.Contains(wide, "甲：1") {
		t.Fatal(wide)
	}
	qa := RenderHTML(map[string]any{
		"sections": []any{map[string]any{"paragraphs": []any{"验收要看什么？答：先对照图纸。", "材料怎么算？", "答：按现场清单。"}}},
	}, "simple", false, false)
	if !strings.Contains(qa, "<strong>验收要看什么？</strong>") || !strings.Contains(qa, "先对照图纸。") || strings.Contains(qa, "答：") {
		t.Fatal(qa)
	}
	if !strings.Contains(qa, "<strong>材料怎么算？</strong>") || !strings.Contains(qa, "按现场清单。") {
		t.Fatal(qa)
	}
	if DisplayURL("a.b.c") != "a点b点c" {
		t.Fatal(DisplayURL("a.b.c"))
	}
	labeled := RenderHTML(map[string]any{"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"乙"}}}, "ai_label": true}, "simple", false, false)
	if !strings.Contains(labeled, "本文由人工智能辅助生成") {
		t.Fatal(labeled)
	}
	further := RenderHTML(map[string]any{
		"source_heading": "延伸阅读",
		"sources":        []any{map[string]any{"name": "示例媒体", "title": "一条报道"}},
	}, "simple", false, false)
	if !strings.Contains(further, "延伸阅读") || strings.Contains(further, "参考来源") {
		t.Fatal(further)
	}
	labeledColumn := RenderHTML(map[string]any{
		"column_label": "工程圈快报",
		"sections":     []any{map[string]any{"heading": "甲", "paragraphs": []any{"乙"}}},
	}, "simple", false, false)
	if !strings.Contains(labeledColumn, "栏目 · 工程圈快报") {
		t.Fatal(labeledColumn)
	}
}
