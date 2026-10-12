package persona

import "strings"

// Profile is one account's positioning. Empty stored fields fall back to Suggest at generation time.
type Profile struct {
	Audience   string
	Tone       string
	Columns    string
	Signatures string
	Taboos     string
}

// Suggest builds a starting persona from the writing direction and vertical pack.
// The caller keeps any field the user already edited.
func Suggest(direction, verticalKey string) Profile {
	text := strings.TrimSpace(direction)
	key := strings.TrimSpace(verticalKey)
	switch {
	case key == "elv" || containsAny(text, "弱电", "智能化", "工程", "物业", "布线"):
		return Profile{
			Audience:   "做工程和物业的读者",
			Tone:       "务实，少形容词",
			Columns:    "现场笔记,验收清单",
			Signatures: "先看现场，再谈方案",
			Taboos:     "保证中标,包过验收",
		}
	case key == "family" || containsAny(text, "母婴", "亲子", "美食", "带娃"):
		return Profile{
			Audience:   "在家过日子的读者",
			Tone:       "像家里人说话",
			Columns:    "一日三餐,带娃记",
			Signatures: "先把今天过好",
			Taboos:     "恐吓式育儿,绝对化健康承诺",
		}
	case key == "tech" || containsAny(text, "数码", "工具", "科技", "软件"):
		return Profile{
			Audience:   "想把工具用顺手的读者",
			Tone:       "直接，可操作",
			Columns:    "工具实测,少踩坑",
			Signatures: "先说怎么用",
			Taboos:     "稳赚,内部消息",
		}
	case containsAny(text, "职场", "公司", "管理"):
		return Profile{
			Audience:   "这个行业里的从业者",
			Tone:       "克制，具体",
			Columns:    "会后复盘,邮件怎么写",
			Signatures: "先把事做完",
			Taboos:     "成功学,月入百万",
		}
	default:
		return Profile{
			Audience:   "想把事情慢慢做好的读者",
			Tone:       "清楚，具体",
			Columns:    "这件事,一个做法",
			Signatures: "先说结论",
			Taboos:     "震惊,不转不是",
		}
	}
}

// Effective resolves one article's voice. A plan field wins, then the account, then the suggestion.
func Effective(plan, account, suggested Profile) Profile {
	return Fill(Fill(plan, account), suggested)
}

// Fill copies suggestion into blank fields and leaves edited text alone.
func Fill(stored, suggested Profile) Profile {
	out := stored
	if strings.TrimSpace(out.Audience) == "" {
		out.Audience = suggested.Audience
	}
	if strings.TrimSpace(out.Tone) == "" {
		out.Tone = suggested.Tone
	}
	if strings.TrimSpace(out.Columns) == "" {
		out.Columns = suggested.Columns
	}
	if strings.TrimSpace(out.Signatures) == "" {
		out.Signatures = suggested.Signatures
	}
	if strings.TrimSpace(out.Taboos) == "" {
		out.Taboos = suggested.Taboos
	}
	return out
}

func containsAny(text string, words ...string) bool {
	for _, word := range words {
		if word != "" && strings.Contains(text, word) {
			return true
		}
	}
	return false
}
