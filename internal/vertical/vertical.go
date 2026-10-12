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

// Words splits a user-entered list. Commas, Chinese commas, and newlines are separators.
func Words(raw string) []string {
	return splitWords(raw)
}

// Resolve picks the pack for one plan. An explicit key wins. Keywords are prepended to that plan's subtopics only.
func Resolve(direction string, extra store.PlanExtra, packs []store.VerticalPack) store.VerticalPack {
	pack := Match(direction, packs)
	key := strings.TrimSpace(extra.VerticalKey)
	if key != "" {
		for _, item := range packs {
			if item.Enabled && item.Key == key {
				pack = item
				break
			}
		}
	}
	words := splitWords(extra.Keywords)
	if len(words) == 0 {
		return pack
	}
	pack.Subtopics = strings.Join(append(words, splitWords(pack.Subtopics)...), ",")
	return pack
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
