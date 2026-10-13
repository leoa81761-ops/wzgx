package llm

import (
	"encoding/json"
	"strings"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
)

const lengthRule = "word_target 只是篇幅参考。写完要说的事就可以停，不要为了凑字数重复或注水。" +
	"正文字数统计各小节的小标题、段落、列表、表格单元格和结尾，不含文章标题、摘要、参考来源和脚注。" +
	"每个小节最多四段，把做法写具体。"

const sourcedFacts = "不得编造来源里没有的阈值、电压、标准编号或统计数字。没有来源支撑时写成定性说明，不要写具体数字。"

const namedSourceRule = "不要写出 research_sources 或 sources 里没有出现的机构名、标准号或文件名。来源名单里没有的部委、协会、学会、GB、GB/T 和书名号标题都不要写。"

const citationRule = "用到 research_sources 里的事实时，在句末用 [1]、[2] 这种序号标出对应来源，序号从 1 开始连续编号，不能只有 [2] 而没有 [1]。没有用到的来源不要写进 sources。没有来源时不要编造 [n]。不要整句照抄来源里的原句，用自己的话转述，事实和数字保持一致。"

const factSheetRule = "fact_sheet 是这篇稿唯一允许使用的外部证据。每句事实的句末必须带上对应的 [n]，序号和事实清单一致，从 [1] 连续编号。不要写清单以外的数字、机构、标准、新闻或网址。引用在写正文时就标上，不要留到后面再补。不要整句照抄来源里的原句，用自己的话转述。"

const healthRule = "治疗、疗效、降血压、血糖、癌症、偏方、剂量、诊断、服用、治愈、处方、药品、炎症这类说法，句末必须有 [n]；没有来源就改成不涉及诊断或用药的生活习惯提醒。"

const experienceRule = "writing_mode 是 experience 且没有可引用的来源：这是经验分享。用这个账号自己的专业来写，遵守 direction、tone、signatures 和 persona_columns。" +
	"不要编造外部机构、标准、新闻、网址或引用，不要使用 [n]。没有来源时不要写百分比、掉线率或版本号，例如 15%、ONVIF 2.4。数字只能写成亲历的范围，例如「我见过的项目里大多」。" +
	"仍然要写具体：句子里要有数字、工具和场景，并用这个账号的口吻，技术号写现场，家庭号写家里的事。不要温馨、默契、从容这类暖场空话，也不要单独甩一句没有数字和工具的提示，也不要为了凑篇幅重复。文末来源会标成经验分享。" +
	"如果已经有来源网址，不要标成经验分享，在用到该来源的句子后写 [n]。" +
	"仍然要遵守 column_structure 和 structure 里的栏目结构模板：问答至少三对，问句以问号结尾，答案用「答：」接上，答案里不要再出现问号；步骤每一段都要有序号，例如「1. 」或「第一步」；快报没有可核对的新闻时不要编造日期和引用。" +
	"每一段只写 3 到 4 句，不要把很多句挤成一大段，也不要让相邻段重复同一句话。不要写诊断、剂量、疗效或用药。"

func lengthInstruction(stageLabel string, payload map[string]any) string {
	if stageLabel == "正文" && intOf(payload["section_target"]) > 0 {
		return "这一次只写一个小节，不要写其他小节。" +
			"section_heading 是这一节的小标题，sections 里只放这一节，heading 必须用 section_heading。" +
			"section_intent 是这一节要写完的事。section_index 和 section_count 表示第几节、一共几节。" +
			"这一节最多四段。section_target 是这一节的篇幅参考，写完 section_intent 就可以停，不要为了凑满预算重复同一句。" +
			"字数统计小标题、段落、列表和表格单元格。不要把选题、栏目或题目嵌进「先打开……的检修口」这类套话。" +
			"问答栏目这一节只写一对：问句以问号结尾，答案用「答：」接上。步骤栏目这一节只写这一步，序号用 section_index，例如「1. 」。" +
			"title 用 topic_title。只有最后一节写 closing，前面几节 closing 用空字符串。sources 保留已有来源，不要新造网址。"
	}
	return lengthRule
}

func evidenceRule(payload map[string]any) string {
	if asString(payload["writing_mode"]) == "experience" {
		return experienceRule
	}
	if strings.TrimSpace(asString(payload["fact_sheet"])) != "" {
		return factSheetRule + healthRule + markerRule
	}
	return citationRule + healthRule + markerRule
}

const markerRule = "保留正文里已有的 [n] 引用标记，不要删掉，也不要改动序号。"

const anecdoteRule = "事例只能来自已经给出的来源，或者明确写成假设，例如「比如……假设」。不要编造读者的生活，不要编造别人的原话。"

const readerRule = "reader_profile 是给编辑看的读者画像，不要用这串文字称呼读者，也不要把它写进标题、摘要或正文。"

