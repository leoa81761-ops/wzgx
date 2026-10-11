package llm

import "encoding/json"

// Schema 是某一写作环节要求模型输出的 JSON Schema。
type Schema struct {
	Name string
	Body map[string]any
}

func StageSchema(stageLabel string) *Schema {
	switch stageLabel {
	case "选题":
		return &Schema{Name: "topic", Body: objectSchema(
			[]string{"title", "angle"},
			map[string]any{
				"title": strField("文章标题，不超过 64 个字"),
				"angle": strField("这一篇具体要写的角度"),
			},
		)}
	case "大纲":
		return &Schema{Name: "outline", Body: objectSchema(
			[]string{"hook", "points", "closing"},
			map[string]any{
				"hook": strField("开头用来进入主题的一句话"),
				"points": map[string]any{
					"type":        "array",
					"description": "2 到 5 个小节",
					"items": objectSchema(
						[]string{"heading", "intent"},
						map[string]any{
							"heading": strField("小节标题"),
							"intent":  strField("这一节要让读者明白的事"),
						},
					),
				},
				"closing": strField("结尾收束"),
			},
		)}
	case "资料检索":
		return &Schema{Name: "research", Body: objectSchema(
			[]string{"notes", "sources"},
			map[string]any{
				"notes":   strField("检索到的事实摘要，概括 3 到 5 条来源分别说了什么"),
				"sources": sourceArrayField("3 到 5 条最近、相关、可核对的来源，每条都要有网址和简要说明"),
			},
		)}
	default:
		return &Schema{Name: "draft", Body: draftSchema()}
	}
}

func draftSchema() map[string]any {
	return objectSchema(
		[]string{"title", "alt_titles", "summary", "sections", "closing", "sources"},
		map[string]any{
			"title": strField("文章标题，不超过 64 个字"),
			"alt_titles": map[string]any{
				"type":        "array",
				"description": "备选标题，可以是空数组",
				"items":       map[string]any{"type": "string"},
			},
			"summary": strField("一两句摘要"),
			"sections": map[string]any{
				"type":        "array",
				"description": "正文小节",
				"items": objectSchema(
					[]string{"heading", "paragraphs", "list"},
					map[string]any{
						"heading": strField("小节标题"),
						"paragraphs": map[string]any{
							"type":  "array",
							"items": map[string]any{"type": "string"},
						},
						"list": map[string]any{
							"type":        "array",
							"description": "这一节的要点列表，没有则为空数组",
							"items":       map[string]any{"type": "string"},
						},
					},
				),
			},
			"closing": strField("结尾"),
			"sources": sourceArrayField("文末参考来源。没有外部资料时必须是空数组，不要编造网址"),
		},
	)
}

func sourceArrayField(desc string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": desc,
		"items": objectSchema(
			[]string{"name", "title", "published_on", "url", "source_type"},
			map[string]any{
				"name":         strField("来源名称，例如媒体或发文机关"),
				"title":        strField("资料标题"),
				"published_on": strField("发布日期，不知道就留空字符串"),
				"url":          strField("以 http:// 或 https:// 开头的网址，没有则留空字符串"),
				"source_type": map[string]any{
					"type": "string",
					"enum": []any{"news", "policy", "standard", "other"},
				},
			},
		),
	}
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	req := make([]any, len(required))
	for i, name := range required {
		req[i] = name
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             req,
		"properties":           properties,
	}
}

func strField(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func schemaText(schema *Schema) string {
	if schema == nil {
		return ""
	}
	raw, err := json.Marshal(schema.Body)
	if err != nil {
		return ""
	}
	return string(raw)
}
