package editorial

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/content"
)

// Article 86 died in the topic stage. The retry lives on the pipeline.
// Articles 87 and 88 were exported with the wrong subject, a stub body, a doubled closing, and editor instructions.

func TestArticle87TitleCoverageAndVagueQuantities(t *testing.T) {
	sections := make([]any, 0, 5)
	for i := 0; i < 5; i++ {
		text := "先把现场的步骤做完，再记下结果。"
		if i == 0 {
			text = "招标文件先看投标人须知，再对编号。"
		}
		sections = append(sections, map[string]any{
			"heading": "小节", "paragraphs": []any{text}, "list": []any{},
		})
	}
	draft := map[string]any{"title": "招标文件怎么核对", "sections": sections}
	if !OffTitle(draft) {
		t.Fatal("one of five sections on 招标 should be off the title")
	}
	for i := 1; i < 3; i++ {
		sections[i].(map[string]any)["paragraphs"] = []any{"招标文件这一页先核对编号。"}
	}
	if OffTitle(draft) {
		t.Fatal("three of five sections is the 60 percent line")
	}
	vague := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{
		"24小时内交卷。两轮以内改完。我见过的项目里大多两轮以内。",
	}, "list": []any{}}}}
	out := StripVagueQuantities(vague)
	blob := textOf(out["sections"].([]any)[0].(map[string]any)["paragraphs"].([]any)[0])
	if strings.Contains(blob, "24小时内") || strings.Contains(blob, "两轮以内改完") || !strings.Contains(blob, "我见过的项目里大多两轮以内") {
		t.Fatal(blob)
	}
}

func TestArticle88DedupesClosingStripsLeakAndFlagsStub(t *testing.T) {
	line := "把编号核对完就可以收工。"
	body := "先看图纸。再对编号。记下读数。收好工具。核对现场。整理记录。放回仪器。关好柜门。"
	draft := map[string]any{
		"title": "验收收工",
		"sections": []any{map[string]any{
			"heading":    "收工",
			"paragraphs": []any{body + line + "只需写这一段，不用列公式。"},
			"list":       []any{},
		}},
		"closing": line,
	}
	if utf8.RuneCountInString(body+line) >= 600 {
		t.Fatal("fixture should stay a stub")
	}
	if !PromptLeak(draft) {
		t.Fatal("instruction should be visible before the strip")
	}
	stripped := StripPromptLeak(draft)
	para := textOf(stripped["sections"].([]any)[0].(map[string]any)["paragraphs"].([]any)[0])
	if strings.Contains(para, "只需写") || strings.Contains(para, "不用列公式") || PromptLeak(stripped) {
		t.Fatal(para)
	}
	deduped := DedupeSentences(stripped)
	if strings.TrimSpace(textOf(deduped["closing"])) != "" {
		t.Fatal(deduped["closing"])
	}
	kept := textOf(deduped["sections"].([]any)[0].(map[string]any)["paragraphs"].([]any)[0])
	if !strings.Contains(kept, line) {
		t.Fatal(kept)
	}
	if content.BodyRunes(deduped) >= 600 || !BelowFloor(deduped) {
		t.Fatalf("runes %d", content.BodyRunes(deduped))
	}
}

func TestFAQMovesIntoStructuredPairs(t *testing.T) {
	draft := map[string]any{"sections": []any{map[string]any{
		"heading": "问答",
		"paragraphs": []any{
			"招标文件看哪一页？",
			"答：先看投标人须知。",
			"答：",
		},
		"list": []any{},
	}}}
	out := MaterializeQA(draft)
	section := out["sections"].([]any)[0].(map[string]any)
	qa := section["qa"].([]any)
	if len(qa) != 1 {
		t.Fatal(section)
	}
	row := qa[0].(map[string]any)
	if row["question"] != "招标文件看哪一页？" || row["answer"] != "先看投标人须知。" {
		t.Fatal(row)
	}
	for _, item := range section["paragraphs"].([]any) {
		if strings.Contains(textOf(item), "答") {
			t.Fatal(section["paragraphs"])
		}
	}
}