const sceneRule = "recent_titles 只用来避开这个账号已经用过的标题。不要把其他文章、其他账号的人物、场景或原句写进开头和正文。"

func tabooRule(payload map[string]any) string {
	if strings.TrimSpace(asString(payload["taboos"])) == "" {
		return ""
	}
	return "taboos 是这个号不写的说法。不要在标题、摘要或正文里写出这些原话，也不要把它们当成要解释的词。"
}

func columnRule(payload map[string]any) string {
	var b strings.Builder
	if strings.TrimSpace(asString(payload["column_positioning"])) != "" {
		b.WriteString("column_positioning 是栏目定位的全文，要整段遵守，不要只取前几个字。")
	}
	switch strings.ToLower(strings.TrimSpace(asString(payload["column_structure"]))) {
	case "news", "news-brief", "快报":
		if asString(payload["writing_mode"]) == "experience" {
			b.WriteString("栏目原是快报，但这篇没有可核对的新闻来源，改成经验分享：不要写外部新闻，不要使用 [n]。")
		} else {
			b.WriteString("栏目是快报：每一段都要有可以核对的日期，至少一处 [n]，不要每段用同一个日期词开头。")
		}
	case "steps", "step-by-step", "breakdown", "拆解":
		b.WriteString("栏目是步骤：每一段都要有序号，例如「1. 」或「第一步」，不要留下没有序号的段落。")
	case "qa", "q&a", "faq", "问答":
		b.WriteString("栏目是问答：至少三对。问句以问号结尾，答案用「答：」接在问号后面或单独成下一段；答案里不要再出现问号。")
	}
	return b.String()
}

func motifRule(payload map[string]any) string {
	var motifs []string
	switch items := payload["avoid_motifs"].(type) {
	case []string:
		motifs = items
	case []any:
		for _, item := range items {
			if text := strings.TrimSpace(asString(item)); text != "" {
				motifs = append(motifs, text)
			}
		}
	}
	if len(motifs) == 0 {
		return ""
	}
	return "不要再用这些已经写过的意象：" + strings.Join(motifs, "、") + "。"
}

func voiceRule(payload map[string]any) string {
	var b strings.Builder
	name := asString(payload["account_name"])
	tone := asString(payload["tone"])
	if strings.Contains(name+tone+asString(payload["direction"]), "奶奶") {
		b.WriteString("这个号用奶奶的口气。只讲 reader_pain 这一件家里的事，用「我在家见过」这种亲历，不要写成通用教程，也不要换到另一件家务。")
	} else if strings.TrimSpace(tone) != "" || strings.TrimSpace(asString(payload["signatures"])) != "" {
		b.WriteString("用 tone、signatures 和 account_name 里这个号自己的口气写，不要换成别的号的说法。")
	}
	if technicalVoice(payload) {
		b.WriteString("技术号可以用第一人称写现场里反复见到的规律，例如「我在现场见过这类情况」。这是经验模式，不是某一天编出来的事件，也不要写成某个有名有姓的人的原话。")
	}
	return b.String()
}

func technicalVoice(payload map[string]any) bool {
	if asString(payload["vertical_key"]) == "elv" {
		return true
	}
	text := asString(payload["vertical"]) + asString(payload["direction"]) + asString(payload["tone"])
	return strings.Contains(text, "弱电") || strings.Contains(text, "工程") || strings.Contains(text, "智能化") || strings.Contains(text, "现场")
}

func elvContext(payload map[string]any) bool {
	if asString(payload["vertical_key"]) == "elv" {
		return true
	}
	blob := asString(payload["vertical"]) + asString(payload["direction"])
	if strings.Contains(blob, "弱电") || strings.Contains(blob, "工程") || strings.Contains(blob, "智能化") {
		return true
	}
	raw, _ := json.Marshal(payload["draft"])
	return strings.Contains(string(raw), "弱电")
}

