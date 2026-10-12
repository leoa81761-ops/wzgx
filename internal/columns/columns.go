package columns

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/schedule"
)

// Column is one sub-column on a bound WeChat account.
type Column struct {
	ID          int64  `json:"id"`
	AccountID   int64  `json:"wx_account_id"`
	Name        string `json:"name"`
	Positioning string `json:"positioning"`
	Structure   string `json:"structure"`
	WordTarget  int    `json:"word_target"`
	ImageStyle  string `json:"image_style"`
	Weight      int    `json:"weight"`
	SortOrder   int    `json:"sort_order"`
	Status      string `json:"status"`
}

// Suggest builds columns the user can confirm after binding.
// Names come from the vertical, the saved persona columns, and titles already used on the account.
func Suggest(direction, verticalKey, personaColumns string, history []string) []Column {
	var out []Column
	seen := map[string]bool{}
	add := func(col Column) {
		col.Name = strings.TrimSpace(col.Name)
		if col.Name == "" || seen[col.Name] {
			return
		}
		seen[col.Name] = true
		if col.Structure == "" {
			col.Structure = "custom"
		}
		col.Structure = NormalizeStructure(col.Structure)
		if col.WordTarget < 1 {
			col.WordTarget = 800
		}
		if col.Weight < 1 {
			col.Weight = 1
		}
		if col.ImageStyle == "" {
			col.ImageStyle = "简洁配图"
		}
		col.Status = "suggested"
		col.SortOrder = len(out)
		out = append(out, col)
	}
	for _, col := range preset(direction, verticalKey) {
		add(col)
	}
	for _, name := range splitNames(personaColumns) {
		add(Column{Name: name, Positioning: name + "这个栏目自己的定位", Structure: "custom"})
	}
	for _, name := range history {
		add(Column{Name: name, Positioning: "沿用这个号以前写过的栏目", Structure: "custom"})
	}
	if len(out) == 0 {
		add(Column{Name: "这件事", Positioning: "把一件具体的事讲清楚", Structure: "custom"})
		add(Column{Name: "一个做法", Positioning: "把做法拆成步骤", Structure: "steps"})
		add(Column{Name: "读者来问", Positioning: "回答一个具体问题", Structure: "qa"})
	}
	return out
}

func preset(direction, verticalKey string) []Column {
	text := direction + verticalKey
	switch {
	case strings.Contains(text, "弱电") || strings.Contains(text, "智能化") || strings.Contains(text, "工程") || verticalKey == "elv":
		return []Column{
			{Name: "工程圈快报", Positioning: "用几条行业消息讲清和读者的关系", Structure: "news", WordTarget: 800, ImageStyle: "现场照片"},
			{Name: "方案拆解", Positioning: "把一个方案拆成可以照着做的步骤", Structure: "steps", WordTarget: 1200, ImageStyle: "示意图"},
			{Name: "知识FAQ", Positioning: "用问答把一个具体问题讲清楚", Structure: "qa", WordTarget: 800, ImageStyle: "简洁配图"},
		}
	case strings.Contains(text, "家庭") || strings.Contains(text, "亲子") || strings.Contains(text, "母婴") || verticalKey == "family":
		return []Column{
			{Name: "家里新鲜事", Positioning: "用家里的一件小事开头", Structure: "news", WordTarget: 800, ImageStyle: "生活场景"},
			{Name: "一步一步做", Positioning: "把家务或带娃拆成步骤", Structure: "steps", WordTarget: 1000, ImageStyle: "步骤图"},
			{Name: "读者来问", Positioning: "回答一个家庭里的具体问题", Structure: "qa", WordTarget: 800, ImageStyle: "简洁配图"},
		}
	case strings.Contains(text, "职场") || strings.Contains(text, "公司") || strings.Contains(text, "管理"):
		return []Column{
			{Name: "会后复盘", Positioning: "把一次会议里的决定写清楚", Structure: "steps", WordTarget: 800, ImageStyle: "简洁配图"},
			{Name: "邮件怎么写", Positioning: "用一封邮件的场景讲写法", Structure: "custom", WordTarget: 800, ImageStyle: "简洁配图"},
			{Name: "读者来问", Positioning: "回答一个工作里的具体问题", Structure: "qa", WordTarget: 800, ImageStyle: "简洁配图"},
		}
	default:
		return nil
	}
}

