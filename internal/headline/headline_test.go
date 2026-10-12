package headline

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChoosePrefersSpecificTitleAndKeepsAlternatives(t *testing.T) {
	topic := Topic{Subtopic: "验收", Angle: "少做一件事", Column: "清单", Direction: "弱电智能化"}
	cands := Candidates(topic)
	if len(cands) < 10 {
		t.Fatal(len(cands))
	}
	for _, title := range cands {
		n := utf8.RuneCountInString(title)
		if n < 18 || n > 28 {
			t.Fatal(title, n)
		}
	}
	best, alts := Choose("震惊！验收必看", []string{"稳赚不赔的验收", "验收可以先做的一件小事"}, topic, []string{"包过验收"}, []string{"验收之前，先核对的三件小事，写给正在做的人"}, "")
	if strings.Contains(best, "震惊") || strings.Contains(best, "稳赚") || strings.Contains(best, "包过") {
		t.Fatal(best)
	}
	n := utf8.RuneCountInString(best)
	if n < 18 || n > 28 {
		t.Fatal(best, n)
	}
	if len(alts) < 1 {
		t.Fatal(alts)
	}
	for _, alt := range alts {
		if alt == best || strings.Contains(alt, "震惊") {
			t.Fatal(alt)
		}
	}
	again, _ := Choose("为什么验收总在最后才发现", nil, topic, nil, nil, "question")
	numbered, _ := Choose("验收要留3张照片", nil, topic, nil, nil, "number")
	if Score(again, "验收", nil, nil, "question") < Score(again, "验收", nil, nil, "") {
		t.Fatal("question bias")
	}
	if Score(numbered, "验收", nil, nil, "number") < Score(numbered, "验收", nil, nil, "") {
		t.Fatal("number bias")
	}
}