func StageMessages(stageLabel string, payload map[string]any) []map[string]string {
	schema := StageSchema(stageLabel)
	var system string
	switch stageLabel {
	case "来源判断":
		system = "你是资料相关性判断。对照 topic，判断 candidates 里每一条能不能当这篇的来源。" +
			"每条必须给出 page_type、on_topic、snippet 和 fact。page_type 只能是 news、official、standard、professional、forum、ad、novel、dictionary、job、ecommerce、other。professional 表示专业文章。" +
			"只有 news、official、standard、professional 才可能保留。on_topic 表示是不是在讲 article_title 这篇文章本身，不是同一个部委或同一个域名。只共享规范、标准、工程这类词必须是 false。" +
			"snippet 必须是页面原文里连续出现的一句，用来支撑 fact，不要改写，不要编造。" +
			"下面这些一律判成对应类型，而且 on_topic 为 false：TXT小说下载、章节全文、全文阅读是 novel；小说帖、同人、虚构故事是 novel；词典、百科、例句、造句是 dictionary；贷款、借钱、信用卡是 ad；招聘、职位、月薪是 job；豆瓣图书、书评是 other；手机或数码论坛帖是 forum。" +
			"软件下载、系统镜像、影视剧、视频站和应用商店是 other，on_topic 为 false。index 必须使用候选里的 index，不要改序号。" +
			"没有标题时用 url 和 snippet 判断。只输出一个 JSON 对象，不要使用 Markdown 代码块，不要增加字段，不要输出解释。" +
			"\nJSON Schema：\n" + schemaText(schema)
	case "资料检索":
		system = "你是资料检索助手。请立刻联网搜索，先用中文搜索，优先中文网页。" +
			"为下面的选题查找 3 到 5 条最近、相关、可公开核对的资料。每条都要讲这篇选题本身，不要用只沾到一个词的页面。" +
			"优先 gov.cn、samr.gov.cn、std.samr.gov.cn、openstd.samr.gov.cn、mohurd.gov.cn、国家标准、中国行业协会和主流媒体，不要用转载站、聚合站、文库镜像或内容农场。" +
			"不要搜索结果页、百科词条、词典页面、标准下载器或检索页。不要网店、帮助台、登录页和购物车页面。同一网站最多两条。" +
			"输入里的 blocked_domains 一律不要引用。" +
			"如果输入里有 allowed_domains，只引用这些域名及其子域名。" +
			"不要在回答里编写网址。程序只采用接口返回的 citations、search_results 或 annotations，正文里的链接不会被当作来源。" +
			"用中文概括核对过的事实。如果检索没有真正引用网页，写明「未检索到可靠来源」。"
		if asString(payload["vertical_key"]) == "elv" || strings.Contains(asString(payload["vertical"]), "弱电") {
			system += "这是弱电智能化选题：优先 gov.cn、国家标准（例如 GB 50311）、mohurd.gov.cn、samr.gov.cn、openstd.samr.gov.cn，以及中国行业协会（例如中国工程建设标准化协会）。"
		}
		if asString(payload["query_mode"]) == "short" {
			system += "这是补救检索。只写一条中文检索词，最多 3 条权威来源。"
		}
	case "标题":
		system = "你是公众号标题编辑。请用输入里的语气，写 10 到 12 个彼此不同的标题。" +
			"每个标题不超过 28 个字，不要写成叙述句，不要用逗号铺成一段话。" +
			"每个标题都是完整的一句，不要为了凑字数截断，也不要在后面补套话。" +
			"不要用「震惊」「不转不是」「稳赚不赔」。不要靠关键词堆砌。" +
			"每个标题用一种微信上读得完的句式：数字、冲突、身份，或者好奇缺口。标题里的对象必须已经出现在正文里，不要和正文说成两件事。" +
			"优先写具体场景或数字。每个标题都要自评分：hook、concrete、consistent、persona，都是 1 到 5 的整数。" +
			"consistent 表示标题和 summary、正文是不是同一件事，对不上就低于 3。自评分只用于淘汰对不上的标题，不要靠高分把长句排到前面。" +
			readerRule +
			"只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "结构润色":
		system = "你是公众号编辑。只改写输入里标记为需要改的部分：rewrite_hook、rewrite_headings、rewrite_closing 为 true 的才写。" +
			"没有要求改的部分返回空字符串或空数组。" +
			"不要改表格、列表、序号和小标题层级，只改每个区块里的句子。不要删除已有段落，不要改标点和引用标记，不要新造数字或来源。" + markerRule +
			"开头如果要改，写成 2 到 3 句，里面要有一个场景、一个冲突，或者一个数字。不要把两段开头叠在一起。和原开头重叠超过一半就返回空字符串。" +
			"小标题要能扫读，结尾留下一句收获并邀请读者留言。" +
			anecdoteRule + voiceRule(payload) + readerRule + sceneRule + tabooRule(payload) + columnRule(payload) +
			"只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "补引用":
		schema = StageSchema("正文")
		system = "你是公众号编辑。正文漏了引用序号。请对照 research_sources，只在这句话确实能被该来源的标题或摘要支撑时，在句末补上 [1]、[2]，序号从 1 开始，对应来源顺序。" +
			"来源没讲到的句子不要标引用。不要编造来源里没有的事实，不要新增网址，不要改已经写对的数字。没有用到的来源不要写进 sources。" +
			markerRule + readerRule +
			"只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "开头比较":
		system = "你是公众号编辑。比较原来的第一段和改写后的开头，留下更具体、更不像叙述句的那一个。" +
			"choice 只能是 original 或 rewrite。只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "标题比较":
		system = "你是公众号标题编辑。从 candidates 里选出一个标题作为 winner，必须和某一条候选完全相同。" +
			"优先 28 个字以内、有具体场景或数字、不是叙述句的标题。不要新写标题。" +
			"只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "意象改写":
		schema = StageSchema("正文")
		system = "你是公众号编辑。正文里同一句式或意象反复出现。请改写重复的说法，保留事实、数字和 [n] 引用，不要新增材料或网址。" +
			motifRule(payload) + markerRule + readerRule +
			"只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "栏目结构":
		schema = StageSchema("正文")
		if asString(payload["writing_mode"]) == "experience" {
			system = "你是公众号编辑。这篇是经验分享，没有可引用的外部来源。请按这个账号的 direction、tone、signatures 和栏目结构重写成一篇完整稿。" +
				"不要写外部机构、标准、新闻或网址，不要使用 [n]。现场的数量、工具和场景要写具体，不要用温馨、默契这类空话，也不要单独甩一句没有数字和工具的提示，也不要为了凑篇幅重复。" +
				"qa 或问答：至少三对。问句以问号结尾，答案用「答：」接在同一段问号后面，或单独写成下一段；答案里不要再出现问号。问答必须相邻。" +
				"steps 或拆解：每一段都要有序号，例如「1. 」或「第一步」，不要留下没有序号的段落。" +
				"news 或快报：没有可核对的新闻时不要编造日期和 [n]，按栏目定位写成经验分享。" +
				"每一段只写 3 到 4 句，不要把很多句挤成一大段，也不要让相邻段重复同一句话。" +
				"不要在句首机械地加「近日」，不要补空泛的「答：」。" +
				"只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
			break
		}
		system = "你是公众号编辑。正文的栏目结构没对上，请按 column_structure 重写成一篇完整稿。" +
			"不要删掉已有段落、列表和表格，不要把表格压成一段话，不要增加大纲里没有的步骤。序号必须从 1 连续编到最后，不要把资料清单的编号抄进正文。只改每个区块里的句子，表格和列表的结构保持原样。" +
			"news 或快报：每一段都要有可以核对的日期，至少一处 [n] 引用，而且不能每段都用同一个日期词开头。" +
			"qa 或问答：至少三对。问句以问号结尾，答案用「答：」接在同一段问号后面，或单独写成下一段；答案里不要再出现问号。问答必须相邻。" +
			"steps 或拆解：每一段都要有序号，例如「1. 」或「第一步」，不要留下没有序号的段落。" +
			"不要在句首机械地加「近日」，不要补空泛的「答：」，不要给没有来源的句子加 [1]。" +
			"保留已有事实和 sources，不要新造机构、标准号和网址。" + namedSourceRule + markerRule +
			"只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "禁忌改写":
		schema = StageSchema("正文")
		system = "你是公众号编辑。taboos 里是不能写的承诺说法，例如包过验收、保证中标。请改写含有这些原话的段落。" +
			"不要因为单独出现验收或中标就改。不要插入「这一步按现场记录来写」这类占位句；改写不了就删掉那一句。" +
			namedSourceRule + markerRule +
			"只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "套话改写":
		schema = StageSchema("正文")
		system = "你是公众号编辑。正文里有套话。请改写含有这些说法的句子，不要只删掉几个字留下破句。" +
			"常见套话包括：综上所述、在当今社会、不言而喻、值得一提的是、结论是、更稳妥的做法是、一件小事一份真心、安心不是一下子。" +
			markerRule +
			"只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "表述改写":
		schema = StageSchema("正文")
		system = "你是公众号编辑。正文有一段和来源逐字相同。请改写 copied_spans 里的原句，保留事实、数字和来源，不要整段照抄。" +
			markerRule +
			"只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "事实核对":
		schema = StageSchema("正文")
		system = "你是事实核对编辑。请对照 research_notes、research_sources 和可靠的领域常识，检查正文里的核心定义、阈值、标准编号和数字。" +
			"与来源或领域常识冲突的说法要直接改对。来源没有支撑、又不是稳定常识的数字、电压阈值和标准编号，删掉或改成不带具体数字的说法。" +
			"不要编造网址，也不要编造标准编号（包括 GB、GB/T）。research_sources 里没有的链接和标准号必须删掉，不能补一条看起来像真的网址或标准号。" +
			sourcedFacts + namedSourceRule + evidenceRule(payload) +
			"保留原有结构、标题和 sources，不要新造网址。核对时不要把正文撑过 word_target 的 130%。" + lengthRule
		if elvContext(payload) {
			system += "行业术语不要套用相邻概念。建筑智能化里的「弱电」指电子信息系统，包括通信、安防、楼宇自控、火灾报警和音视频，工作电压通常在安全特低电压（常见 36V 及以下）和信号电平，不是供配电里交流 1000V 或直流 1500V 以下的低压电力。不要把 1000V 写成弱电的定义。"
		}
		system += "只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	default:
		system = "你是公众号写作助手。只输出一个 JSON 对象，不要使用 Markdown 代码块，不要输出解释或 HTML。" +
			"字段必须和下面的 JSON Schema 完全一致，不要增加字段，字符串字段没有内容时用空字符串，数组没有内容时用空数组。" +
			"不得编造具体政策、标准、新闻事件、统计数据、人名引语。" + sourcedFacts +
			"输入里如果有 research_notes 或 research_sources，事实和 sources 只能来自这些材料，sources 必须保留这些来源，不要删掉网址。" +
			"不得使用「震惊」「不转不是」「稳赚不赔」等标题党或承诺性用语，也不得写药物剂量、诊断、偏方或收益承诺。" +
			"免费或无来源时 sources 必须是空数组。"
		if stageLabel == "选题" {
			system += "recent_titles 和 recent_topics 是这个账号最近写过的标题和主题。不要再选同一件事，同一栏目里换个说法也不要。已经写过环形、星形，就不要再写环状、星状或同一组拓扑对比。rejected_title 是刚被否掉的题，不要再交回来。" +
				"题目必须同时用上 column_positioning、tone、signatures 和 reader_pain。reader_pain 是这篇唯一的读者麻烦，不要换成「先看失败点」这种每个号都能套的空角度。" +
				"题目必须跟着栏目定位走，不要套「两种做法」或「对比两种」。题目要具体到对象和问题，例如「综合布线六类线测试失败常见原因」，不要写成「施工坑点：更稳妥的做法」这种栏目名加空角度。知识FAQ 写成这个账号主题里的一个具体问题，弱电号就问弱电现场的问题。家长里短写成一件家里的事，再带一个能照做的提醒。和账号主题无关的题不要选，例如弱电号不要写代理机构信息登记。奶奶号只写这一件家务。"
		}
		if stageLabel == "大纲" {
			system += "points 里每一节给出 word_budget，各节 word_budget 相加必须等于 word_target。正文会按这个预算一节一节写满。小节标题要写成具体的事，不要写成「更稳妥的做法」。"
		}
		if stageLabel == "正文" || stageLabel == "审核" {
			system += lengthInstruction(stageLabel, payload) + evidenceRule(payload) + anecdoteRule + namedSourceRule + voiceRule(payload) + sceneRule + tabooRule(payload) + columnRule(payload)
			system += "不要把正文改写成 recent_titles 或 recent_topics 里已经写过的那件事。" +
				"全文只写 reader_pain 这一件事，不要换到另一件家务、另一台设备或另一个场景。" +
				"开头用 2 到 3 句，里面要有一个场景、一个冲突，或者一个数字。小标题不要加栏目名前缀，例如不要写「拆解：」「简单：第一步：」。" +
				"不要每节都做成编号列表：第一节用一段引言，中间可以有列表，最后一节用一段可以转发的收获。同一条例子不要在两节里再写一遍。结尾只留一句自然的邀请，不要叠两句关注。"
		}
		system += readerRule + motifRule(payload)
		if stageLabel == "审核" {
			system += "如果同一句式、意象或口头禅反复出现，在返回的正文里改写掉，并把 quality.motif 标为 true；没有反复就标 false。" +
				"quality.hook 是开头 1 到 5 分，4 分及以上表示开头已经够具体，不必再改。quality.headings 和 quality.closing 表示小标题和结尾是否已经够用。"
		}
		system += "\nJSON Schema：\n" + schemaText(schema)
	}
	body := map[string]any{"环节": stageLabel}
	for key, value := range payload {
		body[key] = value
	}
	raw, _ := json.Marshal(body)
	return []map[string]string{
		{"role": "system", "content": system},
		{"role": "user", "content": string(raw)},
	}
}

