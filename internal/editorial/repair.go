package editorial

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const reviewNote = "请发布前审阅。"

var markupTag = regexp.MustCompile(`(?s)<(h2|p|li|th|td)((?:\s[^>]*)?)>(.*?)</(?:h2|p|li|th|td)>`)

// RepairStructure fixes the draft before it is stored: duplicate FAQ headings,
// a heading glued onto its paragraph, a numbered list that starts at 2,
// table-header text copied into a list, and a table cell cut off mid-sentence.
func RepairStructure(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	draft = dedupeSectionHeadings(draft)
	draft = splitMergedHeadings(draft)
	draft = renumberBrokenLists(draft)
	draft = dropHeaderLeaks(draft)
	draft = restoreTruncatedCells(draft)
	dropEmptySections(draft)
	return StripReviewNote(draft)
}

// StripReviewNote removes the publish-review reminder from the body and footnotes.
func StripReviewNote(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	for _, key := range []string{"title", "summary", "closing"} {
		if text := textOf(draft[key]); strings.Contains(text, reviewNote) {
			draft[key] = cleanReview(text)
		}
	}
	for _, section := range sectionsOf(draft) {
		if text := textOf(section["heading"]); strings.Contains(text, reviewNote) {
			section["heading"] = cleanReview(text)
		}
		section["paragraphs"] = cleanList(anyList(section["paragraphs"]))
		if len(anyList(section["list"])) > 0 {
			section["list"] = cleanList(anyList(section["list"]))
		}
		if table, _ := section["table"].(map[string]any); table != nil {
			table["headers"] = cleanList(anyList(table["headers"]))
			var rows []any
			for _, raw := range anyList(table["rows"]) {
				switch row := raw.(type) {
				case []any:
					rows = append(rows, cleanList(row))
				case []string:
					items := make([]any, len(row))
					for i, cell := range row {
						items[i] = cell
					}
					rows = append(rows, cleanList(items))
				default:
					rows = append(rows, raw)
				}
			}
			if rows != nil {
				table["rows"] = rows
			}
		}
	}
	var notes []any
	for _, item := range anyList(draft["footnotes"]) {
		text := cleanReview(textOf(item))
		if strings.TrimSpace(text) == "" {
			continue
		}
		notes = append(notes, text)
	}
	if notes == nil {
		notes = []any{}
	}
	if _, ok := draft["footnotes"]; ok {
		draft["footnotes"] = notes
	}
	return draft
}

