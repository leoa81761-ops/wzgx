package setup

import (
	"strings"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/persona"
	"github.com/leoa81761-ops/wzgx/internal/store"
	"github.com/leoa81761-ops/wzgx/internal/vertical"
)

// Suggestion is the starting point for one WeChat account. The caller can still edit every field.
type Suggestion struct {
	Direction   string
	StyleKey    string
	TemplateKey string
	TimeWindow  string
	VerticalKey string
	Audience    string
	Keywords    string
	Tone        string
	Columns     string
	Signatures  string
	Taboos      string
	WordTarget  int
	TopicMode   string
	ImageMode   string
	Reason      string
}

// Input is what the first-run guide knows before the user edits anything.
type Input struct {
	AccountName string
	SubjectType string
	ServiceType string
	Verified    bool
	Direction   string
	Tier        string
	MaxWords    int
	Packs       []store.VerticalPack
}

// Suggest picks a direction, voice, and schedule from the account type and the vertical implied by the name.
func Suggest(in Input) Suggestion {
	name := strings.TrimSpace(in.AccountName)
	direction := clipRunes(strings.TrimSpace(in.Direction), 40)
	pack := vertical.Match(name+" "+direction, in.Packs)
	if direction == "" {
		direction = inferDirection(name, in.SubjectType, in.ServiceType, pack)
	}
	style := "warm"
	template := "fresh"
	window := "evening"
	audience := "想把事情慢慢做好的读者"
	enterprise := in.SubjectType == "enterprise" || in.Verified || in.ServiceType == "service"
	if enterprise {
		style = "formal"
		template = "business"
		window = "morning"
		audience = "这个行业里的从业者"
	}
	switch pack.Key {
	case "family":
		style = "warm"
		template = "fresh"
		audience = "在家过日子的读者"
		if !enterprise {
			window = "evening"
		}
	case "elv":
		style = "plain"
		template = "simple"
		audience = "做工程和物业的读者"
		window = "morning"
	case "tech":
		style = "casual"
		template = "simple"
		audience = "想把工具用顺手的读者"
	}
	if in.Tier == "free" {
		window = "night"
	}
	fallback := "warm"
	if pack.Key == "elv" {
		fallback = "formal"
	}
	style = allowedStyle(in.Tier, style, fallback)
	template = allowedTemplate(in.Tier, template)
	verticalKey := ""
	keywords := ""
	if pack.Key != "" && pack.Key != "general" {
		verticalKey = pack.Key
		keywords = clipRunes(keywordList(pack), 80)
	}
	reason := explain(name, in.SubjectType, in.ServiceType, in.Tier, direction, style, window, pack)
	voice := persona.Suggest(direction, verticalKey)
	return Suggestion{
		Direction: direction, StyleKey: style, TemplateKey: template, TimeWindow: window,
		VerticalKey: verticalKey, Audience: audience, Keywords: keywords,
		Tone: voice.Tone, Columns: voice.Columns, Signatures: voice.Signatures, Taboos: voice.Taboos,
		WordTarget: pickWords(in.MaxWords), TopicMode: "auto", ImageMode: "auto", Reason: reason,
	}
}

// Sample is a 200-rune preview. It does not call a model and does not use the daily trial allowance.
func Sample(direction, styleName string) string {
	direction = strings.TrimSpace(direction)
	if direction == "" {
		direction = "这个方向"
	}
	styleName = strings.TrimSpace(styleName)
	if styleName == "" {
		styleName = "温暖亲和"
	}
	lead := "这是「" + direction + "」的一段" + styleName + "试读。"
	rest := "先从一个很小的场景说起，再给出今天就能做的一步，最后提醒一个容易忽略的地方。把句子长短错开，少用空话，把读者能立刻执行的动作写清楚。"
	text := lead
	for utf8.RuneCountInString(text) < 200 {
		text += rest
	}
	return clipRunes(text, 200)
}

func inferDirection(name, subject, service string, pack store.VerticalPack) string {
	switch pack.Key {
	case "elv":
		return "弱电智能化"
	case "tech":
		return "数码工具"
	case "family":
		if strings.Contains(name, "母婴") || strings.Contains(name, "亲子") {
			return "母婴"
		}
		return "美食日常"
	default:
		if subject == "enterprise" || service == "service" {
			return "职场成长"
		}
		return "个人成长"
	}
}

func keywordList(pack store.VerticalPack) string {
	words := vertical.Words(pack.Keywords)
	if len(words) == 0 {
		words = vertical.Words(pack.Subtopics)
	}
	if len(words) > 4 {
		words = words[:4]
	}
	return strings.Join(words, ",")
}

func explain(name, subject, service, tier, direction, style, window string, pack store.VerticalPack) string {
	who := "个人订阅号"
	if subject == "enterprise" {
		who = "企业号"
	} else if service == "service" {
		who = "服务号"
	}
	parts := []string{"这是" + who}
	if pack.Key != "" && pack.Key != "general" && strings.TrimSpace(name) != "" {
		parts = append(parts, "号名贴近「"+pack.Name+"」")
	}
	styleName := catalog.StyleByKey(style).Name
	if styleName == "" {
		styleName = style
	}
	parts = append(parts, "建议写「"+direction+"」，风格用"+styleName)
	if tier == "free" {
		parts = append(parts, "免费版在夜间低峰生成")
	} else if window == "morning" {
		parts = append(parts, "放在上午生成")
	} else if window == "evening" {
		parts = append(parts, "放在晚间生成")
	}
	return strings.Join(parts, "，") + "。"
}

func pickWords(max int) int {
	if max <= 0 {
		max = 800
	}
	for _, n := range []int{1500, 1000, 800} {
		if n <= max && catalog.WordAllowed(n) {
			return n
		}
	}
	return 800
}

func allowedStyle(tier, key, fallback string) string {
	if styleOK(tier, key) {
		return key
	}
	if styleOK(tier, fallback) {
		return fallback
	}
	return "warm"
}

func styleOK(tier, key string) bool {
	row := catalog.StyleByKey(key)
	return row.Key != "" && tierRank(tier) >= tierRank(row.MinTier)
}

func allowedTemplate(tier, key string) string {
	row := catalog.TemplateByKey(key)
	if row.Key != "" && tierRank(tier) >= tierRank(row.MinTier) {
		return row.Key
	}
	return "simple"
}

func tierRank(tier string) int {
	switch tier {
	case "standard":
		return 1
	case "premium":
		return 2
	case "lifetime":
		return 3
	default:
		return 0
	}
}

func clipRunes(text string, n int) string {
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	return string([]rune(text)[:n])
}
