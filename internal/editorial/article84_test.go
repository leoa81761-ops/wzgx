package editorial

import (
	"strings"
	"testing"

	"github.com/leoa81761-ops/wzgx/internal/render"
)

// Articles 84 and 85 scored 2-6 after post-processing cut words out of the middle of a sentence.
func TestArticle84DropsTheSentenceInsteadOfSplicing(t *testing.T) {
	draft := map[string]any{
		"title":        "现场核对线序",
		"column_label": "方案拆解",
		"sections": []any{
			map[string]any{
				"heading": "拆解：先看线序",
				"paragraphs": []any{
					"接头松动要先做记号。",
					"接头松动再拍一张。",
					"问题出在接头松动本身。衰减超过 3 dB，一半以上的链路要重测。",
					"先从活动范围入手。活动范围不够就停下。",
					"测试表明衰减偏大，活动范围再量一次。",
					"先看，线槽盖板，再固定。活动范围不能靠猜。",
				},
				"list": []any{},
			},
			map[string]any{"heading": "老年人常：", "paragraphs": []any{"这一节不该留下半截标题。"}, "list": []any{}},
		},
	}
	out := CapHanPhrases(draft, 4, 2)
	out = StripHeadingPrefixes(out)
	blob := dump(out)
	for _, bad := range []string{"问题出在本身", "先从入手", "，，", "表明，", "老年人常：", "老年人常:"} {
		if strings.Contains(blob, bad) {
			t.Fatal(bad, blob)
		}
	}
	if strings.Count(blob, "接头松动") > 2 || strings.Count(blob, "活动范围") > 2 {
		t.Fatal(blob)
	}
	if strings.Contains(blob, "问题出在接头松动本身") {
		t.Fatal("third copy should be dropped whole", blob)
	}
	if BrokenGrammar(out) {
		t.Fatal(blob)
	}
	headings := []string{}
	for _, section := range sectionsOf(out) {
		headings = append(headings, textOf(section["heading"]))
	}
	if strings.Contains(strings.Join(headings, "\n"), "拆解") || strings.Contains(strings.Join(headings, "\n"), "老年人常") {
		t.Fatal(headings)
	}
}

func TestArticle84BrokenTextIsDetected(t *testing.T) {
	broken := map[string]any{"sections": []any{map[string]any{
		"heading": "老年人常：",
		"paragraphs": []any{
			"问题出在本身。",
			"先从入手。",
			"先看，，再测。",
			"测试表明，再量一次。",
		},
	}}}
	if !BrokenGrammar(broken) {
		t.Fatal("grammar check missed article 84")
	}
	if heading := stripLabelPrefix("拆解：现场核对线序", []string{"拆解", "老年人常"}); heading != "现场核对线序" {
		t.Fatal(heading)
	}
	if heading := stripLabelPrefix("老年人常：", []string{"拆解", "老年人常"}); heading != "" {
		t.Fatal(heading)
	}
}

func TestArticle84InventedStatsAreRewrittenWhole(t *testing.T) {
	for _, sample := range []string{"一半以上的链路要重测。", "衰减超过 3 dB。", "留出 30 米。", "损耗有 2 倍。", "高于 40 的读数不要直接用。", "30%以上要重测。", "百分比不能这么写。"} {
		if !hardStatSentence(sample) {
			t.Fatal(sample)
		}
	}
	if !experientialStat("我见过的项目里大多在 3 米上下。") || hardStatSentence("我见过的项目里大多在 3 米上下。") && !experientialStat("我见过的项目里大多在 3 米上下。") {
		t.Fatal("experience range")
	}
	draft := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{
		"衰减超过 3 dB，一半以上要重测。",
		"先把线序核对完。",
	}}}}
	flagged := InventedSentences(draft)
	if len(flagged) != 1 {
		t.Fatal(flagged)
	}
	kept := ApplyRewrites(draft, flagged, map[string]string{
		flagged[0]: "衰减偏大时先停下来再测一次。",
	})
	blob := dump(kept)
	if strings.Contains(blob, "dB") || strings.Contains(blob, "一半以上") || !strings.Contains(blob, "衰减偏大时先停下来再测一次。") || !strings.Contains(blob, "先把线序核对完") {
		t.Fatal(blob)
	}
	splicedOut := ApplyRewrites(draft, flagged, map[string]string{
		flagged[0]: "衰减要重测。",
	})
	if strings.Contains(dump(splicedOut), "衰减要重测") || strings.Contains(dump(splicedOut), "dB") {
		t.Fatal(dump(splicedOut))
	}
	if !Dangling("问题出在本身。") || !Dangling("先从入手。") || !Dangling("测试表明，再量一次。") || !Dangling("先看，，再测。") || !Dangling("先从。") {
		t.Fatal("article 84 fragments")
	}
	if Dangling("现场要逐项核对。") || Dangling("下一步看编号。") {
		t.Fatal("a finished verb is not a dangling particle")
	}
}

