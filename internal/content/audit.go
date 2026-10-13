package content

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
)

func EffectiveWords(extra []string) []string {
	words := append([]string{}, catalog.DefaultSensitiveWords...)
	seen := map[string]bool{}
	for _, word := range words {
		seen[word] = true
	}
	for _, word := range extra {
		cleaned := strings.TrimSpace(word)
		if cleaned == "" || seen[cleaned] {
			continue
		}
		seen[cleaned] = true
		words = append(words, cleaned)
	}
	return words
}

// BodyRunes 统计推送正文里的字：各小节小标题、段落、列表、表格单元格和结尾。
// 不含文章标题、摘要、参考来源和脚注。表格和标题会进微信正文，所以和段落一起计数。
func BodyRunes(draft map[string]any) int {
	if draft == nil {
		return 0
	}
	n := 0
	sections, _ := draft["sections"].([]any)
	for _, raw := range sections {
		section, _ := raw.(map[string]any)
		if section == nil {
			continue
		}
		n += utf8.RuneCountInString(asText(section["heading"]))
		n += runesOf(section["paragraphs"])
		n += runesOf(section["list"])
		if table, _ := section["table"].(map[string]any); table != nil {
			n += tableRunes(table)
		}
	}
	n += utf8.RuneCountInString(asText(draft["closing"]))
	return n
}

// ShortOfTarget reports a body under 85% of the plan target.
// runes and target are both Chinese characters, the same unit BodyRunes counts.
func ShortOfTarget(runes, target int) bool {
	return target > 0 && runes < target*85/100
}

func tableRunes(table map[string]any) int {
	n := runesOf(table["headers"])
	switch rows := table["rows"].(type) {
	case []any:
		for _, raw := range rows {
			n += runesOf(raw)
		}
	case []string:
		for _, cell := range rows {
			n += utf8.RuneCountInString(cell)
		}
	}
	return n
}

func runesOf(raw any) int {
	switch list := raw.(type) {
	case []any:
		n := 0
		for _, item := range list {
			n += runesOf(item)
		}
		return n
	case []string:
		n := 0
		for _, item := range list {
			n += utf8.RuneCountInString(item)
		}
		return n
	case string:
		return utf8.RuneCountInString(list)
	default:
		return utf8.RuneCountInString(asText(raw))
	}
}

func asText(v any) string {
	s, _ := v.(string)
	return s
}

// TrimToMax 把正文压到最多 max 个字。从结尾往前删，文章标题和摘要不动。
func TrimToMax(draft map[string]any, max int) map[string]any {
	if draft == nil || max <= 0 || BodyRunes(draft) <= max {
		return draft
	}
	cloned, ok := cloneDraft(draft)
	if !ok {
		return draft
	}
	for BodyRunes(cloned) > max {
		if !shortenTail(cloned, BodyRunes(cloned)-max) {
			break
		}
	}
	return cloned
}

func cloneDraft(draft map[string]any) (map[string]any, bool) {
	buf, err := json.Marshal(draft)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if json.Unmarshal(buf, &out) != nil || out == nil {
		return nil, false
	}
	return out, true
}

func shortenTail(draft map[string]any, overflow int) bool {
	if overflow < 1 {
		return false
	}
	if cutText(draft, "closing", overflow) {
		return true
	}
	sections, _ := draft["sections"].([]any)
	for i := len(sections) - 1; i >= 0; i-- {
		section, _ := sections[i].(map[string]any)
		if section == nil {
			continue
		}
		if cutTable(section, overflow) || cutList(section, "list", overflow) || cutList(section, "paragraphs", overflow) || cutText(section, "heading", overflow) {
			return true
		}
	}
	return false
}

func cutText(holder map[string]any, key string, overflow int) bool {
	text := asText(holder[key])
	if text == "" {
		return false
	}
	n := utf8.RuneCountInString(text)
	if n <= overflow {
		holder[key] = ""
		return true
	}
	holder[key] = string([]rune(text)[:n-overflow])
	return true
}

func cutTable(section map[string]any, overflow int) bool {
	table, _ := section["table"].(map[string]any)
	if table == nil || overflow < 1 {
		return false
	}
	rows, _ := table["rows"].([]any)
	for i := len(rows) - 1; i >= 0; i-- {
		row, _ := rows[i].([]any)
		for j := len(row) - 1; j >= 0; j-- {
			text := asText(row[j])
			if text == "" {
				continue
			}
			n := utf8.RuneCountInString(text)
			if n <= overflow {
				row[j] = ""
			} else {
				row[j] = string([]rune(text)[:n-overflow])
			}
			rows[i] = row
			table["rows"] = rows
			section["table"] = table
			return true
		}
	}
	if cutList(table, "headers", overflow) {
		section["table"] = table
		return true
	}
	return false
}

func cutList(section map[string]any, key string, overflow int) bool {
	list, _ := section[key].([]any)
	for i := len(list) - 1; i >= 0; i-- {
		text := asText(list[i])
		if text == "" {
			continue
		}
		n := utf8.RuneCountInString(text)
		if n <= overflow {
			list[i] = ""
		} else {
			list[i] = string([]rune(text)[:n-overflow])
		}
		section[key] = list
		return true
	}
	return false
}

func FindSensitive(payload map[string]any, words []string) []string {
	text := flatten(payload)
	var hits []string
	for _, word := range words {
		if word != "" && strings.Contains(text, word) {
			hits = append(hits, word)
		}
	}
	return hits
}

func StripCliches(value any) any {
	switch v := value.(type) {
	case string:
		for _, phrase := range catalog.Cliches {
			v = strings.ReplaceAll(v, phrase, "")
		}
		return v
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = StripCliches(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = StripCliches(item)
		}
		return out
	default:
		return value
	}
}

func flatten(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, flatten(item))
		}
		return strings.Join(parts, "\n")
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, flatten(item))
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}
