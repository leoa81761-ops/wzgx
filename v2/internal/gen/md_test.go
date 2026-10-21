package gen

import "testing"

func TestParseArticleMarkdown(t *testing.T) {
	in := "```markdown\n标题：尾款怎么要\n备选标题：A｜B\n摘要：讲清楚。\n\n开头一段，\n接着写。\n\n## 第一部分\n\n- 一\n- 二\n\n| 项 | 钱 |\n| --- | --- |\n| 材料 | 3万 |\n\n> 引用一句。\n\n![现场照片]()\n\n**结尾**这段写完了。\n```"
	a, err := parseArticle(in)
	if err != nil || a.Title != "尾款怎么要" || len(a.AltTitles) != 2 || a.Summary != "讲清楚。" {
		t.Fatalf("%v %+v", err, a)
	}
	want := []string{"p", "h2", "list", "table", "quote", "image", "p"}
	if len(a.Blocks) != len(want) {
		t.Fatalf("blocks %+v", a.Blocks)
	}
	for i, w := range want {
		if a.Blocks[i].Type != w {
			t.Errorf("block %d = %s want %s", i, a.Blocks[i].Type, w)
		}
	}
	if a.Blocks[0].Text != "开头一段，接着写。" || len(a.Blocks[3].Rows) != 2 {
		t.Errorf("%+v", a.Blocks)
	}
	b, err := parseArticle(articleMarkdown(a))
	if err != nil || len(b.Blocks) != len(a.Blocks) || b.Title != a.Title {
		t.Errorf("roundtrip %v %+v", err, b)
	}
}

func TestSplitNotes(t *testing.T) {
	n, _, body := splitNotes("差距：\n- 开头太平\n- 结尾重复\n=====\n标题：X\n\n正文。")
	if len(n) != 2 || body != "标题：X\n\n正文。" {
		t.Errorf("%v %q", n, body)
	}
	_, head, body := splitNotes("无差距")
	if body != "" || head != "无差距" {
		t.Errorf("%q %q", head, body)
	}
	n, _, body = splitNotes("结论：需修改\n问题：\n- 诉讼时效写成1年，应为3年\n标题：Y\n\n正文。")
	if len(n) != 1 || body == "" {
		t.Errorf("%v %q", n, body)
	}
}

func TestParseArticleWrapped(t *testing.T) {
	for _, in := range []string{`{"content":"标题：T\n\n正文一段。"}`, "```json\n{\"article\":\"标题：T\\n\\n正文一段。\"}\n```"} {
		a, err := parseArticle(in)
		if err != nil || a.Title != "T" || len(a.Blocks) != 1 {
			t.Errorf("%q: %v %+v", in, err, a)
		}
	}
	if !proseStage("write") || proseStage("plan") {
		t.Error("proseStage")
	}
}
