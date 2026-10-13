package editorial

import (
	"strings"
	"testing"
)

func TestRepairArticle70And71Structure(t *testing.T) {
	draft := map[string]any{
		"sections": []any{
			map[string]any{
				"heading":    "线槽检修口留在哪？",
				"paragraphs": []any{"线槽检修口留在哪？答：留在吊顶旁边。"},
			},
			map[string]any{
				"heading":    "线槽检修口留在哪？",
				"paragraphs": []any{"再问一遍也不要重复标题。"},
			},
			map[string]any{
				"heading":    "晚饭后的这段路",
				"paragraphs": []any{"晚饭后的这段路老人先走，家人把拐杖放在门边。", "老人先行只是节奏不是把路让开。"},
				"list":       []any{"2. 先打开检修口。", "3. 再核对编号。", "核对项", "现场要求"},
				"table": map[string]any{
					"headers": []any{"核对项", "现场要求"},
					"rows":    []any{[]any{"老人先行只是节奏不", "家人把灯打开。"}},
				},
			},
		},
		"closing":   "请发布前审阅。",
		"footnotes": []any{"请发布前审阅。"},
	}
	out := RepairStructure(draft)
	blob := fillerText(out)
	if strings.Contains(blob, "请发布前审阅") || strings.Contains(textOf(out["closing"]), "请发布前审阅") {
		t.Fatal(blob)
	}
	if len(anyList(out["footnotes"])) != 0 {
		t.Fatal(out["footnotes"])
	}
	sections := sectionsOf(out)
	faq := 0
	for _, section := range sections {
		if textOf(section["heading"]) == "线槽检修口留在哪？" {
			faq++
			joined := strings.Join(stringList(section["paragraphs"]), "")
			if strings.HasPrefix(joined, "线槽检修口留在哪？") || !strings.Contains(joined, "答：留在吊顶旁边。") {
				t.Fatal(joined)
			}
		}
		if textOf(section["heading"]) == "晚饭后的这段路" {
			paras := stringList(section["paragraphs"])
			if len(paras) == 0 || strings.HasPrefix(paras[0], "晚饭后的这段路") {
				t.Fatal(paras)
			}
			list := stringList(section["list"])
			if len(list) < 2 || !strings.HasPrefix(list[0], "1.") || strings.HasPrefix(list[0], "2.") {
				t.Fatal(list)
			}
			for _, item := range list {
				if item == "核对项" || item == "现场要求" {
					t.Fatal(list)
				}
			}
			table, _ := section["table"].(map[string]any)
			rows, _ := table["rows"].([]any)
			if len(rows) != 1 {
				t.Fatal(rows)
			}
			cells, _ := rows[0].([]any)
			if textOf(cells[0]) != "老人先行只是节奏不是把路让开。" {
				t.Fatal(cells)
			}
		}
	}
	if faq != 1 {
		t.Fatal(sections)
	}
	shifted := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{"2. 先看线槽。", "3. 再核对编号。", "4. 量一下层高。"}}}}
	paras := stringList(RepairStructure(shifted)["sections"].([]any)[0].(map[string]any)["paragraphs"])
	if paras[0] != "1. 先看线槽。" || paras[2] != "3. 量一下层高。" {
		t.Fatal(paras)
	}
}

func TestRepairMarkupHTMLAndMarkdown(t *testing.T) {
	html := `<h2>线槽检修口留在哪？</h2><h2>线槽检修口留在哪？</h2><p>线槽检修口留在哪？答：留在吊顶旁边。</p>` +
		`<ul><li>2. 先打开检修口。</li><li>3. 再核对编号。</li><li>核对项</li></ul>` +
		`<table><tr><th>核对项</th><th>现场要求</th></tr><tr><td>老人先行只是节奏不</td><td>家人把灯打开。</td></tr></table>` +
		`<p>老人先行只是节奏不是把路让开。</p><p>请发布前审阅。</p>`
	fixed := RepairMarkup(html)
	if strings.Count(fixed, "线槽检修口留在哪？") != 1 || strings.Contains(fixed, "请发布前审阅") || strings.Contains(fixed, ">2. 先打开") || strings.Contains(fixed, ">核对项</li>") {
		t.Fatal(fixed)
	}
	if !strings.Contains(fixed, ">1. 先打开检修口。</li>") || !strings.Contains(fixed, "老人先行只是节奏不是把路让开。") {
		t.Fatal(fixed)
	}
	md := "## 线槽检修口留在哪？\n## 线槽检修口留在哪？\n线槽检修口留在哪？答：留在吊顶旁边。\n2. 先打开检修口。\n3. 再核对编号。\n- 核对项\n| 核对项 | 现场要求 |\n| --- | --- |\n| 老人先行只是节奏不 | 家人把灯打开。 |\n老人先行只是节奏不是把路让开。\n请发布前审阅。\n"
	fixedMD := RepairMarkup(md)
	if n := strings.Count(fixedMD, "线槽检修口留在哪？"); n != 1 {
		t.Fatalf("question %d\n%s", n, fixedMD)
	}
	if strings.Contains(fixedMD, "请发布前审阅") || strings.Contains(fixedMD, "\n2. 先打开") || strings.Contains(fixedMD, "- 核对项") {
		t.Fatal(fixedMD)
	}
	if !strings.Contains(fixedMD, "1. 先打开检修口。") || !strings.Contains(fixedMD, "| 老人先行只是节奏不是把路让开。 |") {
		t.Fatal(fixedMD)
	}
}

func TestExperienceTipsDropWarmFiller(t *testing.T) {
	draft := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{
		"今晚的气氛很温馨。",
		"家人之间要有默契。",
	}}}}
	out := EnsureConcreteTips(StripWarmFiller(draft))
	text := fillerText(out)
	if strings.Contains(text, "温馨") || strings.Contains(text, "默契") || ConcreteTips(out) < 5 {
		t.Fatal(ConcreteTips(out), text)
	}
}
