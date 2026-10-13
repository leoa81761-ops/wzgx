package editorial

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// BrokenGrammar reports a sentence that lost a word in the middle:
// an empty slot, a dangling particle, or a heading that stops at a colon.
func BrokenGrammar(draft map[string]any) bool {
	if draft == nil {
		return false
	}
	for _, key := range []string{"title", "summary", "closing"} {
		if Dangling(textOf(draft[key])) {
			return true
		}
	}
	for _, section := range sectionsOf(draft) {
		if Dangling(textOf(section["heading"])) || truncatedHeading(textOf(section["heading"])) {
			return true
		}
		for _, item := range anyList(section["paragraphs"]) {
			if Dangling(textOf(item)) {
				return true
			}
		}
		for _, item := range anyList(section["list"]) {
			if Dangling(textOf(item)) {
				return true
			}
		}
	}
	return false
}

var (
	emptySlot   = regexp.MustCompile(`，，|,,|、、|：：|::`)
	brokenFrame = regexp.MustCompile(`从入手|在本身|表明[，,。：:]`)
	colonOnly   = regexp.MustCompile(`^[^：:]{1,8}[：:]$`)
)

// Dangling reports a sentence with an empty slot or a particle left without its object.
func Dangling(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if emptySlot.MatchString(text) || brokenFrame.MatchString(text) {
		return true
	}
	if truncatedHeading(text) {
		return true
	}
	for _, sentence := range splitSentences(text) {
		if danglingParticle(sentence) {
			return true
		}
	}
	return false
}

// danglingLeft is a character that leaves the following particle without an object.
// A content character, as in 核对 or 存在, is a finished word and is not dangling.
var danglingLeft = map[rune]bool{
	'先': true, '再': true, '就': true, '才': true, '又': true, '也': true,
	'还': true, '更': true, '最': true, '要': true, '会': true, '能': true,
	'可': true, '别': true, '请': true, '来': true, '去': true, '出': true,
	'正': true,
}

func danglingParticle(sentence string) bool {
	s := strings.TrimRight(strings.TrimSpace(sentence), "。！？!?，,：:、 ")
	r := []rune(s)
	if len(r) == 0 {
		return false
	}
	last := r[len(r)-1]
	if !strings.ContainsRune("在从把被向对给让将于", last) {
		return false
	}
	if len(r) == 1 {
		return true
	}
	return danglingLeft[r[len(r)-2]]
}

func truncatedHeading(text string) bool {
	text = strings.TrimSpace(text)
	return colonOnly.MatchString(text)
}

// spliced reports a rewrite that is the original sentence with a middle span deleted.
func spliced(original, next string) bool {
	o := []rune(strings.TrimSpace(original))
	n := []rune(strings.TrimSpace(next))
	if len(n) == 0 || len(n) >= len(o) {
		return false
	}
	for cut := 1; cut < len(o); cut++ {
		for end := cut + 1; end <= len(o); end++ {
			if string(o[:cut])+string(o[end:]) == string(n) {
				return true
			}
		}
	}
	return false
}

// InventedSentences lists sentences that state a figure with no source and no experience frame.
func InventedSentences(draft map[string]any) []string {
	if draft == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(text string) {
		for _, sentence := range splitSentences(text) {
			sentence = strings.TrimSpace(sentence)
			if sentence == "" || seen[sentence] || !hardStatSentence(sentence) || experientialStat(sentence) {
				continue
			}
			seen[sentence] = true
			out = append(out, sentence)
		}
	}
	for _, key := range []string{"summary", "closing"} {
		add(textOf(draft[key]))
	}
	for _, section := range sectionsOf(draft) {
		for _, item := range anyList(section["paragraphs"]) {
			add(textOf(item))
		}
		for _, item := range anyList(section["list"]) {
			add(textOf(item))
		}
	}
	return out
}

// RewriteMap reads {"sentences":[{"from":"...","to":"..."}]} from a sentence rewrite.
func RewriteMap(raw map[string]any) map[string]string {
	out := map[string]string{}
	if raw == nil {
		return out
	}
	for _, item := range anyList(raw["sentences"]) {
		row, _ := item.(map[string]any)
		if row == nil {
			continue
		}
		from := strings.TrimSpace(textOf(row["from"]))
		to := strings.TrimSpace(textOf(row["to"]))
		if from == "" || to == "" {
			continue
		}
		out[from] = to
	}
	return out
}

