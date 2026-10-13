package editorial

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestUnsourcedInstitutionIsRemoved(t *testing.T) {
	draft := map[string]any{
		"title":   "验收",
		"summary": "住建部发了通知。",
		"sections": []any{map[string]any{"paragraphs": []any{
			"住建部要求先看 GB 50311。现场再核对编号。",
			"《综合布线设计规范》写了这一步。",
			"对照来源里的说明即可。",
		}}},
		"sources": []any{map[string]any{"title": "综合布线验收说明", "url": "https://www.gov.cn/a", "snippet": "公开说明"}},
	}
	out := StripUnsourcedNames(draft)
	text := out["summary"].(string)
	section := out["sections"].([]any)[0].(map[string]any)
	var body string
	for _, item := range section["paragraphs"].([]any) {
		body += item.(string)
	}
	if strings.Contains(text, "住建部") || strings.Contains(body, "住建部") || strings.Contains(body, "GB 50311") || strings.Contains(body, "综合布线设计规范") {
		t.Fatal(text, body)
	}
	if !strings.Contains(body, "对照来源里的说明即可") {
		t.Fatal(body)
	}
	kept := StripUnsourcedNames(map[string]any{
		"sections": []any{map[string]any{"paragraphs": []any{"住建部的公开说明可以核对。[1]"}}},
		"sources":  []any{map[string]any{"title": "住建部公开说明", "name": "住建部", "snippet": "GB 50311"}},
	})
	keptBody := kept["sections"].([]any)[0].(map[string]any)["paragraphs"].([]any)[0].(string)
	if !strings.Contains(keptBody, "住建部") {
		t.Fatal(keptBody)
	}
}

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
	items := stringList(section["list"])
	if len(items) != 1 || items[0] != "单独的一条提醒。" {
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
	if strings.Contains(string(raw), "这一步按现场记录来写") || out["closing"] != "" {
		t.Fatal(string(raw))
	}
}

func TestBareAcceptanceIsNotATaboo(t *testing.T) {
	draft := map[string]any{
		"sections": []any{map[string]any{"paragraphs": []any{
			"现场验收要核对中标之后的回路。不要写保证中标。",
			"包过验收。",
		}}},
	}
	out := RewriteTaboos(draft, "中标,验收,保证中标,包过验收")
	raw, _ := json.Marshal(out)
	text := string(raw)
	if !strings.Contains(text, "现场验收要核对中标之后的回路") || strings.Contains(text, "保证中标") || strings.Contains(text, "包过验收") || strings.Contains(text, "这一步按现场记录来写") {
		t.Fatal(text)
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

func TestDuplicateTablesAreDropped(t *testing.T) {
	table := map[string]any{"headers": []any{"项目", "说明"}, "rows": []any{[]any{"回路", "先编号"}}}
	same := map[string]any{"headers": []any{"项目", "说明"}, "rows": []any{[]any{"回路", "先编号"}}}
	draft := map[string]any{"sections": []any{
		map[string]any{"paragraphs": []any{"第一条。"}, "table": table},
		map[string]any{"paragraphs": []any{"第二条。"}, "table": same},
	}}
	out := DedupeTables(draft)
	sections := sectionsOf(out)
	if _, ok := sections[0]["table"]; !ok {
		t.Fatal("first table dropped")
	}
	if _, ok := sections[1]["table"]; ok {
		t.Fatal("duplicate table kept")
	}
}

func TestMetaLeakIsStripped(t *testing.T) {
	draft := map[string]any{
		"title":    "按原稿再改一版",
		"summary":  "上一版写得太满。",
		"sections": []any{map[string]any{"paragraphs": []any{"这是改写时留下的话。现场先核对回路。"}}},
	}
	out := StripMeta(draft)
	raw := dump(out)
	for _, word := range []string{"原稿", "改写", "上一版"} {
		if strings.Contains(raw, word) {
			t.Fatal(raw)
		}
	}
	if !strings.Contains(raw, "现场先核对回路") {
		t.Fatal(raw)
	}
}

func TestUnsupportedCitationIsRewritten(t *testing.T) {
	draft := map[string]any{
		"sources": []any{map[string]any{"title": "综合布线验收标准", "snippet": "回路编号要逐点核对", "url": "https://www.mohurd.gov.cn/a"}},
		"sections": []any{map[string]any{"paragraphs": []any{
			"爷爷奶奶捧着温茶转圈，下午继续喝茶养生。[1]",
			"综合布线验收时要逐点核对回路编号。[1]",
		}}},
	}
	out := SupportCitations(draft)
	paras := paragraphsOf(out)
	if strings.Contains(paras[0], "[1]") || !strings.Contains(paras[0], "爷爷奶奶") {
		t.Fatal(paras)
	}
	if !strings.Contains(paras[1], "[1]") {
		t.Fatal(paras)
	}
}

func TestCitationDoesNotAttachAnUnrelatedSource(t *testing.T) {
	draft := map[string]any{
		"sources": []any{
			map[string]any{"title": "住房和城乡建设部关于发布国家标准《无线局域网工程设计标准》的公告", "url": "https://www.mohurd.gov.cn/a"},
			map[string]any{"title": "综合布线施工前材料消耗核算", "snippet": "放线前先核算材料消耗", "url": "https://www.gov.cn/b"},
		},
		"sections": []any{map[string]any{"paragraphs": []any{
			"协会会议要求先看现场再落实施工安排。[1]",
			"放线前先核算材料消耗，避免临时加料。[2]",
		}}},
	}
	out := SupportCitations(draft)
	paras := paragraphsOf(out)
	if strings.Contains(paras[0], "[1]") {
		t.Fatal(paras)
	}
	if !strings.Contains(paras[1], "[2]") {
		t.Fatal(paras)
	}
	cited := CiteSupported(out)
	again := paragraphsOf(cited)
	if strings.Contains(again[0], "[1]") || strings.Contains(again[0], "[2]") {
		t.Fatal(again)
	}
	if !strings.Contains(again[1], "[2]") {
		t.Fatal(again)
	}
}

func TestLimitParagraphsSplitsLongStepsAndKeepsAnswers(t *testing.T) {
	draft := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{
		"第一句。第二句。第三句。第四句。第五句。第六句。",
		"怎么看？答：先看这一项。再看下一项。",
		"1. 一句。二句。三句。四句。五句。",
	}}}}
	out := LimitParagraphs(draft, 4)
	paras := paragraphsOf(out)
	if len(paras) != 5 {
		t.Fatal(paras)
	}
	if strings.Contains(paras[0], "第六句") || !strings.Contains(paras[1], "第六句") {
		t.Fatal(paras)
	}
	if !strings.Contains(paras[2], "怎么看？") || !strings.Contains(paras[2], "答：") {
		t.Fatal(paras)
	}
	if !strings.HasPrefix(paras[3], "1.") || !strings.HasPrefix(strings.TrimSpace(paras[4]), "1.") || !strings.Contains(paras[4], "五句") {
		t.Fatal(paras)
	}
}

