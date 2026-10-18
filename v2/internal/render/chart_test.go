package render

import "testing"

func TestChartTables(t *testing.T) {
	a := Article{Blocks: []Block{
		{Type: "h2", Text: "三款台灯怎么选"},
		{Type: "table", Rows: [][]string{{"台灯", "照度", "价格", "备注"}, {"甲款", "300 lx", "99元", "便宜"}, {"乙款", "约 500 lx", "1,299元", "好"}, {"丙款", "750lx", "239元", "贵"}}},
		{Type: "table", Rows: [][]string{{"项目", "说明"}, {"位置", "左前方"}, {"高度", "40 厘米以上"}, {"色温", "4000K"}}},
		{Type: "list", Items: []string{"一楼：120 户", "二楼：98 户", "三楼：131 户"}},
		{Type: "table", Rows: [][]string{{"年份", "数量"}, {"2024", "5"}, {"2025", "5"}, {"2026", "5"}}},
	}}
	cs := ChartTables(a)
	if len(cs) != 2 || cs[0].Block != 1 || cs[1].Block != 3 {
		t.Fatalf("charts: %+v", cs)
	}
	if len(cs[0].Series) != 1 || cs[0].Series[0].Unit != "lx" || cs[0].Series[0].Values[1] != 500 || cs[0].Title != "三款台灯怎么选" {
		t.Fatalf("one scale per chart: %+v", cs[0])
	}
	if cs[1].Series[0].Unit != "户" || cs[1].Labels[2] != "三楼" {
		t.Fatalf("list table: %+v", cs[1])
	}
	out := Render(a, Options{Charts: map[int]string{1: "/api/media/abc"}})
	if out.Charts != 1 {
		t.Fatal("chart not placed")
	}
	if len(ChartTables(Article{Blocks: a.Blocks[2:3]})) != 0 {
		t.Fatal("text table charted")
	}
}
