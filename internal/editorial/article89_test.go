package editorial

import (
	"strings"
	"testing"

	"github.com/leoa81761-ops/wzgx/internal/render"
)

func TestArticle89SceneOnceIntroBeforeFAQAndAnswerPrefix(t *testing.T) {
	draft := map[string]any{"sections": []any{
		map[string]any{
			"heading": "综合布线的水晶头怎么压",
			"paragraphs": []any{
				"先把线序摊在桌上。",
				"孙子端着一碗汤走过来。两轮以内改完，一般就过了。先看这一项。答：再看下一项。",
			},
			"qa":   []any{map[string]any{"question": "综合布线的水晶头怎么压？", "answer": "答：先看橙白。"}},
			"list": []any{"再核对绿线。"},
		},
		map[string]any{
			"heading":    "收尾",
			"paragraphs": []any{"孙子端着汤又走过来。孙子端着碗走过来。孙子端着水杯走过来。"},
			"list":       []any{},
		},
	}}
	out := StripVagueQuantities(StripAnswerPrefixes(OnceScene(draft)))
	blob := dump(out)
	if strings.Count(blob, "孙子端着") != 1 || !strings.Contains(blob, "孙子端着一碗汤") {
		t.Fatal(blob)
	}
	if strings.Contains(blob, "答：") || strings.Contains(blob, "两轮以内") || strings.Contains(blob, "一般就过了") {
		t.Fatal(blob)
	}
	html := RepairMarkup(render.RenderHTML(out, "simple", false, false))
	intro := strings.Index(html, "先把线序摊在桌上")
	answer := strings.Index(html, "先看橙白")
	list := strings.Index(html, "再核对绿线")
	if intro < 0 || answer < 0 || list < 0 || intro > answer || answer > list {
		t.Fatal(html)
	}
	if strings.Count(html, "综合布线的水晶头怎么压") != 1 || strings.Contains(html, "答：") || strings.Contains(html, "两轮以内") {
		t.Fatal(html)
	}
	if !OutlineMisses(map[string]any{"points": []any{
		map[string]any{"heading": "孙子端着汤", "intent": "讲故事"},
		map[string]any{"heading": "再端一次", "intent": "再讲"},
	}}, "综合布线的水晶头怎么压？") {
		t.Fatal("outline should miss the title")
	}
	if OutlineMisses(map[string]any{"points": []any{
		map[string]any{"heading": "综合布线的水晶头怎么压", "intent": "说明线序"},
		map[string]any{"heading": "综合布线测完再收", "intent": "收尾"},
	}}, "综合布线的水晶头怎么压？") {
		t.Fatal("second outline covers the title")
	}
}

func TestArticle90FlattenedTableRendersInFinalHTML(t *testing.T) {
	draft := map[string]any{"sections": []any{map[string]any{
		"heading":    "线序",
		"paragraphs": []any{"1. | 项目 | 说明 | | --- | --- | | 线序 | 按568B |"},
		"list":       []any{},
	}}}
	shaped := ShapeCopy(draft)
	section := shaped["sections"].([]any)[0].(map[string]any)
	table, _ := section["table"].(map[string]any)
	if table == nil {
		t.Fatal(section)
	}
	html := RepairMarkup(render.RenderHTML(shaped, "simple", false, false))
	if !strings.Contains(html, "<table") || !strings.Contains(html, "<td") || !strings.Contains(html, "按568B") || strings.Contains(html, "| 项目 |") {
		t.Fatal(html)
	}
	md := render.Markdown(shaped)
	if !strings.Contains(md, "| 项目 | 说明 |") || !strings.Contains(md, "| 线序 | 按568B |") {
		t.Fatal(md)
	}
	cell := StripVagueQuantities(map[string]any{"sections": []any{map[string]any{
		"heading": "线序",
		"table": map[string]any{
			"headers": []any{"项目", "说明"},
			"rows":    []any{[]any{"线序", "两轮以内"}},
		},
		"paragraphs": []any{},
		"list":       []any{},
	}}})
	kept := cell["sections"].([]any)[0].(map[string]any)["table"].(map[string]any)
	flat := dump(kept)
	if strings.Contains(flat, "两轮以内") {
		t.Fatal(flat)
	}
}