func TestRenumberCitationsFillsTheGap(t *testing.T) {
	draft := map[string]any{
		"sections": []any{map[string]any{"paragraphs": []any{"先看现场。[2]"}}},
		"sources": []any{
			map[string]any{"title": "没用上", "url": "https://www.example.com/a"},
			map[string]any{"title": "布线说明", "url": "https://www.gov.cn/b"},
		},
	}
	out := RenumberCitations(draft)
	paras := paragraphsOf(out)
	if len(paras) != 1 || !strings.Contains(paras[0], "[1]") || strings.Contains(paras[0], "[2]") {
		t.Fatal(paras)
	}
	sources := out["sources"].([]any)
	if len(sources) != 1 || sources[0].(map[string]any)["url"] != "https://www.gov.cn/b" {
		t.Fatal(sources)
	}
}

func TestSoftenHealthClaimsWithoutCitation(t *testing.T) {
	draft := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{
		"这个偏方可以治疗炎症。",
		"剂量写在说明里。[1]",
	}}}}
	out := SoftenHealthClaims(draft)
	paras := paragraphsOf(out)
	joined := strings.Join(paras, "")
	if strings.Contains(joined, "偏方") || strings.Contains(joined, "治疗") || !strings.Contains(joined, "生活习惯") {
		t.Fatal(paras)
	}
	if !strings.Contains(joined, "剂量写在说明里。[1]") {
		t.Fatal(paras)
	}
}