func cleanList(items []any) []any {
	var out []any
	for _, item := range items {
		text := cleanReview(textOf(item))
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

func cleanReview(text string) string {
	text = strings.ReplaceAll(text, reviewNote, "")
	return strings.TrimSpace(text)
}

// StripWarmFiller drops sentences that only lean on 温馨 or 默契.
func StripWarmFiller(draft map[string]any) map[string]any {
	if draft == nil {
		return nil
	}
	for _, section := range sectionsOf(draft) {
		section["paragraphs"] = dropWarm(anyList(section["paragraphs"]))
		if len(anyList(section["list"])) > 0 {
			section["list"] = dropWarm(anyList(section["list"]))
		}
	}
	if text := textOf(draft["closing"]); strings.Contains(text, "温馨") || strings.Contains(text, "默契") {
		draft["closing"] = joinKept(splitSentences(text))
	}
	return draft
}

func dropWarm(items []any) []any {
	var out []any
	for _, item := range items {
		kept := joinKept(splitSentences(textOf(item)))
		if strings.TrimSpace(kept) == "" {
			continue
		}
		out = append(out, kept)
	}
	if out == nil {
		out = []any{}
	}
	return out
}

func joinKept(parts []string) string {
	var kept []string
	for _, part := range parts {
		if strings.Contains(part, "温馨") || strings.Contains(part, "默契") {
			continue
		}
		if strings.TrimSpace(part) == "" {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, "")
}

// EnsureConcreteTips does not invent steps. A draft that still lacks concrete
// tips is held later; canned sentences were showing up in the published body.
func EnsureConcreteTips(draft map[string]any) map[string]any {
	return draft
}

func dedupeSectionHeadings(draft map[string]any) map[string]any {
	seen := map[string]int{}
	var kept []any
	for _, raw := range anyList(draft["sections"]) {
		section, _ := raw.(map[string]any)
		if section == nil {
			continue
		}
		heading := strings.TrimSpace(textOf(section["heading"]))
		if heading != "" {
			if prev, ok := seen[heading]; ok {
				mergeSection(kept[prev].(map[string]any), section)
				continue
			}
			seen[heading] = len(kept)
		}
		kept = append(kept, section)
	}
	if kept == nil {
		kept = []any{}
	}
	draft["sections"] = kept
	return draft
}

func mergeSection(dst, src map[string]any) {
	dst["paragraphs"] = append(anyList(dst["paragraphs"]), anyList(src["paragraphs"])...)
	if len(anyList(src["list"])) > 0 {
		dst["list"] = append(anyList(dst["list"]), anyList(src["list"])...)
	}
	if dst["table"] == nil && src["table"] != nil {
		dst["table"] = src["table"]
	}
}

func splitMergedHeadings(draft map[string]any) map[string]any {
	for _, section := range sectionsOf(draft) {
		heading := strings.TrimSpace(textOf(section["heading"]))
		if heading == "" {
			continue
		}
		var paras []any
		for _, item := range anyList(section["paragraphs"]) {
			text := strings.TrimSpace(textOf(item))
			if strings.HasPrefix(text, heading) && text != heading {
				text = strings.TrimSpace(strings.TrimPrefix(text, heading))
				text = strings.TrimLeft(text, "：:，, 、 ")
			}
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
	return draft
}

func renumberBrokenLists(draft map[string]any) map[string]any {
	for _, section := range sectionsOf(draft) {
		if shifted, ok := renumberOffset(anyList(section["paragraphs"])); ok {
			section["paragraphs"] = shifted
		}
		if len(anyList(section["list"])) > 0 {
			section["list"] = renumberContinuous(anyList(section["list"]))
		}
	}
	return draft
}

func dropHeaderLeaks(draft map[string]any) map[string]any {
	for _, section := range sectionsOf(draft) {
		headers := map[string]bool{}
		table, _ := section["table"].(map[string]any)
		if table != nil {
			for _, item := range anyList(table["headers"]) {
				if text := strings.TrimSpace(textOf(item)); text != "" {
					headers[text] = true
				}
			}
		}
		if len(headers) == 0 || len(anyList(section["list"])) == 0 {
			continue
		}
		var list []any
		for _, item := range anyList(section["list"]) {
			text := strings.TrimSpace(textOf(item))
			if text == "" || headers[text] || headerOnly(text, headers) {
				continue
			}
			list = append(list, text)
		}
		if list == nil {
			list = []any{}
		}
		section["list"] = list
	}
	return draft
}

func headerOnly(text string, headers map[string]bool) bool {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '　' || r == '|' || r == '、' || r == '，'
	})
	if len(fields) == 0 {
		return false
	}
	for _, field := range fields {
		if !headers[strings.TrimSpace(field)] {
			return false
		}
	}
	return true
}

func restoreTruncatedCells(draft map[string]any) map[string]any {
	blob := draftBlob(draft)
	for _, section := range sectionsOf(draft) {
		table, _ := section["table"].(map[string]any)
		if table == nil {
			continue
		}
		var rows []any
		for _, raw := range anyList(table["rows"]) {
			cells, ok := rowCells(raw)
			if !ok {
				rows = append(rows, raw)
				continue
			}
			var next []any
			empty := true
			for _, cell := range cells {
				text := strings.TrimSpace(textOf(cell))
				if truncatedCell(text) {
					if full := completeSentence(text, blob); full != "" {
						text = full
					} else {
						text = ""
					}
				}
				if text != "" {
					empty = false
				}
				next = append(next, text)
			}
			if empty {
				continue
			}
			rows = append(rows, next)
		}
		if rows == nil {
			rows = []any{}
		}
		table["rows"] = rows
	}
	return draft
}

func rowCells(raw any) ([]any, bool) {
	switch row := raw.(type) {
	case []any:
		return row, true
	case []string:
		out := make([]any, len(row))
		for i, cell := range row {
			out[i] = cell
		}
		return out, true
	default:
		return nil, false
	}
}

func truncatedCell(text string) bool {
	if utf8.RuneCountInString(text) < 6 || endsSentence(text) {
		return false
	}
	runes := []rune(text)
	switch runes[len(runes)-1] {
	case '不', '的', '是', '在', '和', '与', '把', '将', '就', '了', '着', '过', '而', '或', '到', '对':
		return true
	default:
		return false
	}
}

func endsSentence(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	switch []rune(text)[len([]rune(text))-1] {
	case '。', '！', '？', '；', '.', '!', '?':
		return true
	default:
		return false
	}
}

func completeSentence(prefix, blob string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || !strings.Contains(blob, prefix) {
		return ""
	}
	best := ""
	search := blob
	for {
		i := strings.Index(search, prefix)
		if i < 0 {
			break
		}
		rest := search[i:]
		end := -1
		for j, r := range rest {
			if j < len(prefix) {
				continue
			}
			switch r {
			case '。', '！', '？', '!', '?':
				end = j + utf8.RuneLen(r)
			}
			if end >= 0 {
				break
			}
		}
		if end > len(prefix) {
			part := strings.TrimSpace(rest[:end])
			if !strings.ContainsAny(part, "\n|") && (best == "" || utf8.RuneCountInString(part) < utf8.RuneCountInString(best)) {
				best = part
			}
		}
		if i+len(prefix) >= len(search) {
			break
		}
		search = search[i+len(prefix):]
	}
	return best
}

func draftBlob(draft map[string]any) string {
	var b strings.Builder
	for _, section := range sectionsOf(draft) {
		for _, item := range anyList(section["paragraphs"]) {
			b.WriteString(textOf(item))
			b.WriteByte('\n')
		}
		for _, item := range anyList(section["list"]) {
			b.WriteString(textOf(item))
			b.WriteByte('\n')
		}
		b.WriteString(textOf(section["heading"]))
		b.WriteByte('\n')
	}
	b.WriteString(textOf(draft["closing"]))
	return b.String()
}

// RepairMarkup applies the same structural repairs to stored HTML or Markdown.
func RepairMarkup(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	if strings.Contains(text, "<") {
		return repairHTML(text)
	}
	return repairMarkdown(text)
}

type markupNode struct {
	start int
	end   int
	tag   string
	attrs string
	text  string
	drop  bool
}

func repairHTML(text string) string {
	indexes := markupTag.FindAllStringSubmatchIndex(text, -1)
	if len(indexes) == 0 {
		return strings.ReplaceAll(text, reviewNote, "")
	}
	nodes := make([]markupNode, 0, len(indexes))
	for _, loc := range indexes {
		if len(loc) < 8 {
			continue
		}
		nodes = append(nodes, markupNode{
			start: loc[0],
			end:   loc[1],
			tag:   text[loc[2]:loc[3]],
			attrs: text[loc[4]:loc[5]],
			text:  unescapeLite(text[loc[6]:loc[7]]),
		})
	}
	repairNodes(nodes)
	var b strings.Builder
	cursor := 0
	for _, node := range nodes {
		b.WriteString(text[cursor:node.start])
		if !node.drop && strings.TrimSpace(node.text) != "" {
			b.WriteByte('<')
			b.WriteString(node.tag)
			b.WriteString(node.attrs)
			b.WriteByte('>')
			b.WriteString(escapeLite(node.text))
			b.WriteString("</")
			b.WriteString(node.tag)
			b.WriteByte('>')
		}
		cursor = node.end
	}
	b.WriteString(text[cursor:])
	return strings.ReplaceAll(b.String(), reviewNote, "")
}

func repairNodes(nodes []markupNode) {
	seenH := map[string]bool{}
	var lastH2 string
	headers := map[string]bool{}
	for i := range nodes {
		if nodes[i].tag == "th" {
			if text := strings.TrimSpace(nodes[i].text); text != "" {
				headers[text] = true
			}
		}
	}
	blob := ""
	for _, node := range nodes {
		blob += node.text + "\n"
	}
	for i := range nodes {
		nodes[i].text = cleanReview(nodes[i].text)
		if strings.TrimSpace(nodes[i].text) == "" {
			nodes[i].drop = true
			continue
		}
		switch nodes[i].tag {
		case "h2":
			if seenH[nodes[i].text] {
				nodes[i].drop = true
				continue
			}
			seenH[nodes[i].text] = true
			lastH2 = nodes[i].text
		case "p", "li":
			if lastH2 != "" && strings.HasPrefix(nodes[i].text, lastH2) && nodes[i].text != lastH2 {
				nodes[i].text = strings.TrimSpace(strings.TrimPrefix(nodes[i].text, lastH2))
				nodes[i].text = strings.TrimLeft(nodes[i].text, "：:，, 、 ")
			}
			if nodes[i].tag == "li" && (headers[strings.TrimSpace(nodes[i].text)] || headerOnly(nodes[i].text, headers)) {
				nodes[i].drop = true
			}
		case "td":
			if truncatedCell(nodes[i].text) {
				if full := completeSentence(nodes[i].text, blob); full != "" {
					nodes[i].text = full
				} else {
					nodes[i].drop = true
				}
			}
		}
		if strings.TrimSpace(nodes[i].text) == "" {
			nodes[i].drop = true
		}
	}
	renumberMarkup(nodes, "li")
	renumberMarkup(nodes, "p")
}

func renumberMarkup(nodes []markupNode, tag string) {
	var group []int
	flush := func() {
		if len(group) < 2 {
			group = nil
			return
		}
		items := make([]any, len(group))
		for i, at := range group {
			items[i] = nodes[at].text
		}
		if shifted, ok := renumberOffset(items); ok {
			for i, at := range group {
				nodes[at].text = textOf(shifted[i])
			}
		}
		group = nil
	}
	for i := range nodes {
		if nodes[i].drop || nodes[i].tag != tag || !factIndex.MatchString(strings.TrimSpace(nodes[i].text)) {
			flush()
			continue
		}
		group = append(group, i)
	}
	flush()
}

func repairMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	var headers []string
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "|") && !strings.Contains(trim, "---") && len(headers) == 0 {
			headers = splitMarkdownRow(trim)
		}
	}
	headerSet := map[string]bool{}
	for _, header := range headers {
		if header != "" {
			headerSet[header] = true
		}
	}
	blob := strings.Join(lines, "\n")
	var out []string
	seen := map[string]bool{}
	lastH := ""
	var numbered []int
	flushNumbers := func() {
		if len(numbered) < 2 {
			numbered = nil
			return
		}
		items := make([]any, len(numbered))
		for i, at := range numbered {
			items[i] = strings.TrimSpace(strings.TrimLeft(out[at], "-* "))
		}
		if shifted, ok := renumberOffset(items); ok {
			for i, at := range numbered {
				prefix := ""
				trim := strings.TrimSpace(out[at])
				if strings.HasPrefix(trim, "- ") || strings.HasPrefix(trim, "* ") {
					prefix = trim[:2]
				}
				out[at] = prefix + textOf(shifted[i])
			}
		}
		numbered = nil
	}
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		trim = cleanReview(trim)
		if trim == "" {
			flushNumbers()
			out = append(out, "")
			continue
		}
		if strings.HasPrefix(trim, "## ") {
			flushNumbers()
			heading := strings.TrimSpace(strings.TrimPrefix(trim, "## "))
			if seen[heading] {
				continue
			}
			seen[heading] = true
			lastH = heading
			out = append(out, "## "+heading)
			continue
		}
		if lastH != "" && strings.HasPrefix(trim, lastH) && trim != lastH && !strings.HasPrefix(trim, "|") {
			trim = strings.TrimSpace(strings.TrimPrefix(trim, lastH))
			trim = strings.TrimLeft(trim, "：:，, 、 ")
		}
		if strings.HasPrefix(trim, "|") && !strings.Contains(trim, "---") {
			flushNumbers()
			cells := splitMarkdownRow(trim)
			changed := false
			empty := true
			for i, cell := range cells {
				if truncatedCell(cell) {
					if full := completeSentence(cell, blob); full != "" {
						cells[i] = full
					} else {
						cells[i] = ""
					}
					changed = true
				}
				if strings.TrimSpace(cells[i]) != "" {
					empty = false
				}
			}
			if empty {
				continue
			}
			if changed {
				trim = "| " + strings.Join(cells, " | ") + " |"
			}
			out = append(out, trim)
			continue
		}
		item := strings.TrimSpace(strings.TrimLeft(trim, "-* "))
		if headerSet[item] || headerOnly(item, headerSet) {
			flushNumbers()
			continue
		}
		out = append(out, trim)
		if factIndex.MatchString(strings.TrimLeft(trim, "-* ")) {
			numbered = append(numbered, len(out)-1)
			continue
		}
		flushNumbers()
	}
	flushNumbers()
	return strings.TrimSpace(strings.ReplaceAll(strings.Join(out, "\n"), reviewNote, ""))
}

func splitMarkdownRow(line string) []string {
	line = strings.Trim(strings.TrimSpace(line), "|")
	parts := strings.Split(line, "|")
	var out []string
	for _, part := range parts {
		out = append(out, strings.TrimSpace(part))
	}
	return out
}

func escapeLite(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
}

func unescapeLite(text string) string {
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(text)
}
