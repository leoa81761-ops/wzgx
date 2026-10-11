package content

import (
	"strings"

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
