package quality

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/content"
	"github.com/leoa81761-ops/wzgx/internal/editorial"
)

var (
	citedMark    = regexp.MustCompile(`\[\d+\]`)
	numberedStep = regexp.MustCompile(`(?:^|\s)(?:\d+[\.、]|第[一二三四五六七八九十]|（\d+）)`)
	dateMark     = regexp.MustCompile(`\d{4}|\d{1,2}月|\d{1,2}日`)
)

// Fixture is one offline article. It is not a model transcript.
type Fixture struct {
	Title      string
	Summary    string
	Paragraphs []string
	Audience   string
	Sources    int
	WordTarget int
	Structure  string
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
	if structure := strings.TrimSpace(fix.Structure); structure != "" && !StructureOK(structure, fix.Paragraphs) {
		failures = append(failures, "structure")
	}
	return failures
}

// StructureOK reports whether the body matches a column template.
// news-brief needs dated items and a source mark. Q&A needs a question and an answer. steps needs numbered steps.
func StructureOK(structure string, paragraphs []string) bool {
	paragraphs = nonempty(paragraphs)
	if len(paragraphs) == 0 {
		return false
	}
	switch normalizeStructure(structure) {
	case "news":
		sourced := false
		for _, para := range paragraphs {
			if !hasDate(para) {
				return false
			}
			if citedMark.MatchString(para) {
				sourced = true
			}
		}
		return sourced && !repeatedOpener(paragraphs)
	case "qa":
		return qaPaired(paragraphs) && !repeatedOpener(paragraphs)
	case "steps":
		for _, para := range paragraphs {
			if !numberedStep.MatchString(para) {
				return false
			}
		}
		return !repeatedOpener(paragraphs)
	default:
		return true
	}
}

// RepairDraft rewrites paragraphs so a news, Q&A, or steps column matches its template.
// It does not invent a source mark when the draft has no sources.
func RepairDraft(structure string, draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	kind := normalizeStructure(structure)
	if kind != "news" && kind != "qa" && kind != "steps" {
		return draft
	}
	hasSource := false
	if sources, _ := draft["sources"].([]any); len(sources) > 0 {
		hasSource = true
	}
	sections, _ := draft["sections"].([]any)
	for _, raw := range sections {
		section, _ := raw.(map[string]any)
		if section == nil {
			continue
		}
		paras, _ := section["paragraphs"].([]any)
		var next []any
		for _, item := range paras {
			text, _ := item.(string)
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			switch kind {
			case "news":
				if !hasDate(text) {
					text = dateWord(len(next)) + "，" + text
				}
				if hasSource && !citedMark.MatchString(text) {
					text += " [1]"
				}
			case "qa":
				if len(next)%2 == 0 {
					text = asQuestion(text)
				} else {
					text = asAnswer(text)
				}
			case "steps":
				if !numberedStep.MatchString(text) {
					text = strconv.Itoa(len(next)+1) + ". " + text
				}
			}
			next = append(next, text)
		}
		next = varyOpeners(next)
		if kind == "qa" && len(next)%2 == 1 {
			next = append(next, "答：按上面的问题，先做最小的一步。")
		}
		if next == nil {
			next = []any{}
		}
		section["paragraphs"] = next
	}
	return draft
}

func nonempty(paragraphs []string) []string {
	var out []string
	for _, para := range paragraphs {
		if strings.TrimSpace(para) != "" {
			out = append(out, strings.TrimSpace(para))
		}
	}
	return out
}

func normalizeStructure(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "news", "news-brief", "快报":
		return "news"
	case "steps", "step-by-step", "breakdown", "拆解":
		return "steps"
	case "qa", "q&a", "faq", "问答":
		return "qa"
	default:
		return "custom"
	}
}

func qaPaired(paragraphs []string) bool {
	pending := false
	pairs := 0
	for _, para := range paragraphs {
		answer := strings.Contains(para, "答")
		question := strings.Contains(para, "？") || strings.Contains(para, "?")
		if answer && question {
			return false
		}
		if answer {
			if !pending {
				return false
			}
			pending = false
			pairs++
			continue
		}
		if question {
			if pending {
				return false
			}
			trimmed := strings.TrimSpace(para)
			if !strings.HasSuffix(trimmed, "？") && !strings.HasSuffix(trimmed, "?") {
				return false
			}
			pending = true
			continue
		}
		if pending {
			return false
		}
	}
	return pairs > 0 && !pending
}

func repeatedOpener(paragraphs []string) bool {
	seen := map[string]bool{}
	for _, para := range paragraphs {
		key := leadingDate(para)
		if key == "" {
			continue
		}
		if seen[key] {
			return true
		}
		seen[key] = true
	}
	return false
}

func leadingDate(text string) string {
	text = strings.TrimSpace(text)
	for _, word := range []string{"近日", "今天", "昨天", "本周", "上周"} {
		if strings.HasPrefix(text, word) {
			return word
		}
	}
	return ""
}

func dateWord(index int) string {
	words := []string{"近日", "今天", "昨天", "本周", "上周"}
	return words[index%len(words)]
}

func asQuestion(text string) string {
	text = strings.ReplaceAll(text, "?", "")
	text = strings.ReplaceAll(text, "？", "")
	text = strings.TrimSpace(strings.TrimPrefix(text, "答："))
	text = strings.TrimSpace(strings.TrimPrefix(text, "答:"))
	text = strings.TrimRight(text, "。")
	return text + "？"
}

func asAnswer(text string) string {
	text = strings.ReplaceAll(text, "?", "")
	text = strings.ReplaceAll(text, "？", "")
	text = strings.TrimSpace(text)
	if !strings.Contains(text, "答") {
		text = "答：" + text
	}
	return text
}

func varyOpeners(paras []any) []any {
	seen := map[string]bool{}
	var out []any
	for _, item := range paras {
		text, _ := item.(string)
		if key := leadingDate(text); key != "" && seen[key] {
			word := dateWord(len(out) + 1)
			if word == key {
				word = dateWord(len(out) + 2)
			}
			text = word + "，" + strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, key), "，"))
		}
		if key := leadingDate(text); key != "" {
			seen[key] = true
		}
		out = append(out, text)
	}
	return out
}

func hasDate(text string) bool {
	if dateMark.MatchString(text) {
		return true
	}
	for _, word := range []string{"近日", "今天", "昨天", "本周", "上周", "周一", "周二", "周三", "周四", "周五", "周六", "周日"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
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
