package editorial

import (
	"encoding/json"
	"regexp"
	"strconv"
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
	if original == nil {
		return original
	}
	return ApplyNeed(original, model, NeedsRewrite(original), callErr)
}

// ApplyNeed is Apply with the rewrite targets already chosen.
// The hook can be requested even when the review rubric scored it as good enough.
func ApplyNeed(original map[string]any, model map[string]any, need Needs, callErr error) map[string]any {
	if callErr != nil || original == nil || model == nil {
		return original
	}
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

// BetterHook keeps a rewrite when the compare call fails: it must be a short opening, not a narrative sentence.
func BetterHook(rewrite, opening string) bool {
	if !HookAccepted(rewrite, opening) {
		return false
	}
	rewrite = strings.TrimSpace(rewrite)
	if strings.HasSuffix(rewrite, "。") || strings.HasSuffix(rewrite, "！") {
		return false
	}
	if strings.Contains(rewrite, "，") && utf8.RuneCountInString(rewrite) > 22 {
		return false
	}
	return true
}

// SetFirstParagraph replaces the opening paragraph and leaves the rest of the draft in place.
func SetFirstParagraph(draft map[string]any, text string) {
	if draft == nil {
		return
	}
	text = strings.TrimSpace(text)
	sections := sectionsOf(draft)
	if len(sections) == 0 || text == "" {
		return
	}
	paras := anyList(sections[0]["paragraphs"])
	if len(paras) == 0 {
		sections[0]["paragraphs"] = []any{text}
		return
	}
	paras[0] = text
	sections[0]["paragraphs"] = paras
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
		if Overlap(text, prev) > 0.5 || SharesNGram(text, prev, 12) || ShingleSimilarity(text, prev, 3) >= 0.45 {
			return true
		}
	}
	return false
}

// ShingleSimilarity is the share of character n-grams in the shorter passage that also appear in the longer one.
// It catches a rephrased paragraph that does not repeat a long exact run.
func ShingleSimilarity(a, b string, n int) float64 {
	if n < 1 {
		return 0
	}
	left := shingles(a, n)
	right := shingles(b, n)
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	shared := 0
	for gram := range left {
		if _, ok := right[gram]; ok {
			shared++
		}
	}
	denom := len(left)
	if len(right) < denom {
		denom = len(right)
	}
	if denom == 0 {
		return 0
	}
	return float64(shared) / float64(denom)
}

func shingles(text string, n int) map[string]struct{} {
	runes := []rune(compact(text))
	out := map[string]struct{}{}
	if len(runes) < n {
		return out
	}
	for i := 0; i+n <= len(runes); i++ {
		out[string(runes[i:i+n])] = struct{}{}
	}
	return out
}

// SharesNGram reports whether two passages share any run of n characters after punctuation is ignored.
func SharesNGram(a, b string, n int) bool {
	if n < 1 {
		return false
	}
	ar := []rune(compact(a))
	br := []rune(compact(b))
	if len(ar) < n || len(br) < n {
		return false
	}
	seen := map[string]struct{}{}
	for i := 0; i+n <= len(ar); i++ {
		seen[string(ar[i:i+n])] = struct{}{}
	}
	for i := 0; i+n <= len(br); i++ {
		if _, ok := seen[string(br[i:i+n])]; ok {
			return true
		}
	}
	return false
}

