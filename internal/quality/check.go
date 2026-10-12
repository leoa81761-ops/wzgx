package quality

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/content"
	"github.com/leoa81761-ops/wzgx/internal/editorial"
)

var citedMark = regexp.MustCompile(`\[\d+\]`)

// Fixture is one offline article. It is not a model transcript.
type Fixture struct {
	Title      string
	Summary    string
	Paragraphs []string
	Audience   string
	Sources    int
	WordTarget int
}

// Check reports regressions: a title over 30 runes, an audience-label leak,
// a body under 90% of the target, no cited source, or a repeated 12-rune phrase.
func Check(fix Fixture) []string {
	var failures []string
	if utf8.RuneCountInString(strings.TrimSpace(fix.Title)) > 30 {
		failures = append(failures, "title")
	}
	label := strings.TrimSpace(fix.Audience)
	if label != "" && leaks(label, fix) {
		failures = append(failures, "audience")
	}
	runes := content.BodyRunes(draftOf(fix))
	if fix.WordTarget > 0 && runes*100 < fix.WordTarget*90 {
		failures = append(failures, "length")
	}
	if fix.Sources < 1 || !cited(fix.Paragraphs) {
		failures = append(failures, "sources")
	}
	if repeatedNGram(fix.Paragraphs, 12) {
		failures = append(failures, "repetition")
	}
	return failures
}

func draftOf(fix Fixture) map[string]any {
	paras := make([]any, len(fix.Paragraphs))
	for i, para := range fix.Paragraphs {
		paras[i] = para
	}
	return map[string]any{"sections": []any{map[string]any{"paragraphs": paras}}}
}

func leaks(label string, fix Fixture) bool {
	if strings.Contains(fix.Title, label) || strings.Contains(fix.Summary, label) {
		return true
	}
	for _, para := range fix.Paragraphs {
		if strings.Contains(para, label) {
			return true
		}
	}
	return false
}

func cited(paragraphs []string) bool {
	for _, para := range paragraphs {
		if citedMark.MatchString(para) {
			return true
		}
	}
	return false
}

func repeatedNGram(paragraphs []string, n int) bool {
	for i := 0; i < len(paragraphs); i++ {
		for j := i + 1; j < len(paragraphs); j++ {
			if editorial.SharesNGram(paragraphs[i], paragraphs[j], n) {
				return true
			}
		}
	}
	return false
}
