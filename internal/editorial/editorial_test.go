package editorial

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRewriteReplacesTheOpening(t *testing.T) {
	first := "验收时核对线槽，是否盖好。"
	second := "拍完留在当天的记录里[1]。"
	draft := map[string]any{
		"title": "短",
		"_quality": map[string]any{
			"hook": 2, "headings": false, "closing": false, "motif": false,
		},
		"sections": []any{
			map[string]any{"heading": "", "paragraphs": []any{first}},
			map[string]any{"heading": "", "paragraphs": []any{second}, "list": []any{"不要删掉这条列表。"}},
		},
		"closing": "结束。",
		"sources": []any{map[string]any{"url": "https://www.gov.cn/a", "title": "原文"}},
	}
	if !NeedsRewrite(draft).Hook {
		t.Fatal("rubric should request a new hook")
	}
	model := map[string]any{
		"hook":     "为什么现场总卡在这一步？",
		"headings": []any{"线槽", "记录"},
		"closing":  "可以先记住这一步。欢迎留言说说你的做法。",
	}
	out := Apply(draft, model, nil)
	raw := dump(out)
	if strings.Contains(raw, first) {
		t.Fatal("old opening should be replaced", raw)
	}
	if !strings.Contains(raw, second) || !strings.Contains(raw, "[1]") || !strings.Contains(raw, "https://www.gov.cn/a") {
		t.Fatal(raw)
	}
	if !strings.Contains(raw, "为什么现场总卡在这一步？") || !strings.Contains(raw, "结束。") {
		t.Fatal(raw)
	}
	if strings.Contains(raw, "结束。可以先记住") || !strings.Contains(raw, "结束。\\n\\n可以先记住") {
		t.Fatal(raw)
	}
	paras := paragraphsOf(out)
	if paras[0] != "为什么现场总卡在这一步？" || paras[1] != second {
		t.Fatal(paras)
	}
	if Score(out) < Score(draft) {
		t.Fatal(Score(out), Score(draft))
	}
}

func TestLongOrOverlappingHookIsRejected(t *testing.T) {
	first := "验收时核对线槽，是否盖好。"
	draft := map[string]any{
		"_quality": map[string]any{"hook": 1, "headings": true, "closing": true},
		"sections": []any{
			map[string]any{"heading": "线槽", "paragraphs": []any{first}},
			map[string]any{"heading": "记录", "paragraphs": []any{"拍完留在当天的记录里。"}},
		},
		"closing": "可以先记住这一步。欢迎留言说说你的做法。",
	}
	long := strings.Repeat("现场这一步为什么总是卡住，", 4)
	if HookAccepted(long, first) {
		t.Fatal("hook longer than 40 runes")
	}
	out := Apply(draft, map[string]any{"hook": long}, nil)
	if paragraphsOf(out)[0] != first {
		t.Fatal(paragraphsOf(out))
	}
	overlap := "验收时核对线槽，是否盖好？再补一句。"
	if HookAccepted(overlap, first) {
		t.Fatal("overlap", Overlap(overlap, first))
	}
	out = Apply(draft, map[string]any{"hook": overlap}, nil)
	if paragraphsOf(out)[0] != first {
		t.Fatal(paragraphsOf(out))
	}
}

func TestKeywordDoesNotTriggerHookPolish(t *testing.T) {
	draft := map[string]any{
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"验收时核对线槽，是否盖好。"}}},
		"closing":  "可以先记住这一步。欢迎留言说说你的做法。",
	}
	if NeedsRewrite(draft).Hook {
		t.Fatal("missing rubric must not use 先 or 为什么 as a hook trigger")
	}
	strong := map[string]any{
		"_quality": map[string]any{"hook": 5, "headings": true, "closing": true},
		"sections": draft["sections"],
		"closing":  draft["closing"],
	}
	if NeedsRewrite(strong).Any() {
		t.Fatal(NeedsRewrite(strong))
	}
}

func TestRepairDropsNearDuplicateParagraph(t *testing.T) {
	draft := map[string]any{
		"sections": []any{map[string]any{"paragraphs": []any{
			"验收时先核对线槽，盖板要盖好。",
			"验收时先核对线槽，盖板要盖好！",
			"拍完留在当天的记录里。",
		}}},
	}
	out := RepairNearDuplicates(draft)
	paras := paragraphsOf(out)
	if len(paras) != 2 || paras[1] != "拍完留在当天的记录里。" {
		t.Fatal(paras)
	}
}

func TestSectionKeepsListOrTable(t *testing.T) {
	para := "先核对线槽盖板，再把当天的照片留下。"
	draft := map[string]any{
		"sections": []any{map[string]any{
			"heading":    "现场",
			"paragraphs": []any{para, "另一件独立的事。"},
			"list":       []any{"先核对线槽盖板，再把当天的照片留下", "单独的一条提醒。"},
			"table": map[string]any{
				"headers": []any{"做法", "说明"},
				"rows": []any{
					[]any{"先核对线槽盖板", "再把当天的照片留下"},
					[]any{"收工前", "再看一眼编号"},
				},
			},
		}},
	}
	out := DedupeBlocks(draft)
	section := sectionsOf(out)[0]
	if len(stringList(section["list"])) != 0 {
		t.Fatal(section["list"])
	}
	table, _ := section["table"].(map[string]any)
	rows, _ := table["rows"].([]any)
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	if !strings.Contains(strings.Join(paragraphsOf(out), ""), para) {
		t.Fatal(paragraphsOf(out))
	}
}

func TestGlossaryStaysOnlyForTheTrade(t *testing.T) {
	line := "弱电指电子信息系统，工作电压通常在安全特低电压，不是交流1000V以下的低压电力。"
	draft := map[string]any{
		"sections": []any{map[string]any{"paragraphs": []any{"周末把玩具收进盒子。", line}}},
	}
	home := DropGlossary(draft, "family", "家庭生活")
	if strings.Contains(dump(home), "1000V") || !strings.Contains(dump(home), "玩具") {
		t.Fatal(dump(home))
	}
	trade := map[string]any{
		"sections": []any{map[string]any{"paragraphs": []any{line}}},
	}
	kept := DropGlossary(trade, "elv", "弱电智能化")
	if !strings.Contains(dump(kept), "1000V") {
		t.Fatal(dump(kept))
	}
}

func TestFailedRewriteReturnsOriginal(t *testing.T) {
	draft := map[string]any{
		"title":    "短",
		"sections": []any{map[string]any{"heading": "", "paragraphs": []any{"验收时先看线槽，是否盖好。"}}},
		"closing":  "",
	}
	out := Apply(draft, map[string]any{"hook": "为什么？"}, errors.New("model down"))
	if out["title"] != "短" || paragraphsOf(out)[0] != "验收时先看线槽，是否盖好。" {
		t.Fatal(out)
	}
	worse := Apply(draft, map[string]any{"hook": "", "headings": []any{}, "closing": ""}, nil)
	if paragraphsOf(worse)[0] != paragraphsOf(draft)[0] || worse["title"] != "短" {
		t.Fatal(worse)
	}
}

func dump(draft map[string]any) string {
	buf, err := json.Marshal(draft)
	if err != nil {
		return ""
	}
	return string(buf)
}
