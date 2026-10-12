package headline

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRankLeavesTitlesUnchanged(t *testing.T) {
	short := "短标题"
	long := "这是一个明显超过二十八字的标题，用来确认打分不会把它截断，也不会在后面补上套话"
	mid := "验收之前，先把这三件事都核对完再收工"
	best, alts := Rank([]string{short, "震惊！验收必看", long, mid, "包过验收的办法"}, []string{"包过"}, nil, "")
	if utf8.RuneCountInString(mid) < 18 || utf8.RuneCountInString(mid) > 28 {
		t.Fatal("fixture should sit inside the preferred length", utf8.RuneCountInString(mid))
	}
	pool := append([]string{best}, alts...)
	allowed := map[string]bool{short: true, long: true, mid: true}
	for _, title := range pool {
		if !allowed[title] {
			t.Fatal("title was padded, cut, or invented", title)
		}
		if strings.Contains(title, "写给正在做") || strings.Contains(title, "震惊") || strings.Contains(title, "包过") {
			t.Fatal(title)
		}
	}
	if best != mid {
		t.Fatal(best, alts)
	}
	if len(alts) != 2 {
		t.Fatal(alts)
	}
	outside := Score(short, nil, nil, "")
	inside := Score(mid, nil, nil, "")
	if inside-outside > 12 {
		t.Fatal("length bonus is too large", inside, outside)
	}
	keyword := Score("为什么验收总是过不了这一关呢", nil, nil, "")
	concrete := Score("验收现场还有 3 处线槽没盖", nil, nil, "")
	if concrete <= keyword {
		t.Fatal("a number should outrank a keyword question", concrete, keyword)
	}
}

func TestInconsistentNumberIsDropped(t *testing.T) {
	body := "周末两口人把玩具收进盒子，没有提到任何人数以外的数字。"
	clash := Scored{Title: "全家 8 口人的周末收纳", HasScores: true, Hook: 4, Concrete: 5, Consistent: 5, Persona: 4}
	fit := Scored{Title: "两口人把玩具收进一个盒子", HasScores: true, Hook: 4, Concrete: 5, Consistent: 5, Persona: 4}
	weak := Scored{Title: "周末收纳其实一点也不难", HasScores: true, Hook: 4, Concrete: 2, Consistent: 1, Persona: 4}
	best, alts := RankScored([]Scored{clash, weak, fit}, "两个人的周末收纳", body, nil, nil, "")
	if best.Title != fit.Title {
		t.Fatal(best, alts)
	}
	for _, alt := range alts {
		if alt.Title == clash.Title || alt.Title == weak.Title {
			t.Fatal(alt)
		}
	}
}
