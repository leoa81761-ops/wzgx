package gen

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"gonghao/internal/render"
)

// The writer, revise and review steps reply in plain Markdown prose with a few header lines;
// parseArticle turns that into blocks so rendering, gates and push stay unchanged.
// A reply that is a JSON object (older prompts, mock) is still accepted.

const articleFormat = `输出格式（纯文本，不要 JSON，不要代码块）：
标题：文章标题
备选标题：备选一｜备选二
摘要：一两句摘要
（空一行后是正文，用 Markdown：## 小标题；自然段之间空一行；- 列表；| 表格 |；> 引用；需要配图的位置单独一行写 ![配图说明]()）`

var (
	fenceRe   = regexp.MustCompile("(?m)^```[a-zA-Z]*\\s*$")
	headRe    = regexp.MustCompile(`^#{1,6}\s+(.+)$`)
	listRe    = regexp.MustCompile(`^\s*(?:[-*+•]|\d+[.、)）])\s+(.+)$`)
	imageRe   = regexp.MustCompile(`^!\[([^\]]*)\]\([^)]*\)\s*$|^【配图[:：]?([^】]*)】$`)
	ruleRe    = regexp.MustCompile(`^\s*(?:-{3,}|\*{3,}|_{3,})\s*$`)
	tableSep  = regexp.MustCompile(`^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$`)
	metaRe    = regexp.MustCompile(`^\s*\**\s*(标题|备选标题|摘要)\s*\**\s*[:：]\s*(.*)$`)
	sepLineRe = regexp.MustCompile(`(?m)^\s*={3,}\s*$`)
)

func cleanReply(s string) string {
	s = thinkRe.ReplaceAllString(s, "")
	s = fenceRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// parseArticle reads an article reply (Markdown with header lines, or a JSON object).
func parseArticle(text string) (render.Article, error) {
	var a render.Article
	s := cleanReply(text)
	if strings.HasPrefix(s, "{") {
		if err := decodeJSON(s, &a); err == nil && len(a.Blocks) > 0 {
			return a, nil
		}
		if inner := wrappedText(s); inner != "" {
			return parseArticle(inner)
		}
		a = render.Article{}
	}
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	a.Title, lines = titleLine(lines)
	var para []string
	flush := func() {
		if len(para) > 0 {
			a.Blocks = append(a.Blocks, render.Block{Type: "p", Text: joinLines(para)})
			para = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		ln := strings.TrimSpace(lines[i])
		if m := metaRe.FindStringSubmatch(ln); m != nil && (len(a.Blocks) == 0 || m[1] != "摘要") && !(m[1] == "标题" && a.Title != "") {
			flush()
			v := strings.Trim(strings.TrimSpace(m[2]), "*")
			switch m[1] {
			case "标题":
				a.Title = strings.Trim(v, "《》 ")
			case "备选标题":
				for _, t := range strings.FieldsFunc(v, func(r rune) bool { return r == '｜' || r == '|' || r == '；' || r == ';' }) {
					if t = strings.Trim(strings.TrimSpace(t), "《》"); t != "" {
						a.AltTitles = append(a.AltTitles, t)
					}
				}
			case "摘要":
				a.Summary = v
			}
			continue
		}
		switch {
		case ln == "" || ruleRe.MatchString(ln):
			flush()
		case headRe.MatchString(ln):
			flush()
			h := strings.TrimSpace(headRe.FindStringSubmatch(ln)[1])
			if a.Title == "" && strings.HasPrefix(ln, "# ") && len(a.Blocks) == 0 {
				a.Title = strings.Trim(h, "*《》 ")
			} else {
				a.Blocks = append(a.Blocks, render.Block{Type: "h2", Text: strings.Trim(h, "* ")})
			}
		case imageRe.MatchString(ln):
			flush()
			m := imageRe.FindStringSubmatch(ln)
			a.Blocks = append(a.Blocks, render.Block{Type: "image", Caption: strings.TrimSpace(m[1] + m[2])})
		case strings.HasPrefix(ln, "|"):
			flush()
			var rows [][]string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
				r := strings.TrimSpace(lines[i])
				if tableSep.MatchString(r) {
					continue
				}
				r = strings.TrimSuffix(strings.TrimPrefix(r, "|"), "|")
				var cells []string
				for _, c := range strings.Split(r, "|") {
					cells = append(cells, strings.TrimSpace(c))
				}
				rows = append(rows, cells)
			}
			i--
			if len(rows) > 0 {
				a.Blocks = append(a.Blocks, render.Block{Type: "table", Rows: rows})
			}
		case listRe.MatchString(ln):
			flush()
			var items []string
			for ; i < len(lines); i++ {
				m := listRe.FindStringSubmatch(lines[i])
				if m == nil {
					break
				}
				items = append(items, strings.TrimSpace(m[1]))
			}
			i--
			a.Blocks = append(a.Blocks, render.Block{Type: "list", Items: items})
		case strings.HasPrefix(ln, ">"):
			flush()
			var q []string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">"); i++ {
				q = append(q, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">")))
			}
			i--
			a.Blocks = append(a.Blocks, render.Block{Type: "quote", Text: joinLines(q)})
		default:
			para = append(para, ln)
		}
	}
	flush()
	if a.Title == "" || len(a.Blocks) == 0 {
		return a, errors.New("reply has no title or body")
	}
	return a, nil
}

