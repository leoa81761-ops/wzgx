package gen

import (
	"fmt"
	"strings"
)

// The system prompts hold platform rules only. How the article reads comes from the user's
// account and column settings, passed through verbatim in the user message.

const topicSystem = `你为微信公众号选定下一篇文章的具体题目。必须是一件具体的事、一个具体的问题或一个具体的做法，不能只是账号名、栏目名或宽泛的领域。不得与最近用过的题目重复。只输出 JSON：{"topic":"具体题目","angle":"切入角度","search_query":"用于检索资料的中文关键词"}`

const researchSystem = `你是资料检索助手。请立刻联网搜索，先用中文搜索，优先中文网页。为下面的题目查找 3 到 8 条相关、可公开核对的资料：标准规范、政策文件、技术参数、价格行情、案例、权威机构的说法都可以；题目是新闻动态的，优先最近的。优先政府网站、国家标准、行业协会、主流媒体和专业网站，不用内容农场，同一网站最多两条。不要编写网址。
每条事实写一行，以「- 」开头：事实内容（来源：来源名称或文件标题｜标准号或文号，没有就省略｜发布日期）
只有完全没有搜到相关内容时，才只写：未检索到可靠来源`

const scanSystem = `你是资料检索助手。请立刻联网搜索，先用中文搜索。检索给定范围内最近 30 天发生的具体事件（政策、标准、招投标、产品、项目），列出 5 到 8 条。不要编写网址。
每条写一行，以「- 」开头：事件内容（来源：来源名称或文件标题｜文号，没有就省略｜日期）
只有完全没有搜到时，才只写：未检索到可靠来源`

const writeSystem = `你为微信公众号撰写一篇完整文章。遵守中国法律法规和平台规范；不编造数据、案例、文号；不写签名、不写给编辑的说明。按用户给出的账号与栏目设置决定写法。用到标记为【官方依据】的资料（国家标准、政府部门正式文件）时，在该句末标注它的 [编号]；其他资料不标注编号。只输出 JSON：{"title":"标题","alt_titles":["备选标题"],"summary":"摘要","blocks":[{"type":"h2|p|list|table|quote|image","text":"","items":[],"rows":[[]],"caption":""}]}`

const reviewSystem = `你审校一篇公众号文章：只检查事实错误、前后矛盾、没写完、违反用户设置之处。没有问题输出 {"pass":true}；有问题输出 {"pass":false,"problems":["问题"],"article":{与原文相同结构的完整修订稿}}。只输出 JSON。`

type writingContext struct {
	Rotation    string // what the stats weighting did when picking the column (not given to the model)
	AccountName string
	Profile     map[string]string // label -> text, only non-empty
	ColumnName  string
	TypeName    string
	TypeDesc    string
	ColumnNote  string
	LengthHint  *int
	NeedsNews   bool
	Research    bool // article type setting 是否检索资料
	Practical   bool // news-type column fell back to a non-news topic (set after the topic stage)
	Recent      []string
	Recentnews  string // what a live search found recently, for news-type columns
	Today       string
	Layout      string // render template; not part of any prompt
	FactCheck   bool   // account setting: check concrete claims against the research after writing (not a prompt)
	Style       string // the user's style traits (from their sample articles), if switched on
}

func (w writingContext) settings() string {
	var b strings.Builder
	fmt.Fprintf(&b, "公众号：%s\n", w.AccountName)
	for _, k := range profileOrder {
		if v := w.Profile[k]; v != "" {
			fmt.Fprintf(&b, "%s：%s\n", k, v)
		}
	}
	if w.Style != "" {
		fmt.Fprintf(&b, "写作风格（从用户自己的范文里提炼）：%s\n", w.Style)
	}
	fmt.Fprintf(&b, "栏目：%s\n文章类型：%s\n", w.ColumnName, w.TypeName)
	if w.TypeDesc != "" {
		fmt.Fprintf(&b, "类型说明：%s\n", w.TypeDesc)
	}
	if w.ColumnNote != "" {
		fmt.Fprintf(&b, "栏目说明：%s\n", w.ColumnNote)
	}
	if w.Practical {
		b.WriteString("本篇：近期没有检索到可核实的动态，这一篇是本栏目范围内的实用题目，不按新闻写，也不要求新闻来源。\n")
	}
	return b.String()
}

var profileOrder = []string{"账号定位", "人设", "读者", "语气", "忌讳"}

func topicUser(w writingContext, hint string, rejected []string) string {
	var b strings.Builder
	b.WriteString(w.settings())
	fmt.Fprintf(&b, "今天：%s\n", w.Today)
	if w.NeedsNews {
		if w.Recentnews != "" {
			b.WriteString("这个类型需要一件最近发生的具体事情。近期检索到的动态（只从中选一件，不要另编事件）：\n" + w.Recentnews + "\n")
		} else {
			b.WriteString("近期没有检索到可核实的具体动态。这一篇不写新闻：不要假设或编造任何近期事件、通知、招标或数据，在本栏目范围内选一个实用的非新闻题目。\n")
		}
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
	var official, other []string
	for _, e := range ev {
		line := fmt.Sprintf("[%d] %s", e.N, e.Title)
		if e.DocNumber != "" && !strings.Contains(e.Title, e.DocNumber) {
			line += "（" + e.DocNumber + "）"
		}
		if e.Date != "" {
			line += " " + e.Date
		}
		if e.Snippet != "" && e.Snippet != e.Title {
			line += "：" + e.Snippet
		}
		if e.Official() {
			official = append(official, "【官方依据】"+line)
		} else {
			other = append(other, line)
		}
	}
	if len(official) > 0 {
		b.WriteString("\n官方依据（用到时句末标注编号）：\n" + strings.Join(official, "\n") + "\n")
	}
	if len(other) > 0 {
		b.WriteString("\n其他资料（不标注编号）：\n" + strings.Join(other, "\n") + "\n")
	}
	return b.String()
}