func mockComplete(provider Provider, messages []map[string]string) (Completion, error) {
	if provider.ExtraParams != nil {
		if fail, ok := provider.ExtraParams["_mock_fail"].(bool); ok && fail {
			return Completion{}, &Error{Message: provider.Name + " 模拟失败"}
		}
	}
	payload := userPayload(messages)
	stage := asString(payload["环节"])
	if stage == "" {
		stage = "正文"
	}
	if stage == "栏目结构" && provider.ExtraParams != nil && provider.ExtraParams["_mock_structure_fail"] == true {
		data := mockDraftCopy(payload)
		content, _ := json.Marshal(data)
		return Completion{
			Content: string(content), PromptTokens: 120, CompletionTokens: 180,
			ProviderID: provider.ID, ProviderName: provider.Name, Protocol: provider.Protocol, Model: provider.Model,
		}, nil
	}
	data := MockForStage(stage, payload)
	if stage == "开头比较" {
		choice := "original"
		if provider.ExtraParams != nil {
			if picked, ok := provider.ExtraParams["_mock_hook_choice"].(string); ok && picked == "rewrite" {
				choice = "rewrite"
			}
		}
		data = map[string]any{"choice": choice}
	}
	content, _ := json.Marshal(data)
	return Completion{
		Content:          string(content),
		PromptTokens:     120,
		CompletionTokens: 180,
		ProviderID:       provider.ID,
		ProviderName:     provider.Name,
		Protocol:         provider.Protocol,
		Model:            provider.Model,
	}, nil
}

