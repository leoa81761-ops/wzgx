package topics

import (
	"time"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
)

type Choice struct {
	Subtopic string
	Column   string
	Angle    string
	TopicTag string
}

type Coverage struct {
	Subtopic  string
	Column    string
	Angle     string
	CreatedAt time.Time
}

func SubtopicTree(direction, tier string) []string {
	base := catalog.FreeTrees[direction]
	if base == nil {
		base = catalog.DefaultTree
	}
	items := append([]string{}, base...)
	if tier != "free" {
		items = append(items, catalog.PaidExtra[direction]...)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func Pick(direction, tier string, history []Coverage, now time.Time) Choice {
	tree := SubtopicTree(direction, tier)
	var combos [][3]string
	for _, sub := range tree {
		for _, column := range catalog.Columns {
			for _, angle := range catalog.Angles[column] {
				combos = append(combos, [3]string{sub, column, angle})
			}
		}
	}
	if len(combos) == 0 {
		return choice("入门概念", catalog.Columns[0], catalog.Angles[catalog.Columns[0]][0])
	}
	cutoff := schedule.AsUTC(now).Add(-90 * 24 * time.Hour)
	recent := map[[3]string]bool{}
	for _, item := range history {
		if !schedule.AsUTC(item.CreatedAt).Before(cutoff) {
			recent[[3]string{item.Subtopic, item.Column, item.Angle}] = true
		}
	}
	start := len(history)
	for offset := 0; offset < len(combos); offset++ {
		combo := combos[(start+offset)%len(combos)]
		if !recent[combo] {
			return choice(combo[0], combo[1], combo[2])
		}
	}
	if len(history) > 0 {
		oldest := history[0]
		oldestAt := schedule.AsUTC(oldest.CreatedAt)
		for _, item := range history[1:] {
			at := schedule.AsUTC(item.CreatedAt)
			if at.Before(oldestAt) {
				oldest = item
				oldestAt = at
			}
		}
		return choice(oldest.Subtopic, oldest.Column, oldest.Angle)
	}
	return choice(combos[0][0], combos[0][1], combos[0][2])
}

func choice(sub, column, angle string) Choice {
	return Choice{Subtopic: sub, Column: column, Angle: angle, TopicTag: sub + "|" + column + "|" + angle}
}

func ParseTag(tag string) (Choice, bool) {
	parts := split3(tag)
	if parts == nil {
		return Choice{}, false
	}
	return choice(parts[0], parts[1], parts[2]), true
}

func split3(tag string) []string {
	first := -1
	second := -1
	for i, r := range tag {
		if r == '|' {
			if first < 0 {
				first = i
			} else if second < 0 {
				second = i
			} else {
				return nil
			}
		}
	}
	if first <= 0 || second <= first+1 || second >= len(tag)-1 {
		return nil
	}
	return []string{tag[:first], tag[first+1 : second], tag[second+1:]}
}
