package vertical

import (
	"strings"

	"github.com/leoa81761-ops/wzgx/internal/store"
)

func Match(direction string, packs []store.VerticalPack) store.VerticalPack {
	var general store.VerticalPack
	best := store.VerticalPack{}
	bestLen := 0
	for _, pack := range packs {
		if !pack.Enabled {
			continue
		}
		if pack.Key == "general" {
			general = pack
		}
		for _, word := range splitWords(pack.Keywords) {
			if strings.Contains(direction, word) && len([]rune(word)) > bestLen {
				best = pack
				bestLen = len([]rune(word))
			}
		}
	}
	if best.Key != "" {
		return best
	}
	if general.Key != "" {
		return general
	}
	return store.VerticalPack{Key: "general", Name: "通用", Enabled: true}
}

func Subtopics(pack store.VerticalPack) []string {
	return splitWords(pack.Subtopics)
}

func splitWords(raw string) []string {
	raw = strings.ReplaceAll(raw, "，", ",")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == '、' || r == ' ' || r == '　' }) {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