// ApplyRewrites replaces each flagged sentence with a whole-sentence rewrite.
// A rewrite that is empty, spliced, still a hard figure, or grammatically broken is dropped with the sentence.
func ApplyRewrites(draft map[string]any, flagged []string, repl map[string]string) map[string]any {
	if draft == nil || len(flagged) == 0 {
		return draft
	}
	bad := map[string]bool{}
	for _, sentence := range flagged {
		bad[strings.TrimSpace(sentence)] = true
	}
	rewrite := func(text string) string {
		parts := splitSentences(text)
		if len(parts) == 0 {
			if bad[strings.TrimSpace(text)] {
				return ""
			}
			return text
		}
		var kept []string
		for _, part := range parts {
			key := strings.TrimSpace(part)
			if !bad[key] {
				kept = append(kept, part)
				continue
			}
			next := strings.TrimSpace(repl[key])
			if next == "" || spliced(key, next) || Dangling(next) || (hardStatSentence(next) && !experientialStat(next)) {
				continue
			}
			if len(splitSentences(next)) != 1 {
				continue
			}
			kept = append(kept, next)
		}
		return strings.Join(kept, "")
	}
	for _, key := range []string{"summary", "closing"} {
		if text := textOf(draft[key]); text != "" {
			draft[key] = rewrite(text)
		}
	}
	for _, section := range sectionsOf(draft) {
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			if text := strings.TrimSpace(rewrite(textOf(item))); text != "" {
				paras = append(paras, text)
			}
		}
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
		if len(anyList(section["list"])) == 0 {
			continue
		}
		var items []any
		for _, item := range anyList(section["list"]) {
			if text := strings.TrimSpace(rewrite(textOf(item))); text != "" {
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

// StayOnTitle drops a paragraph that switches to another subject the title does not name.
// A paragraph that does not name a rival subject stays, so one detail is not copied into every section.
func StayOnTitle(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	title := textOf(draft["title"]) + textOf(draft["summary"])
	type topic struct {
		words []string
	}
	topics := []topic{
		{[]string{"关节", "膝盖", "膝关节", "肩关节", "腰椎"}},
		{[]string{"皮肤", "皱纹", "保湿", "面霜"}},
		{[]string{"视力", "老花", "眼睛"}},
		{[]string{"记忆", "记性", "健忘"}},
		{[]string{"做饭", "炒菜", "灶台"}},
		{[]string{"除尘", "灰尘"}},
		{[]string{"绿植", "浇花", "花盆"}},
		{[]string{"布线", "水晶头", "线槽"}},
		{[]string{"监控", "摄像头"}},
	}
	var titled [][]string
	for _, item := range topics {
		if containsAny(title, item.words...) {
			titled = append(titled, item.words)
		}
	}
	if len(titled) == 0 {
		return draft
	}
	onTitle := func(text string) bool {
		for _, words := range titled {
			if containsAny(text, words...) {
				return true
			}
		}
		return false
	}
	rival := func(text string) bool {
		if onTitle(text) {
			return false
		}
		for _, item := range topics {
			if containsAny(title, item.words...) {
				continue
			}
			if containsAny(text, item.words...) {
				return true
			}
		}
		return false
	}
	for _, section := range sectionsOf(draft) {
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			text := strings.TrimSpace(textOf(item))
			if text == "" || rival(text) {
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

// EnsureOpening moves one prose paragraph out in front of the first heading.
// It does not invent a sentence. A section that would be left empty keeps its paragraph.
func EnsureOpening(draft map[string]any) map[string]any {
	if draft == nil || HasOpening(draft) {
		return draft
	}
	sections := sectionsOf(draft)
	if len(sections) == 0 {
		return draft
	}
	first := sections[0]
	if strings.TrimSpace(textOf(first["heading"])) == "" {
		return draft
	}
	paras := anyList(first["paragraphs"])
	picked := -1
	for i, item := range paras {
		text := strings.TrimSpace(textOf(item))
		if text == "" || leadingNumber(text) || utf8.RuneCountInString(text) < 12 {
			continue
		}
		picked = i
		break
	}
	if picked < 0 {
		return draft
	}
	hook := strings.TrimSpace(textOf(paras[picked]))
	var rest []any
	for i, item := range paras {
		if i == picked {
			continue
		}
		if text := strings.TrimSpace(textOf(item)); text != "" {
			rest = append(rest, text)
		}
	}
	hasMore := len(rest) > 0 || len(anyList(first["list"])) > 0 || first["table"] != nil
	if !hasMore {
		return draft
	}
	if rest == nil {
		rest = []any{}
	}
	first["paragraphs"] = rest
	lead := map[string]any{"heading": "", "paragraphs": []any{hook}, "list": []any{}}
	var next []any
	next = append(next, lead)
	for _, raw := range anyList(draft["sections"]) {
		next = append(next, raw)
	}
	draft["sections"] = next
	return draft
}

// HasOpening reports a prose paragraph that renders before the first heading.
func HasOpening(draft map[string]any) bool {
	sections := sectionsOf(draft)
	if len(sections) == 0 {
		return false
	}
	if strings.TrimSpace(textOf(sections[0]["heading"])) != "" {
		return false
	}
	for _, item := range anyList(sections[0]["paragraphs"]) {
		text := strings.TrimSpace(textOf(item))
		if utf8.RuneCountInString(text) >= 12 && !leadingNumber(text) {
			return true
		}
	}
	return false
}

func leadingNumber(text string) bool {
	_, ok := leadingIndex(text)
	return ok
}

// RenumberLists rewrites every list and every numbered paragraph inside one section to 1..n.
// The section's own index is not a list number.
func RenumberLists(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	for _, section := range sectionsOf(draft) {
		section["paragraphs"] = renumberLeaders(anyList(section["paragraphs"]))
		if len(anyList(section["list"])) > 0 {
			section["list"] = renumberLeaders(anyList(section["list"]))
		}
	}
	return draft
}

func renumberLeaders(items []any) []any {
	numbered := 0
	for _, item := range items {
		if _, ok := leadingIndex(textOf(item)); ok {
			numbered++
		}
	}
	if numbered == 0 {
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

// ShouldSkipDraft decides whether this run stays out of the WeChat draft box.
// stored is -1 when the plan has not chosen, 0 to push, 1 to skip.
// An admin running a test account skips unless the plan or this trigger explicitly pushes.
func ShouldSkipDraft(stored int, explicit *bool, exempt, admin bool, trigger string) bool {
	if explicit != nil {
		return *explicit
	}
	if stored == 1 {
		return true
	}
	if stored == 0 {
		return false
	}
	return exempt && admin && trigger != "schedule"
}
