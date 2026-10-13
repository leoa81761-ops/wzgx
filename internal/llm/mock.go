package llm

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
)

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
		name := asString(payload["account_name"])
		column := asString(payload["column"])
		cands := []any{map[string]any{"title": full, "angle": angle, "pain": title}}
		for i := 2; i <= 5; i++ {
			extra := full
			if name != "" || column != "" {
				extra = strings.TrimSpace(name + " " + column + " " + title)
			}
			extra = strings.TrimSpace(extra + " " + strconv.Itoa(i))
			if len([]rune(extra)) > 64 {
				extra = string([]rune(extra)[:64])
			}
			cands = append(cands, map[string]any{"title": extra, "angle": angle, "pain": title})
		}
		return map[string]any{"title": full, "angle": angle, "candidates": cands}
	case "选题判断":
		winner := ""
		for _, item := range anySlice(payload["candidates"]) {
			row, _ := item.(map[string]any)
			if row == nil {
				winner = strings.TrimSpace(asString(item))
			} else {
				winner = strings.TrimSpace(asString(row["title"]))
			}
			if winner != "" {
				break
			}
		}
		if winner == "" {
			winner = asString(payload["title"])
		}
		return map[string]any{"winner": winner}
	case "检索词":
		topic := asString(payload["topic_title"])
		if topic == "" {
			topic = asString(payload["subtopic"])
		}
		return map[string]any{"queries": []any{topic, topic + " 现场", topic + " 步骤"}}
	case "大纲":
		label := asString(payload["topic_title"])
		if label == "" {
			label = asString(payload["subtopic"])
		}
		if label == "" {
			label = "这件事"
		}
		return map[string]any{
			"hook": "先从「" + label + "」里一个具体场景说起。",
			"points": []any{
				map[string]any{"heading": label + "要看清的问题", "intent": label, "need_table": false},
				map[string]any{"heading": label + "今天能做的一步", "intent": label, "need_table": true},
				map[string]any{"heading": label + "少踩的一个坑", "intent": label, "need_table": false},
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
	case "句子改写":
		return mockSentenceRewrite(payload)
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
		section["heading"] = "验收要先看哪一项"
		section["paragraphs"] = []any{"进场时先把图纸摊开，再对照现场的编号。"}
		section["qa"] = []any{
			map[string]any{"question": "验收要先看哪一项？", "answer": "先对照图纸核对编号。"},
			map[string]any{"question": "材料清单放在哪一步？", "answer": "放在进场之前核对。"},
			map[string]any{"question": "编号对不上怎么办？", "answer": "先停下再对照图纸。"},
			map[string]any{"question": "图纸和现场不一致时先改哪边？", "answer": "先改现场标记，再回头改图纸。"},
			map[string]any{"question": "收工前还要留什么？", "answer": "留下一份签过名的编号清单。"},
		}
	case "steps", "step-by-step", "breakdown", "拆解":
		section["paragraphs"] = []any{"1. 先看现场。", "2. 再核对编号。"}
	}
	sections[0] = section
	draft["sections"] = sections
	return draft
}

func mockSentenceRewrite(payload map[string]any) map[string]any {
	var items []any
	for _, raw := range anySlice(payload["sentences"]) {
		from := strings.TrimSpace(asString(raw))
		if from == "" {
			if row, ok := raw.(map[string]any); ok {
				from = strings.TrimSpace(asString(row["from"]))
			}
		}
		if from == "" {
			continue
		}
		items = append(items, map[string]any{"from": from, "to": "这一处按现场再看一遍，不写没有来源的数字。"})
	}
	if items == nil {
		items = []any{}
	}
	return map[string]any{"sentences": items}
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
		if json.Unmarshal([]byte(PayloadText(messages[i]["content"])), &data) != nil || data == nil {
			return map[string]any{}
		}
		return data
	}
	return map[string]any{}
}