func MockForStage(stage string, payload map[string]any) map[string]any {
	switch stage {
	case "资料检索":
		return map[string]any{"notes": "", "sources": []any{}}
	case "来源判断":
		return mockJudge(payload)
	case "选题":
		title := asString(payload["subtopic"])
		if title == "" {
			title = "实用方法"
		}
		angle := asString(payload["angle"])
		if angle == "" {
			angle = "一个小做法"
		}
		if strings.Contains(angle, "两种做法") || strings.Contains(angle, "对比两种") {
			angle = "一个具体问题"
		}
		full := title + "：" + angle
		if len([]rune(full)) > 64 {
			full = string([]rune(full)[:64])
		}
		return map[string]any{"title": full, "angle": payload["angle"]}
	case "大纲":
		sub := asString(payload["subtopic"])
		if sub == "" {
			sub = "这件事"
		}
		return map[string]any{
			"hook": "先从「" + sub + "」里一个具体场景说起。",
			"points": []any{
				map[string]any{"heading": "先看清眼前的问题", "intent": "用一个很小的场景说明读者为什么会卡住", "need_table": false},
				map[string]any{"heading": "一个今天就能做的做法", "intent": "给出可以立刻执行的步骤", "need_table": true},
				map[string]any{"heading": "少踩一个常见坑", "intent": "提醒一个容易做错的地方", "need_table": false},
			},
			"closing": "选一个最小的步骤，做完再决定要不要继续。",
		}
	case "审核", "事实核对":
		draft, _ := payload["draft"].(map[string]any)
		if draft == nil {
			draft = MockForStage("正文", payload)
		}
		return cleanDraft(draft)
	case "标题":
		return mockTitles(payload)
	case "结构润色":
		return mockPolish(payload)
	case "补引用":
		return mockAddCitations(payload)
	case "开头比较":
		return map[string]any{"choice": "original"}
	case "标题比较":
		return mockTitleChoice(payload)
	case "意象改写", "禁忌改写", "套话改写":
		return mockDraftCopy(payload)
	case "表述改写":
		return mockUncopy(payload)
	case "栏目结构":
		return mockStructured(payload)
	default:
		return draftFromPayload(payload)
	}
}