// StripAudience removes a reader-profile label from the title, summary, headings, body, closing, and alternate titles.
// A paragraph, heading, summary, or closing that is only the label is dropped. Alternate titles that contain it are dropped.
func StripAudience(draft map[string]any, label string) map[string]any {
	label = strings.TrimSpace(label)
	if draft == nil || utf8.RuneCountInString(label) < 2 {
		return draft
	}
	if text, changed := stripLabel(textOf(draft["title"]), label, false); changed {
		draft["title"] = text
	}
	if text, changed := stripLabel(textOf(draft["summary"]), label, true); changed {
		draft["summary"] = text
	}
	if text, changed := stripLabel(textOf(draft["closing"]), label, true); changed {
		draft["closing"] = text
	}
	for _, section := range sectionsOf(draft) {
		if text, changed := stripLabel(textOf(section["heading"]), label, true); changed {
			section["heading"] = text
		}
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			text, changed := stripLabel(textOf(item), label, true)
			if changed && strings.TrimSpace(text) == "" {
				continue
			}
			if changed {
				paras = append(paras, text)
				continue
			}
			paras = append(paras, item)
		}
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
	}
	var alts []any
	for _, item := range anyList(draft["alt_titles"]) {
		if strings.Contains(altText(item), label) {
			continue
		}
		alts = append(alts, item)
	}
	if alts == nil {
		alts = []any{}
	}
	draft["alt_titles"] = alts
	return draft
}

func altText(item any) string {
	switch row := item.(type) {
	case string:
		return row
	case map[string]any:
		if text := textOf(row["title"]); text != "" {
			return text
		}
		return textOf(row["text"])
	default:
		return ""
	}
}

func stripLabel(text, label string, dropShort bool) (string, bool) {
	if label == "" || !strings.Contains(text, label) {
		return text, false
	}
	next := tidyEdge(strings.ReplaceAll(text, label, ""))
	if dropShort && utf8.RuneCountInString(next) < 8 {
		return "", true
	}
	return next, true
}

func tidyEdge(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimLeftFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})
	return strings.TrimSpace(text)
}

// DropSummaryEcho removes a first paragraph that repeats the summary. The summary itself stays for the WeChat digest.
func DropSummaryEcho(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	summary := strings.TrimSpace(textOf(draft["summary"]))
	if utf8.RuneCountInString(compact(summary)) < 8 {
		return draft
	}
	sections := sectionsOf(draft)
	if len(sections) == 0 {
		return draft
	}
	paras := anyList(sections[0]["paragraphs"])
	if len(paras) == 0 {
		return draft
	}
	first := strings.TrimSpace(textOf(paras[0]))
	if first == "" {
		return draft
	}
	if compact(first) == compact(summary) || Overlap(first, summary) >= 0.9 {
		rest := paras[1:]
		if rest == nil {
			rest = []any{}
		}
		sections[0]["paragraphs"] = rest
	}
	return draft
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

// RewriteTaboos drops sentences that contain a persona taboo and leaves the rest of the draft.
// An empty paragraph is removed. Bare 验收 and 中标 are not taboos, and no placeholder is inserted.
func RewriteTaboos(draft map[string]any, taboos string) map[string]any {
	words := tabooWords(taboos)
	if draft == nil || len(words) == 0 {
		return draft
	}
	if text := rewriteTabooText(textOf(draft["title"]), words); text != textOf(draft["title"]) {
		draft["title"] = text
	}
	if text := rewriteTabooText(textOf(draft["summary"]), words); text != textOf(draft["summary"]) {
		draft["summary"] = text
	}
	if text := rewriteTabooText(textOf(draft["closing"]), words); text != textOf(draft["closing"]) {
		draft["closing"] = text
	}
	for _, section := range sectionsOf(draft) {
		if text := rewriteTabooText(textOf(section["heading"]), words); text != textOf(section["heading"]) {
			section["heading"] = text
		}
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			text := rewriteTabooText(textOf(item), words)
			if strings.TrimSpace(text) == "" {
				continue
			}
			paras = append(paras, text)
		}
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
	}
	var alts []any
	for _, item := range anyList(draft["alt_titles"]) {
		text := altText(item)
		if containsAny(text, words...) {
			continue
		}
		alts = append(alts, item)
	}
	if alts == nil {
		alts = []any{}
	}
	draft["alt_titles"] = alts
	return draft
}

