package gen

import (
	"regexp"
	"strings"

	"gonghao/internal/render"
)

// Hard gates only block; they never edit the text.

var junkRe = regexp.MustCompile(`(?i)(<\|[a-z_]*\|>|</?think>|<\/s>|\[/?INST\]|` + "```" + `|\{\s*"(type|blocks)"\s*:|as an ai|作为(一个)?(ai|人工智能)(语言)?模型|系统提示词|system prompt)`)

var endPunct = "。！？!?…”」』）)."

// Gate returns "" if the article may be delivered, else the blocking reason.
func Gate(a render.Article, finish string, sensitive []string) string {
	if finish == "length" {
		return "截断：模型输出达到长度上限"
	}
	if strings.TrimSpace(a.Title) == "" {
		return "空：没有标题"
	}
	var all strings.Builder
	all.WriteString(a.Title + "\n" + a.Summary + "\n")
	bodyChars := 0
	for _, b := range a.Blocks {
		t := b.Text + " " + strings.Join(b.Items, " ") + " " + b.Caption
		for _, r := range b.Rows {
			t += " " + strings.Join(r, " ")
		}
		if b.Type != "h2" && b.Type != "image" {
			bodyChars += len([]rune(strings.TrimSpace(t)))
		}
		all.WriteString(t + "\n")
	}
	if bodyChars == 0 {
		return "空：没有正文"
	}
	if last := lastTextBlock(a.Blocks); last != nil && last.Type == "p" {
		t := strings.TrimSpace(last.Text)
		r := []rune(t)
		if len(r) > 0 && !strings.ContainsRune(endPunct, r[len(r)-1]) {
			return "截断：正文最后一段没有写完"
		}
	}
	text := all.String()
	if m := junkRe.FindString(text); m != "" {
		return "异常内容：正文里出现模型控制符或说明文字（" + m + "）"
	}
	low := strings.ToLower(text)
	for _, w := range sensitive {
		if w != "" && strings.Contains(low, strings.ToLower(w)) {
			return "敏感词：" + w
		}
	}
	return ""
}

func lastTextBlock(bs []render.Block) *render.Block {
	for i := len(bs) - 1; i >= 0; i-- {
		if bs[i].Type != "image" {
			return &bs[i]
		}
	}
	return nil
}
