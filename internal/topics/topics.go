package topics

import (
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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

// AccountTree keeps pack subtopics that match the account keywords or direction.
// A direction that contains the pack's own keywords keeps that pack, so a 弱电 account
// still rotates 综合布线. Keywords that miss the pack, or a direction outside the pack,
// derive topics from the columns, keywords, and direction instead of the pack list.
func AccountTree(packSubs []string, packKeywords, direction, keywords, columns string) []string {
	userHints := accountHints(keywords)
	if len(userHints) > 0 {
		if matched := matchSubs(packSubs, userHints); len(matched) > 0 {
			return matched
		}
		return deriveTopics(columns, keywords, direction, packSubs)
	}
	if packOnTheme(packKeywords, direction) {
		if matched := matchSubs(packSubs, accountHints(direction)); len(matched) > 0 {
			return matched
		}
		return append([]string{}, packSubs...)
	}
	if matched := matchSubs(packSubs, accountHints(direction, columns)); len(matched) > 0 {
		return matched
	}
	return deriveTopics(columns, keywords, direction, packSubs)
}

func matchSubs(packSubs []string, hints []string) []string {
	if len(hints) == 0 {
		return nil
	}
	var matched []string
	seen := map[string]bool{}
	for _, sub := range packSubs {
		sub = strings.TrimSpace(sub)
		if sub == "" || seen[sub] {
			continue
		}
		if hintMatches(sub, hints) {
			seen[sub] = true
			matched = append(matched, sub)
		}
	}
	return matched
}

func deriveTopics(columns, keywords, direction string, packSubs []string) []string {
	var derived []string
	seen := map[string]bool{}
	for _, hint := range accountHints(columns, keywords, direction) {
		if seen[hint] {
			continue
		}
		seen[hint] = true
		derived = append(derived, hint)
	}
	if len(derived) == 0 {
		return append([]string{}, packSubs...)
	}
	return derived
}

func packOnTheme(packKeywords, direction string) bool {
	direction = strings.TrimSpace(direction)
	if direction == "" {
		return false
	}
	for _, word := range accountHints(packKeywords) {
		if strings.Contains(direction, word) {
			return true
		}
	}
	return false
}

func accountHints(parts ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range parts {
		for _, hint := range splitColumns(part) {
			if len([]rune(hint)) < 2 || seen[hint] {
				continue
			}
			seen[hint] = true
			out = append(out, hint)
		}
	}
	return out
}

func hintMatches(sub string, hints []string) bool {
	for _, hint := range hints {
		if strings.Contains(sub, hint) || strings.Contains(hint, sub) {
			return true
		}
	}
	return false
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

// AssignColumn picks the article column from the account persona instead of the shared catalog list.
// 避坑辟谣 is not used unless that name is one of the persona columns.
func AssignColumn(topic Choice, columns string, history []Coverage) Choice {
	cols := splitColumns(columns)
	if len(cols) == 0 {
		return topic
	}
	topic.Column = cols[len(history)%len(cols)]
	topic.TopicTag = topic.Subtopic + "|" + topic.Column + "|" + topic.Angle
	return topic
}

func splitColumns(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == '，' || r == '、' || r == '\n' || r == ';' || r == '；'
	}) {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

// Diverge moves off a subtopic or angle already used in recent titles, topics, or hook motifs.
func Diverge(choice Choice, subs, angles, titles []string) Choice {
	if next := nextUnused(subs, choice.Subtopic, titles); next != "" {
		choice.Subtopic = next
	}
	if next := nextUnused(angles, choice.Angle, titles); next != "" {
		choice.Angle = next
	}
	choice.TopicTag = choice.Subtopic + "|" + choice.Column + "|" + choice.Angle
	return choice
}

// RepeatedMotifs returns distinctive 4-rune images already used in recent titles, such as 温茶转圈.
func RepeatedMotifs(texts []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(word string) {
		if utf8.RuneCountInString(word) != 4 || seen[word] || motifStop(word) || len(out) >= 12 {
			return
		}
		seen[word] = true
		out = append(out, word)
	}
	for _, text := range texts {
		var run []rune
		flush := func() {
			if len(run) >= 4 {
				add(string(run[:4]))
				if len(run) > 4 {
					add(string(run[len(run)-4:]))
				}
			}
			run = nil
		}
		for _, r := range text {
			if !unicode.Is(unicode.Han, r) || motifStop(string(r)) {
				flush()
				continue
			}
			run = append(run, r)
		}
		flush()
	}
	return out
}

// AngleOptions lists the current angle first, then the catalog angles in a stable order.
func AngleOptions(current string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" || seen[text] {
			return
		}
		seen[text] = true
		out = append(out, text)
	}
	add(current)
	var rest []string
	for _, list := range catalog.Angles {
		rest = append(rest, list...)
	}
	sort.Strings(rest)
	for _, item := range rest {
		add(item)
	}
	return out
}

func nextUnused(options []string, current string, titles []string) string {
	if len(options) == 0 {
		if usedInTitles(current, titles) {
			return current
		}
		return current
	}
	start := 0
	for i, item := range options {
		if item == current {
			start = i
			break
		}
	}
	for offset := 0; offset < len(options); offset++ {
		item := options[(start+offset)%len(options)]
		if !usedInTitles(item, titles) {
			return item
		}
	}
	return options[start]
}

func usedInTitles(text string, titles []string) bool {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) < 2 {
		return false
	}
	keys := contentKeys(text)
	for _, title := range titles {
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		if strings.Contains(title, text) || strings.Contains(text, title) && utf8.RuneCountInString(title) >= 4 {
			return true
		}
		if utf8.RuneCountInString(text) >= 4 && shareRunes(text, title, 4) {
			return true
		}
		if len(keys) >= 2 {
			hit := 0
			for _, key := range keys {
				if strings.Contains(title, key) {
					hit++
				}
			}
			if hit >= 2 {
				return true
			}
		}
	}
	return false
}

func contentKeys(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '，', '。', '；', '、', '！', '？', ',', '.', ';', ' ', '\n', '的', '了', '和', '与', '把', '用', '在', '是', '将', '就', '都', '而', '要', '给', '让', '从', '对', '及', '或', '：', ':':
			return true
		default:
			return false
		}
	}) {
		part = strings.TrimSpace(part)
		if utf8.RuneCountInString(part) < 2 || seen[part] || motifStop(part) {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func motifStop(text string) bool {
	for _, r := range text {
		switch r {
		case '的', '了', '在', '是', '和', '与', '及', '把', '用', '就', '都', '而', '要', '对', '从', '给', '让', '这', '那', '一', '个', '不', '也', '又', '很', '最', '会', '能', '可', '被', '向', '中', '上', '下', '来', '去', '周', '月', '日', '年', '早', '晚', '今', '昨', '你', '我', '他', '她', '它', '，', '。', ' ':
			return true
		}
	}
	return false
}

func shareRunes(a, b string, n int) bool {
	ar := []rune(a)
	br := []rune(b)
	if len(ar) < n || len(br) < n {
		return false
	}
	seen := map[string]bool{}
	for i := 0; i+n <= len(ar); i++ {
		seen[string(ar[i:i+n])] = true
	}
	for i := 0; i+n <= len(br); i++ {
		if seen[string(br[i:i+n])] {
			return true
		}
	}
	return false
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
