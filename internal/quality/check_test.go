package quality

import (
	"strings"
	"testing"
)

func TestArticleFixtures(t *testing.T) {
	label := "在家过日子的读者"
	good := Fixture{
		Title:      "两口人把玩具收进一个盒子",
		Summary:    "周末把玩具收进盒子。",
		Paragraphs: []string{strings.Repeat("把这一步写具体。", 12) + "[1]", "下午换一本故事，茶还是温的。"},
		Audience:   label,
		Sources:    1,
		WordTarget: 100,
	}
	if failures := Check(good); len(failures) != 0 {
		t.Fatal(failures)
	}
	phrase := "爷爷奶奶眼睛里面闪闪发光"
	bad := Fixture{
		Title:    "周末早晨，阳光从窗外洒进客厅，你推开门，看到爷爷奶奶捧着温茶转圈，那一小步就让上午亮堂起来。",
		Summary:  label + "，通过家里日常小事也能让晚年好心情。",
		Audience: label,
		Paragraphs: []string{
			label + "，你好！" + phrase + "，孩子也跟着笑。",
			"下午又翻开一本，" + phrase + "，屋里安静下来。",
		},
		Sources:    0,
		WordTarget: 800,
	}
	got := map[string]bool{}
	for _, item := range Check(bad) {
		got[item] = true
	}
	for _, want := range []string{"title", "audience", "length", "sources", "repetition"} {
		if !got[want] {
			t.Fatal(got)
		}
	}
}
