package render

import (
	"html"
	"strings"
	"unicode/utf8"
)

var themes = map[string]map[string]string{
	"simple": {
		"wrap":  "font-size:16px;line-height:1.75;color:#1f2933;",
		"h2":    "font-size:18px;line-height:1.45;font-weight:700;margin:28px 0 12px;color:#1f2933;",
		"p":     "margin:0 0 14px;font-size:16px;line-height:1.75;color:#1f2933;",
		"lead":  "margin:0 0 18px;font-size:15px;line-height:1.7;color:#5c5148;",
		"ul":    "margin:0 0 16px;padding-left:1.2em;",
		"li":    "margin:0 0 8px;line-height:1.7;color:#1f2933;",
		"hr":    "border:none;border-top:1px solid #e6dccf;margin:22px 0;",
		"src_h": "font-size:15px;font-weight:700;margin:8px 0;color:#1f2933;",
		"src":   "font-size:13px;line-height:1.65;color:#6b6258;margin:0 0 8px;",
		"foot":  "font-size:13px;line-height:1.65;color:#8a8178;margin:8px 0 0;",
	},
	"fresh": {
		"wrap":  "font-size:16px;line-height:1.75;color:#24312c;",
		"h2":    "font-size:18px;line-height:1.45;font-weight:700;margin:28px 0 12px;color:#1f6a4d;",
		"p":     "margin:0 0 14px;font-size:16px;line-height:1.75;color:#24312c;",
		"lead":  "margin:0 0 18px;font-size:15px;line-height:1.7;color:#3e5c50;",
		"ul":    "margin:0 0 16px;padding-left:1.2em;",
		"li":    "margin:0 0 8px;line-height:1.7;color:#24312c;",
		"hr":    "border:none;border-top:1px solid #d5e6dc;margin:22px 0;",
		"src_h": "font-size:15px;font-weight:700;margin:8px 0;color:#1f6a4d;",
		"src":   "font-size:13px;line-height:1.65;color:#4d675c;margin:0 0 8px;",
		"foot":  "font-size:13px;line-height:1.65;color:#6d7f76;margin:8px 0 0;",
	},
	"business": {
		"wrap":  "font-size:16px;line-height:1.75;color:#1c2834;",
		"h2":    "font-size:18px;line-height:1.45;font-weight:700;margin:28px 0 12px;color:#1e3a5f;",
		"p":     "margin:0 0 14px;font-size:16px;line-height:1.75;color:#1c2834;",
		"lead":  "margin:0 0 18px;font-size:15px;line-height:1.7;color:#3d4d60;",
		"ul":    "margin:0 0 16px;padding-left:1.2em;",
		"li":    "margin:0 0 8px;line-height:1.7;color:#1c2834;",
		"hr":    "border:none;border-top:1px solid #d5deea;margin:22px 0;",
		"src_h": "font-size:15px;font-weight:700;margin:8px 0;color:#1e3a5f;",
		"src":   "font-size:13px;line-height:1.65;color:#546274;margin:0 0 8px;",
		"foot":  "font-size:13px;line-height:1.65;color:#6a7684;margin:8px 0 0;",
	},
	"card": {
		"wrap":  "font-size:16px;line-height:1.75;color:#1f2933;",
		"h2":    "font-size:17px;line-height:1.45;font-weight:700;margin:22px 0 10px;padding:8px 12px;background:#f6f1ea;color:#1f2933;",
		"p":     "margin:0 0 14px;font-size:16px;line-height:1.75;color:#1f2933;",
		"lead":  "margin:0 0 18px;font-size:15px;line-height:1.7;color:#5c5148;",
		"ul":    "margin:0 0 16px;padding-left:1.2em;",
		"li":    "margin:0 0 8px;line-height:1.7;color:#1f2933;",
		"hr":    "border:none;border-top:1px solid #e6dccf;margin:22px 0;",
		"src_h": "font-size:15px;font-weight:700;margin:8px 0;color:#1f2933;",
		"src":   "font-size:13px;line-height:1.65;color:#6b6258;margin:0 0 8px;",
		"foot":  "font-size:13px;line-height:1.65;color:#8a8178;margin:8px 0 0;",
	},
	"magazine": {
		"wrap":  "font-size:16px;line-height:1.8;color:#241c16;",
		"h2":    "font-size:20px;line-height:1.4;font-weight:700;margin:30px 0 12px;padding-left:10px;border-left:4px solid #9a3412;color:#241c16;",
		"p":     "margin:0 0 14px;font-size:16px;line-height:1.8;color:#241c16;",
		"lead":  "margin:0 0 18px;font-size:17px;line-height:1.7;color:#5c4638;",
		"ul":    "margin:0 0 16px;padding-left:1.2em;",
		"li":    "margin:0 0 8px;line-height:1.7;color:#241c16;",
		"hr":    "border:none;border-top:1px solid #eadfd4;margin:22px 0;",
		"src_h": "font-size:15px;font-weight:700;margin:8px 0;color:#9a3412;",
		"src":   "font-size:13px;line-height:1.65;color:#6b574c;margin:0 0 8px;",
		"foot":  "font-size:13px;line-height:1.65;color:#8a7368;margin:8px 0 0;",
	},
}