var (
	orgName  = regexp.MustCompile(`[\p{Han}]{2,8}(?:委员会|协会|学会|研究院)`)
	orgBu    = regexp.MustCompile(`[\p{Han}]{2,6}部`)
	stdNum   = regexp.MustCompile(`GB\s*/?\s*T?\s*\d+(?:[.\-]\d+)*`)
	bookName = regexp.MustCompile(`《[^》]{2,40}》`)
	orgStop  = map[string]bool{"俱乐部": true}
)

// StripUnsourcedNames drops sentences that name an institution, standard, or document missing from sources.
func StripUnsourcedNames(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	blob := sourceNameBlob(draft)
	rewrite := func(text string) string {
		names := unsourcedNames(text, blob)
		if len(names) == 0 {
			return text
		}
		return rewriteTabooText(text, names)
	}
	if text := rewrite(textOf(draft["title"])); text != textOf(draft["title"]) {
		draft["title"] = text
	}
	if text := rewrite(textOf(draft["summary"])); text != textOf(draft["summary"]) {
		draft["summary"] = text
	}
	if text := rewrite(textOf(draft["closing"])); text != textOf(draft["closing"]) {
		draft["closing"] = text
	}
	for _, section := range sectionsOf(draft) {
		if text := rewrite(textOf(section["heading"])); text != textOf(section["heading"]) {
			section["heading"] = text
		}
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			text := rewrite(textOf(item))
			if strings.TrimSpace(text) == "" {
				continue
			}
			paras = append(paras, text)
		}
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
	}
	return draft
}

