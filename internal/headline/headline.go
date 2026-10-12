package headline

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Rank orders titles the model already wrote. It filters red lines and clickbait.
// Titles are returned unchanged: no padding, no clipping, no template fill.
func Rank(candidates, redLines, recent []string, prefer string) (string, []string) {
	type item struct {
		title string
		score int
	}
	var ranked []item
	seen := map[string]bool{}
	for _, title := range candidates {
		title = strings.TrimSpace(title)
		if title == "" || seen[title] {
			continue
		}
		seen[title] = true
		score := Score(title, redLines, recent, prefer)
		if score <= 0 {
			continue
		}
		ranked = append(ranked, item{title: title, score: score})
	}
	if len(ranked) == 0 {
		return "", nil
	}
	best := 0
	for i := 1; i < len(ranked); i++ {
		if ranked[i].score > ranked[best].score {
			best = i
		}
	}
	chosen := ranked[best].title
	var alts []string
	for _, row := range ranked {
		if row.title == chosen {
			continue
		}
		alts = append(alts, row.title)
	}
	return chosen, alts
}

// Score ranks one title. Length outside 18–28 runes is a mild penalty.
// Clickbait, red lines and duplicates score zero and are dropped.
func Score(title string, redLines, recent []string, prefer string) int {
	title = strings.TrimSpace(title)
	if title == "" || banned(title, redLines) {
		return 0
	}
	for _, old := range recent {
		if strings.TrimSpace(old) != "" && strings.TrimSpace(old) == title {
			return 0
		}
	}
	score := 20
	n := utf8.RuneCountInString(title)
	if n < 18 || n > 28 {
		score -= 6
	}
	if hasDigit(title) {
		score += 4
	}
	if containsAny(title, "为什么", "怎么", "哪", "吗", "？", "?") {
		score += 6
	}
	if containsAny(title, "可惜", "值得", "安心", "麻烦", "省", "稳", "容易") {
		score += 4
	}
	switch strings.TrimSpace(prefer) {
	case "question":
		if containsAny(title, "为什么", "怎么", "哪", "吗", "？", "?") {
			score += 4
		}
	case "number":
		if hasDigit(title) {
			score += 4
		}
	}
	if score < 1 {
		return 1
	}
	return score
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
