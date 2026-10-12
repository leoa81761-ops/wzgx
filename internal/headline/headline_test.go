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
	allowed := map[string]bool{short: true, mid: true}
	for _, title := range pool {
		if !allowed[title] {
			t.Fatal("title was padded, cut, or invented", title)
		}
		if title == long {
			t.Fatal("titles over 30 runes stay out of the pool", title)
		}
		if strings.Contains(title, "写给正在做") || strings.Contains(title, "震惊") || strings.Contains(title, "包过") {
			t.Fatal(title)
		}
	}
	if best != mid {
		t.Fatal(best, alts)
	}
	if len(alts) != 1 || alts[0] != short {
		t.Fatal(alts)
	}
	outside := Score(short, nil, nil, "")
	inside := Score(mid, nil, nil, "")
	if inside <= outside {
		t.Fatal("a hooked title should outrank a flat one", inside, outside)
	}
	shortHook := "先核这三处线"
	if Score(mid, nil, nil, "")-Score(shortHook, nil, nil, "") > 12 {
		t.Fatal("length bonus is too large", Score(mid, nil, nil, ""), Score(shortHook, nil, nil, ""))
	}
	flat := "验收按顺序做完就行"
	if Score(flat, nil, nil, "") >= Score(mid, nil, nil, "") {
		t.Fatal("flat declarative title should lose", Score(flat, nil, nil, ""), Score(mid, nil, nil, ""))
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

func TestSelfScoresDoNotOutrankAShortTitle(t *testing.T) {
	narrative := "周末早晨，阳光从窗外洒进客厅，你推开门，看到爷爷奶奶捧着温茶转圈，那一小步就让上午亮堂起来。"
	clause := "周末早晨的阳光从窗外洒进客厅，你推开门就看到了转圈"
	if utf8.RuneCountInString(narrative) <= 30 {
		t.Fatal("fixture should be over 30", utf8.RuneCountInString(narrative))
	}
	if utf8.RuneCountInString(clause) <= 22 || !strings.Contains(clause, "，") || strings.HasSuffix(clause, "。") {
		t.Fatal("fixture should be a long comma clause", utf8.RuneCountInString(clause))
	}
	if Score(narrative, nil, nil, "") != 0 || Score(clause, nil, nil, "") != 0 {
		t.Fatal("descriptive titles are rejected")
	}
	mid := "验收之前，先把这三件事都核对完再收工"
	if utf8.RuneCountInString(mid) > 22 {
		t.Fatal("the comma title inside 22 runes must stay eligible", utf8.RuneCountInString(mid))
	}
	scored := Scored{Title: narrative, HasScores: true, Hook: 5, Concrete: 5, Consistent: 5, Persona: 5}
	plain := Scored{Title: mid}
	best, alts := RankScored([]Scored{scored, plain}, "", "", nil, nil, "")
	if best.Title != mid {
		t.Fatal(best, alts)
	}
	for _, alt := range alts {
		if alt.Title == narrative {
			t.Fatal(alt)
		}
	}
}