func mockTitleChoice(payload map[string]any) map[string]any {
	for _, item := range anySlice(payload["candidates"]) {
		title := strings.TrimSpace(asString(item))
		if title != "" && len([]rune(title)) <= 28 {
			return map[string]any{"winner": title}
		}
	}
	cands := anySlice(payload["candidates"])
	if len(cands) > 0 {
		return map[string]any{"winner": strings.TrimSpace(asString(cands[0]))}
	}
	return map[string]any{"winner": ""}
}

func mockJudge(payload map[string]any) map[string]any {
	topic := asString(payload["topic"])
	var items []any
	for i, raw := range anySlice(payload["candidates"]) {
		row, _ := raw.(map[string]any)
		blob := ""
		if row != nil {
			blob = asString(row["title"]) + asString(row["snippet"]) + asString(row["url"])
		}
		score := 0
		if judgeOnTopic(topic, blob) {
			score = 3
		}
		fact := ""
		if row != nil {
			fact = asString(row["snippet"])
			if fact == "" {
				fact = asString(row["title"])
			}
		}
		facts := []any{}
		if score >= 2 && fact != "" {
			facts = []any{fact}
		}
		index := i + 1
		if row != nil && intOf(row["index"]) > 0 {
			index = intOf(row["index"])
		}
		pageType := "other"
		onTopic := false
		snippet := ""
		if score >= 2 {
			pageType = "professional"
			onTopic = true
			snippet = fact
		}
		items = append(items, map[string]any{
			"index": index, "score": score, "facts": facts,
			"page_type": pageType, "on_topic": onTopic, "snippet": snippet, "fact": fact,
		})
	}
	if items == nil {
		items = []any{}
	}
	return map[string]any{"items": items}
}