func DisplayURL(url string) string {
	return strings.ReplaceAll(url, ".", "点")
}

func cloneTheme(key string) map[string]string {
	src := themes[key]
	if src == nil {
		src = themes["simple"]
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func RenderHTML(article map[string]any, templateKey string, dark, compact bool) string {
	theme := cloneTheme(templateKey)
	if compact {
		theme["p"] = "margin:0 0 10px;font-size:15px;line-height:1.65;color:#1f2933;"
		theme["h2"] = "font-size:17px;font-weight:700;margin:16px 0 8px;color:#1f2933;"
	}
	if dark {
		theme["wrap"] = "font-size:16px;line-height:1.75;color:#f5f0e8;background:#1c1917;padding:12px;"
		theme["h2"] = strings.NewReplacer("#1f2933", "#e7b8a8", "#1f6a4d", "#e7b8a8", "#1e3a5f", "#e7b8a8", "#241c16", "#f5f0e8", "#9a3412", "#e7b8a8").Replace(theme["h2"])
		theme["h2"] = strings.ReplaceAll(theme["h2"], "background:#f6f1ea;", "")
		theme["p"] = "margin:0 0 14px;font-size:16px;line-height:1.75;color:#f5f0e8;"
		theme["lead"] = "margin:0 0 18px;font-size:15px;line-height:1.7;color:#d9cfc3;"
		theme["li"] = "margin:0 0 8px;line-height:1.7;color:#f5f0e8;"
		theme["hr"] = "border:none;border-top:1px solid #3a332c;margin:22px 0;"
		theme["src_h"] = "font-size:15px;font-weight:700;margin:8px 0;color:#e7b8a8;"
		theme["src"] = "font-size:13px;line-height:1.65;color:#d9cfc3;margin:0 0 8px;"
		theme["foot"] = "font-size:13px;line-height:1.65;color:#d9cfc3;margin:8px 0 0;"
	}
	var parts []string
	parts = append(parts, `<section style="`+theme["wrap"]+`">`)
	if label := strings.TrimSpace(asString(article["column_label"])); label != "" {
		parts = append(parts, `<p style="`+theme["lead"]+`">`+html.EscapeString("栏目 · "+label)+`</p>`)
	}
	for _, raw := range asList(article["sections"]) {
		section, _ := raw.(map[string]any)
		if section == nil {
			continue
		}
		if heading := strings.TrimSpace(asString(section["heading"])); heading != "" {
			parts = append(parts, `<h2 style="`+theme["h2"]+`">`+html.EscapeString(heading)+`</h2>`)
		}
		parts = append(parts, renderParagraphs(theme, section["paragraphs"])...)
		var items []string
		for _, item := range asList(section["list"]) {
			text := strings.TrimSpace(asString(item))
			if text != "" {
				items = append(items, text)
			}
		}
		if len(items) > 0 && !compact {
			var lis strings.Builder
			for _, item := range items {
				lis.WriteString(`<li style="` + theme["li"] + `">` + html.EscapeString(item) + `</li>`)
			}
			parts = append(parts, `<ul style="`+theme["ul"]+`">`+lis.String()+`</ul>`)
		}
		if table, _ := section["table"].(map[string]any); table != nil && !compact {
			if rendered := renderTable(theme, table); rendered != "" {
				parts = append(parts, rendered)
			}
		}
		if imageURL := strings.TrimSpace(asString(section["image_url"])); imageURL != "" {
			parts = append(parts, `<img src="`+html.EscapeString(imageURL)+`" alt="" style="max-width:100%;height:auto;display:block;margin:0 0 16px;" />`)
		}
	}
	if closing := strings.TrimSpace(asString(article["closing"])); closing != "" {
		for _, para := range strings.Split(closing, "\n\n") {
			para = strings.TrimSpace(para)
			if para == "" {
				continue
			}
			parts = append(parts, `<p style="`+theme["p"]+`">`+html.EscapeString(para)+`</p>`)
		}
	}
	sources := asList(article["sources"])
	if len(sources) > 0 {
		heading := strings.TrimSpace(asString(article["source_heading"]))
		if heading == "" {
			heading = "参考来源"
		}
		parts = append(parts, `<hr style="`+theme["hr"]+`" />`)
		parts = append(parts, `<p style="`+theme["src_h"]+`">`+html.EscapeString(heading)+`</p>`)
		for i, raw := range sources {
			source, _ := raw.(map[string]any)
			if source == nil {
				continue
			}
			parts = append(parts, `<p style="`+theme["src"]+`">`+html.EscapeString(sourceLine(i+1, source))+`</p>`)
		}
	}
	footnotes := asList(article["footnotes"])
	if len(footnotes) > 0 {
		parts = append(parts, `<hr style="`+theme["hr"]+`" />`)
		for _, note := range footnotes {
			parts = append(parts, `<p style="`+theme["foot"]+`">`+html.EscapeString(asString(note))+`</p>`)
		}
	}
	if aiLabelOn(article["ai_label"]) {
		parts = append(parts, `<p style="`+theme["foot"]+`">本文由人工智能辅助生成</p>`)
	}
	parts = append(parts, "</section>")
	return strings.Join(parts, "")
}

func aiLabelOn(v any) bool {
	switch n := v.(type) {
	case bool:
		return n
	case string:
		return n == "1" || n == "true"
	default:
		return false
	}
}

func RenderWithinLimit(article map[string]any, templateKey string, dark bool) (string, error) {
	htmlText := RenderHTML(article, templateKey, dark, false)
	if utf8.RuneCountInString(htmlText) <= 20000 {
		return htmlText, nil
	}
	compact := RenderHTML(article, "simple", dark, true)
	if utf8.RuneCountInString(compact) <= 20000 {
		return compact, nil
	}
	return "", errTooLong
}

type tooLong struct{}

func (tooLong) Error() string {
	return "正文超过微信草稿长度上限（2 万字符），请缩短篇幅后重试"
}

var errTooLong = tooLong{}

func renderTable(theme map[string]string, table map[string]any) string {
	var headers []string
	for _, item := range asList(table["headers"]) {
		text := strings.TrimSpace(asString(item))
		if text != "" {
			headers = append(headers, text)
		}
	}
	var rows [][]string
	for _, raw := range asList(table["rows"]) {
		var cells []string
		switch row := raw.(type) {
		case []any:
			for _, cell := range row {
				cells = append(cells, strings.TrimSpace(asString(cell)))
			}
		}
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
	}
	if len(headers) == 0 || len(rows) == 0 {
		return ""
	}
	cell := "border:1px solid #e6dccf;padding:6px 8px;font-size:14px;line-height:1.5;color:#1f2933;"
	head := cell + "font-weight:700;background:#f6f1ea;"
	var b strings.Builder
	b.WriteString(`<table style="width:100%;border-collapse:collapse;margin:0 0 16px;">`)
	b.WriteString("<tr>")
	for _, item := range headers {
		b.WriteString(`<th style="` + head + `">` + html.EscapeString(item) + `</th>`)
	}
	b.WriteString("</tr>")
	for _, row := range rows {
		b.WriteString("<tr>")
		for i := 0; i < len(headers); i++ {
			value := ""
			if i < len(row) {
				value = row[i]
			}
			b.WriteString(`<td style="` + cell + `">` + html.EscapeString(value) + `</td>`)
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</table>")
	return b.String()
}

func renderParagraphs(theme map[string]string, raw any) []string {
	var texts []string
	for _, paragraph := range asList(raw) {
		text := strings.TrimSpace(asString(paragraph))
		if text != "" {
			texts = append(texts, text)
		}
	}
	var parts []string
	for i := 0; i < len(texts); i++ {
		text := texts[i]
		if question, answer, ok := splitAnswer(text); ok {
			parts = append(parts, boldLine(theme, question), paragraphLine(theme, answer))
			continue
		}
		if isQuestionLine(text) && i+1 < len(texts) {
			if answer, ok := stripAnswerLead(texts[i+1]); ok {
				parts = append(parts, boldLine(theme, text), paragraphLine(theme, answer))
				i++
				continue
			}
		}
		parts = append(parts, paragraphLine(theme, text))
	}
	return parts
}

func boldLine(theme map[string]string, text string) string {
	return `<p style="` + theme["p"] + `"><strong>` + html.EscapeString(text) + `</strong></p>`
}

func paragraphLine(theme map[string]string, text string) string {
	return `<p style="` + theme["p"] + `">` + html.EscapeString(text) + `</p>`
}

func isQuestionLine(text string) bool {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "答") {
		return false
	}
	return strings.HasSuffix(text, "？") || strings.HasSuffix(text, "?")
}

func splitAnswer(text string) (string, string, bool) {
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
	question := strings.TrimSpace(text[:q+markLen])
	answer := strings.TrimSpace(text[answerAt+len("答"):])
	answer = strings.Trim(answer, "：: \t")
	if question == "" || answer == "" {
		return "", "", false
	}
	return question, answer, true
}

func stripAnswerLead(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !strings.Contains(text, "答") {
		return "", false
	}
	if strings.HasPrefix(text, "答") {
		text = strings.TrimSpace(strings.Trim(text[len("答"):], "：: \t"))
		return text, text != ""
	}
	return "", false
}

func InjectImage(htmlText, imageURL string) string {
	if strings.TrimSpace(imageURL) == "" {
		return htmlText
	}
	tag := `<img src="` + html.EscapeString(imageURL) + `" alt="配图" style="max-width:100%;height:auto;display:block;margin:0 0 16px;" />`
	const marker = "</p>"
	if i := strings.Index(htmlText, marker); i >= 0 {
		return htmlText[:i+len(marker)] + tag + htmlText[i+len(marker):]
	}
	return tag + htmlText
}

func sourceLine(index int, source map[string]any) string {
	if n := citeNumber(source["cite"]); n > 0 {
		index = n
	}
	name := strings.TrimSpace(asString(source["name"]))
	note := strings.TrimSpace(asString(source["note"]))
	if note == "" {
		note = strings.TrimSpace(asString(source["title"]))
	}
	if utf8.RuneCountInString(note) > 40 {
		note = string([]rune(note)[:40])
	}
	bits := []string{"[" + itoa(index) + "]"}
	if name != "" {
		bits = append(bits, name)
	}
	if note != "" && note != name {
		bits = append(bits, note)
	}
	return strings.Join(bits, " ")
}

func citeNumber(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return ""
	}
}

func asList(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case nil:
		return nil
	default:
		return nil
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
