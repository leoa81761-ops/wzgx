package gen

import (
	"fmt"
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
	if why := structureProblem(a.Blocks); why != "" {
		return why
	}
	if last := lastTextBlock(a.Blocks); last != nil && last.Type == "p" {
		// closing brackets and quotes after the sentence end are fine: "…技能。]" "…。》"
		t := strings.TrimRight(strings.TrimSpace(last.Text), "]】》’'\" ")
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

// structureProblem blocks (never edits) an article with a block type the renderer cannot show,
// a content block with nothing in it, or a heading with no content before the next heading or the end.
func structureProblem(bs []render.Block) string {
	for i, b := range bs {
		if !render.Known(b.Type) {
			return "结构：无法识别的段落类型「" + b.Type + "」"
		}
		empty := false
		switch b.Type {
		case "h2", "p", "quote":
			empty = strings.TrimSpace(b.Text) == ""
		case "list":
			empty = len(b.Items) == 0
		case "table":
			empty = len(b.Rows) == 0
		}
		if empty {
			return fmt.Sprintf("结构：第 %d 段是空的", i+1)
		}
		if b.Type == "h2" {
			j := i + 1
			for j < len(bs) && bs[j].Type == "image" {
				j++
			}
			if j == len(bs) || bs[j].Type == "h2" {
				return "空小节：「" + strings.TrimSpace(b.Text) + "」下面没有内容"
			}
		}
	}
	return ""
}
