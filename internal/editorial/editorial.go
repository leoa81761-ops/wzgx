package editorial

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
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

// Quality is the rubric returned by the review call. Present is false when that call did not score the draft.
type Quality struct {
	Present  bool
	Hook     int
	Headings bool
	Closing  bool
	Motif    bool
}

// Score is 0 to 5 from structure: an opening, two subheads, a takeaway, and a closing invitation.
// Hook words such as 先 / ？ / 为什么 are not a score.
func Score(draft map[string]any) int {
	n := 0
	if strings.TrimSpace(firstParagraph(draft)) != "" {
		n++
	}
	if headingCount(draft) >= 2 {
		n++
	}
	if containsAny(bodyText(draft), "比如", "有一次", "上次", "例如", "假设") {
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

// NeedsRewrite uses the review rubric for the hook. Without a rubric, the hook is left alone.
func NeedsRewrite(draft map[string]any) Needs {
	if draft == nil {
		return Needs{}
	}
	q := QualityOf(draft)
	headings := headingCount(draft) < 2
	closing := !containsAny(bodyText(draft)+" "+closingOf(draft), "所以", "可以先", "记住") || !containsAny(closingOf(draft), "欢迎", "留言", "下次", "如果你")
	hook := false
	if q.Present {
		hook = q.Hook < 4
		if q.Headings {
			headings = false
		}
		if q.Closing {
			closing = false
		}
	}
	if !hook && !headings && !closing {
		return Needs{}
	}
	return Needs{Hook: hook, Headings: headings, Closing: closing}
}

// QualityOf reads a rubric stored by the review validator.
func QualityOf(draft map[string]any) Quality {
	if draft == nil {
		return Quality{}
	}
	raw, _ := draft["_quality"].(map[string]any)
	if raw == nil {
		raw, _ = draft["quality"].(map[string]any)
	}
	if raw == nil {
		return Quality{}
	}
	return Quality{
		Present:  true,
		Hook:     intOf(raw["hook"]),
		Headings: boolOf(raw["headings"]),
		Closing:  boolOf(raw["closing"]),
		Motif:    boolOf(raw["motif"]),
	}
}

// Apply splices a model rewrite of the weak parts back into the draft.
// A failed call, a lower score, or any lost body paragraph keeps the original.
// A short hook may replace the first paragraph. A long or overlapping hook is ignored.
func Apply(original map[string]any, model map[string]any, callErr error) map[string]any {
	if callErr != nil || original == nil || model == nil {
		return original
	}
	need := NeedsRewrite(original)
	if !need.Any() {
		return original
	}
	spliced := Splice(original, model, need)
	if spliced == nil || !keepsParagraphs(original, spliced) || Score(spliced) < Score(original) {
		return original
	}
	if sameText(original, spliced) {
		return original
	}
	return spliced
}

// Splice copies the draft and only fills the requested weak parts.
// Existing body paragraphs, lists, tables and sources stay, except the opening when a short hook replaces it.
func Splice(original, model map[string]any, need Needs) map[string]any {
	cloned := clone(original)
	if cloned == nil {
		return nil
	}
	if need.Hook {
		if hook := strings.TrimSpace(textOf(model["hook"])); hook != "" {
			applyHook(cloned, hook)
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

// HookAccepted reports whether a hook is short enough and distinct enough to replace the opening.
func HookAccepted(hook, opening string) bool {
	hook = strings.TrimSpace(hook)
	if hook == "" || utf8.RuneCountInString(hook) > 40 {
		return false
	}
	opening = strings.TrimSpace(opening)
	if opening == "" {
		return true
	}
	return Overlap(hook, opening) <= 0.5
}

func applyHook(draft map[string]any, hook string) bool {
	if !HookAccepted(hook, firstParagraph(draft)) {
		return false
	}
	sections := sectionsOf(draft)
	if len(sections) == 0 {
		draft["sections"] = []any{map[string]any{"heading": "", "paragraphs": []any{hook}}}
		return true
	}
	paras, _ := sections[0]["paragraphs"].([]any)
	if len(paras) == 0 {
		sections[0]["paragraphs"] = []any{hook}
		return true
	}
	paras[0] = hook
	sections[0]["paragraphs"] = paras
	return true
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
	if len(old) == 0 {
		return true
	}
	start := 0
	if len(neu) > 0 && neu[0] != old[0] {
		start = 1
	}
	i := start
	for _, paragraph := range neu {
		if i < len(old) && paragraph == old[i] {
			i++
		}
	}
	return i == len(old)
}

func sameText(original, next map[string]any) bool {
	return strings.Join(paragraphsOf(original), "\n") == strings.Join(paragraphsOf(next), "\n") &&
		closingOf(original) == closingOf(next) &&
		strings.Join(headingsOf(original), "\n") == strings.Join(headingsOf(next), "\n")
}

func headingsOf(draft map[string]any) []string {
	var out []string
	for _, section := range sectionsOf(draft) {
		out = append(out, textOf(section["heading"]))
	}
	return out
}

// RepairNearDuplicates drops a later paragraph that overlaps an earlier one by more than half.
func RepairNearDuplicates(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	var seen []string
	for _, section := range sectionsOf(draft) {
		paras, _ := section["paragraphs"].([]any)
		var kept []any
		for _, item := range paras {
			text := strings.TrimSpace(textOf(item))
			if text == "" {
				continue
			}
			if nearDuplicate(text, seen) {
				continue
			}
			seen = append(seen, text)
			kept = append(kept, text)
		}
		if kept == nil {
			kept = []any{}
		}
		section["paragraphs"] = kept
	}
	return draft
}

func nearDuplicate(text string, seen []string) bool {
	for _, prev := range seen {
		if Overlap(text, prev) > 0.5 {
			return true
		}
	}
	return false
}

// DedupeBlocks leaves each section with a list or a table, and drops items that restate a paragraph.
func DedupeBlocks(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	for _, section := range sectionsOf(draft) {
		paras := paragraphsOf(map[string]any{"sections": []any{section}})
		items := filterRestated(stringList(section["list"]), paras)
		table, _ := section["table"].(map[string]any)
		if table != nil {
			rows := filterTableRows(table, paras)
			if len(rows) == 0 {
				delete(section, "table")
				table = nil
			} else {
				table["rows"] = rows
			}
		}
		if len(items) > 0 && table != nil {
			items = nil
		}
		if items == nil {
			section["list"] = []any{}
		} else {
			listed := make([]any, len(items))
			for i, item := range items {
				listed[i] = item
			}
			section["list"] = listed
		}
	}
	return draft
}

func filterRestated(items, paras []string) []string {
	var out []string
	for _, item := range items {
		if restates(item, paras) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func filterTableRows(table map[string]any, paras []string) []any {
	var rows []any
	for _, raw := range anyList(table["rows"]) {
		cells, _ := raw.([]any)
		text := strings.TrimSpace(strings.Join(stringList(cells), ""))
		if text == "" || restates(text, paras) {
			continue
		}
		rows = append(rows, raw)
	}
	return rows
}

func restates(text string, paras []string) bool {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) < 6 {
		return false
	}
	for _, para := range paras {
		if Overlap(text, para) > 0.5 {
			return true
		}
	}
	return false
}

// DropGlossary removes a canned 弱电 definition from an article that is not about that trade.
func DropGlossary(draft map[string]any, verticalKey, direction string) map[string]any {
	if draft == nil || glossaryBelongs(verticalKey, direction) {
		return draft
	}
	for _, section := range sectionsOf(draft) {
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			if glossaryLine(textOf(item)) {
				continue
			}
			paras = append(paras, item)
		}
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
		var items []any
		for _, item := range anyList(section["list"]) {
			if glossaryLine(textOf(item)) {
				continue
			}
			items = append(items, item)
		}
		section["list"] = items
	}
	return draft
}

func glossaryBelongs(verticalKey, direction string) bool {
	if verticalKey == "elv" {
		return true
	}
	text := direction
	return strings.Contains(text, "弱电") || strings.Contains(text, "工程") || strings.Contains(text, "智能化")
}

func glossaryLine(text string) bool {
	if !strings.Contains(text, "弱电") {
		return false
	}
	return containsAny(text, "1000V", "1000伏", "安全特低电压", "电子信息系统", "低压电力")
}

// Overlap is the longest common subsequence divided by the shorter text, after punctuation is ignored.
func Overlap(a, b string) float64 {
	ar := []rune(compact(a))
	br := []rune(compact(b))
	if len(ar) == 0 || len(br) == 0 {
		return 0
	}
	n := lcsLen(ar, br)
	shorter := len(ar)
	if len(br) < shorter {
		shorter = len(br)
	}
	return float64(n) / float64(shorter)
}

func compact(text string) string {
	var b strings.Builder
	for _, r := range text {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || r == '…' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func lcsLen(a, b []rune) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for _, ra := range a {
		for j, rb := range b {
			if ra == rb {
				cur[j+1] = prev[j] + 1
			} else if prev[j+1] >= cur[j] {
				cur[j+1] = prev[j+1]
			} else {
				cur[j+1] = cur[j]
			}
		}
		prev, cur = cur, prev
		for i := range cur {
			cur[i] = 0
		}
	}
	return prev[len(b)]
}

func paragraphsOf(draft map[string]any) []string {
	var out []string
	for _, section := range sectionsOf(draft) {
		for _, item := range anyList(section["paragraphs"]) {
			if text := textOf(item); text != "" {
				out = append(out, text)
			}
		}
	}
	return out
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
	out := make([]map[string]any, 0)
	for _, item := range anyList(draft["sections"]) {
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
	var out []string
	for _, item := range anyList(raw) {
		if text := strings.TrimSpace(textOf(item)); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func anyList(raw any) []any {
	switch list := raw.(type) {
	case []any:
		return list
	case []string:
		out := make([]any, len(list))
		for i, item := range list {
			out[i] = item
		}
		return out
	default:
		return nil
	}
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

func boolOf(v any) bool {
	on, _ := v.(bool)
	return on
}

func intOf(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0
		}
		return int(i)
	default:
		return 0
	}
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
