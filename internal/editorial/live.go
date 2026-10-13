package editorial

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/content"
)

const (
	// bodyFloor is the stub line. A shorter body is not a length target; it is unfinished.
	bodyFloor = 600
	// minSubstantiveSections is how many sections still have something to read.
	minSubstantiveSections = 3
)

var vagueQuantityRe = regexp.MustCompile(`(?:\d+|[一两二三四五六七八九十]+)\s*(?:个)?(?:小时|分钟|天|周|轮)\s*(?:以内|内)`)

var instructionLeak = regexp.MustCompile(`只需写|不用列公式|不用列|字数|word_target|JSON Schema|只输出|编辑要求|稿件数据|reader_pain|section_heading|section_target|栏目是问答|栏目是步骤|请改写|请对照|请重写|重写这一节|请用完整`)

// vagueQuantity is a soft figure such as 24小时内 or 两轮以内 with no source.
func vagueQuantity(text string) bool {
	return vagueQuantityRe.MatchString(text)
}

// vagueAsExperience keeps a quantity only when the writer is reporting what they saw.
// A bare 一般 or 往往 in the same sentence does not make 两轮以内 an experience.
func vagueAsExperience(text string) bool {
	for _, word := range []string{"我见过", "我一般", "我通常", "大多", "大概", "大约"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

// StripVagueQuantities drops an unsourced time or round limit.
// A sentence that is the writer's own experience, or that already cites [n], stays.
func StripVagueQuantities(draft map[string]any) map[string]any {
	return dropMatchingSentences(draft, func(sentence string) bool {
		if !vagueQuantity(sentence) || vagueAsExperience(sentence) || strings.Contains(sentence, "[") {
			return false
		}
		return true
	})
}

func dropMatchingSentences(draft map[string]any, drop func(string) bool) map[string]any {
	if draft == nil {
		return nil
	}
	rewrite := func(text string) string {
		parts := splitSentences(text)
		if len(parts) == 0 {
			if drop(strings.TrimSpace(text)) {
				return ""
			}
			return text
		}
		var kept []string
		changed := false
		for _, part := range parts {
			if drop(strings.TrimSpace(part)) {
				changed = true
				continue
			}
			kept = append(kept, part)
		}
		if !changed {
			return text
		}
		return strings.Join(kept, "")
	}
	for _, key := range []string{"title", "summary", "closing"} {
		if text := textOf(draft[key]); text != "" {
			draft[key] = rewrite(text)
		}
	}
	for _, section := range sectionsOf(draft) {
		if heading := rewrite(textOf(section["heading"])); heading != textOf(section["heading"]) {
			section["heading"] = heading
		}
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
		cleanQA(section, rewrite)
		rewriteTable(section, rewrite)
	}
	return draft
}

func rewriteTable(section map[string]any, rewrite func(string) string) {
	table, _ := section["table"].(map[string]any)
	if table == nil || rewrite == nil {
		return
	}
	var headers []any
	for _, item := range anyList(table["headers"]) {
		headers = append(headers, strings.TrimSpace(rewrite(textOf(item))))
	}
	if len(headers) > 0 {
		table["headers"] = headers
	}
	var rows []any
	for _, raw := range anyList(table["rows"]) {
		cells, ok := rowCells(raw)
		if !ok {
			rows = append(rows, raw)
			continue
		}
		line := make([]any, len(cells))
		empty := true
		for i, cell := range cells {
			text := strings.TrimSpace(rewrite(textOf(cell)))
			if text != "" {
				empty = false
			}
			line[i] = text
		}
		if !empty {
			rows = append(rows, line)
		}
	}
	if rows == nil {
		rows = []any{}
	}
	table["rows"] = rows
}

func cleanQA(section map[string]any, rewrite func(string) string) {
	var qa []any
	for _, item := range anyList(section["qa"]) {
		row, _ := item.(map[string]any)
		if row == nil {
			continue
		}
		question := strings.TrimSpace(rewrite(textOf(row["question"])))
		answer := strings.TrimSpace(rewrite(textOf(row["answer"])))
		if question == "" || answer == "" {
			continue
		}
		qa = append(qa, map[string]any{"question": question, "answer": answer})
	}
	if len(qa) == 0 {
		delete(section, "qa")
		return
	}
	section["qa"] = qa
}

// DedupeSentences drops a later copy of the same sentence, including one that
// shows up again in the closing. A paragraph that is only a repeated pad is left
// intact so a length fixture is not collapsed into one line.
func DedupeSentences(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	seen := map[string]bool{}
	note := func(sentence string) {
		key := sentenceKey(sentence)
		if key != "" {
			seen[key] = true
		}
	}
	take := func(sentence string) string {
		key := sentenceKey(sentence)
		if key == "" {
			return sentence
		}
		if seen[key] {
			return ""
		}
		seen[key] = true
		return sentence
	}
	rewrite := func(text string) string {
		if lengthPad(text) {
			parts := splitSentences(text)
			if len(parts) > 0 {
				note(parts[0])
			}
			return text
		}
		parts := splitSentences(text)
		if len(parts) == 0 {
			if take(text) == "" {
				return ""
			}
			return text
		}
		var kept []string
		changed := false
		for _, part := range parts {
			next := take(part)
			if next == "" {
				changed = true
				continue
			}
			kept = append(kept, next)
		}
		if !changed {
			return text
		}
		return strings.Join(kept, "")
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
		cleanQA(section, rewrite)
	}
	if text := textOf(draft["closing"]); text != "" {
		draft["closing"] = rewrite(text)
	}
	return draft
}

func sentenceKey(sentence string) string {
	sentence = strings.TrimSpace(sentence)
	if utf8.RuneCountInString(sentence) < 8 {
		return ""
	}
	return sentence
}

// BelowFloor reports a stub: under about 600 characters, or fewer than 3 sections that still say something.
func BelowFloor(draft map[string]any) bool {
	if draft == nil {
		return true
	}
	if content.BodyRunes(draft) < bodyFloor {
		return true
	}
	return SubstantiveSections(draft) < minSubstantiveSections
}

// SubstantiveSections counts sections with enough prose, list, table, or answers to read.
func SubstantiveSections(draft map[string]any) int {
	n := 0
	for _, section := range sectionsOf(draft) {
		if sectionBodyRunes(section) >= 40 {
			n++
		}
	}
	return n
}

func sectionBodyRunes(section map[string]any) int {
	n := 0
	for _, item := range anyList(section["paragraphs"]) {
		n += utf8.RuneCountInString(strings.TrimSpace(textOf(item)))
	}
	for _, item := range anyList(section["list"]) {
		n += utf8.RuneCountInString(strings.TrimSpace(textOf(item)))
	}
	for _, item := range anyList(section["qa"]) {
		row, _ := item.(map[string]any)
		if row == nil {
			continue
		}
		n += utf8.RuneCountInString(textOf(row["question"]))
		n += utf8.RuneCountInString(textOf(row["answer"]))
	}
	if table, _ := section["table"].(map[string]any); table != nil {
		for _, item := range anyList(table["headers"]) {
			n += utf8.RuneCountInString(strings.TrimSpace(textOf(item)))
		}
		for _, row := range anyList(table["rows"]) {
			for _, cell := range anyList(row) {
				n += utf8.RuneCountInString(strings.TrimSpace(textOf(cell)))
			}
		}
	}
	return n
}

// Gutted reports a section that lost its prose. A missing section is gutted.
func Gutted(section map[string]any) bool {
	if section == nil {
		return true
	}
	return sectionBodyRunes(section) < 80
}

// MostlyRepeated reports a draft whose sentences are one line copied many times.
func MostlyRepeated(draft map[string]any) bool {
	var sentences []string
	collect := func(text string) {
		for _, part := range splitSentences(text) {
			part = strings.TrimSpace(part)
			if utf8.RuneCountInString(part) >= 4 {
				sentences = append(sentences, part)
			}
		}
	}
	for _, section := range sectionsOf(draft) {
		for _, item := range anyList(section["paragraphs"]) {
			collect(textOf(item))
		}
		for _, item := range anyList(section["list"]) {
			collect(textOf(item))
		}
	}
	collect(textOf(draft["closing"]))
	if len(sentences) < 8 {
		return false
	}
	count := map[string]int{}
	max := 0
	for _, sentence := range sentences {
		count[sentence]++
		if count[sentence] > max {
			max = count[sentence]
		}
	}
	return max*2 > len(sentences)
}

// PromptLeak reports editor instructions left in the title or the body.
func PromptLeak(draft map[string]any) bool {
	if draft == nil {
		return false
	}
	if leakedInstruction(textOf(draft["title"])) || leakedInstruction(textOf(draft["summary"])) || leakedInstruction(textOf(draft["closing"])) {
		return true
	}
	for _, section := range sectionsOf(draft) {
		if leakedInstruction(textOf(section["heading"])) {
			return true
		}
		for _, item := range anyList(section["paragraphs"]) {
			if leakedInstruction(textOf(item)) {
				return true
			}
		}
		for _, item := range anyList(section["list"]) {
			if leakedInstruction(textOf(item)) {
				return true
			}
		}
		for _, item := range anyList(section["qa"]) {
			row, _ := item.(map[string]any)
			if row == nil {
				continue
			}
			if leakedInstruction(textOf(row["question"])) || leakedInstruction(textOf(row["answer"])) {
				return true
			}
		}
	}
	return false
}

// StripPromptLeak removes a sentence that is an editor instruction.
func StripPromptLeak(draft map[string]any) map[string]any {
	return dropMatchingSentences(draft, leakedInstruction)
}

func leakedInstruction(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if instructionLeak.MatchString(text) {
		return true
	}
	if strings.Contains(text, "要求") && (strings.Contains(text, "写") || strings.Contains(text, "不要") || strings.Contains(text, "字")) {
		return true
	}
	if strings.Contains(text, "请") && (strings.Contains(text, "只需") || strings.Contains(text, "不要写") || strings.Contains(text, "输出")) {
		return true
	}
	return false
}

// OffTitle reports that fewer than 60% of the sections address the title's subject.
// Two-section drafts are left alone. A title whose subject never appears is not guessed.
func OffTitle(draft map[string]any) bool {
	if draft == nil {
		return false
	}
	sections := sectionsOf(draft)
	if len(sections) < minSubstantiveSections {
		return false
	}
	core := coreSubject(textOf(draft["title"]))
	if utf8.RuneCountInString(core) < 2 {
		return false
	}
	hit := 0
	for _, section := range sections {
		if sectionAddresses(section, core) {
			hit++
		}
	}
	if hit == 0 {
		return false
	}
	return hit*5 < len(sections)*3
}

func sectionAddresses(section map[string]any, core string) bool {
	var b strings.Builder
	b.WriteString(textOf(section["heading"]))
	for _, item := range anyList(section["paragraphs"]) {
		b.WriteString(textOf(item))
	}
	for _, item := range anyList(section["list"]) {
		b.WriteString(textOf(item))
	}
	for _, item := range anyList(section["qa"]) {
		row, _ := item.(map[string]any)
		if row == nil {
			continue
		}
		b.WriteString(textOf(row["question"]))
		b.WriteString(textOf(row["answer"]))
	}
	text := b.String()
	if strings.Contains(text, core) {
		return true
	}
	prefix := string([]rune(core)[:2])
	if prefixStop(prefix) {
		return false
	}
	return strings.Contains(text, prefix)
}

func prefixStop(text string) bool {
	switch text {
	case "先看", "再看", "怎么", "如何", "可以", "不要", "一个", "一篇", "我们", "如果", "应该", "需要", "这个", "那个", "什么", "不是":
		return true
	default:
		return false
	}
}

func coreSubject(title string) string {
	title = strings.TrimSpace(title)
	for _, sep := range []string{"怎么", "如何", "哪些", "什么", "为什么", "里", "的", "：", ":", "，", "。", "？", "?"} {
		title = strings.ReplaceAll(title, sep, " ")
	}
	best := ""
	for _, part := range strings.Fields(title) {
		part = strings.TrimSpace(part)
		if utf8.RuneCountInString(part) < 2 || titleStop(part) {
			continue
		}
		if utf8.RuneCountInString(part) > utf8.RuneCountInString(best) {
			best = part
		}
	}
	if utf8.RuneCountInString(best) > 6 {
		best = string([]rune(best)[:4])
	}
	return best
}

func titleStop(text string) bool {
	switch text {
	case "一个", "一篇", "这件事", "题目", "正文", "文章", "我们", "可以", "不是", "不要":
		return true
	default:
		return false
	}
}

// MaterializeQA moves question and answer lines into section qa.
// A line that is only 「答：」 is dropped. Render reads qa and does not print that marker.
func MaterializeQA(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	for _, section := range sectionsOf(draft) {
		var qa []any
		for _, item := range anyList(section["qa"]) {
			row, _ := item.(map[string]any)
			if row == nil {
				continue
			}
			question := cleanQuestion(textOf(row["question"]))
			answer := cleanAnswer(textOf(row["answer"]))
			if question == "" || answer == "" {
				continue
			}
			qa = append(qa, map[string]any{"question": question, "answer": answer})
		}
		var paras []any
		pending := ""
		flush := func() {
			if pending != "" {
				paras = append(paras, pending)
				pending = ""
			}
		}
		for _, item := range anyList(section["paragraphs"]) {
			text := strings.TrimSpace(textOf(item))
			if text == "" || loneAnswer(text) {
				continue
			}
			if question, answer, ok := splitQA(text); ok {
				flush()
				qa = append(qa, map[string]any{"question": question, "answer": answer})
				continue
			}
			if isQuestionLine(text) {
				flush()
				pending = text
				continue
			}
			if answer, ok := stripAnswerLead(text); ok {
				if pending != "" {
					qa = append(qa, map[string]any{"question": pending, "answer": answer})
					pending = ""
					continue
				}
				if answer != "" {
					paras = append(paras, answer)
				}
				continue
			}
			flush()
			paras = append(paras, text)
		}
		flush()
		if paras == nil {
			paras = []any{}
		}
		section["paragraphs"] = paras
		if len(qa) == 0 {
			delete(section, "qa")
		} else {
			section["qa"] = qa
		}
	}
	return draft
}

func isQuestionLine(text string) bool {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "答") {
		return false
	}
	return strings.HasSuffix(text, "？") || strings.HasSuffix(text, "?")
}

func splitQA(text string) (string, string, bool) {
	answerAt := strings.Index(text, "答")
	if answerAt < 0 {
		return "", "", false
	}
	q := strings.Index(text, "？")
	if alt := strings.Index(text, "?"); q < 0 || (alt >= 0 && alt < q) {
		q = alt
	}
	if q < 0 || q > answerAt {
		return "", "", false
	}
	markLen := 1
	if strings.HasPrefix(text[q:], "？") {
		markLen = len("？")
	}
	question := cleanQuestion(text[:q+markLen])
	answer := cleanAnswer(text[answerAt+len("答"):])
	if question == "" || answer == "" {
		return "", "", false
	}
	return question, answer, true
}

func stripAnswerLead(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "答") {
		return "", false
	}
	return cleanAnswer(text[len("答"):]), true
}

