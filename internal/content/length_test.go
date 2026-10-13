package content

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestShortOfTargetUsesChineseCharacters(t *testing.T) {
	if utf8.RuneCountInString("住宅综合布线") != 6 || len("住宅综合布线") == 6 {
		t.Fatal("a Chinese character is one rune, not one byte")
	}
	short := map[string]any{
		"title":    "不计入标题",
		"summary":  "不计入摘要",
		"sections": []any{map[string]any{"paragraphs": []any{strings.Repeat("甲", 1080)}}},
		"sources":  []any{map[string]any{"title": "不计入来源"}},
	}
	if BodyRunes(short) != 1080 {
		t.Fatal(BodyRunes(short))
	}
	if !ShortOfTarget(BodyRunes(short), 1500) {
		t.Fatal("1080 characters must fail a 1500 target")
	}
	if ShortOfTarget(1275, 1500) {
		t.Fatal("85% of 1500 is enough")
	}
	if ShortOfTarget(0, 0) {
		t.Fatal("no target")
	}
	withBlocks := map[string]any{
		"sections": []any{map[string]any{
			"heading":    "标题",
			"paragraphs": []any{"甲"},
			"list":       []string{"列表文字"},
			"table": map[string]any{
				"headers": []string{"表头"},
				"rows":    []any{[]string{"单元格"}},
			},
		}},
	}
	// 标题2 + 甲1 + 列表文字4 + 表头2 + 单元格3
	if BodyRunes(withBlocks) != 12 {
		t.Fatal(BodyRunes(withBlocks))
	}
}
