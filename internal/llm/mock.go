package llm

import (
	"encoding/json"
	"strings"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/content"
)

const lengthRule = "正文字数只统计各小节的段落、列表和结尾，不含标题和摘要。这个数字要落在 word_target 的 85% 到 130% 之间：不要写成短摘要，也不要明显写超。" +
	"每个小节最多四段，请把篇幅写进这些段落，把做法写具体。"

const sourcedFacts = "不得编造来源里没有的阈值、电压、标准编号或统计数字。没有来源支撑时写成定性说明，不要写具体数字。"

func StageMessages(stageLabel string, payload map[string]any) []map[string]string {
	schema := StageSchema(stageLabel)
	var system string
	switch stageLabel {
	case "资料检索":
		system = "你是资料检索助手。请立刻联网搜索，为下面的选题查找 3 到 5 条最近、相关、可公开核对的资料。" +
			"每条都要有名称、标题、以 http:// 或 https:// 开头的真实网址、发布日期（知道再写）、类型（news、policy、standard、other 之一），以及一两句说明这条资料写了什么。" +
			"优先政府网站（.gov.cn）、标准组织、机构原文和主流媒体，不要用转载站、聚合站或内容农场。输入里的 blocked_domains 一律不要引用。" +
			"如果输入里有 allowed_domains，只引用这些域名及其子域名。" +
			"只写你实际搜到的网址，不要编造链接。请直接给出来源。如果确实搜不到，写明「未检索到可靠来源」。" +
			"可以用 JSON：{\"notes\":\"事实摘要\",\"sources\":[{\"name\":\"\",\"title\":\"\",\"published_on\":\"\",\"url\":\"\",\"source_type\":\"news\",\"note\":\"简要说明\"}]}。" +
			"也可以用 Markdown：每条写成 [标题](https://网址)，同一行或下一行写简要说明。"
	case "扩写":
		schema = StageSchema("正文")
		system = "你是公众号编辑。当前正文偏短。请在不编造政策、数据、人名和网址的前提下补写，使正文字数落到 word_target 的 85% 到 130% 之间，不要写超。" +
			lengthRule + sourcedFacts +
			"保留输入里已有的事实和 sources，不要新造网址。只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "缩写":
		schema = StageSchema("正文")
		system = "你是公众号编辑。当前正文偏长。请压缩到 word_target 的 85% 到 130% 之间，删掉重复和空话，保留事实、步骤和 sources，不要新增材料或网址。" +
			lengthRule + sourcedFacts +
			"只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	case "事实核对":
		schema = StageSchema("正文")
		system = "你是事实核对编辑。请对照 research_notes、research_sources 和可靠的领域常识，检查正文里的核心定义、阈值、标准编号和数字。" +
			"与来源或领域常识冲突的说法要直接改对。来源没有支撑、又不是稳定常识的数字、电压阈值和标准编号，删掉或改成不带具体数字的说法。" +
			sourcedFacts +
			"行业术语不要套用相邻概念。建筑智能化里的「弱电」指电子信息系统，包括通信、安防、楼宇自控、火灾报警和音视频，工作电压通常在安全特低电压（常见 36V 及以下）和信号电平，不是供配电里交流 1000V 或直流 1500V 以下的低压电力。不要把 1000V 写成弱电的定义。" +
			"保留原有结构、标题和 sources，不要新造网址。只输出一个 JSON 对象，不要使用 Markdown 代码块。\nJSON Schema：\n" + schemaText(schema)
	default:
		system = "你是公众号写作助手。只输出一个 JSON 对象，不要使用 Markdown 代码块，不要输出解释或 HTML。" +
			"字段必须和下面的 JSON Schema 完全一致，不要增加字段，字符串字段没有内容时用空字符串，数组没有内容时用空数组。" +
			"不得编造具体政策、标准、新闻事件、统计数据、人名引语。" + sourcedFacts +
			"输入里如果有 research_notes 或 research_sources，事实和 sources 只能来自这些材料，sources 必须保留这些来源，不要删掉网址。" +
			"不得使用「震惊」「不转不是」「稳赚不赔」等标题党或承诺性用语，也不得写药物剂量、诊断、偏方或收益承诺。" +
			"免费或无来源时 sources 必须是空数组。"
		if stageLabel == "正文" || stageLabel == "审核" {
			system += lengthRule
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
	content, _ := json.Marshal(MockForStage(stage, payload))
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
	default:
		return draftFromPayload(payload)
	}
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
	filler := "把这一步写得再具体一些：先看清眼前的材料，再做一个最小的动作，做完后用一句话记下结果，明天再决定要不要继续。"
	for content.BodyRunes(cloned) < target && len(sections) > 0 {
		last, _ := sections[len(sections)-1].(map[string]any)
		if last == nil {
			break
		}
		paras, _ := last["paragraphs"].([]any)
		if len(paras) >= 4 {
			if text, ok := paras[len(paras)-1].(string); ok {
				paras[len(paras)-1] = text + filler
			}
		} else {
			paras = append(paras, filler)
		}
		last["paragraphs"] = paras
		sections[len(sections)-1] = last
		cloned["sections"] = sections
		if content.BodyRunes(cloned) > target+fillerLen(filler)*2 {
			break
		}
	}
	return cloned
}

func fillerLen(s string) int {
	return len([]rune(s))
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
	for _, raw := range points {
		point, _ := raw.(map[string]any)
		heading := "一个小节"
		if point != nil && asString(point["heading"]) != "" {
			heading = asString(point["heading"])
		}
		section := map[string]any{
			"heading": heading,
			"paragraphs": []any{
				"这篇文章用「" + style + "」的语气来写" + direction + "里的" + subtopic + "。",
				"这一节顺着「" + angle + "」展开：把动作写小，读者才知道下一步做什么。",
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
		"summary":  "一篇关于" + direction + "／" + subtopic + "的短文，供你阅读后自己判断是否采用。",
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
