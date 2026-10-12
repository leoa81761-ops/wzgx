package llm

import (
	"encoding/json"
	"strings"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/content"
)

const lengthRule = "正文字数统计各小节的小标题、段落、列表、表格单元格和结尾，不含文章标题、摘要、参考来源和脚注。这个数字要落在 word_target 的 85% 到 130% 之间：不要写成短摘要，也不要明显写超。" +
	"每个小节最多四段，请把篇幅写进这些段落，把做法写具体。"

const sourcedFacts = "不得编造来源里没有的阈值、电压、标准编号或统计数字。没有来源支撑时写成定性说明，不要写具体数字。"

const citationRule = "用到 research_sources 里的事实时，在句末用 [1]、[2] 这种序号标出对应来源，序号从 1 开始，对应 sources 的顺序。没有用到的来源不要写进 sources。没有来源时不要编造 [n]。"

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
	if strings.TrimSpace(asString(payload["column_positioning"])) == "" {
		return ""
	}
	return "column_positioning 是栏目定位的全文，要整段遵守，不要只取前几个字。"
}

func voiceRule(payload map[string]any) string {
	if !technicalVoice(payload) {
		return ""
	}
	return "技术号可以用第一人称写现场里反复见到的规律，例如「我在现场见过这类情况」。这是经验模式，不是某一天编出来的事件，也不要写成某个有名有姓的人的原话。"
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
	case "资料检索":
		system = "你是资料检索助手。请立刻联网搜索，先用中文搜索，优先中文网页。" +
			"为下面的选题查找 3 到 5 条最近、相关、可公开核对的资料。" +
			"优先 gov.cn、samr.gov.cn、std.samr.gov.cn、openstd.samr.gov.cn、mohurd.gov.cn、国家标准、中国行业协会和主流媒体，不要用转载站、聚合站、文库镜像或内容农场。" +
			"不要网店、帮助台、登录页和购物车页面。同一网站最多两条。" +
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
	case "扩写":
		schema = StageSchema("正文")
		system = "你是公众号编辑。当前正文偏短。请在不编造政策、数据、人名和网址的前提下补写。" +
			"目标大约是 word_target 的 105%（输入里的 expand_goal）。写到 100% 到 110% 就停，不要为了凑到 130% 而注水。" +
			"如果输入里有 shortfall，这是离 expand_goal 还差的字数，请把这段篇幅补上。" +
			"正文字数统计各小节的小标题、段落、列表、表格单元格和结尾，不含文章标题、摘要、参考来源和脚注。" +
			sourcedFacts + anecdoteRule +
			"保留输入里已有的事实和 sources，不要新造网址。只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "缩写":
		schema = StageSchema("正文")
		system = "你是公众号编辑。当前正文偏长。请压缩到不少于 word_target、不超过 word_target 的 130%，删掉重复和空话，保留事实、步骤和 sources，不要新增材料或网址。" +
			"正文字数统计各小节的小标题、段落、列表、表格单元格和结尾，不含文章标题、摘要、参考来源和脚注。不要短于 word_target。" +
			sourcedFacts + readerRule +
			"只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "标题":
		system = "你是公众号标题编辑。请用输入里的语气，写 10 到 12 个彼此不同的标题。" +
			"每个标题不超过 28 个字，不要写成叙述句，不要用逗号铺成一段话。" +
			"每个标题都是完整的一句，不要为了凑字数截断，也不要在后面补套话。" +
			"不要用「震惊」「不转不是」「稳赚不赔」。不要靠关键词堆砌。" +
			"优先写具体场景或数字。每个标题都要自评分：hook、concrete、consistent、persona，都是 1 到 5 的整数。" +
			"consistent 表示标题和 summary、正文是不是同一件事，对不上就低于 3。自评分只用于淘汰对不上的标题，不要靠高分把长句排到前面。" +
			readerRule +
			"只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "结构润色":
		system = "你是公众号编辑。只改写输入里标记为需要改的部分：rewrite_hook、rewrite_headings、rewrite_closing 为 true 的才写。" +
			"没有要求改的部分返回空字符串或空数组。" +
			"不要删除已有段落，不要改标点和引用标记，不要新造数字或来源。" +
			"开头如果要改，只写不超过 40 个字的一句，用来替换原来的第一段，不要把两段开头叠在一起。和原开头重叠超过一半就返回空字符串。" +
			"小标题要能扫读，结尾留下一句收获并邀请读者留言。" +
			anecdoteRule + voiceRule(payload) + readerRule + sceneRule + tabooRule(payload) + columnRule(payload) +
			"只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "补引用":
		schema = StageSchema("正文")
		system = "你是公众号编辑。正文漏了引用序号。请对照 research_sources，在用到这些事实的句末补上 [1]、[2]，序号从 1 开始，对应来源顺序。" +
			"不要编造来源里没有的事实，不要新增网址，不要改已经写对的数字。没有用到的来源不要写进 sources。" +
			readerRule +
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
			readerRule +
			"只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "事实核对":
		schema = StageSchema("正文")
		system = "你是事实核对编辑。请对照 research_notes、research_sources 和可靠的领域常识，检查正文里的核心定义、阈值、标准编号和数字。" +
			"与来源或领域常识冲突的说法要直接改对。来源没有支撑、又不是稳定常识的数字、电压阈值和标准编号，删掉或改成不带具体数字的说法。" +
			"不要编造网址，也不要编造标准编号（包括 GB、GB/T）。research_sources 里没有的链接和标准号必须删掉，不能补一条看起来像真的网址或标准号。" +
			sourcedFacts +
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
		if stageLabel == "正文" || stageLabel == "审核" {
			system += lengthRule + citationRule + anecdoteRule + voiceRule(payload) + sceneRule + tabooRule(payload) + columnRule(payload)
		}
		system += readerRule
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
	case "选题":
		title := asString(payload["subtopic"])
		if title == "" {
			title = "实用方法"
		}
		angle := asString(payload["angle"])
		if angle == "" {
			angle = "一个小做法"
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
	case "扩写":
		draft, _ := payload["draft"].(map[string]any)
		if draft == nil {
			draft = draftFromPayload(payload)
		}
		return padDraft(draft, intOf(payload["word_target"]))
	case "缩写":
		draft, _ := payload["draft"].(map[string]any)
		if draft == nil {
			draft = draftFromPayload(payload)
		}
		target := intOf(payload["word_target"])
		if target < 1 {
			target = 800
		}
		return content.TrimToMax(draft, target*130/100)
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
	case "意象改写":
		return mockDraftCopy(payload)
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

func padDraft(draft map[string]any, target int) map[string]any {
	if target < 1 {
		target = 800
	}
	raw, _ := json.Marshal(draft)
	var cloned map[string]any
	if json.Unmarshal(raw, &cloned) != nil || cloned == nil {
		cloned = draftFromPayload(map[string]any{})
	}
	sections, _ := cloned["sections"].([]any)
	if len(sections) == 0 {
		cloned = draftFromPayload(map[string]any{})
		sections, _ = cloned["sections"].([]any)
	}
	last, _ := sections[len(sections)-1].(map[string]any)
	if last == nil {
		return cloned
	}
	paras, _ := last["paragraphs"].([]any)
	paras = append(paras, "")
	for i := 0; content.BodyRunes(cloned) < target && i < 400; i++ {
		text, _ := paras[len(paras)-1].(string)
		paras[len(paras)-1] = text + padLine(i)
		last["paragraphs"] = paras
		sections[len(sections)-1] = last
		cloned["sections"] = sections
	}
	return cloned
}

func padLine(i int) string {
	return strings.Repeat(string(rune(0x4E00+(i%400))), 8)
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
		lead := leads[i%len(leads)]
		if i == 0 {
			lead += "语气是" + style + "。"
		}
		section := map[string]any{
			"heading": heading,
			"paragraphs": []any{
				lead,
				follows[i%len(follows)],
			},
			"list": []any{"先做一个最小步骤", "做完后用一句话记下结果", "不追求一次到位"},
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
	return map[string]any{
		"title": title, "alt_titles": []any{subtopic + "可以先做的一件小事", "关于" + subtopic + "，少做一件事"},
		"summary":  "一篇关于" + direction + "／" + subtopic + "的短文，语气是" + style + "。",
		"sections": sections, "closing": closing, "sources": []any{},
	}
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
