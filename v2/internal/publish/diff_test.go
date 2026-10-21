package publish

import "testing"

func TestChangeRatio(t *testing.T) {
	pushed := `<section><h2 style="x">先看亮度</h2><p style="a">学习桌面照度不低于 300 lx。灯放在左前方。</p></section>`
	// WeChat rewrites markup and attributes; the text is the same
	same := `<section data-x="1"><h2>先看亮度</h2><p>学习桌面照度不低于&nbsp;300 lx。灯放在左前方。</p></section>`
	if r := changeRatio(pushed, same); r != 0 {
		t.Fatalf("markup-only change counted: %v", r)
	}
	edited := `<h2>先看亮度</h2><p>学习桌面照度不低于 300 lx。我家的灯放在右边，因为孩子用左手写字。</p>`
	if r := changeRatio(pushed, edited); r < 0.2 || r > 0.8 {
		t.Fatalf("one sentence rewritten: %v", r)
	}
	if r := changeRatio(pushed, "<p>完全不同的一篇文章。</p>"); r != 1 {
		t.Fatalf("rewrite: %v", r)
	}
}