func TestStripEnglishAndCapRepeats(t *testing.T) {
	draft := map[string]any{
		"title": "综合布线验收",
		"sections": []any{map[string]any{"paragraphs": []any{
			"先用 HTTP 打开记录。This sentence should go. 再核对编号。",
			"近日核对材料。",
			"今天也要核对。",
			"近日再看一遍现场编号是否一致。",
			"近日最后再记一笔。",
			"第一条写综合布线验收说明。",
			"第二条也写综合布线验收说明。",
			"第三条还是综合布线验收说明要补。",
		}}},
	}
	out := StripEnglishLeaks(draft)
	out = CapRepeatedPhrases(out, 2)
	paras := paragraphsOf(out)
	joined := strings.Join(paras, "")
	if strings.Contains(joined, "This sentence") {
		t.Fatal(paras)
	}
	if !strings.Contains(joined, "HTTP") {
		t.Fatal(paras)
	}
	if strings.Count(joined, "近日") > 2 {
		t.Fatal(paras)
	}
	if strings.Count(joined, "综合布线验收说明") > 2 {
		t.Fatal(paras)
	}
}

func TestDropOffTopicParagraph(t *testing.T) {
	draft := map[string]any{
		"title": "综合布线验收",
		"sections": []any{map[string]any{"paragraphs": []any{
			"综合布线验收先看材料。",
			"奶奶下午去喝茶。",
		}}},
	}
	out := DropOffTopicParagraphs(draft)
	paras := paragraphsOf(out)
	if len(paras) != 1 || !strings.Contains(paras[0], "材料") {
		t.Fatal(paras)
	}
}

func TestStripFactNumbersKeepsARealList(t *testing.T) {
	draft := map[string]any{
		"sections": []any{map[string]any{"paragraphs": []any{"2. 先看线槽。", "3. 再核对编号。", "8. 层高不低于3米。"}}},
	}
	out := StripFactNumbers(draft)
	paras := paragraphsOf(out)
	if len(paras) != 3 || strings.HasPrefix(paras[0], "2") || strings.Contains(paras[2], "8.") {
		t.Fatal(paras)
	}
	kept := map[string]any{
		"sections": []any{map[string]any{"paragraphs": []any{"1. 先看线槽。", "2. 再核对编号。", "3. 量一下层高。"}}},
	}
	paras = paragraphsOf(StripFactNumbers(kept))
	if paras[0] != "1. 先看线槽。" || paras[2] != "3. 量一下层高。" {
		t.Fatal(paras)
	}
}

func TestConcreteTipsIgnoreGenericFiller(t *testing.T) {
	generic := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{
		"把杯子放在窗边，人会更从容。",
		"省力的动作让日常更轻松。",
		"在桌前走动一下就好。",
	}}}}
	if ConcreteTips(generic) != 0 || !FillerHeavy(generic) {
		t.Fatal(ConcreteTips(generic), FillerHeavy(generic))
	}
	tips := map[string]any{"sections": []any{map[string]any{
		"paragraphs": []any{
			"把白醋倒进杯里泡5分钟。",
			"用牙刷蘸小苏打擦水龙头。",
			"晾10分钟再拧干抹布。",
		},
		"list": []any{"贴一条胶带把线槽固定住。", "先量一下窗框宽度再剪。"},
	}}}
	if ConcreteTips(tips) < 5 {
		t.Fatal(ConcreteTips(tips))
	}
}

func TestWordCapKeepsTheTitleWordAndIdenticalSentences(t *testing.T) {
	line := "合适的正文。"
	draft := map[string]any{
		"title":    "布线核对",
		"sections": []any{map[string]any{"paragraphs": []any{strings.Repeat(line, 12), "布线先看线槽。", "布线再看插座。", "布线还要看水管。", "布线最后看纱窗。", "布线收工前看花洒。"}}},
	}
	out := CapWordRepetition(draft, 4)
	joined := strings.Join(paragraphsOf(out), "")
	if strings.Count(joined, line) < 12 {
		t.Fatal("identical padding was stripped")
	}
	if strings.Count(joined, "布线") < 5 {
		t.Fatal(joined)
	}
	filler := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{
		"坐在窗边会更从容。", "换个杯子也从容。", "走路从容一点。", "说话从容一点。", "收工也从容。",
	}}}}
	capped := strings.Join(paragraphsOf(CapWordRepetition(filler, 4)), "")
	if strings.Count(capped, "从容") > 4 {
		t.Fatal(capped)
	}
}

