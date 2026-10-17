package publish

import (
	"html"
	"regexp"
	"strings"
)

var (
	tagRe   = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRe = regexp.MustCompile(`\s+`)
	sentRe  = regexp.MustCompile(`[^。！？!?；;\n]+[。！？!?；;]?`)
)

// plainText strips tags and entities so WeChat's own HTML rewriting doesn't count as an edit.
func plainText(h string) string {
	h = strings.NewReplacer("</p>", "\n", "<br>", "\n", "<br/>", "\n", "</li>", "\n", "</h2>", "\n", "</section>", "\n").Replace(h)
	t := strings.ReplaceAll(html.UnescapeString(tagRe.ReplaceAllString(h, "")), "\u00a0", " ")
	var lines []string
	for _, l := range strings.Split(t, "\n") {
		if l = strings.TrimSpace(spaceRe.ReplaceAllString(l, " ")); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// changeRatio is a rough share of text that differs between what we pushed and what is in the
// draft now: sentences present on one side only, weighted by length, over the length of both.
// 0 = untouched, 1 = entirely rewritten.
func changeRatio(before, after string) float64 {
	a, b := sentences(plainText(before)), sentences(plainText(after))
	count := map[string]int{}
	total := 0
	for _, s := range a {
		count[s]++
		total += len([]rune(s))
	}
	changed := 0
	for _, s := range b {
		total += len([]rune(s))
		if count[s] > 0 {
			count[s]--
		} else {
			changed += len([]rune(s))
		}
	}
	for s, n := range count {
		changed += n * len([]rune(s))
	}
	if total == 0 {
		return 0
	}
	return float64(changed) / float64(total)
}

func sentences(t string) []string {
	var out []string
	for _, m := range sentRe.FindAllString(t, -1) {
		if m = strings.TrimSpace(m); m != "" {
			out = append(out, m)
		}
	}
	return out
}
