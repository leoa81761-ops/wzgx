package editorial

import (
	"encoding/json"
	"strings"
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

// Needs says which parts are weak enough to send back to the model.
type Needs struct {
	Hook     bool
	Headings bool
	Closing  bool
}

func (n Needs) Any() bool {
	return n.Hook || n.Headings || n.Closing
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

// NeedsRewrite is empty when the draft already scores 4 or more.
func NeedsRewrite(draft map[string]any) Needs {
	if draft == nil || Score(draft) >= 4 {
		return Needs{}
	}
	return Needs{
		Hook:     !hasHook(draft),
		Headings: headingCount(draft) < 2,
		Closing:  !containsAny(bodyText(draft)+" "+closingOf(draft), "所以", "可以先", "记住") || !containsAny(closingOf(draft), "欢迎", "留言", "下次", "如果你"),
	}
}

// Apply splices a model rewrite of the weak parts back into the draft.
// A failed call, a lower score, or any lost paragraph keeps the original.
func Apply(original map[string]any, model map[string]any, callErr error) map[string]any {
	if callErr != nil || original == nil || model == nil {
		return original
	}
	need := NeedsRewrite(original)
	if !need.Any() {
		return original
	}
	spliced := Splice(original, model, need)
	if spliced == nil || !keepsParagraphs(original, spliced) || Score(spliced) <= Score(original) {
		return original
	}
	return spliced
}

// Splice copies the draft and only fills the requested weak parts.
// Existing paragraphs, lists, tables and sources are left character for character.
func Splice(original, model map[string]any, need Needs) map[string]any {
	cloned := clone(original)
	if cloned == nil {
		return nil
	}
	if need.Hook {
		if hook := strings.TrimSpace(textOf(model["hook"])); hook != "" {
			prependHook(cloned, hook)
		}
	}
	if need.Headings {
		applyHeadings(cloned, stringList(model["headings"]))
	}
	if need.Closing {
		if closing := strings.TrimSpace(textOf(model["closing"])); closing != "" {
			old := closingOf(cloned)
			switch {
			case old == "":
				cloned["closing"] = closing
			case strings.Contains(closing, old):
				cloned["closing"] = closing
			default:
				cloned["closing"] = old + "\n\n" + closing
			}
		}
	}
	return cloned
}

func prependHook(draft map[string]any, hook string) {
	sections := sectionsOf(draft)
	if len(sections) == 0 {
		draft["sections"] = []any{map[string]any{"heading": "", "paragraphs": []any{hook}}}
		return
	}
	paras, _ := sections[0]["paragraphs"].([]any)
	sections[0]["paragraphs"] = append([]any{hook}, paras...)
}

func applyHeadings(draft map[string]any, headings []string) {
	if len(headings) == 0 {
		return
	}
	sections := sectionsOf(draft)
	used := 0
	for _, section := range sections {
		if strings.TrimSpace(textOf(section["heading"])) != "" || used >= len(headings) {
			continue
		}
		section["heading"] = headings[used]
		used++
	}
}

func keepsParagraphs(original, next map[string]any) bool {
	old := paragraphsOf(original)
	neu := paragraphsOf(next)
	i := 0
	for _, paragraph := range neu {
		if i < len(old) && paragraph == old[i] {
			i++
		}
	}
	return i == len(old)
}

func paragraphsOf(draft map[string]any) []string {
	var out []string
	for _, section := range sectionsOf(draft) {
		paras, _ := section["paragraphs"].([]any)
		for _, item := range paras {
			if text := textOf(item); text != "" {
				out = append(out, text)
			}
		}
	}
	return out
}

func hasHook(draft map[string]any) bool {
	return containsAny(firstParagraph(draft), "先", "？", "?", "为什么", "怎么")
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
		for _, paragraph := range paragraphsOf(map[string]any{"sections": []any{section}}) {
			b.WriteString(paragraph)
		}
	}
	return b.String()
}

func firstParagraph(draft map[string]any) string {
	paras := paragraphsOf(draft)
	if len(paras) == 0 {
		return ""
	}
	return paras[0]
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

func stringList(raw any) []string {
	list, _ := raw.([]any)
	var out []string
	for _, item := range list {
		if text := strings.TrimSpace(textOf(item)); text != "" {
			out = append(out, text)
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