func TestArticle73RepeatedTemplateIsHeld(t *testing.T) {
	var lines []any
	for i := 1; i <= 40; i++ {
		lines = append(lines, "第"+strconv.Itoa(i)+"处要先看什么？答：先打开辟谣不吓人的检修口，用螺丝把盖板固定住。")
	}
	draft := map[string]any{"sections": []any{map[string]any{"heading": "读者还会问", "paragraphs": lines}}}
	if !RepeatedBody(draft) {
		t.Fatal("article 73 must be held")
	}
	jammed := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{"先打开辟谣不吓人的检修口，然后就收工。"}}}}
	if !RepeatedBody(jammed) {
		t.Fatal("a topic jammed into the template must be held")
	}
	var near []any
	for i := 1; i <= 12; i++ {
		near = append(near, "晚饭后把绘本放回第"+strconv.Itoa(i)+"格，再把椅子靠回桌边。")
	}
	if !RepeatedBody(map[string]any{"sections": []any{map[string]any{"paragraphs": near}}}) {
		t.Fatal("near-duplicate sentences must be held")
	}
	twice := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{
		"先把线槽盖板掀开，再核对编号。",
		"先把线槽盖板掀开，再核对编号。",
	}}}}
	if !RepeatedBody(twice) {
		t.Fatal("two copies of a 15-character line must be held")
	}
	pad := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{strings.Repeat("合适的正文。", 120)}}}}
	if RepeatedBody(pad) {
		t.Fatal("a short repeated sentence used as length padding is not article 73")
	}
	ordinary := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{
		"进户先打开信息箱，把图纸摊开。",
		"过线盒留在吊顶旁边，转弯不要挤死。",
		"面板高度按开关那一排来。",
	}}}}
	if RepeatedBody(ordinary) {
		t.Fatal(ordinary)
	}
}

func TestArticle74And75Blocks(t *testing.T) {
	canned := []string{
		"先打开检修口再看线槽。",
		"用螺丝把线槽盖板固定住。",
		"量一下插座离地的高度。",
	}
	thin := EnsureConcreteTips(StripWarmFiller(map[string]any{"sections": []any{map[string]any{"paragraphs": []any{"今晚的气氛很温馨。"}}}}))
	blob := fillerText(thin)
	for _, line := range canned {
		if strings.Contains(blob, line) {
			t.Fatal(line)
		}
	}
	block := "先打开检修口再看线槽，用螺丝把线槽盖板固定住，再量一下插座离地的高度。"
	step := "贴一条胶带标出弱电箱的位置后再盘线。"
	doubled := map[string]any{"sections": []any{
		map[string]any{"heading": "先看线槽", "paragraphs": []any{block}, "list": []any{"1. " + step}},
		map[string]any{"heading": "再看一遍", "paragraphs": []any{block}, "list": []any{"2. " + step}, "table": map[string]any{
			"headers": []any{"项目", "做法"},
			"rows":    []any{[]any{"线槽", "盖板固定住再量高度"}},
		}},
		map[string]any{"heading": "再看一遍", "paragraphs": []any{}, "table": map[string]any{
			"headers": []any{"项目", "做法"},
			"rows":    []any{[]any{"线槽", "盖板固定住再量高度"}},
		}},
	}}
	if !RepeatedBody(doubled) {
		t.Fatal("articles 74 and 75 repeat a whole block")
	}
	clean := DedupeHashedBlocks(doubled)
	paras := paragraphsOf(clean)
	if len(paras) != 1 || paras[0] != block {
		t.Fatal(paras)
	}
	lists := 0
	tables := 0
	for _, section := range sectionsOf(clean) {
		lists += len(anyList(section["list"]))
		if section["table"] != nil {
			tables++
		}
	}
	if lists != 1 || tables != 1 {
		t.Fatal(lists, tables)
	}
	once := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{block}}}}
	if RepeatedBody(once) {
		t.Fatal("one copy is not a repeat")
	}
}

