package headline

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Topic is the material a title has to stay specific about.
type Topic struct {
	Subtopic  string
	Angle     string
	Column    string
	Direction string
}

// Candidates returns at least ten titles built from the topic. Scoring picks among these and the model's titles.
func Candidates(topic Topic) []string {
	sub := clip(strings.TrimSpace(topic.Subtopic), 6)
	if sub == "" {
		sub = "这件事"
	}
	raw := []string{
		sub + "之前，先核对的三件小事",
		"为什么" + sub + "总卡在同一步",
		sub + "可以先做的那一步",
		"关于" + sub + "，少做这一件",
		"做" + sub + "的人最容易漏掉什么",
		sub + "现场，先看这一个点",
		"把" + sub + "拆成今天能做的一步",
		sub + "别急着收尾，先补这一项",
		"一份能把" + sub + "做稳的顺序",
		sub + "时，记录和照片怎么留",
		"同样是" + sub + "，换个顺序更稳",
		sub + "清单里，哪一项最容易空",
	}
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, item := range raw {
		title := fitTitle(item)
		if title == "" || seen[title] {
			continue
		}
		seen[title] = true
		out = append(out, title)
	}
	return out
}

// Score rewards a specific 18–28 character title and rejects clickbait, red lines, and duplicates.
func Score(title, subtopic string, redLines, recent []string, prefer string) int {
	title = strings.TrimSpace(title)
	if title == "" || banned(title, redLines) {
		return 0
	}
	for _, old := range recent {
		if strings.TrimSpace(old) != "" && strings.TrimSpace(old) == title {
			return 0
		}
	}
	n := utf8.RuneCountInString(title)
	score := 10
	switch {
	case n >= 18 && n <= 28:
		score += 40
	case n >= 14 && n <= 32:
		score += 15
	}
	if sub := strings.TrimSpace(subtopic); sub != "" && strings.Contains(title, sub) {
		score += 20
	}
	if hasDigit(title) {
		score += 12
	}
	if containsAny(title, "为什么", "怎么", "哪", "吗", "？", "?") {
		score += 15
	}
	if containsAny(title, "可惜", "值得", "安心", "麻烦", "省", "稳", "容易") {
		score += 8
	}
	switch strings.TrimSpace(prefer) {
	case "question":
		if containsAny(title, "为什么", "怎么", "哪", "吗", "？", "?") {
			score += 12
		}
	case "number":
		if hasDigit(title) {
			score += 12
		}
	}
	return score
}

// Choose scores the generated titles plus the model's title and alternatives.
// The highest score wins. Alternatives stay available for the reader.
func Choose(modelTitle string, modelAlts []string, topic Topic, redLines, recent []string, prefer string) (string, []string) {
	pool := Candidates(topic)
	if strings.TrimSpace(modelTitle) != "" {
		pool = append(pool, modelTitle)
	}
	pool = append(pool, modelAlts...)
	type item struct {
		title string
		score int
	}
	var ranked []item
	seen := map[string]bool{}
	for _, title := range pool {
		title = strings.TrimSpace(title)
		if title == "" || seen[title] {
			continue
		}
		seen[title] = true
		ranked = append(ranked, item{title: title, score: Score(title, topic.Subtopic, redLines, recent, prefer)})
	}
	if len(ranked) == 0 {
		return fitTitle("先把这件事说清楚"), nil
	}
	best := 0
	for i := 1; i < len(ranked); i++ {
		if ranked[i].score > ranked[best].score {
			best = i
		}
	}
	chosen := ranked[best].title
	if ranked[best].score == 0 {
		chosen = Candidates(topic)[0]
	}
	var alts []string
	for _, row := range ranked {
		if row.title == chosen || row.score <= 0 {
			continue
		}
		alts = append(alts, row.title)
		if len(alts) >= 9 {
			break
		}
	}
	return chosen, alts
}

func banned(title string, redLines []string) bool {
	words := append([]string{}, "震惊", "不转不是", "稳赚不赔", "月入", "百分百", "必涨", "内部消息")
	words = append(words, redLines...)
	for _, word := range words {
		word = strings.TrimSpace(word)
		if word != "" && strings.Contains(title, word) {
			return true
		}
	}
	return false
}

func hasDigit(title string) bool {
	for _, r := range title {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func containsAny(title string, words ...string) bool {
	for _, word := range words {
		if strings.Contains(title, word) {
			return true
		}
	}
	return false
}

func fitTitle(s string) string {
	s = strings.TrimSpace(s)
	rs := []rune(s)
	if len(rs) > 28 {
		return string(rs[:28])
	}
	if len(rs) < 18 {
		s += "，写给正在做的人"
		rs = []rune(s)
	}
	if len(rs) > 28 {
		return string(rs[:28])
	}
	if len(rs) < 18 {
		s += "看完能用"
		rs = []rune(s)
		if len(rs) > 28 {
			return string(rs[:28])
		}
	}
	return s
}

func clip(s string, n int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n])
}
