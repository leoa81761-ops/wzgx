package editorial

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/content"
)

// Structure is the prompt hint for one vertical. It does not add facts by itself.
func Structure(verticalKey, direction string) string {
	text := strings.TrimSpace(direction)
	switch {
	case verticalKey == "elv" || strings.Contains(text, "弱电") || strings.Contains(text, "工程"):
		return "结构：开头用现场的一个具体问题钩住读者；用能扫读的小标题；中间放一个工序或案例，只用已经核对过的事实；结尾给一条可执行的收获，再邀请读者留言补充做法。不要新造数字和标准号。"
	case verticalKey == "family" || strings.Contains(text, "母婴") || strings.Contains(text, "美食"):
		return "结构：开头用家里的一个小场景；小标题要短；中间讲一件具体的事；结尾收成一句能记住的话，并邀请读者说说自己的做法。不要写诊断、剂量或恐吓。"
	case verticalKey == "tech" || strings.Contains(text, "数码") || strings.Contains(text, "工具"):
		return "结构：开头点出要用的工具和卡点；小标题按步骤；中间写一个真实操作；结尾留一条今天就能做的收获，并邀请读者补充自己的用法。不要写稳赚或内部消息。"
	default:
		return "结构：开头用一个具体问题钩住读者；小标题能扫读；中间有一个例子或做法；结尾是一句收获，再加一句邀请留言。不要标题党，不要编造数字。"
	}
}

// Score is 0 to 5: hook, two subheads, a case, a takeaway, and a closing invitation.
func Score(draft map[string]any) int {
	n := 0
	if hasHook(draft) {
		n++
	}
	if headingCount(draft) >= 2 {
		n++
	}
	if containsAny(bodyText(draft), "比如", "有一次", "上次", "例如") {
		n++
	}
	if containsAny(bodyText(draft)+" "+closingOf(draft), "所以", "可以先", "记住") {
		n++
	}
	if containsAny(closingOf(draft), "欢迎", "留言", "下次", "如果你") {
		n++
	}
	return n
}

// Reshape lifts a draft under 4 points by rearranging words already in the draft
// and adding a fixed invitation. It does not invent cases, numbers, or sources.
// max is the hard rune cap, usually 130% of the word target. Zero means a small allowance.
func Reshape(draft map[string]any, max int) (map[string]any, bool) {
	if draft == nil || Score(draft) >= 4 {
		return draft, false
	}
	cloned := clone(draft)
	if cloned == nil {
		return draft, false
	}
	if max <= 0 {
		max = content.BodyRunes(cloned) + 80
	}
	changed := false
	if !hasHook(cloned) && addHook(cloned, max) {
		changed = true
	}
	if headingCount(cloned) < 2 && splitSection(cloned, max) {
		changed = true
	}
	if !containsAny(bodyText(cloned)+" "+closingOf(cloned), "所以", "可以先", "记住") && addTakeaway(cloned, max) {
		changed = true
	}
	if !containsAny(closingOf(cloned), "欢迎", "留言", "下次", "如果你") && addCTA(cloned, max) {
		changed = true
	}
	if content.BodyRunes(cloned) > max {
		cloned = content.TrimToMax(cloned, max)
	}
	return cloned, changed
}

func addHook(draft map[string]any, max int) bool {
	para := firstParagraph(draft)
	if para == "" {
		return false
	}
	hook := "先看这一处：" + clip(firstSentence(para), 12)
	if !room(draft, max, utf8.RuneCountInString(hook)) {
		return false
	}
	sections := sectionsOf(draft)
	if len(sections) == 0 {
		return false
	}
	section := sections[0]
	paras, _ := section["paragraphs"].([]any)
	section["paragraphs"] = append([]any{hook}, paras...)
	return true
}

func addTakeaway(draft map[string]any, max int) bool {
	para := sourceParagraph(draft)
	if para == "" {
		return false
	}
	line := "可以先记住：" + clip(firstSentence(para), 16)
	if !room(draft, max, utf8.RuneCountInString(line)) {
		return false
	}
	if closingOf(draft) == "" {
		draft["closing"] = line
	} else {
		draft["closing"] = closingOf(draft) + line
	}
	return true
}