func sourceNameBlob(draft map[string]any) string {
	var b strings.Builder
	for _, raw := range anyList(draft["sources"]) {
		source, _ := raw.(map[string]any)
		if source == nil {
			continue
		}
		for _, key := range []string{"title", "name", "snippet", "note", "url"} {
			b.WriteString(textOf(source[key]))
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func unsourcedNames(text, blob string) []string {
	compactBlob := strings.ReplaceAll(blob, " ", "")
	var out []string
	seen := map[string]bool{}
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, match := range orgName.FindAllString(text, -1) {
		if !strings.Contains(blob, match) {
			add(match)
		}
	}
	for _, match := range orgBu.FindAllString(text, -1) {
		if orgStop[match] || strings.Contains(blob, match) {
			continue
		}
		add(match)
	}
	for _, match := range stdNum.FindAllString(text, -1) {
		compact := strings.ReplaceAll(match, " ", "")
		if strings.Contains(blob, match) || strings.Contains(compactBlob, compact) {
			continue
		}
		add(match)
	}
	for _, match := range bookName.FindAllString(text, -1) {
		inner := strings.Trim(match, "《》")
		if strings.Contains(blob, match) || strings.Contains(blob, inner) {
			continue
		}
		add(match)
	}
	return out
}

func tabooWords(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == '，' || r == '\n' || r == '、' || r == ';' || r == '；'
	}) {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] || bareTopicWord(part) {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

// bareTopicWord is a subject word, not a promise. 验收 and 中标 stay in the article.
func bareTopicWord(text string) bool {
	switch text {
	case "中标", "验收":
		return true
	default:
		return false
	}
}

// HasTaboo reports a promise phrase from the persona list. Bare 验收 and 中标 do not count.
func HasTaboo(draft map[string]any, taboos string) bool {
	words := tabooWords(taboos)
	if draft == nil || len(words) == 0 {
		return false
	}
	raw, err := json.Marshal(draft)
	if err != nil {
		return false
	}
	return containsAny(string(raw), words...)
}

func rewriteTabooText(text string, words []string) string {
	text = strings.TrimSpace(text)
	if text == "" || !containsAny(text, words...) {
		return text
	}
	var kept []string
	for _, sentence := range splitSentences(text) {
		if containsAny(sentence, words...) {
			continue
		}
		kept = append(kept, sentence)
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "")
}

// LimitParagraphs splits a paragraph that runs past max sentences into chunks of max.
// A combined question-and-answer paragraph stays intact. A numbered step keeps its number on each chunk.
func LimitParagraphs(draft map[string]any, max int) map[string]any {
	if draft == nil || max < 1 {
		return draft
	}
	for _, section := range sectionsOf(draft) {
		var next []any
		for _, item := range anyList(section["paragraphs"]) {
			text := strings.TrimSpace(textOf(item))
			if text == "" {
				continue
			}
			for _, part := range chunkSentences(text, max) {
				next = append(next, part)
			}
		}
		if next == nil {
			next = []any{}
		}
		section["paragraphs"] = next
	}
	return draft
}

func chunkSentences(text string, max int) []string {
	if strings.Contains(text, "答") && (strings.Contains(text, "？") || strings.Contains(text, "?")) {
		return []string{text}
	}
	parts := splitSentences(text)
	if len(parts) <= max {
		return []string{text}
	}
	lead := numberedLead(text)
	var out []string
	for i := 0; i < len(parts); i += max {
		j := i + max
		if j > len(parts) {
			j = len(parts)
		}
		chunk := strings.Join(parts[i:j], "")
		if i > 0 && lead != "" && !strings.HasPrefix(strings.TrimSpace(chunk), strings.TrimSpace(lead)) {
			chunk = lead + chunk
		}
		out = append(out, chunk)
	}
	return out
}

func numberedLead(text string) string {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"第十步", "第九步", "第八步", "第七步", "第六步", "第五步", "第四步", "第三步", "第二步", "第一步"} {
		if strings.HasPrefix(text, prefix) {
			return prefix
		}
	}
	i := 0
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(text) {
		return ""
	}
	if text[i] == '.' {
		return text[:i+1] + " "
	}
	if strings.HasPrefix(text[i:], "、") {
		return text[:i+len("、")]
	}
	return ""
}

func splitSentences(text string) []string {
	var out []string
	var buf strings.Builder
	for _, r := range text {
		buf.WriteRune(r)
		switch r {
		case '。', '！', '？', '!', '?':
			if part := strings.TrimSpace(buf.String()); part != "" {
				out = append(out, part)
			}
			buf.Reset()
		}
	}
	if part := strings.TrimSpace(buf.String()); part != "" {
		out = append(out, part)
	}
	return out
}

// StripForeignScenes removes a sentence that repeats another account's title.
// Titles shorter than 8 runes are ignored so a shared word does not wipe a paragraph.
func StripForeignScenes(draft map[string]any, titles []string) map[string]any {
	var banned []string
	for _, title := range titles {
		title = strings.TrimSpace(title)
		if utf8.RuneCountInString(compact(title)) < 8 {
			continue
		}
		banned = append(banned, title)
	}
	if draft == nil || len(banned) == 0 {
		return draft
	}
	if text, changed := dropForeignSentences(textOf(draft["title"]), banned); changed {
		draft["title"] = text
	}
	if text, changed := dropForeignSentences(textOf(draft["summary"]), banned); changed {
		draft["summary"] = text
	}
	if text, changed := dropForeignSentences(textOf(draft["closing"]), banned); changed {
		draft["closing"] = text
	}
	for _, section := range sectionsOf(draft) {
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			text, changed := dropForeignSentences(textOf(item), banned)
			if changed && strings.TrimSpace(text) == "" {
				continue
			}
			if changed {
				paras = append(paras, text)
				continue
			}
			paras = append(paras, item)
		}
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
	}
	return draft
}

func dropForeignSentences(text string, titles []string) (string, bool) {
	if strings.TrimSpace(text) == "" {
		return text, false
	}
	changed := false
	var kept []string
	for _, sentence := range splitSentences(text) {
		foreign := false
		for _, title := range titles {
			if SharesNGram(sentence, title, 8) {
				foreign = true
				break
			}
		}
		if foreign {
			changed = true
			continue
		}
		kept = append(kept, sentence)
	}
	if !changed {
		return text, false
	}
	return strings.Join(kept, ""), true
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

var citeMark = regexp.MustCompile(`\[(\d+)\]`)

// DedupeTables drops a later table that repeats an earlier one, including a close rephrase.
func DedupeTables(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	var seen []string
	for _, section := range sectionsOf(draft) {
		table, _ := section["table"].(map[string]any)
		if table == nil {
			continue
		}
		key := tableKey(table)
		if key == "" {
			continue
		}
		dup := false
		for _, prev := range seen {
			if key == prev || ShingleSimilarity(key, prev, 3) >= 0.6 {
				dup = true
				break
			}
		}
		if dup {
			delete(section, "table")
			continue
		}
		seen = append(seen, key)
	}
	return draft
}

func tableKey(table map[string]any) string {
	var b strings.Builder
	for _, item := range stringList(table["headers"]) {
		b.WriteString(compact(item))
		b.WriteString("|")
	}
	for _, raw := range anyList(table["rows"]) {
		cells, _ := raw.([]any)
		for _, cell := range stringList(cells) {
			b.WriteString(compact(cell))
			b.WriteString("|")
		}
		b.WriteString(";")
	}
	return b.String()
}

// StripMeta removes sentences that talk about the draft itself.
func StripMeta(draft map[string]any) map[string]any {
	words := []string{"原稿", "改写", "上一版"}
	if draft == nil {
		return nil
	}
	draft["title"] = rewriteTabooText(textOf(draft["title"]), words)
	draft["summary"] = rewriteTabooText(textOf(draft["summary"]), words)
	draft["closing"] = rewriteTabooText(textOf(draft["closing"]), words)
	for _, section := range sectionsOf(draft) {
		section["heading"] = rewriteTabooText(textOf(section["heading"]), words)
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			text := rewriteTabooText(textOf(item), words)
			if strings.TrimSpace(text) == "" {
				continue
			}
			paras = append(paras, text)
		}
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
	}
	return draft
}

// SupportCitations removes a citation whose sentence is not backed by that source's title or snippet.
func SupportCitations(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	sources := anyList(draft["sources"])
	for _, section := range sectionsOf(draft) {
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			paras = append(paras, supportCitationText(textOf(item), sources))
		}
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
	}
	if text := strings.TrimSpace(textOf(draft["closing"])); text != "" {
		draft["closing"] = supportCitationText(text, sources)
	}
	return draft
}

