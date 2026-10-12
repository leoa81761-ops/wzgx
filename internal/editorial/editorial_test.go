package editorial

import (
	"strings"
	"testing"
	"unicode"

	"github.com/leoa81761-ops/wzgx/internal/content"
)

func TestReshapeUsesExistingWordsAndStaysInsideTheBand(t *testing.T) {
	draft := map[string]any{
		"title": "短",
		"sections": []any{map[string]any{
			"heading":    "",
			"paragraphs": []any{"验收时先看线槽是否盖好。拍完留在当天的记录里。"},
		}},
		"closing": "",
	}
	if Score(draft) >= 4 {
		t.Fatal(Score(draft))
	}
	out, changed := Reshape(draft, 80)
	if !changed {
		t.Fatal("expected a reshape")
	}
	if content.BodyRunes(out) > 80 {
		t.Fatal(content.BodyRunes(out))
	}
	if Score(out) < Score(draft) {
		t.Fatal(Score(out))
	}
	raw := mustText(out)
	if strings.Contains(raw, "1000") || strings.Contains(raw, "GB") {
		t.Fatal(raw)
	}
	for _, r := range raw {
		if unicode.IsDigit(r) {
			t.Fatal("reshape invented a digit")
		}
	}
	if !strings.Contains(raw, "线槽") || !strings.Contains(raw, "欢迎留言") {
		t.Fatal(raw)
	}
	good := map[string]any{
		"sections": []any{
			map[string]any{"heading": "线槽", "paragraphs": []any{"先看盖板。例如上次就漏了。"}},
			map[string]any{"heading": "照片", "paragraphs": []any{"拍完留底。"}},
		},
		"closing": "所以可以先补这一项。欢迎留言说说你的做法。",
	}
	same, changed := Reshape(good, 200)
	if changed || Score(same) < 4 {
		t.Fatal(changed, Score(same))
	}
	if Structure("elv", "弱电") == Structure("family", "母婴") {
		t.Fatal("vertical structures collapsed")
	}
}

func mustText(draft map[string]any) string {
	var b strings.Builder
	for _, section := range sectionsOf(draft) {
		b.WriteString(textOf(section["heading"]))
		paras, _ := section["paragraphs"].([]any)
		for _, item := range paras {
			b.WriteString(textOf(item))
		}
	}
	b.WriteString(closingOf(draft))
	return b.String()
}
