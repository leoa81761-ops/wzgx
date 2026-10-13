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
						[]string{"heading", "intent", "need_table"},
						map[string]any{
							"heading":     strField("小节标题"),
							"intent":      strField("这一节要让读者明白的事"),
							"need_table":  map[string]any{"type": "boolean", "description": "这一节用一张对照表会更清楚时为 true，否则 false"},
							"word_budget": map[string]any{"type": "integer", "description": "这一节的字数预算。各节 word_budget 相加必须等于 word_target"},
						},
					),
				},
				"closing": strField("结尾收束"),
			},
		)}
	case "来源判断":
		return &Schema{Name: "source_judge", Body: objectSchema(
			[]string{"items"},
			map[string]any{
				"items": map[string]any{
					"type":        "array",
					"description": "每条候选一个对象，index 与输入一致",
					"items": objectSchema(
						[]string{"index", "page_type", "on_topic", "snippet", "fact"},
						map[string]any{
							"index": map[string]any{"type": "integer", "description": "候选序号，从 1 开始"},
							"page_type": map[string]any{
								"type":        "string",
								"enum":        []any{"news", "official", "standard", "professional", "forum", "ad", "novel", "dictionary", "job", "ecommerce", "other"},
								"description": "页面类型。professional 是专业文章。只有 news、official、standard、professional 才可能保留",
							},
							"on_topic":    map[string]any{"type": "boolean", "description": "是不是在讲文章标题这件事。同一个部委、同一个域名，或只共享规范、标准、工程这类词，必须为 false"},
							"exact_topic": map[string]any{"type": "boolean", "description": "来源标题是否对得上文章标题，而不是只对上领域。拿不准就 false。可以省略"},
							"snippet":     strField("页面原文里连续出现的一句，用来支撑 fact，不要改写"),
							"fact":        strField("这句原句支撑的事实。没有就空字符串"),
							"score":       map[string]any{"type": "integer", "description": "和这篇选题的相关性，0 到 3"},
							"facts": map[string]any{
								"type":        "array",
								"description": "这条来源里能核对的关键事实。无关或没有事实时为空数组",
								"items":       map[string]any{"type": "string"},
							},
						},
					),
				},
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
	case "标题":
		return &Schema{Name: "titles", Body: objectSchema(
			[]string{"titles"},
			map[string]any{
				"titles": map[string]any{
					"type":        "array",
					"description": "10 到 12 个彼此不同的完整标题。每个标题自评分 hook、concrete、consistent、persona，都是 1 到 5",
					"items": objectSchema(
						[]string{"title", "hook", "concrete", "consistent", "persona"},
						map[string]any{
							"title":      strField("完整标题，不超过 28 个字，不要写成叙述句，不要截断，不要用套话补长度"),
							"hook":       scoreField("开头有没有具体场景或问题，1 到 5"),
							"concrete":   scoreField("有没有具体场景或数字，1 到 5"),
							"consistent": scoreField("和 summary、正文是不是同一件事，对不上就低于 3"),
							"persona":    scoreField("是否像这个账号的语气，1 到 5"),
						},
					),
				},
			},
		)}
	case "审核":
		return &Schema{Name: "review", Body: reviewSchema()}
	case "开头比较":
		return &Schema{Name: "hook_choice", Body: objectSchema(
			[]string{"choice"},
			map[string]any{
				"choice": map[string]any{
					"type":        "string",
					"enum":        []any{"original", "rewrite"},
					"description": "original 保留原来的第一段，rewrite 采用改写后的开头",
				},
			},
		)}
	case "标题比较":
		return &Schema{Name: "title_choice", Body: objectSchema(
			[]string{"winner"},
			map[string]any{
				"winner": strField("从候选标题里选出的那一个，必须和某一条候选完全相同"),
			},
		)}
	case "结构润色":
		return &Schema{Name: "polish", Body: objectSchema(
			[]string{"hook", "headings", "closing"},
			map[string]any{
				"hook": strField("只在要求改开头时给出新的开头一段，否则空字符串"),
				"headings": map[string]any{
					"type":        "array",
					"description": "只在要求改小标题时给出新的小标题，否则空数组",
					"items":       map[string]any{"type": "string"},
				},
				"closing": strField("只在要求改结尾时给出新的收获和邀请，否则空字符串"),
			},
		)}
	default:
		return &Schema{Name: "draft", Body: draftSchema()}
	}
}

func reviewSchema() map[string]any {
	body := draftSchema()
	props, _ := body["properties"].(map[string]any)
	props["quality"] = objectSchema(
		[]string{"hook", "headings", "closing", "motif"},
		map[string]any{
			"hook":     scoreField("开头钩子 1 到 5，4 分及以上表示不必再改开头"),
			"headings": map[string]any{"type": "boolean", "description": "小标题已经够扫读时为 true"},
			"closing":  map[string]any{"type": "boolean", "description": "结尾已有收获和邀请时为 true"},
			"motif":    map[string]any{"type": "boolean", "description": "同一句式或意象反复出现、需要改写时为 true"},
		},
	)
	required, _ := body["required"].([]any)
	body["required"] = append(required, "quality")
	return body
}

func scoreField(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
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
				"description": "正文小节。对照、步骤或参数适合用表格时，在 table.rows 里写 2 到 4 列；不需要表格时 rows 为空数组",
				"items": objectSchema(
					[]string{"heading", "paragraphs", "list", "table"},
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
						"table": objectSchema(
							[]string{"headers", "rows"},
							map[string]any{
								"headers": map[string]any{
									"type":        "array",
									"description": "2 到 4 列表头。这一节不需要表格时仍给两个表头",
									"items":       map[string]any{"type": "string"},
								},
								"rows": map[string]any{
									"type":        "array",
									"description": "表格的行。不需要表格时为空数组，程序不会画出来",
									"items": map[string]any{
										"type":  "array",
										"items": map[string]any{"type": "string"},
									},
								},
							},
						),
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