func supportCitationText(text string, sources []any) string {
	if !strings.Contains(text, "[") {
		return text
	}
	parts := splitSentences(text)
	var merged []string
	for _, part := range parts {
		bare := strings.TrimSpace(citeMark.ReplaceAllString(part, ""))
		if bare == "" && len(merged) > 0 {
			merged[len(merged)-1] += part
			continue
		}
		merged = append(merged, part)
	}
	var kept []string
	for _, part := range merged {
		kept = append(kept, supportSentence(part, sources))
	}
	return strings.Join(kept, "")
}

func supportSentence(sentence string, sources []any) string {
	marks := citeMark.FindAllStringSubmatch(sentence, -1)
	if len(marks) == 0 {
		return sentence
	}
	claim := citeMark.ReplaceAllString(sentence, "")
	var keep []string
	seen := map[string]bool{}
	for _, mark := range marks {
		if seen[mark[0]] {
			continue
		}
		seen[mark[0]] = true
		n, _ := strconv.Atoi(mark[1])
		if citationSupported(claim, n, sources) {
			keep = append(keep, mark[0])
		}
	}
	return strings.TrimSpace(claim) + strings.Join(keep, "")
}

// CiteSupported adds [n] only where the sentence is actually backed by that source.
func CiteSupported(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	sources := anyList(draft["sources"])
	if len(sources) == 0 {
		return draft
	}
	for _, section := range sectionsOf(draft) {
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			paras = append(paras, citeParagraph(textOf(item), sources))
		}
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
	}
	if text := strings.TrimSpace(textOf(draft["closing"])); text != "" {
		draft["closing"] = citeParagraph(text, sources)
	}
	return draft
}

