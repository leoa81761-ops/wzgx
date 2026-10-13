package editorial

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestShapeCopyRepairsFormatting(t *testing.T) {
	runOn := strings.Repeat("综合布线验收先核对现场编号材料", 8)
	draft := map[string]any{
		"column_label": "方案拆解",
		"closing":      "先记下来。",
		"sections": []any{
			map[string]any{"heading": "观察日常细节", "paragraphs": []any{}},
			map[string]any{"heading": "做法", "paragraphs": []any{
				runOn,
				"您分享的故事让人想起家里的晚饭。下一句写现场编号。",
				"语气很温暖，也很温暖，还是温暖，再温暖一次。",
				"| 项目 | 说明 |\n| --- | --- |\n| 布线 | 先核对 |\n| 验收 | 看编号 |",
			}},
		},
	}
	out := ShapeCopy(draft)
	sections, _ := out["sections"].([]any)
	if len(sections) != 1 {
		t.Fatal(len(sections), sections)
	}
	section := sections[0].(map[string]any)
	paras, _ := section["paragraphs"].([]any)
	blob := ""
	for _, item := range paras {
		blob += item.(string)
	}
	if !strings.Contains(blob, "。") || utf8.RuneCountInString(strings.Split(blob, "。")[0]) > 40 {
		t.Fatal(blob)
	}
	if strings.Contains(blob, "您分享的") {
		t.Fatal(blob)
	}
	if strings.Count(blob, "温暖") > 2 {
		t.Fatal(blob)
	}
	table, _ := section["table"].(map[string]any)
	if table == nil {
		t.Fatal(section)
	}
	closing, _ := out["closing"].(string)
	if !strings.Contains(closing, "关注") || !strings.Contains(closing, "方案拆解") || !strings.Contains(closing, "评论") {
		t.Fatal(closing)
	}
}
