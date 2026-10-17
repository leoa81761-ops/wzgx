package render

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	a := Article{Title: "T", Blocks: []Block{
		{Type: "p", Text: "按 GB 50174 的要求[1]，机房要分区[2]。<script>"},
		{Type: "h2", Text: "一"},
		{Type: "list", Items: []string{"温度：18–27℃", "湿度：40%–60%", "照度：不低于 500 lx"}},
		{Type: "h2", Text: "二"},
		{Type: "list", Items: []string{"先断电", "再检查"}},
		{Type: "table", Rows: [][]string{{"a", "b"}, {"c"}}},
		{Type: "p", Text: "结束。"},
	}}
	out := Render(a, Options{Signature: "先看现场，再谈方案", DefaultImageURL: "https://x/cover.jpg",
		Sources: map[int]Source{1: {N: 1, Kind: "national_standard", Title: "数据中心设计规范", DocNumber: "GB 50174-2017"}}})
	h := out.HTML
	if strings.Contains(h, "<script>") {
		t.Fatal("not escaped")
	}
	if !strings.Contains(h, "<sup") || strings.Contains(h, "[2]") {
		t.Fatal("only official sources are cited")
	}
	if out.Tables != 1 || !strings.Contains(h, "<th") || !strings.Contains(h, "18–27℃") {
		t.Fatalf("kv list should become a table: %d", out.Tables)
	}
	if !strings.Contains(h, "<li>先断电</li>") {
		t.Fatal("plain list stays a list")
	}
	if out.Images != 1 || !strings.Contains(h, "cover.jpg") {
		t.Fatal("auto image slot")
	}
	if !strings.HasSuffix(strings.SplitN(h, "依据", 2)[0], `">`) || !strings.Contains(h, "先看现场，再谈方案") {
		t.Fatal("signature")
	}
	if len(out.Cited) != 1 || !strings.Contains(h, "GB 50174-2017") {
		t.Fatal("sources section")
	}
	if strings.Contains(h, "AI 辅助") {
		t.Fatal("AI label off by default")
	}
	if out2 := Render(Article{Blocks: []Block{{Type: "p", Text: "x[1]"}}}, Options{AILabel: true}); strings.Contains(out2.HTML, "依据") || !strings.Contains(out2.HTML, "AI 辅助") {
		t.Fatal("no sources / ai label")
	}
}

func TestTableFit(t *testing.T) {
	if _, ok := TableFit([]string{"a：1", "b：2"}); ok {
		t.Fatal("two items is not a table")
	}
	if _, ok := TableFit([]string{"a：12", "b：23", "就是一句普通的话"}); ok {
		t.Fatal("mixed list")
	}
}
