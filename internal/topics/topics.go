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

func Pick(direction, tier string, history []Coverage, now time.Time, vetoed ...string) Choice {
	return PickTree(nil, direction, tier, history, now, vetoed...)
}

// ExpandTree 按权重重复或跳过子方向。没有权重时原样返回，轮换顺序不变。
// 权重 0 少用该子方向，2 或 3 多用。缺省按 1。
func ExpandTree(tree []string, direction, tier string, weights map[string]int) []string {
	if len(weights) == 0 {
		return tree
	}
	base := tree
	if len(base) == 0 {
		base = SubtopicTree(direction, tier)
	}
	changed := false
	var out []string
	for _, sub := range base {
		weight, ok := weights[sub]
		if !ok {
			weight = 1
		}
		if weight < 0 {
			weight = 0
		}
		if weight > 3 {
			weight = 3
		}
		if weight != 1 {
			changed = true
		}
		for i := 0; i < weight; i++ {
			out = append(out, sub)
		}
	}
	if !changed {
		return tree
	}
	if len(out) == 0 {
		return base
	}
	return out
}

func PickTree(tree []string, direction, tier string, history []Coverage, now time.Time, vetoed ...string) Choice {
	blocked := map[string]bool{}
	for _, tag := range vetoed {
		if tag != "" {
			blocked[tag] = true
		}
	}
	if len(tree) == 0 {
		tree = SubtopicTree(direction, tier)
	}
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
		if recent[combo] || blocked[tagOf(combo)] {
			continue
		}
		return choice(combo[0], combo[1], combo[2])
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
		picked := choice(oldest.Subtopic, oldest.Column, oldest.Angle)
		if !blocked[picked.TopicTag] {
			return picked
		}
	}
	for _, combo := range combos {
		if !blocked[tagOf(combo)] {
			return choice(combo[0], combo[1], combo[2])
		}
	}
	return choice(combos[0][0], combos[0][1], combos[0][2])
}

func tagOf(combo [3]string) string {
	return combo[0] + "|" + combo[1] + "|" + combo[2]
}

// Slot 是内容日历上的一次预计生成，不写 topic_coverage。
type Slot struct {
	At     time.Time
	Choice Choice
}

func Project(direction, tier, scheduleType string, weekdays []int, window string, history []Coverage, start time.Time, count int, vetoed []string) ([]Slot, error) {
	return ProjectTree(nil, direction, tier, scheduleType, weekdays, window, history, start, count, vetoed)
}

func ProjectTree(tree []string, direction, tier, scheduleType string, weekdays []int, window string, history []Coverage, start time.Time, count int, vetoed []string) ([]Slot, error) {
	if count < 1 {
		count = 8
	}
	if count > 30 {
		count = 30
	}
	hist := append([]Coverage{}, history...)
	cursor := start
	var out []Slot
	for len(out) < count {
		at, err := schedule.ComputeNextRun(cursor, scheduleType, weekdays, window, schedule.ZeroRNG{})
		if err != nil {
			if len(out) > 0 {
				return out, nil
			}
			return nil, err
		}
		picked := PickTree(tree, direction, tier, hist, at, vetoed...)
		out = append(out, Slot{At: at, Choice: picked})
		hist = append(hist, Coverage{Subtopic: picked.Subtopic, Column: picked.Column, Angle: picked.Angle, CreatedAt: at})
		end, err := schedule.WindowEnd(schedule.ShanghaiDay(at), window)
		if err != nil {
			return out, nil
		}
		cursor = end
	}
	return out, nil
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