func TestArticle85StaysOnJointsAndOpensBeforeTheHeading(t *testing.T) {
	draft := map[string]any{
		"title": "老年人膝盖关节活动前先热身",
		"sections": []any{map[string]any{
			"heading": "关节：先热身",
			"paragraphs": []any{
				"早上起来膝盖发僵，先坐在床边把脚放平。",
				"皮肤干燥就多涂面霜。",
				"视力下降要少看屏幕。",
				"记忆变差就多重复几遍。",
				"弯曲膝盖时慢一点，不要一下子蹲到底。",
			},
			"list": []any{"3. 扶着桌子站起来", "4. 再坐下"},
			"table": map[string]any{
				"headers": []any{"动作", "注意"},
				"rows":    []any{[]any{"弯曲", "慢一点"}},
			},
		}},
	}
	out := StayOnTitle(draft)
	out = StripHeadingPrefixes(out)
	out = EnsureOpening(out)
	out = RenumberLists(out)
	blob := dump(out)
	if strings.Contains(blob, "皮肤") || strings.Contains(blob, "视力") || strings.Contains(blob, "记忆") {
		t.Fatal(blob)
	}
	if !strings.Contains(blob, "膝盖") || !strings.Contains(blob, "弯曲膝盖") {
		t.Fatal(blob)
	}
	if !HasOpening(out) {
		t.Fatal("opening", blob)
	}
	sections := sectionsOf(out)
	if textOf(sections[0]["heading"]) != "" {
		t.Fatal(sections[0]["heading"])
	}
	body := sections[1]
	list := anyList(body["list"])
	if len(list) != 2 || textOf(list[0]) != "1. 扶着桌子站起来" || textOf(list[1]) != "2. 再坐下" {
		t.Fatal(list)
	}
	table, _ := body["table"].(map[string]any)
	if table == nil {
		t.Fatal("table flattened")
	}
	html := render.RenderHTML(out, "simple", false, false)
	if !strings.Contains(html, "<table") || !strings.Contains(html, "<td") || strings.Contains(html, "弯曲：慢一点") {
		t.Fatal(html)
	}
	if strings.Contains(html, "3. 扶着桌子") || strings.Contains(html, "4. 再坐下") {
		t.Fatal(html)
	}
}

func TestSkipDraftDefaultsForAnAdminTestRun(t *testing.T) {
	if !ShouldSkipDraft(-1, nil, true, true, "manual") {
		t.Fatal("admin test run should skip the draft box")
	}
	off := false
	if ShouldSkipDraft(-1, &off, true, true, "manual") {
		t.Fatal("explicit push")
	}
	if ShouldSkipDraft(0, nil, true, true, "manual") {
		t.Fatal("plan chose to push")
	}
	if !ShouldSkipDraft(1, nil, false, false, "schedule") {
		t.Fatal("plan chose to skip")
	}
	if ShouldSkipDraft(-1, nil, true, true, "schedule") {
		t.Fatal("scheduled run follows the plan")
	}
	if ShouldSkipDraft(-1, nil, true, false, "manual") {
		t.Fatal("a test account that is not an admin keeps the plan default")
	}
}