// joinLines joins wrapped lines of one paragraph; Chinese lines join without a space.
func joinLines(ls []string) string {
	var b strings.Builder
	for i, l := range ls {
		if i > 0 {
			prev, _ := utf8.DecodeLastRuneInString(b.String())
			next, _ := utf8.DecodeRuneInString(l)
			if prev < 0x2E80 && next < 0x2E80 {
				b.WriteByte(' ')
			}
		}
		b.WriteString(l)
	}
	return b.String()
}

// splitNotes splits a revise/review reply into the part before a ===== line (notes) and the article after it.
// Without the separator, the article starts at the 标题： line.
func splitNotes(text string) (notes []string, head, article string) {
	s := cleanReply(text)
	if loc := sepLineRe.FindStringIndex(s); loc != nil {
		head, article = s[:loc[0]], s[loc[1]:]
	} else if k := strings.Index(s, "标题："); k >= 0 {
		head, article = s[:k], s[k:]
	} else if k := strings.Index(s, "标题:"); k >= 0 {
		head, article = s[:k], s[k:]
	} else {
		head = s
	}
	for _, l := range strings.Split(head, "\n") {
		if m := listRe.FindStringSubmatch(l); m != nil {
			notes = append(notes, strings.TrimSpace(m[1]))
		}
	}
	return notes, head, strings.TrimSpace(article)
}

// rawNote is a trace note carrying the start of a reply that could not be read (at most 2000 characters).
func rawNote(prefix, text string) string {
	r := []rune(strings.TrimSpace(text))
	if len(r) > 2000 {
		r = append(r[:2000], '…')
	}
	return prefix + "; raw reply: " + string(r)
}

// articleMarkdown writes an article in the same plain format the model is asked to reply in.
func articleMarkdown(a render.Article) string {
	var b strings.Builder
	b.WriteString("标题：" + a.Title + "\n")
	if len(a.AltTitles) > 0 {
		b.WriteString("备选标题：" + strings.Join(a.AltTitles, "｜") + "\n")
	}
	if a.Summary != "" {
		b.WriteString("摘要：" + a.Summary + "\n")
	}
	for _, bl := range a.Blocks {
		b.WriteString("\n")
		switch bl.Type {
		case "h2":
			b.WriteString("## " + bl.Text + "\n")
		case "list":
			for _, it := range bl.Items {
				b.WriteString("- " + it + "\n")
			}
		case "table":
			for i, r := range bl.Rows {
				b.WriteString("| " + strings.Join(r, " | ") + " |\n")
				if i == 0 {
					b.WriteString("|" + strings.Repeat(" --- |", len(r)) + "\n")
				}
			}
		case "quote":
			b.WriteString("> " + bl.Text + "\n")
		case "image":
			b.WriteString("![" + bl.Caption + "]()\n")
		default:
			b.WriteString(bl.Text + "\n")
		}
	}
	return b.String()
}

func jsonPass(s string) bool {
	var v struct {
		Pass *bool `json:"pass"`
	}
	return strings.HasPrefix(strings.TrimSpace(s), "{") && decodeJSON(s, &v) == nil && v.Pass != nil && *v.Pass
}

// proseStage: steps whose reply is free text (Markdown article or research notes), never JSON mode.
func proseStage(stage string) bool {
	switch stage {
	case "research", "scan", "write", "revise", "review":
		return true
	}
	return false
}

// wrappedText unwraps a JSON shell around a prose reply, e.g. {"content":"标题：…"} or {"article":"…"}.
func wrappedText(s string) string {
	var m map[string]any
	if decodeJSON(s, &m) != nil {
		return ""
	}
	for _, k := range []string{"content", "article", "text", "markdown", "body", "output", "result"} {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

var boldLineRe = regexp.MustCompile(`^\*\*(.+?)\*\*$`)

// titleLine: when the reply has no 标题： label, its first non-empty line is the title if it is bold
// (**…**), a # heading, or a short bare line without sentence-ending punctuation. That line is removed.
func titleLine(lines []string) (string, []string) {
	for _, l := range lines {
		if m := metaRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil && m[1] == "标题" {
			return "", lines
		}
	}
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		var title string
		switch {
		case boldLineRe.MatchString(t):
			title = boldLineRe.FindStringSubmatch(t)[1]
		case strings.HasPrefix(t, "# "):
			title = strings.TrimPrefix(t, "# ")
		case utf8.RuneCountInString(t) <= 40 && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "{") && !strings.HasPrefix(t, "|") && !strings.HasPrefix(t, ">") &&
			!listRe.MatchString(t) && !imageRe.MatchString(t) && !strings.ContainsAny(lastRune(t), "。！？!?；;：:，,"):
			title = t
		default:
			return "", lines
		}
		title = strings.Trim(strings.TrimSpace(title), "*《》 ")
		if title == "" {
			return "", lines
		}
		return title, lines[i+1:]
	}
	return "", lines
}

func lastRune(s string) string {
	r, _ := utf8.DecodeLastRuneInString(s)
	return string(r)
}