// Topics are the only subtopics for an active column: its name plus the account theme.
// Fixed pack lists such as 亲子阅读 are not included.
func Topics(name, positioning, direction, keywords string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(text string) {
		text = strings.TrimSpace(text)
		n := utf8.RuneCountInString(text)
		if n < 2 || n > 16 || seen[text] {
			return
		}
		seen[text] = true
		out = append(out, text)
	}
	add(name)
	add(positioning)
	add(direction)
	for _, word := range splitNames(keywords) {
		add(word)
	}
	if len(out) == 0 && strings.TrimSpace(name) != "" {
		runes := []rune(strings.TrimSpace(name))
		if len(runes) > 16 {
			runes = runes[:16]
		}
		out = append(out, string(runes))
	}
	return out
}

// StructureText is the outline instruction for a column template.
func StructureText(structure, positioning string) string {
	var lead string
	switch NormalizeStructure(structure) {
	case "news":
		lead = "结构：新闻快报。开头用一条消息，中间两三条事实，每条一句话，结尾留一句和读者有关的判断。"
	case "steps":
		lead = "结构：步骤拆解。开头点出要拆的方案，中间按顺序写步骤，结尾留下一步。"
	case "qa":
		lead = "结构：问答。开头是一个具体问题，中间用问答讲清楚，结尾把答案收成一句。"
	default:
		lead = "结构：按这个栏目自己的定位来写，不要套用别的栏目。"
	}
	if text := strings.TrimSpace(positioning); text != "" {
		lead += "栏目定位：" + text + "。"
	}
	return lead
}

// NormalizeStructure maps the editor labels onto news, steps, qa, or custom.
func NormalizeStructure(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "news", "news-brief", "快报":
		return "news"
	case "steps", "step-by-step", "breakdown", "拆解":
		return "steps"
	case "qa", "q&a", "faq", "问答":
		return "qa"
	default:
		return "custom"
	}
}

// Pick chooses the column for one run. Weekday mode uses the plan map, then weight rotation.
func Pick(active []Column, mode string, weekday int, days map[int]int64, recent []string) (Column, bool) {
	var pool []Column
	for _, col := range active {
		if col.Status != "" && col.Status != "active" {
			continue
		}
		if strings.TrimSpace(col.Name) == "" {
			continue
		}
		pool = append(pool, col)
	}
	if len(pool) == 0 {
		return Column{}, false
	}
	if mode == "weekday" {
		if id := days[weekday]; id > 0 {
			for _, col := range pool {
				if col.ID == id {
					return col, true
				}
			}
		}
	}
	var bag []Column
	for _, col := range pool {
		weight := col.Weight
		if weight < 1 {
			weight = 1
		}
		if weight > 3 {
			weight = 3
		}
		for i := 0; i < weight; i++ {
			bag = append(bag, col)
		}
	}
	last := ""
	if len(recent) > 0 {
		last = recent[len(recent)-1]
	}
	start := len(recent)
	for offset := 0; offset < len(bag); offset++ {
		col := bag[(start+offset)%len(bag)]
		if col.Name != last || len(pool) == 1 {
			return col, true
		}
	}
	return bag[0], true
}

// ParseDays reads a weekday-to-column-id map. Keys are ISO weekdays 1 to 7.
func ParseDays(raw string) map[int]int64 {
	out := map[int]int64{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(raw), &decoded) != nil {
		return out
	}
	for key, value := range decoded {
		day, err := strconv.Atoi(key)
		if err != nil || day < 1 || day > 7 {
			continue
		}
		id := int64(0)
		switch n := value.(type) {
		case float64:
			id = int64(n)
		case json.Number:
			id, _ = n.Int64()
		}
		if id > 0 {
			out[day] = id
		}
	}
	return out
}

// EncodeDays stores a weekday map as JSON.
func EncodeDays(days map[int]int64) string {
	if len(days) == 0 {
		return ""
	}
	raw := map[string]int64{}
	for day, id := range days {
		if day < 1 || day > 7 || id < 1 {
			continue
		}
		raw[strconv.Itoa(day)] = id
	}
	if len(raw) == 0 {
		return ""
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	return string(buf)
}

// WeekdayOf is the ISO weekday in Shanghai, Monday 1 through Sunday 7.
func WeekdayOf(t time.Time) int {
	return schedule.ISOWeekday(t)
}

// splitNames splits commas and newlines the same way persona columns are stored.
func splitNames(text string) []string {
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
