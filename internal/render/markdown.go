package render

import (
	"strings"
)

// Markdown renders structured sections in order. A heading with no paragraph,
// list, or table is omitted. Tables stay under the section that owns them.
func Markdown(article map[string]any) string {
	if article == nil {
		return ""
	}
	var b strings.Builder
	for _, raw := range asList(article["sections"]) {
		section, _ := raw.(map[string]any)
		if section == nil {
			continue
		}
		body := sectionMarkdown(section)
		if strings.TrimSpace(body) == "" {
			continue
		}
		if heading := strings.TrimSpace(asString(section["heading"])); heading != "" {
			b.WriteString("## ")
			b.WriteString(heading)
			b.WriteString("\n\n")
		}
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteByte('\n')
		}
	}
	if closing := strings.TrimSpace(asString(article["closing"])); closing != "" {
		b.WriteString(closing)
		b.WriteByte('\n')
	}
	return b.String()
}

func sectionMarkdown(section map[string]any) string {
	var b strings.Builder
	for _, item := range asList(section["paragraphs"]) {
		text := strings.TrimSpace(asString(item))
		if text == "" {
			continue
		}
		b.WriteString(text)
		b.WriteString("\n\n")
	}
	var items []string
	for _, item := range asList(section["list"]) {
		text := strings.TrimSpace(asString(item))
		if text != "" {
			items = append(items, text)
		}
	}
	if len(items) > 0 {
		for _, item := range items {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	if table, _ := section["table"].(map[string]any); table != nil {
		if rendered := markdownTable(table); rendered != "" {
			b.WriteString(rendered)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func markdownTable(table map[string]any) string {
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
		case []string:
			cells = append(cells, row...)
		}
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
	}
	if len(headers) == 0 || len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("| ")
	b.WriteString(strings.Join(headers, " | "))
	b.WriteString(" |\n| ")
	for i := range headers {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString("---")
	}
	b.WriteString(" |\n")
	for _, row := range rows {
		b.WriteString("| ")
		for i := range headers {
			if i > 0 {
				b.WriteString(" | ")
			}
			if i < len(row) {
				b.WriteString(row[i])
			}
		}
		b.WriteString(" |\n")
	}
	return b.String()
}
