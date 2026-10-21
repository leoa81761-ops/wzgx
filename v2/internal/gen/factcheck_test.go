package gen

import (
	"testing"

	"gonghao/internal/render"
)

func TestCheckFacts(t *testing.T) {
	a := render.Article{Blocks: []render.Block{
		{Type: "p", Text: "学习桌照明要符合 GB 50034-2024 的要求[1]，照度不低于 300 lx。"},
		{Type: "list", Items: []string{"依据国办发〔2024〕12号文件", "2024年3月1日起实施", "色温 4000K 左右", "我用了3个小办法"}},
		{Type: "p", Text: "据说能提高效率 35%。新规 2025年7月 生效。参照 JGJ 16 设计。"},
	}}
	ev := []Evidence{
		{N: 1, Title: "建筑照明设计标准", DocNumber: "GB50034-2024", Date: "2024-03-01", Snippet: "学习桌面照度不低于３００ lx，色温宜在 4000 K"},
		{N: 2, Title: "关于……的通知", DocNumber: "国办发〔2024〕12号"},
	}
	fc := CheckFacts(a, ev)
	got := map[string]Claim{}
	for _, c := range fc.Claims {
		got[c.Text] = c
	}
	want := map[string]bool{"GB 50034-2024": true, "300 lx": true, "国办发〔2024〕12号": true, "2024年3月1日": true, "4000K": true,
		"35%": false, "2025年7月": false, "JGJ 16": false}
	for k, sup := range want {
		c, ok := got[k]
		if !ok {
			t.Errorf("claim %q not found; got %v", k, fc.Claims)
			continue
		}
		if c.Supported != sup {
			t.Errorf("%q supported=%v, want %v", k, c.Supported, sup)
		}
	}
	if _, ok := got["3个"]; ok {
		t.Error("counting words are not claims")
	}
	if fc.Unsupported != 3 || fc.Claims[0].Supported || fc.NoResearch {
		t.Errorf("summary: %+v", fc)
	}
	if !CheckFacts(a, nil).NoResearch {
		t.Error("no research flag")
	}
}
