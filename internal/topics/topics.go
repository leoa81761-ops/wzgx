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

// SameSubject reports that a new title is the same matter as a recent title or topic,
// including a wording change such as 环状 for 环形.
func SameSubject(title string, recent []string) bool {
	keys := subjectKeys(foldSubject(title))
	if len(keys) == 0 {
		return false
	}
	for _, old := range recent {
		folded := foldSubject(old)
		hits := 0
		seen := map[string]bool{}
		for _, key := range keys {
			if seen[key] || !strings.Contains(folded, key) {
				continue
			}
			seen[key] = true
			hits++
			if hits >= 2 {
				return true
			}
		}
	}
	return false
}

// SubjectHits returns distinctive words from one recent title that the body still uses.
func SubjectHits(body string, recent []string) []string {
	body = foldSubject(body)
	var out []string
	seen := map[string]bool{}
	for _, old := range recent {
		keys := subjectKeys(foldSubject(old))
		var hit []string
		for _, key := range keys {
			if strings.Contains(body, key) {
				hit = append(hit, key)
			}
		}
		if len(hit) < 2 {
			continue
		}
		for _, key := range hit {
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, key)
			if len(out) >= 8 {
				return out
			}
		}
	}
	return out
}

func foldSubject(text string) string {
	replacer := strings.NewReplacer("环状", "环形", "环型", "环形", "星状", "星形", "星型", "星形")
	return replacer.Replace(text)
}