func judgeOnTopic(topic, blob string) bool {
	if strings.TrimSpace(topic) == "" || strings.TrimSpace(blob) == "" {
		return false
	}
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(topic, func(r rune) bool {
		return r == ' ' || r == '，' || r == '、' || r == '。'
	}) {
		runes := []rune(strings.TrimSpace(part))
		for i := 0; i+2 <= len(runes); i++ {
			term := string(runes[i : i+2])
			if seen[term] {
				continue
			}
			seen[term] = true
			if strings.Contains(blob, term) {
				return true
			}
		}
	}
	return false
}

func mockAddCitations(payload map[string]any) map[string]any {
	cloned := mockDraftCopy(payload)
	if len(anySlice(payload["research_sources"])) == 0 {
		return cloned
	}
	raw, _ := json.Marshal(cloned)
	if strings.Contains(string(raw), "[") {
		return cloned
	}
	sections, _ := cloned["sections"].([]any)
	if len(sections) == 0 {
		return cloned
	}
	section, _ := sections[0].(map[string]any)
	if section == nil {
		return cloned
	}
	paras, _ := section["paragraphs"].([]any)
	if len(paras) == 0 {
		section["paragraphs"] = []any{"见来源[1]。"}
	} else if text, ok := paras[0].(string); ok {
		paras[0] = text + "[1]"
		section["paragraphs"] = paras
	}
	sections[0] = section
	cloned["sections"] = sections
	return cloned
}

func mockUncopy(payload map[string]any) map[string]any {
	cloned := mockDraftCopy(payload)
	if cloned == nil {
		return nil
	}
	raw, err := json.Marshal(cloned)
	if err != nil {
		return cloned
	}
	text := string(raw)
	for _, item := range anySlice(payload["copied_spans"]) {
		span := asString(item)
		if span == "" {
			continue
		}
		text = strings.ReplaceAll(text, span, "改成自己的说法。")
	}
	var out map[string]any
	if json.Unmarshal([]byte(text), &out) != nil || out == nil {
		return cloned
	}
	return out
}

func mockDraftCopy(payload map[string]any) map[string]any {
	draft, _ := payload["draft"].(map[string]any)
	if draft == nil {
		return draftFromPayload(payload)
	}
	raw, err := json.Marshal(draft)
	if err != nil {
		return draft
	}
	var cloned map[string]any
	if json.Unmarshal(raw, &cloned) != nil || cloned == nil {
		return draft
	}
	return cloned
}

func anySlice(raw any) []any {
	list, _ := raw.([]any)
	return list
}

func mockTitles(payload map[string]any) map[string]any {
	base := asString(payload["subtopic"])
	if base == "" {
		base = asString(payload["draft_title"])
	}
	if base == "" {
		base = "这件事"
	}
	lines := []string{
		base + "，先看哪一步",
		"为什么" + base + "会卡在这里",
		base + "可以今天做完的一件小事",
		"把" + base + "写成能照着做的步骤",
		"少做一件和" + base + "无关的事",
		base + "里最容易漏掉的核对",
		"下次遇到" + base + "先停一下",
		"关于" + base + "的一个具体做法",
		"怎么把" + base + "说清楚",
		base + "不必一次做完",
		"留给做" + base + "的人一句提醒",
		base + "这件事不必一次做完，也不必为了凑标题去截断句子本身，请原样保留",
	}
	out := make([]any, len(lines))
	for i, line := range lines {
		out[i] = map[string]any{"title": line, "hook": 4, "concrete": 4, "consistent": 5, "persona": 4}
	}
	return map[string]any{"titles": out}
}

func mockStructured(payload map[string]any) map[string]any {
	draft := mockDraftCopy(payload)
	if draft == nil {
		draft = draftFromPayload(payload)
	}
	if asString(draft["title"]) == "" {
		draft["title"] = "验收先看这一步"
	}
	kind := strings.ToLower(asString(payload["column_structure"]))
	sections, _ := draft["sections"].([]any)
	if len(sections) == 0 {
		return draft
	}
	section, _ := sections[0].(map[string]any)
	if section == nil {
		return draft
	}
	switch kind {
	case "news", "news-brief", "快报":
		section["paragraphs"] = []any{"2024年10月12日，综合布线验收有一条公开说明。[1]", "10月11日，另一条记录写的是现场编号。[1]"}
	case "qa", "q&a", "faq", "问答":
		section["paragraphs"] = []any{
			"验收要先看哪一项？答：先对照图纸核对编号。",
			"材料清单放在哪一步？答：放在进场之前核对。",
			"编号对不上怎么办？答：先停下再对照图纸。",
		}
	case "steps", "step-by-step", "breakdown", "拆解":
		section["paragraphs"] = []any{"1. 先看现场。", "2. 再核对编号。"}
	}
	sections[0] = section
	draft["sections"] = sections
	return draft
}

