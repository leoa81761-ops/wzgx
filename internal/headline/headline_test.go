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
}
