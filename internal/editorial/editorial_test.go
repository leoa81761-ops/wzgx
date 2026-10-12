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

func TestSharedNGramDropsTheLaterParagraph(t *testing.T) {
	phrase := "爷爷奶奶眼睛里面闪闪发光"
	if len([]rune(phrase)) != 12 {
		t.Fatal(len([]rune(phrase)))
	}
	first := strings.Repeat("早上孩子把绘本摊在桌上喝茶。", 8) + phrase + "茶还是温的。"
	second := strings.Repeat("傍晚换成院子里的椅子看风。", 8) + phrase + "风从窗外进来。"
	if Overlap(first, second) > 0.5 {
		t.Fatal("fixture should stay under the overlap cutoff", Overlap(first, second))
	}
	if !SharesNGram(first, second, 12) {
		t.Fatal("shared phrase")
	}
	draft := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{first, second, "把玩具收进盒子就好。"}}}}
	out := RepairNearDuplicates(draft)
	paras := paragraphsOf(out)
	if len(paras) != 2 || paras[0] != first || paras[1] != "把玩具收进盒子就好。" {
		t.Fatal(paras)
	}
}

func TestStripAudienceDoesNotLeaveTheLabel(t *testing.T) {
	label := "在家过日子的读者"
	draft := map[string]any{
		"title":      label + "，周末早晨阳光洒进客厅",
		"summary":    label + "，你好！",
		"alt_titles": []any{label + "的一个标题", "两口人收玩具"},
		"sections": []any{map[string]any{
			"heading": label,
			"paragraphs": []any{
				label + "，你好！",
				label + "，你好！想象一个周日早晨。",
				"把玩具收进盒子就好。",
			},
		}},
		"closing": label + "，欢迎留言。",
	}
	out := StripAudience(draft, label)
	if strings.Contains(dump(out), label) {
		t.Fatal(dump(out))
	}
	paras := paragraphsOf(out)
	if len(paras) != 2 || paras[0] != "你好！想象一个周日早晨。" || paras[1] != "把玩具收进盒子就好。" {
		t.Fatal(paras)
	}
	if out["title"] != "周末早晨阳光洒进客厅" || out["summary"] != "" || out["closing"] != "" {
		t.Fatal(out["title"], out["summary"], out["closing"])
	}
	alts, _ := out["alt_titles"].([]any)
	if len(alts) != 1 || alts[0] != "两口人收玩具" {
		t.Fatal(alts)
	}
}

func TestDropSummaryEchoRemovesTheLead(t *testing.T) {
	summary := "在家过日子的读者，通过家里日常小事也能让晚年好心情。"
	draft := map[string]any{
		"summary":  summary,
		"sections": []any{map[string]any{"paragraphs": []any{summary, "把玩具收进盒子。"}}},
	}
	out := DropSummaryEcho(draft)
	paras := paragraphsOf(out)
	if len(paras) != 1 || paras[0] != "把玩具收进盒子。" || out["summary"] != summary {
		t.Fatal(paras, out["summary"])
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

func TestShingleDropsARephrasedParagraph(t *testing.T) {
	first := "验收之前先把回路编号抄进表格，再逐个点位核对。"
	second := "回路编号要先抄进表格，验收时再逐个点位核对。"
	if SharesNGram(first, second, 12) {
		t.Fatal("fixture should not share a 12-rune run")
	}
	if ShingleSimilarity(first, second, 3) < 0.45 {
		t.Fatal(ShingleSimilarity(first, second, 3))
	}
	draft := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{first, second, "收工前再看一眼盖板。"}}}}
	paras := paragraphsOf(RepairNearDuplicates(draft))
	if len(paras) != 2 || paras[0] != first || strings.Contains(paras[1], "回路编号") {
		t.Fatal(paras)
	}
}

func TestTabooSentenceIsRewritten(t *testing.T) {
	draft := map[string]any{
		"title":   "验收清单",
		"summary": "不要承诺包过验收。",
		"sections": []any{map[string]any{"paragraphs": []any{
			"现场要逐项核对。包过验收这种话不写。下一步看编号。",
		}}},
		"closing": "包过验收。",
	}
	out := RewriteTaboos(draft, "包过验收")
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "包过验收") {
		t.Fatal(string(raw))
	}
	if !strings.Contains(string(raw), "现场要逐项核对") || !strings.Contains(string(raw), "下一步看编号") {
		t.Fatal(string(raw))
	}
}

func TestForeignTitleSceneIsRemoved(t *testing.T) {
	title := "周末早晨，阳光从窗外洒进客厅，你推开门，看到爷爷奶奶捧着温茶转圈，那一小步就让上午亮堂起来。"
	draft := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{
		"爷爷奶奶捧着温茶转圈，那一小步就让上午亮堂起来。",
		"验收先核对回路编号。",
	}}}}
	out := StripForeignScenes(draft, []string{title})
	paras := paragraphsOf(out)
	if len(paras) != 1 || strings.Contains(paras[0], "爷爷奶奶") {
		t.Fatal(paras)
	}
}

func dump(draft map[string]any) string {
	buf, err := json.Marshal(draft)
	if err != nil {
		return ""
	}
	return string(buf)
}
