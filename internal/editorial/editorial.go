package editorial

import (
	"encoding/json"
	"hash/fnv"
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

// DedupeBlocks drops a list item or table row that restates a paragraph.
// A section may keep both a list and a table when they say different things.
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
	StripHeadingPrefixes(draft)
	MixProse(draft)
	DedupeExamples(draft)
	ensureCTA(draft)
	return draft
}

// StripHeadingPrefixes removes column and template labels glued onto a heading,
// such as 拆解：, 简单：, and 简单：第一步：.
func StripHeadingPrefixes(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	prefixes := []string{"拆解", "简单", "简约", "第一步", "步骤", "方案拆解", "知识FAQ"}
	if label := strings.TrimSpace(textOf(draft["column_label"])); label != "" {
		prefixes = append(prefixes, label)
	}
	for _, section := range sectionsOf(draft) {
		section["heading"] = stripLabelPrefix(textOf(section["heading"]), prefixes)
	}
	return draft
}

func stripLabelPrefix(text string, prefixes []string) string {
	text = strings.TrimSpace(text)
	changed := true
	for changed {
		changed = false
		for _, prefix := range prefixes {
			if prefix == "" {
				continue
			}
			for _, sep := range []string{"：", ":"} {
				lead := prefix + sep
				if strings.HasPrefix(text, lead) {
					text = strings.TrimSpace(strings.TrimPrefix(text, lead))
					changed = true
				}
			}
		}
	}
	return text
}

// MixProse turns an all-list article into an intro, a list, and a takeaway.
func MixProse(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	sections := sectionsOf(draft)
	if len(sections) == 0 {
		return draft
	}
	listOnly := 0
	for _, section := range sections {
		if len(nonemptyText(anyList(section["paragraphs"]))) == 0 && len(nonemptyText(anyList(section["list"]))) > 0 {
			listOnly++
		}
	}
	if listOnly < 2 || listOnly < len(sections) {
		return draft
	}
	lift := func(section map[string]any, takeLast bool) {
		items := nonemptyText(anyList(section["list"]))
		if len(items) == 0 {
			return
		}
		picked := items[0]
		rest := items[1:]
		if takeLast && len(items) > 1 {
			picked = items[len(items)-1]
			rest = items[:len(items)-1]
		}
		paras := nonemptyText(anyList(section["paragraphs"]))
		paras = append(paras, stripListIndex(picked))
		section["paragraphs"] = toAny(paras)
		section["list"] = toAny(rest)
	}
	lift(sections[0], false)
	if len(sections) > 1 {
		lift(sections[len(sections)-1], true)
	}
	return draft
}

func stripListIndex(text string) string {
	text = strings.TrimSpace(text)
	text = factIndex.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

func nonemptyText(items []any) []string {
	var out []string
	for _, item := range items {
		if text := strings.TrimSpace(textOf(item)); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func toAny(items []string) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

// DedupeExamples drops a later sentence that repeats an example already used in an earlier section.
func DedupeExamples(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, section := range sectionsOf(draft) {
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			text := strings.TrimSpace(textOf(item))
			key := exampleKey(text)
			if key != "" && seen[key] {
				continue
			}
			if key != "" {
				seen[key] = true
			}
			if text != "" {
				paras = append(paras, text)
			}
		}
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
	}
	return draft
}

func exampleKey(text string) string {
	var buf []rune
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			buf = append(buf, r)
		}
	}
	if len(buf) < 8 {
		return ""
	}
	return string(buf[:8])
}

var hardStat = regexp.MustCompile(`\d+(?:\.\d+)?\s*[%％]|掉线率|[A-Za-z]{2,}\s*\d+\.\d+`)

// StripInventedStats removes percentages, rates, and version numbers that have no source
// and are not spoken as the writer's own range.
func StripInventedStats(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	for _, key := range []string{"title", "summary", "closing"} {
		if text := textOf(draft[key]); text != "" {
			draft[key] = dropHardStatSentences(text)
		}
	}
	for _, section := range sectionsOf(draft) {
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			if text := dropHardStatSentences(textOf(item)); strings.TrimSpace(text) != "" {
				paras = append(paras, text)
			}
		}
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
		var items []any
		for _, item := range anyList(section["list"]) {
			if text := dropHardStatSentences(textOf(item)); strings.TrimSpace(text) != "" {
				items = append(items, text)
			}
		}
		if items == nil {
			items = []any{}
		}
		section["list"] = items
	}
	return draft
}

