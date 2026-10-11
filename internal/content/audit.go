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

// BodyRunes 统计正文汉字：各小节段落、列表和结尾，不含标题与摘要。
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
		n += runesOf(section["paragraphs"])
		n += runesOf(section["list"])
	}
	n += utf8.RuneCountInString(asText(draft["closing"]))
	return n
}

func runesOf(raw any) int {
	list, _ := raw.([]any)
	n := 0
	for _, item := range list {
		n += utf8.RuneCountInString(asText(item))
	}
	return n
}

func asText(v any) string {
	s, _ := v.(string)
	return s
}

// TrimToMax 把正文压到最多 max 个字。从结尾往前删，标题和摘要不动。
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
		if cutList(section, "list", overflow) || cutList(section, "paragraphs", overflow) {
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