func TestHeadingPrefixAndInventedStatAndOneCTA(t *testing.T) {
	draft := map[string]any{
		"column_label": "方案拆解",
		"title":        "监控掉线",
		"sections": []any{
			map[string]any{"heading": "拆解：简单：第一步：先看交换机", "paragraphs": []any{"现场先看交换机的灯。"}, "list": []any{}},
			map[string]any{"heading": "方案拆解：再看线", "paragraphs": []any{"ONVIF 2.4 和 2.6 不能混用。掉线率 15%。我见过的项目里大多两天内能恢复。"}, "list": []any{}},
		},
		"closing": "欢迎留言说说你的做法。\n\n关注本号，把你的做法写在评论里。",
	}
	out := StripInventedStats(StripHeadingPrefixes(draft))
	out["closing"] = keepOneCTA(textOf(out["closing"]))
	sections, _ := out["sections"].([]any)
	first := sections[0].(map[string]any)["heading"].(string)
	if strings.Contains(first, "拆解") || strings.Contains(first, "第一步") || first != "先看交换机" {
		t.Fatal(first)
	}
	blob := dump(out)
	if strings.Contains(blob, "15%") || strings.Contains(blob, "ONVIF") || !strings.Contains(blob, "我见过") {
		t.Fatal(blob)
	}
	if strings.Count(textOf(out["closing"]), "关注本号") != 0 && strings.Contains(textOf(out["closing"]), "欢迎留言") {
		if strings.Contains(textOf(out["closing"]), "关注本号") {
			t.Fatal(out["closing"])
		}
	}
	if strings.Contains(textOf(out["closing"]), "关注本号") {
		t.Fatal(out["closing"])
	}
	lists := map[string]any{"sections": []any{
		map[string]any{"heading": "甲", "list": []any{"1. 先看灯", "2. 再看线"}},
		map[string]any{"heading": "乙", "list": []any{"1. 记下端口", "2. 收工前再看一眼"}},
	}}
	mixed := MixProse(lists)
	raw := dump(mixed)
	if !strings.Contains(raw, "先看灯") || !strings.Contains(raw, "收工前再看一眼") {
		t.Fatal(raw)
	}
	if HardStat(out) {
		t.Fatal("stripped draft still has a hard stat")
	}
	if !HardStat(map[string]any{"sections": []any{map[string]any{"paragraphs": []any{"掉线率 30%。"}}}}) {
		t.Fatal("gate missed a percentage")
	}
	prefixed := map[string]any{"column_label": "知识FAQ", "sections": []any{
		map[string]any{"heading": "知识：家长里短：先看灶台", "paragraphs": []any{"晚饭后先看这一处。"}},
	}, "closing": "先把油擦掉。\n\n关注本号，把你的做法写在评论里。\n\n下一期继续写这一处。\n\n本文是家庭生活分享，请结合自己的情况判断。\n\n本文由人工智能辅助生成",
		"footnotes": []any{"本文是家庭生活分享，请结合自己的情况判断。", "本文是家庭生活分享，请结合自己的情况判断。", "本文由人工智能辅助生成"},
	}
	shaped := ShapeCopy(prefixed)
	heading := shaped["sections"].([]any)[0].(map[string]any)["heading"].(string)
	if heading != "先看灶台" {
		t.Fatal(heading)
	}
	closing := textOf(shaped["closing"])
	if strings.Contains(closing, "关注本号") || strings.Contains(closing, "下一期") || strings.Contains(closing, "人工智能") || strings.Contains(closing, "家庭生活分享") {
		t.Fatal(closing)
	}
	if closing != "先把油擦掉。" {
		t.Fatal(closing)
	}
	notes := shaped["footnotes"].([]any)
	if len(notes) != 1 || strings.Contains(textOf(notes[0]), "人工智能") {
		t.Fatal(notes)
	}
	repeated := map[string]any{"title": "灶台", "sections": []any{
		map[string]any{"paragraphs": []any{"我在家见过灶台油多。摸一摸就知道。"}},
		map[string]any{"paragraphs": []any{"我在家见过抹布要拧干。摸一摸边缘。"}},
		map[string]any{"paragraphs": []any{"我在家见过冷水更容易挂油。摸一摸台面。"}},
		map[string]any{"paragraphs": []any{"我在家见过最后用干布收一收。"}},
	}}
	capped := CapHanPhrases(repeated, 4, 2)
	blob = dump(capped)
	if strings.Count(blob, "我在家见过") != 2 || strings.Count(blob, "摸一摸") != 2 {
		t.Fatal(blob)
	}
	varied := VarySectionOpeners(capped)
	openers := 0
	for _, section := range sectionsOf(varied) {
		if strings.Contains(firstSentence(section), "我在家见过") {
			openers++
		}
	}
	if openers > 2 {
		t.Fatal(dump(varied))
	}
}

func TestKeepOneSubjectDropsTheOtherChores(t *testing.T) {
	draft := map[string]any{
		"title": "奶奶晚饭后先把灶台擦一遍",
		"sections": []any{map[string]any{"paragraphs": []any{
			"晚饭后先把灶台擦一遍，锅还温着。",
			"窗台的绿植该浇花了，花盆托盘有水。",
			"书架上的灰尘用干抹布带一下。",
		}}},
	}
	out := KeepOneSubject(draft)
	text := dump(out)
	if strings.Contains(text, "绿植") || strings.Contains(text, "灰尘") || !strings.Contains(text, "灶台") {
		t.Fatal(text)
	}
}

func dump(draft map[string]any) string {
	buf, err := json.Marshal(draft)
	if err != nil {
		return ""
	}
	return string(buf)
}
