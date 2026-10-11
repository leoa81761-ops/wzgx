package risk

import "strings"

// 高风险词命中后不写入公众号草稿。中风险在付费档需要来源，免费档一律留在站内。
var highWords = []string{"剂量", "诊断", "偏方", "处方", "治疗方案", "包治"}

var mediumWords = []string{"喂养", "睡眠", "疫苗", "健康", "投资建议", "收益率"}

func Grade(direction string, draft map[string]any) string {
	text := flatten(draft)
	for _, word := range highWords {
		if strings.Contains(text, word) {
			return "high"
		}
	}
	if strings.Contains(direction, "母婴") {
		for _, word := range []string{"喂养", "睡眠", "疫苗", "健康"} {
			if strings.Contains(text, word) {
				return "medium"
			}
		}
	}
	for _, word := range mediumWords[4:] {
		if strings.Contains(text, word) {
			return "medium"
		}
	}
	return "low"
}

// ShouldHold 决定这篇稿要不要留在站内、不调用微信草稿接口。
func ShouldHold(level, tier string, hasSources bool) (bool, string) {
	switch level {
	case "high":
		return true, "这篇内容风险较高，没有写入公众号草稿。请在站内改掉诊断、剂量、偏方或收益承诺后再生成。"
	case "medium":
		if tier == "free" {
			return true, "免费版只自动推送低风险内容。这篇留在站内，供你自行决定。"
		}
		if !hasSources {
			return true, "中风险内容需要带来源后再写入草稿。这篇留在站内。"
		}
	}
	return false, ""
}

func flatten(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		var b strings.Builder
		for _, item := range t {
			b.WriteString(flatten(item))
			b.WriteByte('\n')
		}
		return b.String()
	case []any:
		var b strings.Builder
		for _, item := range t {
			b.WriteString(flatten(item))
			b.WriteByte('\n')
		}
		return b.String()
	default:
		return ""
	}
}
