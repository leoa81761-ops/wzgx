package headline

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Scored is one title plus the model's own scores. Zero scores mean the model did not score it.
type Scored struct {
	Title      string
	Hook       int
	Concrete   int
	Consistent int
	Persona    int
	HasScores  bool
}

// Rank orders titles the model already wrote. It filters red lines and clickbait.
// Titles are returned unchanged: no padding, no clipping, no template fill.
func Rank(candidates, redLines, recent []string, prefer string) (string, []string) {
	items := make([]Scored, 0, len(candidates))
	for _, title := range candidates {
		items = append(items, Scored{Title: title})
	}
	best, alts := RankScored(items, "", "", redLines, recent, prefer)
	out := make([]string, 0, len(alts))
	for _, alt := range alts {
		out = append(out, alt.Title)
	}
	return best.Title, out
}

// RankScored ranks titles using length, a number or concrete scene, and the model's scores.
// A consistency score below 3, or a number that is not in the summary or body, drops the title.
func RankScored(candidates []Scored, summary, body string, redLines, recent []string, prefer string) (Scored, []Scored) {
	type item struct {
		row   Scored
		score int
	}
	var ranked []item
	seen := map[string]bool{}
	for _, row := range candidates {
		row.Title = strings.TrimSpace(row.Title)
		if row.Title == "" || seen[row.Title] {
			continue
		}
		seen[row.Title] = true
		score := Score(row.Title, redLines, recent, prefer)
		if score <= 0 {
			continue
		}
		if row.HasScores && row.Consistent < 3 {
			continue
		}
		if digitMissing(row.Title, summary, body) {
			continue
		}
		ranked = append(ranked, item{row: row, score: score})
	}
	if len(ranked) == 0 {
		return Scored{}, nil
	}
	best := 0
	for i := 1; i < len(ranked); i++ {
		if ranked[i].score > ranked[best].score {
			best = i
		}
	}
	chosen := ranked[best].row
	var alts []Scored
	for _, row := range ranked {
		if row.row.Title == chosen.Title {
			continue
		}
		alts = append(alts, row.row)
	}
	return chosen, alts
}

// Score ranks one title. Length outside 18–28 runes is a mild penalty.
// A digit or other concrete number is preferred. Keyword and emotion lists are not rewarded.
// Clickbait, red lines, duplicates, titles over 30 runes, and descriptive sentences score zero and are dropped.
// Model self-scores are not added here.
func Score(title string, redLines, recent []string, prefer string) int {
	title = strings.TrimSpace(title)
	if title == "" || banned(title, redLines) || descriptiveSentence(title) {
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
	if strings.TrimSpace(prefer) == "number" && hasDigit(title) {
		score += 4
	}
	if score < 1 {
		return 1
	}
	return score
}

// descriptiveSentence is a narrative line, not a title: over 30 runes, a sentence ending,
// a long clause with a comma, or a scene that tells the reader to imagine or watch.
func descriptiveSentence(title string) bool {
	title = strings.TrimSpace(title)
	n := utf8.RuneCountInString(title)
	if n > 30 {
		return true
	}
	if strings.HasSuffix(title, "。") || strings.HasSuffix(title, "！") || strings.HasSuffix(title, "？") {
		return true
	}
	if n > 22 && (strings.Contains(title, "，") || strings.Contains(title, ",")) {
		return true
	}
	return strings.Contains(title, "想象") || strings.Contains(title, "看着")
}

func digitMissing(title, summary, body string) bool {
	if strings.TrimSpace(summary) == "" && strings.TrimSpace(body) == "" {
		return false
	}
	hay := summary + "\n" + body
	for _, token := range digitTokens(title) {
		if !strings.Contains(hay, token) {
			return true
		}
	}
	return false
}

func digitTokens(title string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range title {
		if unicode.IsDigit(r) {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
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
