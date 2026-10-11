package llm

import (
	"encoding/json"
	"strings"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
)

func StageMessages(stageLabel string, payload map[string]any) []map[string]string {
	system := "你是公众号写作助手。只输出一个 JSON 对象，不要使用 Markdown 代码块，不要输出 HTML。" +
		"不得编造具体政策、标准、新闻事件、统计数据、人名引语。" +
		"不得使用「震惊」「不转不是」「稳赚不赔」等标题党或承诺性用语，也不得写药物剂量、诊断、偏方或收益承诺。" +
		"免费或无来源时不要给出参考网址。"
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
				map[string]any{"heading": "先看清眼前的问题", "intent": "用一个很小的场景说明读者为什么会卡住"},
				map[string]any{"heading": "一个今天就能做的做法", "intent": "给出可以立刻执行的步骤"},
				map[string]any{"heading": "少踩一个常见坑", "intent": "提醒一个容易做错的地方"},
			},
			"closing": "选一个最小的步骤，做完再决定要不要继续。",
		}
	case "审核":
		draft, _ := payload["draft"].(map[string]any)
		if draft == nil {
			draft = MockForStage("正文", payload)
		}
		return cleanDraft(draft)
	default:
		return draftFromPayload(payload)
	}
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
		sections = append(sections, map[string]any{
			"heading": heading,
			"paragraphs": []any{
				"这篇文章用「" + style + "」的语气来写" + direction + "里的" + subtopic + "。",
				"这一节顺着「" + angle + "」展开：把动作写小，读者才知道下一步做什么。",
			},
			"list": []any{"先做一个最小步骤", "做完后用一句话记下结果", "不追求一次到位"},
		})
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