func subjectKeys(text string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(term string) {
		if term == "" || seen[term] || boilerplateKey(term) {
			return
		}
		seen[term] = true
		out = append(out, term)
	}
	var buf []rune
	flush := func() {
		if len(buf) >= 2 && len(buf) <= 8 {
			add(string(buf))
		}
		if len(buf) >= 2 {
			add(string(buf[:2]))
			if len(buf) > 2 {
				add(string(buf[len(buf)-2:]))
			}
		}
		if len(buf) >= 4 {
			for i := 0; i+4 <= len(buf); i++ {
				add(string(buf[i : i+4]))
			}
		}
		buf = nil
	}
	for _, r := range text {
		if unicode.Is(unicode.Han, r) && !motifStop(string(r)) {
			buf = append(buf, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func boilerplateKey(text string) bool {
	switch text {
	case "今天", "就能", "今天就能", "就能做", "今天就能做", "一个做法", "小做法", "实用方法", "这件事", "怎么做", "怎么办", "是什么", "为什么", "系统", "工程", "方案", "问题", "文章", "说明", "正文", "合适", "栏目", "读者", "做法", "步骤", "标准", "设计", "建设", "管理", "工作", "要求", "文件", "公告", "发布", "规范", "技术", "国家", "关于", "这个", "没有", "不是", "可以", "进行", "通过":
		return true
	default:
		return false
	}
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
	if !ComparisonFrame(current) {
		add(current)
	}
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

// ComparisonFrame reports the "两种做法" angle that was being forced onto every column.
func ComparisonFrame(text string) bool {
	text = strings.TrimSpace(text)
	return strings.Contains(text, "两种做法") || strings.Contains(text, "对比两种")
}

// AnglesFor returns angles for this column and this account.
// Shared frames such as 先看失败点 are not reused across accounts.
func AnglesFor(column, positioning, structure, current string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" || seen[text] || GenericAngle(text) || ComparisonFrame(text) {
			return
		}
		seen[text] = true
		out = append(out, text)
	}
	add(ReaderPain(column, positioning, structure, column+" "+positioning, "", "", ""))
	add(current)
	if list := columnAngles(column, positioning, structure); len(list) > 0 {
		for _, item := range list {
			add(item)
		}
	}
	if len(out) == 0 {
		out = append(out, ReaderPain(column, positioning, structure, column+" "+positioning, "", "", ""))
	}
	return out
}

// GenericAngle reports an angle that could be pasted onto any account.
func GenericAngle(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || ComparisonFrame(text) {
		return true
	}
	for _, phrase := range genericAngles {
		if text == phrase || strings.HasSuffix(text, "："+phrase) || strings.HasSuffix(text, ":"+phrase) || strings.Contains(text, phrase) && abstractPhrase(phrase) {
			return true
		}
	}
	return false
}

func abstractPhrase(phrase string) bool {
	switch phrase {
	case "先看失败点", "先把手边这一件说完", "先把手边这一件做完", "先把手边这一处做完", "先把手边", "这一件说完", "这一件做完", "那一个具体麻烦", "晚饭后的那一会儿":
		return true
	default:
		return false
	}
}

var genericAngles = []string{
	"先看失败点", "失败点", "把失败原因写具体", "更稳妥的做法", "一个小做法", "一个具体角度",
	"今天就能做", "先看问题", "一个做法", "先讲结论", "从一个日常场景说起", "把一个具体问题讲清楚",
	"三件今天就能做的小事", "新手最容易漏的步骤", "按顺序的一周小计划", "先把能做的一步写清楚",
	"一个听起来很对的说法", "为什么会有这种误解",
	"先把手边这一件说完", "先把手边这一件做完", "先把手边这一处做完", "先把手边",
	"这一件说完", "这一件做完", "那一个具体麻烦", "晚饭后的那一会儿",
	"从一件家里的小事说起", "讲一个能照做的家庭提醒", "先讲家里的场景再给一个做法",
}

// ReaderPain is one concrete scene for this column, this account, and this reader.
func ReaderPain(column, positioning, structure, theme, tone, signatures, name string) string {
	blob := name + column + positioning + structure + theme + tone + signatures
	object := subjectTerm(theme + " " + name)
	if object == "" || object == strings.TrimSpace(name) || genericTheme(object) {
		object = subjectTerm(positioning)
	}
	if object == "" || object == strings.TrimSpace(name) || genericTheme(object) || instructionalTerm(object) {
		object = strings.TrimSpace(column)
	}
	if object == "" {
		object = "这件事"
	}
	object = concreteObject(blob, object)
	// A detail has to already be in the topic. Do not invent a stove or “还是黏”.
	if object == "" || genericTheme(object) || strings.Contains(object, "奶奶") {
		object = strings.TrimSpace(column)
	}
	if object == "" {
		object = "这件事"
	}
	switch {
	case strings.Contains(blob, "奶奶"):
		return "奶奶先把" + object + "做完"
	case familyColumn(blob):
		return "家里的" + object + "还没收拾完"
	case faqColumn(blob):
		if object == "" {
			object = "这一处接口"
		}
		return object + "对不上号，下一步该量哪一段"
	case strings.Contains(blob, "拆解") || strings.Contains(strings.ToLower(blob), "steps"):
		if object == "" {
			object = "这段线"
		}
		lead := object
		if strings.Contains(blob, "弱电") && !strings.Contains(object, "弱电") {
			lead = "弱电" + object
		}
		return lead + "测到一半对不上，不知道先查哪一段"
	default:
		if object == "" {
			object = "手头这个件"
		}
		return object + "做到一半对不上，不知道先查哪一段"
	}
}

func concreteObject(blob, object string) string {
	switch {
	case strings.Contains(blob, "灶") || strings.Contains(blob, "做饭") || strings.Contains(blob, "炒"):
		return "灶台"
	case strings.Contains(blob, "灰尘") || strings.Contains(blob, "除尘"):
		return "桌面积灰"
	case strings.Contains(blob, "花盆") || strings.Contains(blob, "绿植") || strings.Contains(blob, "浇花"):
		return "花盆"
	case strings.Contains(blob, "布线"):
		return "综合布线"
	case strings.Contains(blob, "监控"):
		return "监控画面"
	}
	if object == "" || genericTheme(object) || abstractObject(object) {
		return ""
	}
	return object
}

func abstractObject(text string) bool {
	for _, word := range []string{"手边", "这一件", "这一处", "家务", "家庭", "日子", "一天", "晚饭", "麻烦", "具体", "失败"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

func columnAngles(column, positioning, structure string) []string {
	blob := column + positioning + structure
	switch {
	case familyColumn(blob):
		return []string{"从一件家里的小事说起", "讲一个能照做的家庭提醒", "先讲家里的场景再给一个做法"}
	case faqColumn(blob):
		return []string{"问一个具体问题", "把现场会卡住的那一步问清楚", "用三问把这件事讲完"}
	default:
		return nil
	}
}

func faqColumn(blob string) bool {
	lower := strings.ToLower(blob)
	return strings.Contains(lower, "faq") || strings.Contains(blob, "问答") || strings.HasSuffix(lower, "qa")
}

func familyColumn(blob string) bool {
	return strings.Contains(blob, "家长里短") || strings.Contains(blob, "家里") || strings.Contains(blob, "家人") || strings.Contains(blob, "家庭")
}

// FitTitle keeps a title that matches the column and the account theme.
// A comparison frame, or a title such as 代理机构信息登记 on a 弱电 account, is replaced.
func FitTitle(title, column, positioning, structure, theme string) string {
	title = strings.TrimSpace(title)
	if title != "" && !ComparisonFrame(title) && !VagueTitle(title) && onTheme(title, theme) && matchesColumn(title, column, positioning, structure) {
		return title
	}
	return ColumnTopic(column, positioning, structure, theme)
}

// ColumnTopic is the fallback title for one column.
func ColumnTopic(column, positioning, structure, theme string) string {
	blob := column + positioning + structure
	entity := longestThemeTerm(theme)
	if entity == "" {
		entity = strings.TrimSpace(column)
	}
	if entity == "" {
		entity = "这件事"
	}
	switch {
	case familyColumn(blob):
		lead := entity
		if !strings.Contains(lead, "家") {
			lead = "家里"
		}
		return "晚饭后" + lead + "的老人先走这一段，家人把拐杖放在顺手的位置"
	case faqColumn(blob):
		return entity + "这一步，先看哪里？"
	default:
		subject := specificSubject(theme)
		if subject == "" || ComparisonFrame(subject) || VagueTitle(subject) {
			subject = entity
		}
		if subject == "" || ComparisonFrame(subject) || VagueTitle(subject) {
			subject = "这件事"
		}
		return subject
	}
}

// VagueTitle reports a column label plus an empty angle, such as 施工坑点：更稳妥的做法.
// A title that still names the subject, such as 综合布线六类线测试失败常见原因, is not vague.
func VagueTitle(title string) bool {
	title = strings.TrimSpace(title)
	if title == "" {
		return true
	}
	cleaned := title
	for _, phrase := range append([]string{"施工坑点", "先看失败点"}, genericAngles...) {
		cleaned = strings.ReplaceAll(cleaned, phrase, "")
	}
	cleaned = strings.Trim(cleaned, "：:，, 、？? ")
	return utf8.RuneCountInString(cleaned) < 4
}

func specificSubject(theme string) string {
	parts := themeTerms(theme)
	if len(parts) == 0 {
		return ""
	}
	for i := 0; i < len(parts); i++ {
		for j := i + 1; j < len(parts); j++ {
			if utf8.RuneCountInString(parts[j]) > utf8.RuneCountInString(parts[i]) {
				parts[i], parts[j] = parts[j], parts[i]
			}
		}
	}
	var b strings.Builder
	for _, part := range parts {
		if utf8.RuneCountInString(b.String()+part) > 24 {
			break
		}
		b.WriteString(part)
		if utf8.RuneCountInString(b.String()) >= 12 {
			break
		}
	}
	return b.String()
}

func matchesColumn(title, column, positioning, structure string) bool {
	blob := column + positioning + structure
	switch {
	case familyColumn(blob):
		return familyScene(title)
	case faqColumn(blob):
		return strings.Contains(title, "？") || strings.Contains(title, "?")
	default:
		return true
	}
}

func familyScene(title string) bool {
	hasFamily := false
	for _, word := range []string{"家", "老人", "孩子", "爸", "妈", "爷", "奶", "娃"} {
		if strings.Contains(title, word) {
			hasFamily = true
			break
		}
	}
	if !hasFamily {
		return false
	}
	for _, word := range []string{"把", "先", "用", "放", "打开", "关上", "量", "擦", "挪"} {
		if strings.Contains(title, word) {
			return true
		}
	}
	return false
}

func onTheme(title, theme string) bool {
	terms := themeTerms(theme)
	if len(terms) == 0 {
		return true
	}
	for _, term := range terms {
		if strings.Contains(title, term) {
			return true
		}
	}
	return false
}

func longestThemeTerm(theme string) string {
	best := ""
	for _, term := range themeTerms(theme) {
		if utf8.RuneCountInString(term) > utf8.RuneCountInString(best) {
			best = term
		}
	}
	return best
}

func themeTerms(theme string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(theme, func(r rune) bool {
		switch r {
		case ' ', '，', '。', '、', '\n', '；', ';', ',', '：', ':', '？', '?', '！', '的', '了', '和', '与', '把', '用':
			return true
		default:
			return false
		}
	}) {
		part = strings.TrimSpace(part)
		n := utf8.RuneCountInString(part)
		if n < 2 || n > 12 || seen[part] || genericTheme(part) || ComparisonFrame(part) {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func subjectTerm(text string) string {
	best := ""
	for _, term := range themeTerms(text) {
		if instructionalTerm(term) {
			continue
		}
		if utf8.RuneCountInString(term) > utf8.RuneCountInString(best) {
			best = term
		}
	}
	return best
}

func instructionalTerm(text string) bool {
	for _, word := range []string{"步骤", "照着", "拆成", "讲清", "可以", "定位", "栏目"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

func genericTheme(text string) bool {
	switch text {
	case "知识", "问题", "具体", "问答", "栏目", "定位", "分享", "经验", "一个", "做法", "今天", "常见", "怎么", "如何", "读者", "自己", "什么", "进行", "通过", "可以", "不要", "我们", "这个", "重点", "几条", "清楚", "讲清", "以及", "或者":
		return true
	default:
		return false
	}
}
