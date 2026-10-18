package gen

import (
	"testing"

	"gonghao/internal/render"
)

// A finished last paragraph that ends with citation markers is not "truncated"; one cut mid-sentence still is.
func TestGateTrailingCitations(t *testing.T) {
	art := func(last string) render.Article {
		return render.Article{Title: "住院楼标准层布线", Summary: "摘要", Blocks: []render.Block{
			{Type: "p", Text: "开头一段，说明这层要做什么。"}, {Type: "h2", Text: "怎么做"},
			{Type: "p", Text: "中间一段，讲清楚点位和路由。"}, {Type: "h2", Text: "收尾"},
			{Type: "p", Text: "第二段，说明验收。"}, {Type: "p", Text: last}}}
	}
	if why := Gate(art("规范负责划边界，不负责替本层填数。[1][2]"), "stop", nil); why == "截断：正文最后一段没有写完" {
		t.Fatal("trailing [1][2] counted as truncated")
	}
	if why := Gate(art("规范负责划边界，不负责替本层[1]"), "stop", nil); why != "截断：正文最后一段没有写完" {
		t.Fatalf("cut sentence passed: %q", why)
	}
}
