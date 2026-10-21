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

const writeSystem = `你为微信公众号撰写一篇完整文章。遵守中国法律法规和平台规范；不编造数据、案例、文号；不写签名、不写给编辑的说明。按用户给出的账号与栏目设置决定写法。用到标记为【官方依据】的资料（国家标准、政府部门正式文件）时，在该句末标注它的 [编号]；其他资料不标注编号。需要加粗的词句用 **加粗** 标出。像人写文章那样成段地写，一个自然段可以有好几句话。给你的资料标签和内部说明（如【官方依据】、资料编号说明、“现有依据”之类的用语）只供你参考，不要写进正文。` + "\n" + articleFormat

const reviewSystem = `你审校一篇公众号文章：逐条核对文中的事实和法律、规范说法（法条与条款号、诉讼时效和各类期限、金额与比例、标准编号和年份、主管部门、程序要求等），按你掌握的现行规定判断是否有错；再检查前后矛盾、没写完、违反用户设置之处。语气和写法以用户设置为准：不要为了稳妥把明确的经验判断改成含糊说法，不要加限定词、免责声明或“仅供参考”之类的话，不要改动没有问题的句子。拿不准的说法改成你确定正确的表述或删去，不要编造新的数据、案例、文号。
正文里如果出现给作者看的内部用语（如“现有依据”“【官方依据】”“资料显示”这类说明），算作问题并改掉。
没有问题只输出一行：结论：通过
有问题输出：
结论：需修改
问题：
- 原句……错在哪，正确是什么
=====
（改好的完整文章，格式同下）
` + articleFormat

// Agent mode (account setting): plan before writing, then compare the draft with the user's own
// settings and excerpts and revise once. Both prompts carry no tone of their own.
const planSystem = `你是这篇公众号文章的作者，动笔前先做写作计划，不写正文。按用户给出的账号设置、栏目设置、范文片段和资料来想：这个号的读者卡在哪件具体的事上，这篇从哪个角度切进去，读者读完能直接拿走什么（例如清单、表格、模板、话术、判断方法，按题目需要选，不需要就不写），全文分几部分、每部分讲什么，用什么口吻和写法才像这个号自己写的。不编造数据、案例、文号。只输出 JSON：{"reader_pain":"","angle":"","takeaway":"","outline":[{"heading":"","point":""}],"voice":""}`

const reviseSystem = `你是这篇公众号文章的作者，现在对照用户给出的账号设置和范文片段（如果有）检查自己的初稿，只看写法和语气的差距：开头、口吻、称呼读者的方式、用词、节奏、结构习惯、读者能拿走的东西、结尾，是否像这个号自己写的。不按篇幅评判，不为凑字数加内容，也不删减有用的内容。事实、数字、文号和 [编号] 保持不变，不新增没有依据的数据、案例或文号；不照搬范文的内容和事实。没有差距只输出一行：无差距
有差距输出：
差距：
- 差距一
=====
（改好的完整文章，格式同下）
` + articleFormat

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
	Layout      string   // render template; not part of any prompt
	FactCheck   bool     // account setting: check concrete claims against the research after writing (not a prompt)
	Style       string   // the user's style traits (from their sample articles), if switched on
	Excerpts    []string // excerpts of the user's own sample articles, kept with their consent
	Agent       bool     // account setting 智能体写作 (not a prompt)
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
		fmt.Fprintf(&b, "篇幅参考：用户填的大约 %d 字，只是参考，不是目标也不是上限；按题目需要写，要点讲透为准，不为贴近这个数压缩内容，也不注水\n", *w.LengthHint)
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
	if e := strings.TrimSpace(w.Profile["结尾"]); e != "" {
		fmt.Fprintf(&b, "文末会自动接上用户设置的结尾语：「%s」，正文最后不要再写一遍意思相同的话。\n", e)
	}
	for i, x := range w.Excerpts {
		fmt.Fprintf(&b, "\n范文片段 %d（用户提供的范文，只学语气和写法，不抄内容，不照搬其中的事实、数字和做法）：\n%s\n", i+1, x)
	}
	if len(official) > 0 {
		b.WriteString("\n官方依据（用到时句末标注编号）：\n" + strings.Join(official, "\n") + "\n")
	}
	if len(other) > 0 {
		b.WriteString("\n其他资料（不标注编号）：\n" + strings.Join(other, "\n") + "\n")
	}
	return b.String()
}

// planText turns the plan reply into lines for the writer; "" if it is not the agreed JSON.
func planText(text string) string {
	var pl struct {
		ReaderPain string `json:"reader_pain"`
		Angle      string `json:"angle"`
		Takeaway   string `json:"takeaway"`
		Outline    []struct {
			Heading string `json:"heading"`
			Point   string `json:"point"`
		} `json:"outline"`
		Voice string `json:"voice"`
	}
	if decodeJSON(text, &pl) != nil {
		return ""
	}
	var b strings.Builder
	for _, kv := range [][2]string{{"读者痛点", pl.ReaderPain}, {"切入角度", pl.Angle}, {"读者能拿走的", pl.Takeaway}, {"口吻和写法", pl.Voice}} {
		if v := strings.TrimSpace(kv[1]); v != "" {
			fmt.Fprintf(&b, "%s：%s\n", kv[0], v)
		}
	}
	for i, o := range pl.Outline {
		if h := strings.TrimSpace(o.Heading); h != "" || strings.TrimSpace(o.Point) != "" {
			fmt.Fprintf(&b, "第%d部分：%s——%s\n", i+1, h, strings.TrimSpace(o.Point))
		}
	}
	return strings.TrimSpace(b.String())
}