func citeParagraph(text string, sources []any) string {
	if strings.TrimSpace(text) == "" || !hasHan(text) {
		return text
	}
	parts := splitSentences(text)
	var out []string
	for _, part := range parts {
		out = append(out, citeSentence(part, sources))
	}
	return strings.Join(out, "")
}

func citeSentence(sentence string, sources []any) string {
	if citeMark.MatchString(sentence) {
		return sentence
	}
	claim := strings.TrimSpace(sentence)
	if utf8.RuneCountInString(compact(claim)) < 8 {
		return sentence
	}
	best := 0
	for i := range sources {
		if citationSupported(claim, i+1, sources) {
			best = i + 1
			break
		}
	}
	if best == 0 {
		return sentence
	}
	return strings.TrimSpace(sentence) + "[" + strconv.Itoa(best) + "]"
}

func hasHan(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func citationSupported(claim string, n int, sources []any) bool {
	if n < 1 || n > len(sources) {
		return false
	}
	source, _ := sources[n-1].(map[string]any)
	if source == nil {
		return false
	}
	backing := citationSourceText(source)
	claim = strings.TrimSpace(claim)
	if backing == "" || claim == "" {
		return false
	}
	if utf8.RuneCountInString(compact(claim)) < 8 {
		return false
	}
	if sharedHanPhrase(claim, backing, 4) {
		return true
	}
	return distinctiveShared(claim, backing) >= 2
}

func sharedHanPhrase(a, b string, n int) bool {
	ar := hanRunes(a)
	br := hanRunes(b)
	if len(ar) < n || len(br) < n {
		return false
	}
	seen := map[string]bool{}
	for i := 0; i+n <= len(ar); i++ {
		seen[string(ar[i:i+n])] = true
	}
	for i := 0; i+n <= len(br); i++ {
		if seen[string(br[i:i+n])] {
			return true
		}
	}
	return false
}

func hanRunes(text string) []rune {
	var out []rune
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			out = append(out, r)
		}
	}
	return out
}

func distinctiveShared(a, b string) int {
	seen := map[string]bool{}
	for _, term := range distinctiveTerms(a) {
		seen[term] = true
	}
	hits := 0
	used := map[string]bool{}
	for _, term := range distinctiveTerms(b) {
		if seen[term] && !used[term] {
			used[term] = true
			hits++
		}
	}
	return hits
}

