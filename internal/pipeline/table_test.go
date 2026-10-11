package pipeline

import "testing"

func TestValidateDraftKeepsAShortTable(t *testing.T) {
	out, err := validateDraft(map[string]any{
		"title": "对照",
		"sections": []any{map[string]any{
			"heading": "甲", "paragraphs": []any{"正文"}, "list": []any{},
			"table": map[string]any{
				"headers": []any{"做法", "说明", "", "第三", "第四", "第五"},
				"rows":    []any{[]any{"先做", "记下"}, []any{"", "", ""}},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	section := out["sections"].([]any)[0].(map[string]any)
	table := section["table"].(map[string]any)
	headers := table["headers"].([]any)
	if len(headers) != 4 || headers[0] != "做法" {
		t.Fatal(headers)
	}
	rows := table["rows"].([]any)
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	plain, err := validateDraft(map[string]any{
		"title": "没有表",
		"sections": []any{map[string]any{
			"heading": "甲", "paragraphs": []any{"正文"}, "list": []any{},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plain["sections"].([]any)[0].(map[string]any)["table"]; ok {
		t.Fatal("missing table should stay omitted")
	}
}
