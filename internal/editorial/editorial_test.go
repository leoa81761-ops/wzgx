package editorial

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRewriteKeepsEveryParagraph(t *testing.T) {
	first := "验收时核对线槽，是否盖好。"
	second := "拍完留在当天的记录里[1]。"
	draft := map[string]any{
		"title": "短",
		"sections": []any{
			map[string]any{"heading": "", "paragraphs": []any{first}},
			map[string]any{"heading": "", "paragraphs": []any{second}, "list": []any{"不要删掉这条列表。"}},
		},
		"closing": "结束。",
		"sources": []any{map[string]any{"url": "https://www.gov.cn/a", "title": "原文"}},
	}
	if Score(draft) >= 4 {
		t.Fatal(Score(draft))
	}
	model := map[string]any{
		"hook":     "为什么现场总卡在这一步？",
		"headings": []any{"线槽", "记录"},
		"closing":  "可以先记住这一步。欢迎留言说说你的做法。",
	}
	out := Apply(draft, model, nil)
	raw := dump(out)
	if !strings.Contains(raw, first) || !strings.Contains(raw, second) || !strings.Contains(raw, "[1]") || !strings.Contains(raw, "https://www.gov.cn/a") {
		t.Fatal(raw)
	}
	if !strings.Contains(raw, "为什么现场总卡在这一步？") || !strings.Contains(raw, "结束。") {
		t.Fatal(raw)
	}
	if Score(out) <= Score(draft) {
		t.Fatal(Score(out), Score(draft))
	}
	paras := paragraphsOf(out)
	if paras[1] != first || paras[2] != second {
		t.Fatal(paras)
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
