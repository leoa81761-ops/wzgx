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

// RepairDraft does not insert questions, date words, filler answers, or citation marks.
// A column that misses its structure is rewritten by the model and checked again.
func RepairDraft(structure string, draft map[string]any) map[string]any {
	return draft
}

// AlignStructure reshapes a draft so it matches the column template the checker uses.
// Q&A may be one paragraph (问句以问号结尾，后面接「答：」，答案里不再出现问号) or two adjacent paragraphs.
// Steps need a number on every paragraph. News is left unchanged, because a date is not invented here.
func AlignStructure(structure string, draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	kind := normalizeStructure(structure)
	switch kind {
	case "qa":
		alignQA(draft)
	case "steps":
		alignSteps(draft)
	}
	return draft
}

// DatedText reports a date in a source title, snippet, or sentence.
func DatedText(text string) bool {
	return hasDate(text)
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
		if combinedQA(para) {
			if pending {
				return false
			}
			pairs++
			continue
		}
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

// combinedQA is one paragraph whose question mark comes before 答, with no question mark in the answer.
func combinedQA(para string) bool {
	answerAt := strings.Index(para, "答")
	if answerAt < 0 {
		return false
	}
	q := strings.Index(para, "？")
	if alt := strings.Index(para, "?"); q < 0 || (alt >= 0 && alt < q) {
		q = alt
	}
	if q < 0 || q > answerAt {
		return false
	}
	rest := para[answerAt+len("答"):]
	if strings.Contains(rest, "？") || strings.Contains(rest, "?") {
		return false
	}
	rest = strings.Trim(rest, "：: \t")
	return strings.TrimSpace(rest) != ""
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

func alignQA(draft map[string]any) {
	sections, _ := draft["sections"].([]any)
	for _, raw := range sections {
		section, _ := raw.(map[string]any)
		if section == nil {
			continue
		}
		paras, _ := section["paragraphs"].([]any)
		for i, item := range paras {
			text, _ := item.(string)
			paras[i] = calmQAParagraph(text)
		}
		section["paragraphs"] = paras
	}
	if StructureOK("qa", draftParagraphs(draft)) {
		return
	}
	if len(sections) == 0 {
		return
	}
	section, _ := sections[0].(map[string]any)
	if section == nil {
		return
	}
	paras, _ := section["paragraphs"].([]any)
	if !hasQuestion(paras) {
		title := strings.TrimSpace(textOf(draft["title"]))
		title = strings.TrimRight(title, "。？?！! ")
		if title == "" {
			return
		}
		paras = append([]any{title + "？"}, paras...)
	}
	if len(paras) >= 2 {
		text, _ := paras[1].(string)
		if !strings.Contains(text, "答") && !strings.Contains(text, "？") && !strings.Contains(text, "?") {
			paras[1] = "答：" + text
		}
	}
	section["paragraphs"] = paras
	sections[0] = section
	draft["sections"] = sections
}

func calmQAParagraph(text string) string {
	if combinedQA(text) || strings.HasSuffix(strings.TrimSpace(text), "？") || strings.HasSuffix(strings.TrimSpace(text), "?") {
		if combinedQA(text) {
			return text
		}
		if !strings.Contains(text, "答") {
			return text
		}
	}
	if i := strings.Index(text, "答"); i >= 0 {
		head, tail := text[:i], text[i:]
		tail = strings.NewReplacer("？", "。", "?", "。").Replace(tail)
		return head + tail
	}
	if strings.Contains(text, "？") || strings.Contains(text, "?") {
		if strings.HasSuffix(strings.TrimSpace(text), "？") || strings.HasSuffix(strings.TrimSpace(text), "?") {
			return text
		}
		return strings.NewReplacer("？", "。", "?", "。").Replace(text)
	}
	return text
}

func hasQuestion(paras []any) bool {
	for _, item := range paras {
		text, _ := item.(string)
		if combinedQA(text) {
			return true
		}
		trimmed := strings.TrimSpace(text)
		if (strings.HasSuffix(trimmed, "？") || strings.HasSuffix(trimmed, "?")) && !strings.Contains(text, "答") {
			return true
		}
	}
	return false
}

func alignSteps(draft map[string]any) {
	if StructureOK("steps", draftParagraphs(draft)) {
		return
	}
	sections, _ := draft["sections"].([]any)
	n := 1
	for _, raw := range sections {
		section, _ := raw.(map[string]any)
		if section == nil {
			continue
		}
		paras, _ := section["paragraphs"].([]any)
		for i, item := range paras {
			text := strings.TrimSpace(textOf(item))
			if text == "" || numberedStep.MatchString(text) {
				continue
			}
			paras[i] = strconv.Itoa(n) + ". " + text
			n++
		}
		section["paragraphs"] = paras
	}
	draft["sections"] = sections
}

func draftParagraphs(draft map[string]any) []string {
	var out []string
	sections, _ := draft["sections"].([]any)
	for _, raw := range sections {
		section, _ := raw.(map[string]any)
		if section == nil {
			continue
		}
		paras, _ := section["paragraphs"].([]any)
		for _, item := range paras {
			if text := strings.TrimSpace(textOf(item)); text != "" {
				out = append(out, text)
			}
		}
	}
	return out
}

func textOf(v any) string {
	s, _ := v.(string)
	return s
}