func loneAnswer(text string) bool {
	text = strings.TrimSpace(text)
	text = strings.Trim(text, "：: \t")
	return text == "答"
}

func cleanQuestion(text string) string {
	return strings.TrimSpace(text)
}

func cleanAnswer(text string) string {
	text = stripOneAnswer(text)
	text = strings.Trim(text, "：: \t")
	if loneAnswer(text) || text == "" {
		return ""
	}
	return text
}

func stripOneAnswer(text string) string {
	text = strings.TrimSpace(text)
	for strings.HasPrefix(text, "答") {
		next := strings.TrimSpace(strings.TrimLeft(strings.TrimPrefix(text, "答"), "：: \t"))
		if next == text {
			break
		}
		text = next
	}
	return text
}

// StripAnswerPrefixes removes a leading 「答：」 from every sentence that will be stored.
func StripAnswerPrefixes(draft map[string]any) map[string]any {
	return mapSentences(draft, stripOneAnswer)
}

func mapSentences(draft map[string]any, edit func(string) string) map[string]any {
	if draft == nil || edit == nil {
		return draft
	}
	rewrite := func(text string) string {
		parts := splitSentences(text)
		if len(parts) == 0 {
			return edit(strings.TrimSpace(text))
		}
		var kept []string
		for _, part := range parts {
			if next := strings.TrimSpace(edit(part)); next != "" {
				kept = append(kept, next)
			}
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
		cleanQA(section, rewrite)
		rewriteTable(section, rewrite)
	}
	return draft
}

var sceneStem = regexp.MustCompile(`(?:孙子|孙女|奶奶|爷爷|孩子|宝宝|爸爸|妈妈).{0,12}(?:端着|拿着|抱着|看着|喊着|跑来|走来|走过来)`)

// OnceScene keeps one family-scene sentence. Later variants of 孙子端着 are dropped.
func OnceScene(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	seen := false
	return mapSentences(draft, func(sentence string) string {
		if lengthPad(sentence) || !sceneStem.MatchString(sentence) {
			return sentence
		}
		if seen {
			return ""
		}
		seen = true
		return sentence
	})
}

// OutlineHits counts outline points that address the title's single subject.
func OutlineHits(outline map[string]any, title string) int {
	if outline == nil {
		return 0
	}
	core := coreSubject(title)
	if utf8.RuneCountInString(core) < 2 {
		return 0
	}
	hit := 0
	for _, item := range anyList(outline["points"]) {
		row, _ := item.(map[string]any)
		if row == nil {
			continue
		}
		if pointAddresses(textOf(row["heading"])+textOf(row["intent"]), core) {
			hit++
		}
	}
	return hit
}

// OutlineMisses reports that the outline does not cover the title.
// Zero hits is a miss. A title with no concrete subject is left alone.
func OutlineMisses(outline map[string]any, title string) bool {
	points := anyList(outline["points"])
	if outline == nil || len(points) < 2 {
		return false
	}
	if utf8.RuneCountInString(coreSubject(title)) < 2 {
		return false
	}
	hit := OutlineHits(outline, title)
	if hit == 0 {
		return true
	}
	return hit*5 < len(points)*3
}

func pointAddresses(text, core string) bool {
	if strings.Contains(text, core) {
		return true
	}
	runes := []rune(core)
	if len(runes) < 2 {
		return false
	}
	prefix := string(runes[:2])
	if prefixStop(prefix) {
		return false
	}
	return strings.Contains(text, prefix)
}
