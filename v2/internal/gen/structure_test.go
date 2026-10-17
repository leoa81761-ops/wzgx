package gen

import (
	"encoding/json"
	"strings"
	"testing"

	"gonghao/internal/render"
)

func TestBlockVariantsAndEmptySections(t *testing.T) {
	var a render.Article
	in := `{"title":"标题","summary":"摘要","blocks":[
	{"type":"h2","text":"引言"},{"type":"paragraph","content":"第一段内容。"},
	{"type":"h3","text":"要点"},{"type":"ul","items":[{"text":"甲"},"乙"]},
	{"type":"table","rows":[["项目","数值"],["电阻",1]]},{"type":"p","text":"结尾。"}]}`
	if err := json.Unmarshal([]byte(in), &a); err != nil {
		t.Fatal(err)
	}
	b := a.Blocks
	if b[1].Type != "p" || b[1].Text != "第一段内容。" || b[2].Type != "h2" || b[3].Type != "list" || len(b[3].Items) != 2 || b[4].Rows[1][1] != "1" {
		t.Fatalf("variants: %+v", b)
	}
	if g := Gate(a, "stop", nil); g != "" {
		t.Fatalf("valid article blocked: %s", g)
	}
	cases := map[string]string{
		`[{"type":"h2","text":"引言"},{"type":"h2","text":"正文"},{"type":"p","text":"内容。"}]`: "空小节：「引言」",
		`[{"type":"p","text":"内容。"},{"type":"h2","text":"结尾"}]`:                            "空小节：「结尾」",
		`[{"type":"p","text":"内容。"},{"type":"callout","text":"x"},{"type":"p","text":"完。"}]`: "无法识别",
		`[{"type":"p","text":"内容。"},{"type":"list","items":[]},{"type":"p","text":"完。"}]`:    "是空的",
	}
	for blocks, want := range cases {
		var x render.Article
		json.Unmarshal([]byte(`{"title":"t","blocks":`+blocks+`}`), &x)
		if g := Gate(x, "stop", nil); !strings.Contains(g, want) {
			t.Errorf("%s: got %q want %q", blocks, g, want)
		}
	}
}