func distinctiveTerms(text string) []string {
	var out []string
	seen := map[string]bool{}
	var buf []rune
	flush := func() {
		if len(buf) >= 2 && len(buf) <= 6 {
			term := string(buf)
			if !seen[term] && !genericCiteTerm(term) {
				seen[term] = true
				out = append(out, term)
			}
		}
		buf = nil
	}
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			buf = append(buf, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func genericCiteTerm(text string) bool {
	switch text {
	case "标准", "工程", "设计", "公告", "发布", "规范", "系统", "建设", "国家", "要求", "文件", "技术", "管理", "工作", "相关", "进行", "通过", "可以", "需要", "应当", "必须", "关于", "住房", "城乡", "市场", "监管", "公司", "有限", "单位", "项目", "内容", "情况", "问题", "方法", "以及", "一个", "我们", "这个", "没有", "不是", "公开", "说明", "正文", "合适":
		return true
	default:
		return false
	}
}

func citationSourceText(source map[string]any) string {
	title := strings.TrimSpace(textOf(source["title"]))
	snippet := strings.TrimSpace(textOf(source["snippet"]))
	if snippet == "" {
		snippet = strings.TrimSpace(textOf(source["note"]))
	}
	return strings.TrimSpace(title + " " + snippet + " " + factText(source["facts"]))
}

func factText(raw any) string {
	switch items := raw.(type) {
	case []string:
		return strings.Join(items, " ")
	case []any:
		var parts []string
		for _, item := range items {
			if text := strings.TrimSpace(textOf(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	default:
		return strings.TrimSpace(textOf(raw))
	}
}

// ShapeCopy repairs formatting the model drops: a run-on with no sentence breaks,
// a markdown or label table left as one paragraph, an empty subheading, a reader
// addressed as if they had sent something in, and a buzzword repeated past twice.
// It also makes sure the closing asks the reader to follow, comment, or wait for the next issue.
func ShapeCopy(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	for _, section := range sectionsOf(draft) {
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			text := dropReaderAddress(textOf(item))
			if strings.TrimSpace(text) == "" {
				continue
			}
			if table := tableFromParagraph(text); table != nil && section["table"] == nil {
				section["table"] = table
				continue
			}
			paras = append(paras, repairRunOn(text))
		}
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
		if heading := dropReaderAddress(textOf(section["heading"])); heading != textOf(section["heading"]) {
			section["heading"] = heading
		}
	}
	dropEmptySections(draft)
	capBuzzword(draft, "温暖", 2)
	draft["closing"] = repairRunOn(dropReaderAddress(textOf(draft["closing"])))
	ensureCTA(draft)
	return draft
}

func dropEmptySections(draft map[string]any) {
	sections := anyList(draft["sections"])
	var kept []any
	for _, raw := range sections {
		section, _ := raw.(map[string]any)
		if section == nil {
			continue
		}
		heading := strings.TrimSpace(textOf(section["heading"]))
		paras := anyList(section["paragraphs"])
		items := anyList(section["list"])
		_, hasTable := section["table"].(map[string]any)
		body := false
		for _, item := range paras {
			if strings.TrimSpace(textOf(item)) != "" {
				body = true
				break
			}
		}
		if !body {
			for _, item := range items {
				if strings.TrimSpace(textOf(item)) != "" {
					body = true
					break
				}
			}
		}
		if heading != "" && !body && !hasTable {
			continue
		}
		kept = append(kept, section)
	}
	if len(kept) == 0 {
		return
	}
	draft["sections"] = kept
}

func capBuzzword(draft map[string]any, word string, keep int) {
	if keep < 1 || word == "" {
		return
	}
	seen := 0
	rewrite := func(text string) string {
		var b strings.Builder
		rest := text
		for {
			i := strings.Index(rest, word)
			if i < 0 {
				b.WriteString(rest)
				return b.String()
			}
			b.WriteString(rest[:i])
			if word == "温暖" && strings.HasPrefix(rest[i:], "温暖亲和") {
				b.WriteString("温暖亲和")
				rest = rest[i+len("温暖亲和"):]
				continue
			}
			seen++
			if seen <= keep {
				b.WriteString(word)
			} else {
				b.WriteString("踏实")
			}
			rest = rest[i+len(word):]
		}
	}
	for _, section := range sectionsOf(draft) {
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			paras = append(paras, rewrite(textOf(item)))
		}
		section["paragraphs"] = paras
	}
	if closing := textOf(draft["closing"]); closing != "" {
		draft["closing"] = rewrite(closing)
	}
}

func dropReaderAddress(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	var kept []string
	changed := false
	for _, sentence := range splitSentences(text) {
		if containsAny(sentence, "您分享的", "您发来的", "您提供的", "你分享的", "您刚发来") {
			changed = true
			continue
		}
		kept = append(kept, sentence)
	}
	if !changed {
		return text
	}
	return strings.Join(kept, "")
}

func ensureCTA(draft map[string]any) {
	closing := strings.TrimSpace(textOf(draft["closing"]))
	if ctaConcrete(closing) {
		return
	}
	column := strings.TrimSpace(textOf(draft["column_label"]))
	line := "关注本号，把你遇到的情况写在评论里。"
	if column != "" {
		line = "关注本号，在评论里留下你的做法。下一期「" + column + "」继续写。"
	}
	if closing == "" {
		draft["closing"] = line
		return
	}
	draft["closing"] = closing + "\n\n" + line
}

func ctaConcrete(text string) bool {
	if strings.Contains(text, "关注") {
		return true
	}
	if strings.Contains(text, "下期") || strings.Contains(text, "下一期") {
		return true
	}
	return strings.Contains(text, "评论")
}

func repairRunOn(text string) string {
	text = strings.TrimSpace(text)
	n := utf8.RuneCountInString(text)
	marks := 0
	for _, r := range text {
		switch r {
		case '。', '！', '？', '!', '?':
			marks++
		}
	}
	if text == "" || n < 36 || (marks > 0 && n/marks < 80) {
		return text
	}
	var b strings.Builder
	clause := 0
	inMark := false
	for _, r := range text {
		if r == '[' {
			inMark = true
		}
		if inMark {
			b.WriteRune(r)
			if r == ']' {
				inMark = false
			}
			continue
		}
		if r == '，' || r == ',' {
			if clause >= 18 {
				b.WriteRune('。')
				clause = 0
				continue
			}
			b.WriteRune(r)
			clause = 0
			continue
		}
		switch r {
		case '。', '！', '？', '!', '?', '；', ';':
			b.WriteRune(r)
			clause = 0
			continue
		}
		b.WriteRune(r)
		clause++
		if clause >= 40 {
			b.WriteRune('。')
			clause = 0
		}
	}
	return b.String()
}

func tableFromParagraph(text string) map[string]any {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "|") {
		if table := markdownTable(text); table != nil {
			return table
		}
	}
	parts := strings.Split(text, "；")
	if len(parts) < 2 {
		parts = strings.Split(text, ";")
	}
	if len(parts) < 2 {
		return nil
	}
	var rows []any
	for _, part := range parts {
		part = strings.TrimSpace(part)
		label, value, ok := strings.Cut(part, "：")
		if !ok {
			label, value, ok = strings.Cut(part, ":")
		}
		if !ok {
			return nil
		}
		label = strings.TrimSpace(label)
		value = strings.TrimSpace(value)
		if label == "" || value == "" || utf8.RuneCountInString(label) > 12 || utf8.RuneCountInString(value) > 40 {
			return nil
		}
		rows = append(rows, []any{label, value})
	}
	if len(rows) < 2 {
		return nil
	}
	return map[string]any{"headers": []any{"项目", "说明"}, "rows": rows}
}

func markdownTable(text string) map[string]any {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "|") {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) < 2 {
		return nil
	}
	cells := splitPipes(lines[0])
	if len(cells) < 2 {
		return nil
	}
	start := 1
	if looksLikeRule(lines[1]) {
		start = 2
	}
	var rows []any
	for _, line := range lines[start:] {
		if looksLikeRule(line) {
			continue
		}
		row := splitPipes(line)
		if len(row) == 0 {
			continue
		}
		item := make([]any, len(cells))
		for i := range cells {
			if i < len(row) {
				item[i] = row[i]
			} else {
				item[i] = ""
			}
		}
		rows = append(rows, item)
	}
	if len(rows) == 0 {
		return nil
	}
	headers := make([]any, len(cells))
	for i, cell := range cells {
		headers[i] = cell
	}
	return map[string]any{"headers": headers, "rows": rows}
}

func splitPipes(line string) []string {
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	var out []string
	for _, part := range parts {
		out = append(out, strings.TrimSpace(part))
	}
	return out
}

func looksLikeRule(line string) bool {
	line = strings.Trim(line, "| ")
	if line == "" {
		return false
	}
	for _, r := range line {
		switch r {
		case '-', ':', '|', ' ':
		default:
			return false
		}
	}
	return true
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
