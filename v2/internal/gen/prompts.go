package gen

import (
	"fmt"
	"strings"
)

// The system prompts hold platform rules only. How the article reads comes from the user's
// account and column settings, passed through verbatim in the user message.

const topicSystem = `你为微信公众号选定下一篇文章的具体题目。必须是一件具体的事、一个具体的问题或一个具体的做法，不能只是账号名、栏目名或宽泛的领域。不得与最近用过的题目重复。只输出 JSON：{"topic":"具体题目","angle":"切入角度","search_query":"用于检索资料的中文关键词"}`

const researchSystem = `你是资料检索员。围绕给定题目检索近期、可靠的中文资料。只输出 JSON：{"facts":[{"claim":"要点","title":"来源标题","url":"链接","date":"YYYY-MM-DD","kind":"national_standard|gov_document|other","doc_number":"文号或标准号","issuer":"发布机关"}]}。kind 只有国家标准、政府或工信部等部门发布的正式文件才算官方依据，其他一律 other。`

const writeSystem = `你为微信公众号撰写一篇完整文章。遵守中国法律法规和平台规范；不编造数据、案例、文号；不写签名、不写给编辑的说明。按用户给出的账号与栏目设置决定写法。只有引用标记为【官方依据】的资料时才在句末标注 [编号]，其他资料不标注。只输出 JSON：{"title":"标题","alt_titles":["备选标题"],"summary":"摘要","blocks":[{"type":"h2|p|list|table|quote|image","text":"","items":[],"rows":[[]],"caption":""}]}`

const reviewSystem = `你审校一篇公众号文章：只检查事实错误、前后矛盾、没写完、违反用户设置之处。没有问题输出 {"pass":true}；有问题输出 {"pass":false,"problems":["问题"],"article":{与原文相同结构的完整修订稿}}。只输出 JSON。`

type writingContext struct {
	AccountName string
	Profile     map[string]string // label -> text, only non-empty
	ColumnName  string
	TypeName    string
	TypeDesc    string
	ColumnNote  string
	LengthHint  *int
	NeedsNews   bool
	Recent      []string
	Today       string
}

func (w writingContext) settings() string {
	var b strings.Builder
	fmt.Fprintf(&b, "公众号：%s\n", w.AccountName)
	for _, k := range profileOrder {
		if v := w.Profile[k]; v != "" {
			fmt.Fprintf(&b, "%s：%s\n", k, v)
		}
	}
	fmt.Fprintf(&b, "栏目：%s\n文章类型：%s\n", w.ColumnName, w.TypeName)
	if w.TypeDesc != "" {
		fmt.Fprintf(&b, "类型说明：%s\n", w.TypeDesc)
	}
	if w.ColumnNote != "" {
		fmt.Fprintf(&b, "栏目说明：%s\n", w.ColumnNote)
	}
	return b.String()
}

var profileOrder = []string{"账号定位", "人设", "读者", "语气", "忌讳"}

func topicUser(w writingContext, hint string, rejected []string) string {
	var b strings.Builder
	b.WriteString(w.settings())
	fmt.Fprintf(&b, "今天：%s\n", w.Today)
	if w.NeedsNews {
		b.WriteString("这个类型需要一件最近发生的具体事情。\n")
	}
	if hint != "" {
		fmt.Fprintf(&b, "用户指定的方向：%s\n", hint)
	}
	if len(w.Recent) > 0 {
		b.WriteString("最近用过的题目（不要重复）：\n- " + strings.Join(w.Recent, "\n- ") + "\n")
	}
	if len(rejected) > 0 {
		b.WriteString("这些候选不合格，换一个更具体的：\n- " + strings.Join(rejected, "\n- ") + "\n")
	}
	return b.String()
}

func writeUser(w writingContext, topic, angle string, ev []Evidence) string {
	var b strings.Builder
	b.WriteString(w.settings())
	if w.LengthHint != nil {
		fmt.Fprintf(&b, "篇幅参考：约 %d 字（仅供参考）\n", *w.LengthHint)
	}
	fmt.Fprintf(&b, "\n题目：%s\n", topic)
	if angle != "" {
		fmt.Fprintf(&b, "切入角度：%s\n", angle)
	}
	if len(ev) > 0 {
		b.WriteString("\n资料：\n")
		for _, e := range ev {
			tag := ""
			if e.Official() {
				tag = "【官方依据】"
			}
			line := fmt.Sprintf("[%d]%s %s", e.N, tag, e.Title)
			if e.DocNumber != "" {
				line += "（" + e.DocNumber + "）"
			}
			if e.Date != "" {
				line += " " + e.Date
			}
			if e.Snippet != "" {
				line += "：" + e.Snippet
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}