func mockPolish(payload map[string]any) map[string]any {
	out := map[string]any{"hook": "", "headings": []any{}, "closing": ""}
	if payload["rewrite_hook"] == true {
		out["hook"] = "为什么先把这一步说清楚？"
	}
	if payload["rewrite_headings"] == true {
		out["headings"] = []any{"先看问题", "一个做法"}
	}
	if payload["rewrite_closing"] == true {
		out["closing"] = "可以先记住这一步。欢迎留言说说你的做法。"
	}
	return out
}

func draftFromPayload(payload map[string]any) map[string]any {
	outline, _ := payload["outline"].(map[string]any)
	if outline == nil {
		outline = MockForStage("大纲", payload)
	}
	style := asString(payload["style_label"])
	if style == "" {
		style = "清楚"
	}
	direction := asString(payload["direction"])
	if direction == "" {
		direction = "这个方向"
	}
	subtopic := asString(payload["subtopic"])
	if subtopic == "" {
		subtopic = "这件事"
	}
	angle := asString(payload["angle"])
	if angle == "" {
		angle = "一个具体角度"
	}
	title := asString(payload["topic_title"])
	if title == "" {
		title = subtopic + "：" + angle
	}
	if len([]rune(title)) > 64 {
		title = string([]rune(title)[:64])
	}
	var sections []any
	points, _ := outline["points"].([]any)
	only := intOf(payload["section_index"])
	if only > 0 {
		if only-1 < len(points) {
			points = []any{points[only-1]}
		} else if heading := asString(payload["section_heading"]); heading != "" {
			points = []any{map[string]any{"heading": heading, "intent": asString(payload["section_intent"]), "need_table": payload["need_table"]}}
		}
	}
	leads := []string{
		"验收以前先把现场条件核对齐。",
		"午饭后只改一处措辞就发出。",
		"傍晚把没做完的那一条单独标出。",
		"周末用十分钟整理刚记下的结果。",
		"第二天早晨优先处理昨天标出的那条。",
		"收工前把还没核对的项目圈出来。",
	}
	follows := []string{
		"记录员把缺项写成清单交给负责人。",
		"同事用回复确认收到不必再开会。",
		"明天一早先处理这个标记再看其他。",
		"写完就停不要把同一句话再说一遍。",
		"做完这一步再决定要不要往下走。",
		"核对的人签上名字日期就结束。",
	}
	for i, raw := range points {
		point, _ := raw.(map[string]any)
		heading := "一个小节"
		if point != nil && asString(point["heading"]) != "" {
			heading = asString(point["heading"])
		}
		slot := i
		if only > 0 {
			slot = only - 1
		}
		lead := leads[slot%len(leads)]
		if slot == 0 {
			lead += "语气是" + style + "。"
		}
		section := map[string]any{
			"heading": heading,
			"paragraphs": []any{
				lead,
				follows[slot%len(follows)],
			},
			"list": []any{"先做一个最小步骤，用测试仪测3遍" + heading, heading + "做完后用一句话记下结果", "不追求一次到位", "语气是" + style},
		}
		if point != nil && point["need_table"] == true {
			section["table"] = map[string]any{
				"headers": []any{"做法", "说明"},
				"rows":    []any{[]any{"先做一个最小步骤", "做完后记一句话"}},
			}
		}
		sections = append(sections, section)
	}
	closing := asString(outline["closing"])
	if closing == "" {
		closing = "今天先完成最小的一步。"
	}
	count := intOf(payload["section_count"])
	if only > 0 && count > 0 && only < count {
		closing = ""
	}
	draft := map[string]any{
		"title": title, "alt_titles": []any{subtopic + "可以先做的一件小事", "关于" + subtopic + "，少做一件事"},
		"summary":  "一篇关于" + direction + "／" + subtopic + "的短文，语气是" + style + "。",
		"sections": sections, "closing": closing, "sources": []any{},
	}
	return draft
}

func cleanDraft(draft map[string]any) map[string]any {
	raw, _ := json.Marshal(draft)
	var cloned any
	_ = json.Unmarshal(raw, &cloned)
	return walkClean(cloned).(map[string]any)
}

func walkClean(value any) any {
	switch v := value.(type) {
	case string:
		for _, word := range append(append([]string{}, catalog.DefaultSensitiveWords...), catalog.Cliches...) {
			v = strings.ReplaceAll(v, word, "")
		}
		return v
	case []any:
		for i := range v {
			v[i] = walkClean(v[i])
		}
		return v
	case map[string]any:
		for key, item := range v {
			v[key] = walkClean(item)
		}
		return v
	default:
		return value
	}
}

func userPayload(messages []map[string]string) map[string]any {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i]["role"] != "user" {
			continue
		}
		var data map[string]any
		if json.Unmarshal([]byte(messages[i]["content"]), &data) != nil || data == nil {
			return map[string]any{}
		}
		return data
	}
	return map[string]any{}
}
