package quality

import (
	"strings"
	"testing"
)

func TestArticleFixtures(t *testing.T) {
	label := "在家过日子的读者"
	good := Fixture{
		Title:      "两口人把玩具收进一个盒子",
		Summary:    "周末把玩具收进盒子。",
		Paragraphs: []string{strings.Repeat("把这一步写具体。", 12) + "[1]", "下午换一本故事，茶还是温的。"},
		Audience:   label,
		Sources:    1,
		WordTarget: 100,
	}
	if failures := Check(good); len(failures) != 0 {
		t.Fatal(failures)
	}
	phrase := "爷爷奶奶眼睛里面闪闪发光"
	bad := Fixture{
		Title:    "周末早晨，阳光从窗外洒进客厅，你推开门，看到爷爷奶奶捧着温茶转圈，那一小步就让上午亮堂起来。",
		Summary:  label + "，通过家里日常小事也能让晚年好心情。",
		Audience: label,
		Paragraphs: []string{
			label + "，你好！" + phrase + "，孩子也跟着笑。",
			"下午又翻开一本，" + phrase + "，屋里安静下来。",
		},
		Sources:    0,
		WordTarget: 800,
	}
	got := map[string]bool{}
	for _, item := range Check(bad) {
		got[item] = true
	}
	for _, want := range []string{"title", "audience", "length", "sources", "repetition"} {
		if !got[want] {
			t.Fatal(got)
		}
	}
}

func TestColumnStructure(t *testing.T) {
	news := Fixture{
		Title: "本周布线", Paragraphs: []string{"2024年10月，综合布线验收有一条公开说明。[1]", "近日，另一条消息也写进了记录。[1]"},
		Sources: 1, WordTarget: 20, Structure: "news-brief",
	}
	if failures := Check(news); len(failures) != 0 {
		t.Fatal(failures)
	}
	broken := Fixture{
		Title: "快报", Paragraphs: []string{"综合布线验收有一条说明。", "另一条消息没有日期。"},
		Sources: 1, WordTarget: 10, Structure: "news-brief",
	}
	if !has(Check(broken), "structure") {
		t.Fatal(Check(broken))
	}
	steps := Fixture{Title: "步骤", Paragraphs: []string{"先看现场。", "再核对编号。"}, Sources: 1, WordTarget: 10, Structure: "steps"}
	if !has(Check(steps), "structure") {
		t.Fatal(Check(steps))
	}
	faq := Fixture{Title: "问答", Paragraphs: []string{"验收要看什么。", "对照图纸。"}, Sources: 1, WordTarget: 10, Structure: "Q&A"}
	if !has(Check(faq), "structure") {
		t.Fatal(Check(faq))
	}
	draft := map[string]any{
		"sources":  []any{map[string]any{"url": "https://www.gov.cn/a"}},
		"sections": []any{map[string]any{"paragraphs": []any{"综合布线验收有一条说明。", "另一条消息没有日期。"}}},
	}
	fixed := RepairDraft("news-brief", draft)
	var paras []string
	section := fixed["sections"].([]any)[0].(map[string]any)
	for _, item := range section["paragraphs"].([]any) {
		paras = append(paras, item.(string))
	}
	if paras[0] != "综合布线验收有一条说明。" || paras[1] != "另一条消息没有日期。" || StructureOK("news", paras) {
		t.Fatal(paras)
	}
	stepDraft := RepairDraft("steps", map[string]any{"sections": []any{map[string]any{"paragraphs": []any{"先看现场。", "再核对编号。"}}}})
	stepSection := stepDraft["sections"].([]any)[0].(map[string]any)
	if stepSection["paragraphs"].([]any)[0] != "先看现场。" {
		t.Fatal(stepSection)
	}
	qaDraft := RepairDraft("qa", map[string]any{"sections": []any{map[string]any{"paragraphs": []any{"验收要看什么", "对照图纸"}}}})
	qaSection := qaDraft["sections"].([]any)[0].(map[string]any)
	joined := qaSection["paragraphs"].([]any)[0].(string) + qaSection["paragraphs"].([]any)[1].(string)
	if strings.Contains(joined, "？") || strings.Contains(joined, "答") {
		t.Fatal(joined)
	}
	repeat := Fixture{Title: "快报", Paragraphs: []string{"近日，第一条。[1]", "近日，第二条。[1]"}, Sources: 1, WordTarget: 10, Structure: "news"}
	if !has(Check(repeat), "structure") {
		t.Fatal(Check(repeat))
	}
	repaired := RepairDraft("news", map[string]any{
		"sources":  []any{map[string]any{"url": "https://www.gov.cn/a"}},
		"sections": []any{map[string]any{"paragraphs": []any{"近日，第一条。", "近日，第二条。"}}},
	})
	varied := repaired["sections"].([]any)[0].(map[string]any)["paragraphs"].([]any)
	if varied[0] != "近日，第一条。" || varied[1] != "近日，第二条。" || strings.Contains(varied[0].(string), "[1]") {
		t.Fatal(varied)
	}
	stray := Fixture{Title: "问答", Paragraphs: []string{"验收要看什么？", "答：这样做对吗？"}, Sources: 1, WordTarget: 10, Structure: "qa"}
	if !has(Check(stray), "structure") {
		t.Fatal(Check(stray))
	}
	orphan := Fixture{Title: "问答", Paragraphs: []string{"答：直接给结论。"}, Sources: 1, WordTarget: 8, Structure: "qa"}
	if !has(Check(orphan), "structure") {
		t.Fatal(Check(orphan))
	}
}

func has(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
