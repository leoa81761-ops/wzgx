package gen

import (
	"testing"

	"gonghao/internal/render"
)

func TestBadTopic(t *testing.T) {
	w := writingContext{AccountName: "奶奶不当家", ColumnName: "妙招", TypeName: "生活妙招", Recent: []string{"温水棉球敷眼 5 分钟"}}
	for _, bad := range []string{"奶奶不当家", "生活妙招", "妙招", "奶奶不当家：妙招", "温水棉球敷眼5分钟"} {
		if badTopic(bad, w) == "" {
			t.Errorf("accepted %q", bad)
		}
	}
	if why := badTopic("换季被子怎么晒才不返潮", w); why != "" {
		t.Errorf("rejected concrete topic: %s", why)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		e    Evidence
		want string
	}{
		{Evidence{Title: "数据中心设计规范", DocNumber: "GB 50174-2017"}, "national_standard"},
		{Evidence{Title: "关于推进智慧楼宇建设的通知", URL: "https://www.miit.gov.cn/a"}, "gov_document"},
		{Evidence{Title: "关于印发某办法的通知", DocNumber: "工信部〔2025〕12号"}, "gov_document"},
		{Evidence{Title: "某公司新闻稿", URL: "https://www.miit.gov.cn/b"}, "other"},
		{Evidence{Title: "博客自称通知", URL: "https://blog.cn/x", Kind: "gov_document"}, "other"},
		{Evidence{Title: "说是标准", Kind: "national_standard"}, "other"},
	}
	for _, c := range cases {
		classify(&c.e)
		if c.e.Kind != c.want {
			t.Errorf("%q: %s want %s", c.e.Title, c.e.Kind, c.want)
		}
	}
}

func TestGateSignatureWithoutPeriodIsFine(t *testing.T) {
	// the signature is appended by the renderer, so the model's last block is a normal sentence
	a := render.Article{Title: "t", Blocks: []render.Block{{Type: "p", Text: "写完了。"}, {Type: "list", Items: []string{"不带句号的条目"}}}}
	if r := Gate(a, "stop", nil); r != "" {
		t.Fatal(r)
	}
	if r := Gate(render.Article{Title: "t"}, "stop", nil); r == "" {
		t.Fatal("empty body passed")
	}
}

func TestJunkURL(t *testing.T) {
	if !junkURL("https://www.douyin.com/v/1") || !junkURL("https://baike.baidu.com/item/x") || junkURL("https://www.nhc.gov.cn/a") {
		t.Fatal("junk filter")
	}
}