func dropHardStatSentences(text string) string {
	parts := splitSentences(text)
	if len(parts) == 0 {
		return text
	}
	var kept []string
	changed := false
	for _, sentence := range parts {
		if hardStatSentence(sentence) && !experientialStat(sentence) {
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

func hardStatSentence(text string) bool {
	return hardStat.MatchString(text)
}

func experientialStat(text string) bool {
	for _, word := range []string{"我见过", "大多", "大概", "大约", "上下", "一般", "往往"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

// HardStat reports a percentage, rate, or version number that is not framed as experience.
func HardStat(draft map[string]any) bool {
	if draft == nil {
		return false
	}
	blob := textOf(draft["title"]) + textOf(draft["summary"]) + textOf(draft["closing"])
	for _, section := range sectionsOf(draft) {
		blob += textOf(section["heading"])
		for _, item := range anyList(section["paragraphs"]) {
			blob += textOf(item)
		}
		for _, item := range anyList(section["list"]) {
			blob += textOf(item)
		}
	}
	for _, sentence := range splitSentences(blob) {
		if hardStatSentence(sentence) && !experientialStat(sentence) {
			return true
		}
	}
	if hardStatSentence(blob) && !experientialStat(blob) {
		return true
	}
	return false
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
		hasTable := false
		if table, _ := section["table"].(map[string]any); table != nil && len(anyList(table["rows"])) > 0 {
			hasTable = true
		}
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
	if kept == nil {
		kept = []any{}
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
	closing := keepOneCTA(strings.TrimSpace(textOf(draft["closing"])))
	if ctaConcrete(closing) {
		draft["closing"] = closing
		return
	}
	column := strings.TrimSpace(textOf(draft["column_label"]))
	line := "关注本号，把你的做法写在评论里。"
	if column != "" {
		line = "关注本号，把你在「" + column + "」里遇到的情况写在评论里。"
	}
	if closing == "" {
		draft["closing"] = line
		return
	}
	draft["closing"] = closing + "\n\n" + line
}

func keepOneCTA(text string) string {
	parts := strings.Split(text, "\n")
	seen := false
	var kept []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if boilerCTA(part) {
			if seen {
				continue
			}
			seen = true
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, "\n\n")
}

func boilerCTA(text string) bool {
	return strings.Contains(text, "关注本号") || strings.Contains(text, "欢迎留言说说") || strings.Contains(text, "下一期")
}

func ctaConcrete(text string) bool {
	return strings.Contains(text, "关注") || strings.Contains(text, "评论") || strings.Contains(text, "留言") || strings.Contains(text, "下期") || strings.Contains(text, "下一期")
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

// RenumberCitations walks the body in order and rewrites the first-seen marks to [1], [2], ...
// Sources are reordered to match. A lone [2] becomes [1].
func RenumberCitations(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	var order []int
	seen := map[int]bool{}
	note := func(text string) {
		for _, mark := range citeMark.FindAllStringSubmatch(text, -1) {
			n, _ := strconv.Atoi(mark[1])
			if n <= 0 || seen[n] {
				continue
			}
			seen[n] = true
			order = append(order, n)
		}
	}
	for _, section := range sectionsOf(draft) {
		for _, item := range anyList(section["paragraphs"]) {
			note(textOf(item))
		}
		for _, item := range anyList(section["list"]) {
			note(textOf(item))
		}
	}
	note(textOf(draft["closing"]))
	note(textOf(draft["summary"]))
	if len(order) == 0 {
		return draft
	}
	mapping := map[int]int{}
	for i, n := range order {
		mapping[n] = i + 1
	}
	rewrite := func(text string) string {
		return citeMark.ReplaceAllStringFunc(text, func(mark string) string {
			sub := citeMark.FindStringSubmatch(mark)
			if len(sub) < 2 {
				return mark
			}
			n, _ := strconv.Atoi(sub[1])
			next, ok := mapping[n]
			if !ok {
				return ""
			}
			return "[" + strconv.Itoa(next) + "]"
		})
	}
	for _, section := range sectionsOf(draft) {
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			paras = append(paras, rewrite(textOf(item)))
		}
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
		if len(anyList(section["list"])) > 0 {
			var list []any
			for _, item := range anyList(section["list"]) {
				list = append(list, rewrite(textOf(item)))
			}
			section["list"] = list
		}
	}
	if text := textOf(draft["closing"]); text != "" {
		draft["closing"] = rewrite(text)
	}
	if text := textOf(draft["summary"]); text != "" {
		draft["summary"] = rewrite(text)
	}
	old := anyList(draft["sources"])
	next := make([]any, 0, len(order))
	for _, n := range order {
		if n-1 < 0 || n-1 >= len(old) || old[n-1] == nil {
			continue
		}
		next = append(next, old[n-1])
	}
	if next == nil {
		next = []any{}
	}
	draft["sources"] = next
	return draft
}

var healthWords = []string{"治疗", "疗效", "降血压", "血糖", "癌症", "偏方", "剂量", "诊断", "服用", "治愈", "处方", "药品", "炎症"}

// SoftenHealthClaims rewrites a health sentence that has no citation into a non-medical reminder.
func SoftenHealthClaims(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	n := 0
	rewrite := func(text string) string {
		parts := mergeCiteTails(splitSentences(text))
		if len(parts) == 0 {
			return text
		}
		changed := false
		for i, part := range parts {
			if citeMark.MatchString(part) || !healthClaim(part) {
				continue
			}
			if n%2 == 0 {
				parts[i] = "这一步只是生活习惯上的提醒，不能代替医疗建议。"
			} else {
				parts[i] = "具体做法因人而异，这里只谈生活上的习惯，不涉及诊断或用药。"
			}
			n++
			changed = true
		}
		if !changed {
			return text
		}
		return strings.Join(parts, "")
	}
	applyText(draft, rewrite)
	return draft
}

func mergeCiteTails(parts []string) []string {
	var merged []string
	for _, part := range parts {
		bare := strings.TrimSpace(citeMark.ReplaceAllString(part, ""))
		if bare == "" && len(merged) > 0 {
			merged[len(merged)-1] += part
			continue
		}
		merged = append(merged, part)
	}
	return merged
}

func healthClaim(text string) bool {
	for _, word := range healthWords {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

var latinWord = regexp.MustCompile(`[A-Za-z]{4,}`)
var latinLetter = regexp.MustCompile(`[A-Za-z]`)

// StripEnglishLeaks drops a sentence that is only English, and removes long Latin words from the rest.
func StripEnglishLeaks(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	clean := func(text string) string {
		parts := splitSentences(text)
		if len(parts) == 0 {
			return stripLatinWords(text)
		}
		var kept []string
		for _, part := range parts {
			if englishLeak(part) {
				continue
			}
			kept = append(kept, stripLatinWords(part))
		}
		return strings.Join(kept, "")
	}
	applyText(draft, clean)
	return draft
}

func englishLeak(text string) bool {
	if hasHan(text) {
		return false
	}
	return len(latinLetter.FindAllString(text, -1)) >= 3
}

func stripLatinWords(text string) string {
	text = latinWord.ReplaceAllStringFunc(text, func(word string) string {
		switch strings.ToUpper(word) {
		case "GB", "PDF", "ISO", "FAQ", "AI", "URL", "HTTP", "HTTPS", "WIFI", "APP":
			return word
		default:
			return ""
		}
	})
	return strings.Join(strings.Fields(text), " ")
}

var datedToken = regexp.MustCompile(`\d{4}年|\d{1,2}月\d{1,2}日`)

// A topic stuffed into the old length-padding formula, for example
// 「先打开辟谣不吓人的检修口」.
var jammedTemplate = regexp.MustCompile(`先打开[^。！？!?\n]{1,24}的检修口|晚饭后先把[^。！？!?\n]{1,24}旁边的椅子挪开|用螺丝把盖板固定住`)

// repeatMin is the shortest block or line that a second copy will hold.
// Shorter sentences, including the length pad 「合适的正文。」, are ignored.
const repeatMin = 15

// RepeatedBody is the hard hold for a draft that would otherwise reach the
// WeChat draft box. Two copies of a paragraph, list, table, or line of at
// least 15 characters are held. So is a body whose 8-character phrases mostly
// recur, or a topic jammed into a canned template.
func RepeatedBody(draft map[string]any) bool {
	if draft == nil {
		return false
	}
	var lines []string
	var blocks []string
	for _, section := range sectionsOf(draft) {
		for _, item := range anyList(section["paragraphs"]) {
			if text := strings.TrimSpace(textOf(item)); text != "" {
				lines = append(lines, text)
				blocks = append(blocks, text)
			}
		}
		var items []string
		for _, item := range anyList(section["list"]) {
			if text := strings.TrimSpace(textOf(item)); text != "" {
				lines = append(lines, text)
				items = append(items, text)
			}
		}
		if len(items) > 0 {
			blocks = append(blocks, strings.Join(items, "\n"))
		}
		if table, _ := section["table"].(map[string]any); table != nil {
			if blob := tableBlockText(table); blob != "" {
				blocks = append(blocks, blob)
			}
			for _, item := range anyList(table["headers"]) {
				if text := strings.TrimSpace(textOf(item)); text != "" {
					lines = append(lines, text)
				}
			}
			for _, row := range anyList(table["rows"]) {
				for _, cell := range anyList(row) {
					if text := strings.TrimSpace(textOf(cell)); text != "" {
						lines = append(lines, text)
					}
				}
			}
		}
	}
	if text := strings.TrimSpace(textOf(draft["closing"])); text != "" {
		lines = append(lines, text)
		blocks = append(blocks, text)
	}
	if jammedTemplate.MatchString(strings.Join(lines, "\n")) {
		return true
	}
	if repeatedBlocks(blocks) || repeatedLines(lines) {
		return true
	}
	return false
}

func repeatedBlocks(parts []string) bool {
	count := map[uint64]int{}
	for _, part := range parts {
		// A block made only of short sentences is a length pad, not a repeated line.
		if !substantialLine(part) || utf8.RuneCountInString(normalizeBlock(part)) < repeatMin {
			continue
		}
		count[blockHash(part)]++
	}
	for _, n := range count {
		if n >= 2 {
			return true
		}
	}
	return false
}

func repeatedLines(lines []string) bool {
	count := map[uint64]int{}
	sentenceCount := map[string]int{}
	var longSentences []string
	for _, line := range lines {
		// A line made only of short sentences is not a repeated block.
		// Those fixtures repeat 「合适的正文。」 to reach a length target.
		if substantialLine(line) && utf8.RuneCountInString(normalizeBlock(line)) >= repeatMin {
			count[blockHash(line)]++
		}
		for _, sentence := range splitSentences(line) {
			key := repeatKey(sentence)
			if utf8.RuneCountInString(key) < repeatMin {
				continue
			}
			sentenceCount[key]++
			longSentences = append(longSentences, key)
		}
	}
	for _, n := range count {
		if n >= 2 {
			return true
		}
	}
	for _, n := range sentenceCount {
		if n >= 2 {
			return true
		}
	}
	return ngramRepetitionHigh(longSentences)
}

func substantialLine(text string) bool {
	parts := splitSentences(text)
	if len(parts) == 0 {
		return utf8.RuneCountInString(repeatKey(text)) >= repeatMin
	}
	for _, part := range parts {
		if utf8.RuneCountInString(repeatKey(part)) >= repeatMin {
			return true
		}
	}
	return false
}

// DedupeHashedBlocks drops a later paragraph, list item, or table whose
// normalized text hashes to one already kept. The repetition gate runs on the
// draft before this, so two copies still hold and the stored body is clean.
func DedupeHashedBlocks(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	seen := map[uint64]bool{}
	keep := func(text string) bool {
		sum := blockHash(text)
		if sum == 0 {
			return strings.TrimSpace(text) != ""
		}
		if seen[sum] {
			return false
		}
		seen[sum] = true
		return true
	}
	for _, section := range sectionsOf(draft) {
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			text := strings.TrimSpace(textOf(item))
			if text == "" || !keep(text) {
				continue
			}
			paras = append(paras, text)
		}
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
		var list []any
		for _, item := range anyList(section["list"]) {
			text := strings.TrimSpace(textOf(item))
			if text == "" || !keep(text) {
				continue
			}
			list = append(list, text)
		}
		if list == nil {
			list = []any{}
		}
		section["list"] = list
		if table, _ := section["table"].(map[string]any); table != nil {
			if blob := tableBlockText(table); blob == "" || !keep(blob) {
				delete(section, "table")
			}
		}
	}
	if text := strings.TrimSpace(textOf(draft["closing"])); text != "" && !keep(text) {
		draft["closing"] = ""
	}
	return draft
}

func tableBlockText(table map[string]any) string {
	var parts []string
	for _, item := range anyList(table["headers"]) {
		if text := strings.TrimSpace(textOf(item)); text != "" {
			parts = append(parts, text)
		}
	}
	for _, row := range anyList(table["rows"]) {
		for _, cell := range anyList(row) {
			if text := strings.TrimSpace(textOf(cell)); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func normalizeBlock(text string) string {
	text = citeMark.ReplaceAllString(text, "")
	text = strings.TrimSpace(factIndex.ReplaceAllString(strings.TrimSpace(text), ""))
	var b strings.Builder
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func blockHash(text string) uint64 {
	norm := normalizeBlock(text)
	if norm == "" {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(norm))
	sum := h.Sum64()
	if sum == 0 {
		return 1
	}
	return sum
}

// renumberContinuous rewrites the leading indexes inside one list to 1..n.
// A list written as 1, 3, 5 or 1, 2, 1, 2 stays in that order and becomes continuous.
func renumberContinuous(items []any) []any {
	numbered := 0
	for _, item := range items {
		if _, ok := leadingIndex(textOf(item)); ok {
			numbered++
		}
	}
	if numbered < 2 {
		return items
	}
	out := append([]any{}, items...)
	n := 0
	for i, item := range items {
		text := strings.TrimSpace(textOf(item))
		if _, ok := leadingIndex(text); !ok {
			continue
		}
		n++
		rest := strings.TrimSpace(factIndex.ReplaceAllString(text, ""))
		out[i] = strconv.Itoa(n) + ". " + rest
	}
	return out
}

func repeatKey(text string) string {
	text = strings.TrimSpace(citeMark.ReplaceAllString(text, ""))
	if lead := numberedLead(text); lead != "" {
		text = strings.TrimSpace(strings.TrimPrefix(text, strings.TrimRight(lead, " ")))
		text = strings.TrimSpace(strings.TrimPrefix(text, strings.TrimSpace(lead)))
	}
	return strings.TrimSpace(text)
}

// ngramRepetitionHigh reports when 8-character phrases that show up in more
// than two sentences account for at least 45% of the phrase occurrences.
// A phrase is counted once per sentence, so a length pad inside one sentence
// does not trip the gate.
func ngramRepetitionHigh(sentences []string) bool {
	const n = 8
	freq := map[string]int{}
	total := 0
	for _, sentence := range sentences {
		runes := []rune(sentence)
		if len(runes) < n {
			continue
		}
		seen := map[string]bool{}
		for i := 0; i+n <= len(runes); i++ {
			gram := string(runes[i : i+n])
			if seen[gram] {
				continue
			}
			seen[gram] = true
			freq[gram]++
			total++
		}
	}
	if total < 24 {
		return false
	}
	repeated := 0
	for _, count := range freq {
		if count >= 6 {
			return true
		}
		if count > 2 {
			repeated += count
		}
	}
	return repeated*100/total >= 45
}

// CapRepeatedPhrases keeps a date token or an 8-character phrase to at most limit occurrences.
func CapRepeatedPhrases(draft map[string]any, limit int) map[string]any {
	if draft == nil {
		return nil
	}
	if limit < 1 {
		limit = 2
	}
	dates := map[string]int{}
	phrases := map[string]int{}
	sentences := map[string]int{}
	rewrite := func(text string) string {
		parts := splitSentences(text)
		if len(parts) == 0 {
			return text
		}
		var kept []string
		for _, part := range parts {
			part = stripExtraDates(part, dates, limit)
			key := strings.TrimSpace(citeMark.ReplaceAllString(part, ""))
			same := sentences[key] > 0
			sentences[key]++
			if !same && repeatedPhrase(part, phrases, limit) {
				continue
			}
			if !same {
				notePhrases(part, phrases)
			}
			if strings.TrimSpace(part) != "" {
				kept = append(kept, part)
			}
		}
		if len(kept) == 0 {
			return ""
		}
		return strings.Join(kept, "")
	}
	applyText(draft, rewrite)
	return draft
}

// CapWordRepetition drops a later sentence once a filler word has been used `limit` times,
// or once any other content word has been used twice that often.
// Stopwords, words from the title, and a repeated identical sentence are left in place.
func CapWordRepetition(draft map[string]any, limit int) map[string]any {
	if draft == nil {
		return nil
	}
	if limit < 1 {
		limit = 4
	}
	general := limit * 2
	exempt := map[string]bool{}
	for _, word := range hanWords(textOf(draft["title"])) {
		exempt[word] = true
	}
	seen := map[string]int{}
	sentences := map[string]int{}
	rewrite := func(text string) string {
		parts := splitSentences(text)
		if len(parts) == 0 {
			return text
		}
		var kept []string
		for _, part := range parts {
			key := strings.TrimSpace(citeMark.ReplaceAllString(part, ""))
			if sentences[key] > 0 {
				sentences[key]++
				kept = append(kept, part)
				continue
			}
			sentences[key]++
			if overWordCap(part, seen, exempt, limit, general) {
				continue
			}
			noteWords(part, seen, exempt)
			if strings.TrimSpace(part) != "" {
				kept = append(kept, part)
			}
		}
		if len(kept) == 0 {
			return ""
		}
		return strings.Join(kept, "")
	}
	applyText(draft, rewrite)
	for _, section := range sectionsOf(draft) {
		var items []any
		for _, item := range anyList(section["list"]) {
			text := strings.TrimSpace(rewrite(textOf(item)))
			if text == "" {
				continue
			}
			items = append(items, text)
		}
		if len(anyList(section["list"])) > 0 {
			if items == nil {
				items = []any{}
			}
			section["list"] = items
		}
	}
	return draft
}

func overWordCap(text string, seen map[string]int, exempt map[string]bool, fillerLimit, general int) bool {
	noted := map[string]bool{}
	for _, word := range hanWords(text) {
		if exempt[word] || noted[word] || !countedWord(word) {
			continue
		}
		noted[word] = true
		limit := general
		if fillerWord(word) {
			limit = fillerLimit
		}
		if seen[word] >= limit {
			return true
		}
	}
	return false
}

func noteWords(text string, seen map[string]int, exempt map[string]bool) {
	noted := map[string]bool{}
	for _, word := range hanWords(text) {
		if exempt[word] || noted[word] || !countedWord(word) {
			continue
		}
		noted[word] = true
		seen[word]++
	}
}

func countedWord(word string) bool {
	return fillerWord(word) || contentBigram(word)
}

func fillerWord(word string) bool {
	switch word {
	case "从容", "省力", "轻松", "日常", "生活", "自己", "事情", "动作", "今天", "明天", "杯子", "窗边", "桌前", "座位", "走动":
		return true
	default:
		return false
	}
}

func contentBigram(word string) bool {
	for _, r := range word {
		switch r {
		case '的', '了', '在', '是', '和', '与', '及', '把', '用', '就', '都', '而', '要', '对', '从', '给', '让', '这', '那', '一', '个', '不', '也', '又', '很', '最', '会', '能', '可', '被', '向', '中', '上', '下', '来', '去', '或', '并', '其', '为', '以', '于', '到', '等', '吧', '吗', '呢':
			return false
		}
	}
	return true
}

func hanWords(text string) []string {
	var out []string
	var buf []rune
	flush := func() {
		for i := 0; i+2 <= len(buf); i++ {
			out = append(out, string(buf[i:i+2]))
		}
		buf = nil
	}
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			buf = append(buf, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

var factIndex = regexp.MustCompile(`^(\d+)[\.、]\s*`)

// StripFactNumbers removes a leading "2." style index when the paragraphs are not a contiguous 1..n list.
// A real step list that starts at 1 and does not skip is kept. List items are renumbered 1..n instead,
// including a list written as 1,1,2 or 2,2.
func StripFactNumbers(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	for _, section := range sectionsOf(draft) {
		section["paragraphs"] = stripBrokenNumbers(anyList(section["paragraphs"]))
		if len(anyList(section["list"])) > 0 {
			section["list"] = renumberContinuous(anyList(section["list"]))
		}
	}
	return draft
}

func stripBrokenNumbers(items []any) []any {
	if numbersContiguous(items) {
		return items
	}
	if shifted, ok := renumberOffset(items); ok {
		return shifted
	}
	var out []any
	for _, item := range items {
		text := textOf(item)
		if factIndex.MatchString(strings.TrimSpace(text)) {
			text = strings.TrimSpace(factIndex.ReplaceAllString(strings.TrimSpace(text), ""))
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, text)
	}
	if out == nil {
		out = []any{}
	}
	return out
}

// renumberOffset turns a contiguous list that starts at 2 (2. 3. 4.) into 1..n.
// A broken sequence such as 2. 3. 8. is left alone so the caller can strip it.
func renumberOffset(items []any) ([]any, bool) {
	var nums, idx []int
	for i, item := range items {
		n, ok := leadingIndex(textOf(item))
		if !ok {
			continue
		}
		nums = append(nums, n)
		idx = append(idx, i)
	}
	if len(nums) < 2 || nums[0] <= 1 {
		return nil, false
	}
	for i := 1; i < len(nums); i++ {
		if nums[i] != nums[0]+i {
			return nil, false
		}
	}
	out := append([]any{}, items...)
	for i, at := range idx {
		rest := strings.TrimSpace(factIndex.ReplaceAllString(strings.TrimSpace(textOf(items[at])), ""))
		out[at] = strconv.Itoa(i+1) + ". " + rest
	}
	return out, true
}

func numbersContiguous(items []any) bool {
	n := 0
	for _, item := range items {
		got, ok := leadingIndex(textOf(item))
		if !ok {
			continue
		}
		n++
		if got != n {
			return false
		}
	}
	return true
}

func leadingIndex(text string) (int, bool) {
	text = strings.TrimSpace(text)
	m := factIndex.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	n := 0
	for _, r := range m[1] {
		n = n*10 + int(r-'0')
	}
	return n, n > 0
}

// ConcreteTips counts sentences and list items a reader can actually do:
// at least 8 characters, an action, and a number, a unit, or a concrete noun.
// Generic filler such as 杯子, 从容, or 省力 does not count.
func ConcreteTips(draft map[string]any) int {
	if draft == nil {
		return 0
	}
	n := 0
	seen := map[string]bool{}
	count := func(text string) {
		for _, part := range splitSentences(text) {
			part = strings.TrimSpace(part)
			if seen[part] || !actionableTip(part) {
				continue
			}
			seen[part] = true
			n++
		}
	}
	for _, section := range sectionsOf(draft) {
		for _, item := range anyList(section["paragraphs"]) {
			count(textOf(item))
		}
		for _, item := range anyList(section["list"]) {
			count(textOf(item))
		}
	}
	return n
}

func actionableTip(text string) bool {
	if utf8.RuneCountInString(text) < 8 || !hasAction(text) {
		return false
	}
	if hasDigit(text) || hasUnit(text) || hasConcreteNoun(text) {
		return true
	}
	return false
}

func hasAction(text string) bool {
	for _, word := range []string{"打开", "关上", "先", "把", "用", "放", "擦", "倒", "煮", "晾", "拧", "贴", "量", "剪", "泡", "洗", "取", "加", "换", "盖"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

func hasDigit(text string) bool {
	for _, r := range text {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func hasUnit(text string) bool {
	for _, word := range []string{"分钟", "小时", "秒钟", "厘米", "毫米", "毫升", "克", "度"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

func hasConcreteNoun(text string) bool {
	for _, word := range []string{"白醋", "小苏打", "牙刷", "胶带", "抹布", "牙膏", "螺丝", "线槽", "插座", "水管", "纱窗", "水壶", "保鲜膜", "柠檬", "酒精", "肥皂", "花洒", "滤网", "水垢", "瓷砖"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

// FillerHeavy reports experience copy that leans on generic words and has no numbers.
func FillerHeavy(draft map[string]any) bool {
	if draft == nil || hasDigit(fillerText(draft)) {
		return false
	}
	n := 0
	text := fillerText(draft)
	for _, word := range []string{"从容", "省力", "轻松", "杯子", "窗边", "桌前", "走动", "温馨", "默契"} {
		n += strings.Count(text, word)
	}
	return n >= 3
}

func fillerText(draft map[string]any) string {
	var b strings.Builder
	for _, section := range sectionsOf(draft) {
		for _, item := range anyList(section["paragraphs"]) {
			b.WriteString(textOf(item))
		}
		for _, item := range anyList(section["list"]) {
			b.WriteString(textOf(item))
		}
	}
	b.WriteString(textOf(draft["closing"]))
	return b.String()
}

func stripExtraDates(text string, seen map[string]int, limit int) string {
	for _, word := range []string{"近日", "今天", "昨天", "本周", "上周", "周一", "周二", "周三", "周四", "周五", "周六", "周日"} {
		for strings.Contains(text, word) {
			seen[word]++
			if seen[word] <= limit {
				break
			}
			text = strings.Replace(text, word, "", 1)
		}
	}
	text = datedToken.ReplaceAllStringFunc(text, func(token string) string {
		seen[token]++
		if seen[token] <= limit {
			return token
		}
		return ""
	})
	return strings.TrimSpace(text)
}

func repeatedPhrase(text string, seen map[string]int, limit int) bool {
	for _, phrase := range hanPhrases(text, 8) {
		if seen[phrase] >= limit {
			return true
		}
	}
	return false
}

func notePhrases(text string, seen map[string]int) {
	noted := map[string]bool{}
	for _, phrase := range hanPhrases(text, 8) {
		if noted[phrase] {
			continue
		}
		noted[phrase] = true
		seen[phrase]++
	}
}

func hanPhrases(text string, n int) []string {
	var out []string
	var buf []rune
	flush := func() {
		if len(buf) < n {
			buf = nil
			return
		}
		for i := 0; i+n <= len(buf); i++ {
			out = append(out, string(buf[i:i+n]))
		}
		buf = nil
	}
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			buf = append(buf, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// DropOffTopicParagraphs removes a paragraph that shares no term with the title.
// KeepOneSubject drops paragraphs that wander into a second household or job topic.
// A paragraph that does not name another topic is kept. The majority topic stays.
func KeepOneSubject(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	subject := textOf(draft["title"]) + textOf(draft["summary"])
	type domain struct {
		name  string
		words []string
	}
	domains := []domain{
		{"做饭", []string{"做饭", "炒菜", "菜谱", "下锅", "灶台", "厨房"}},
		{"除尘", []string{"除尘", "灰尘", "抹布"}},
		{"植物", []string{"绿植", "养花", "浇花", "花盆"}},
		{"布线", []string{"布线", "水晶头", "线槽"}},
		{"监控", []string{"监控", "摄像头", "录像机"}},
	}
	match := func(text string) string {
		for _, item := range domains {
			for _, word := range item.words {
				if strings.Contains(text, word) {
					return item.name
				}
			}
		}
		return ""
	}
	allowed := map[string]bool{}
	if name := match(subject); name != "" {
		allowed[name] = true
	}
	if len(allowed) == 0 {
		counts := map[string]int{}
		best, bestN := "", 0
		for _, section := range sectionsOf(draft) {
			for _, item := range anyList(section["paragraphs"]) {
				if name := match(textOf(item)); name != "" {
					counts[name]++
					if counts[name] > bestN {
						best, bestN = name, counts[name]
					}
				}
			}
		}
		if best == "" {
			return draft
		}
		allowed[best] = true
	}
	for _, section := range sectionsOf(draft) {
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			text := textOf(item)
			if name := match(text); name != "" && !allowed[name] {
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

func DropOffTopicParagraphs(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	terms, phrases := titleTerms(textOf(draft["title"]))
	if len(terms) < 2 {
		return draft
	}
	type slot struct {
		section map[string]any
		index   int
		text    string
		keep    bool
	}
	var slots []slot
	for _, section := range sectionsOf(draft) {
		paras := anyList(section["paragraphs"])
		for i, item := range paras {
			text := strings.TrimSpace(textOf(item))
			if text == "" {
				continue
			}
			slots = append(slots, slot{section: section, index: i, text: text, keep: onTitle(text, terms, phrases)})
		}
	}
	kept := 0
	for _, slot := range slots {
		if slot.keep {
			kept++
		}
	}
	if kept == 0 {
		return draft
	}
	for _, section := range sectionsOf(draft) {
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			text := strings.TrimSpace(textOf(item))
			if text == "" {
				continue
			}
			if !onTitle(text, terms, phrases) {
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

func titleTerms(title string) (terms []string, phrases []string) {
	skip := map[string]bool{"的": true, "了": true, "是": true, "在": true, "和": true, "与": true, "或": true, "等": true, "不": true, "也": true, "就": true, "都": true, "而": true, "及": true, "把": true, "被": true, "这": true, "那": true, "一个": true, "没有": true, "可以": true}
	seen := map[string]bool{}
	for _, run := range hanPhraseRuns(title) {
		if len(run) >= 4 {
			for i := 0; i+4 <= len(run); i++ {
				phrases = append(phrases, string(run[i:i+4]))
			}
		}
		for i := 0; i+2 <= len(run); i++ {
			term := string(run[i : i+2])
			if skip[term] || seen[term] {
				continue
			}
			seen[term] = true
			terms = append(terms, term)
		}
	}
	return terms, phrases
}

func hanPhraseRuns(text string) [][]rune {
	var out [][]rune
	var buf []rune
	flush := func() {
		if len(buf) > 0 {
			out = append(out, buf)
			buf = nil
		}
	}
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			buf = append(buf, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func onTitle(text string, terms, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func applyText(draft map[string]any, rewrite func(string) string) {
	for _, key := range []string{"title", "summary", "closing"} {
		if text := textOf(draft[key]); text != "" {
			draft[key] = rewrite(text)
		}
	}
	for _, section := range sectionsOf(draft) {
		if text := textOf(section["heading"]); text != "" {
			section["heading"] = rewrite(text)
		}
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			text := strings.TrimSpace(rewrite(textOf(item)))
			if text == "" {
				continue
			}
			paras = append(paras, text)
		}
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
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