func addCTA(draft map[string]any, max int) bool {
	const cta = "欢迎留言说说你的做法。"
	if !room(draft, max, utf8.RuneCountInString(cta)) {
		return false
	}
	if closingOf(draft) == "" {
		draft["closing"] = cta
	} else {
		draft["closing"] = closingOf(draft) + cta
	}
	return true
}

func splitSection(draft map[string]any, max int) bool {
	sections := sectionsOf(draft)
	if len(sections) == 0 {
		return false
	}
	if len(sections) >= 2 {
		second := sections[1]
		if strings.TrimSpace(textOf(second["heading"])) == "" {
			para := paragraphAt(second)
			if para == "" || !room(draft, max, min(8, utf8.RuneCountInString(para))) {
				return false
			}
			second["heading"] = clip(firstSentence(para), 8)
			return true
		}
		return false
	}
	para := paragraphAt(sections[0])
	parts := sentences(para)
	if len(parts) < 2 {
		return false
	}
	heading := clip(parts[1], 8)
	if heading == "" || !room(draft, max, utf8.RuneCountInString(heading)) {
		return false
	}
	sections[0]["paragraphs"] = []any{parts[0]}
	second := map[string]any{"heading": heading, "paragraphs": []any{parts[1]}}
	draft["sections"] = []any{sections[0], second}
	return true
}

func room(draft map[string]any, max, extra int) bool {
	return extra > 0 && content.BodyRunes(draft)+extra <= max
}

func hasHook(draft map[string]any) bool {
	para := firstParagraph(draft)
	return containsAny(para, "先", "？", "?", "为什么", "怎么")
}

func headingCount(draft map[string]any) int {
	n := 0
	for _, section := range sectionsOf(draft) {
		if strings.TrimSpace(textOf(section["heading"])) != "" {
			n++
		}
	}
	return n
}

func bodyText(draft map[string]any) string {
	var b strings.Builder
	for _, section := range sectionsOf(draft) {
		b.WriteString(textOf(section["heading"]))
		paras, _ := section["paragraphs"].([]any)
		for _, item := range paras {
			b.WriteString(textOf(item))
		}
	}
	return b.String()
}

func sourceParagraph(draft map[string]any) string {
	for _, section := range sectionsOf(draft) {
		paras, _ := section["paragraphs"].([]any)
		for _, item := range paras {
			text := strings.TrimSpace(textOf(item))
			if text == "" || strings.HasPrefix(text, "先看这一处：") {
				continue
			}
			return text
		}
	}
	return ""
}

func firstParagraph(draft map[string]any) string {
	sections := sectionsOf(draft)
	if len(sections) == 0 {
		return ""
	}
	return paragraphAt(sections[0])
}

func paragraphAt(section map[string]any) string {
	paras, _ := section["paragraphs"].([]any)
	for _, item := range paras {
		if text := strings.TrimSpace(textOf(item)); text != "" {
			return text
		}
	}
	return ""
}

func sectionsOf(draft map[string]any) []map[string]any {
	raw, _ := draft["sections"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		section, _ := item.(map[string]any)
		if section != nil {
			out = append(out, section)
		}
	}
	return out
}

func closingOf(draft map[string]any) string {
	return strings.TrimSpace(textOf(draft["closing"]))
}

func firstSentence(text string) string {
	parts := sentences(text)
	if len(parts) == 0 {
		return strings.TrimSpace(text)
	}
	return parts[0]
}

func sentences(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	chunks := strings.FieldsFunc(text, func(r rune) bool {
		return r == '。' || r == '！' || r == '？' || r == '!' || r == '?'
	})
	var out []string
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk != "" {
			out = append(out, chunk)
		}
	}
	return out
}

func containsAny(text string, words ...string) bool {
	for _, word := range words {
		if word != "" && strings.Contains(text, word) {
			return true
		}
	}
	return false
}

func textOf(v any) string {
	s, _ := v.(string)
	return s
}

func clip(s string, n int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n])
}

func clone(draft map[string]any) map[string]any {
	buf, err := json.Marshal(draft)
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(buf, &out) != nil {
		return nil
	}
	return out
}
