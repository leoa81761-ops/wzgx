// Package render turns the structured article into WeChat-safe HTML with inline styles.
//
// The renderer, not the model, decides layout details: whether a list reads better as a table,
// where image slots go (filled with the default cover for now), and it appends the account's
// opening, ending, signature and optional AI label. Citations are kept only for official bases
// (national standards, government red-header documents); other [n] markers are dropped.
package render

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

type Block struct {
	Type    string     `json:"type"` // h2 | p | list | table | quote | image
	Text    string     `json:"text,omitempty"`
	Items   []string   `json:"items,omitempty"`
	Rows    [][]string `json:"rows,omitempty"`
	Caption string     `json:"caption,omitempty"`
}

type Article struct {
	Title     string   `json:"title"`
	AltTitles []string `json:"alt_titles"`
	Summary   string   `json:"summary"`
	Blocks    []Block  `json:"blocks"`
}

type Source struct {
	N         int    `json:"n"`
	Kind      string `json:"kind"` // national_standard | gov_document
	Title     string `json:"title"`
	Issuer    string `json:"issuer,omitempty"`
	DocNumber string `json:"doc_number,omitempty"`
	Date      string `json:"date,omitempty"`
	URL       string `json:"url,omitempty"`
}

type Options struct {
	Opening, Ending, Signature string
	AILabel                    bool
	DefaultImageURL            string
	Sources                    map[int]Source // official sources only, by evidence number
}

type Output struct {
	HTML      string
	Cited     []Source // official sources actually cited, in order of first use
	Tables    int
	Images    int
	PlainText string
}

const (
	stH2    = `margin:28px 0 12px;font-size:18px;font-weight:bold;color:#222;`
	stP     = `margin:0 0 16px;font-size:16px;line-height:1.8;color:#333;`
	stList  = `margin:0 0 16px;padding-left:22px;font-size:16px;line-height:1.8;color:#333;`
	stQuote = `margin:0 0 16px;padding:10px 14px;border-left:3px solid #ccc;background:#f7f7f7;color:#555;font-size:15px;line-height:1.7;`
	stTable = `width:100%;border-collapse:collapse;margin:0 0 16px;font-size:14px;`
	stTd    = `border:1px solid #ddd;padding:6px 8px;text-align:left;vertical-align:top;`
	stTh    = `border:1px solid #ddd;padding:6px 8px;text-align:left;background:#f2f2f2;font-weight:bold;`
	stImg   = `margin:0 0 16px;text-align:center;`
	stSmall = `margin:24px 0 0;font-size:13px;line-height:1.7;color:#888;`
)

var citeRe = regexp.MustCompile(`\[(\d{1,2})\]`)

// kvRe matches "要点：说明" list items, which read better as a two-column table.
var kvRe = regexp.MustCompile(`^\s*([^：:]{1,16})[：:]\s*(.{2,})$`)

// TableFit reports whether a list should be shown as a two-column table: at least 3 items and
// every item is a short label followed by a colon and an explanation.
func TableFit(items []string) ([][]string, bool) {
	if len(items) < 3 {
		return nil, false
	}
	rows := make([][]string, 0, len(items))
	for _, it := range items {
		m := kvRe.FindStringSubmatch(it)
		if m == nil {
			return nil, false
		}
		rows = append(rows, []string{strings.TrimSpace(m[1]), strings.TrimSpace(m[2])})
	}
	return rows, true
}

// validTable: at least header + 1 row, 2–6 columns, all rows the same width.
func validTable(rows [][]string) bool {
	if len(rows) < 2 || len(rows[0]) < 2 || len(rows[0]) > 6 {
		return false
	}
	for _, r := range rows {
		if len(r) != len(rows[0]) {
			return false
		}
	}
	return true
}

func Render(a Article, o Options) Output {
	var b strings.Builder
	out := Output{}
	cited := map[int]bool{}
	var plain strings.Builder

	inline := func(s string) string {
		plain.WriteString(s + "\n")
		esc := html.EscapeString(strings.TrimSpace(s))
		return citeRe.ReplaceAllStringFunc(esc, func(m string) string {
			var n int
			fmt.Sscanf(m, "[%d]", &n)
			src, ok := o.Sources[n]
			if !ok {
				return "" // not an official basis: no citation
			}
			if !cited[n] {
				cited[n] = true
				out.Cited = append(out.Cited, src)
			}
			return fmt.Sprintf(`<sup style="color:#888;">[%d]</sup>`, n)
		})
	}
	para := func(s string) {
		if strings.TrimSpace(s) != "" {
			fmt.Fprintf(&b, `<p style="%s">%s</p>`, stP, inline(s))
		}
	}
	image := func(caption string) {
		if o.DefaultImageURL == "" || out.Images >= 3 {
			return
		}
		out.Images++
		fmt.Fprintf(&b, `<section style="%s"><img src="%s" alt="%s" style="max-width:100%%;"/>`, stImg, html.EscapeString(o.DefaultImageURL), html.EscapeString(caption))
		if caption != "" {
			fmt.Fprintf(&b, `<p style="font-size:13px;color:#999;margin:6px 0 0;">%s</p>`, html.EscapeString(caption))
		}
		b.WriteString(`</section>`)
	}
	table := func(rows [][]string, header bool) {
		out.Tables++
		fmt.Fprintf(&b, `<table style="%s">`, stTable)
		for i, r := range rows {
			b.WriteString("<tr>")
			for _, c := range r {
				st, tag := stTd, "td"
				if header && i == 0 {
					st, tag = stTh, "th"
				}
				fmt.Fprintf(&b, `<%s style="%s">%s</%s>`, tag, st, inline(c), tag)
			}
			b.WriteString("</tr>")
		}
		b.WriteString("</table>")
	}

	para(o.Opening)
	hasImage := false
	for _, bl := range a.Blocks {
		if bl.Type == "image" {
			hasImage = true
		}
	}
	sections := 0
	for _, bl := range a.Blocks {
		switch bl.Type {
		case "h2":
			sections++
			// with no image proposed by the writer, place one before the second section of a longer piece
			if !hasImage && sections == 2 && len(a.Blocks) >= 6 {
				image("")
			}
			if t := strings.TrimSpace(bl.Text); t != "" {
				fmt.Fprintf(&b, `<h2 style="%s">%s</h2>`, stH2, inline(t))
			}
		case "list":
			if rows, ok := TableFit(bl.Items); ok {
				table(append([][]string{{"项目", "说明"}}, rows...), true)
				continue
			}
			fmt.Fprintf(&b, `<ul style="%s">`, stList)
			for _, it := range bl.Items {
				if strings.TrimSpace(it) != "" {
					fmt.Fprintf(&b, `<li>%s</li>`, inline(it))
				}
			}
			b.WriteString(`</ul>`)
		case "table":
			if validTable(bl.Rows) {
				table(bl.Rows, true)
			} else { // not a real table: show as a list
				fmt.Fprintf(&b, `<ul style="%s">`, stList)
				for _, r := range bl.Rows {
					fmt.Fprintf(&b, `<li>%s</li>`, inline(strings.Join(r, "，")))
				}
				b.WriteString(`</ul>`)
			}
		case "quote":
			fmt.Fprintf(&b, `<blockquote style="%s">%s</blockquote>`, stQuote, inline(bl.Text))
		case "image":
			image(bl.Caption)
		default:
			para(bl.Text)
		}
	}
	para(o.Ending)
	if s := strings.TrimSpace(o.Signature); s != "" {
		fmt.Fprintf(&b, `<p style="%stext-align:right;color:#666;">%s</p>`, stP, html.EscapeString(s))
	}
	if len(out.Cited) > 0 {
		fmt.Fprintf(&b, `<section style="%s"><p style="margin:0 0 4px;">依据</p>`, stSmall)
		for _, s := range out.Cited {
			line := s.Title
			if s.DocNumber != "" && !strings.Contains(line, s.DocNumber) {
				line = s.DocNumber + " " + line
			}
			if r := []rune(line); len(r) > 60 {
				line = string(r[:60]) + "…"
			}
			if s.Issuer != "" {
				line += "（" + s.Issuer + "）"
			}
			fmt.Fprintf(&b, `<p style="margin:0;">[%d] %s</p>`, s.N, html.EscapeString(line))
		}
		b.WriteString(`</section>`)
	}
	if o.AILabel {
		fmt.Fprintf(&b, `<p style="%s">本文由 AI 辅助生成</p>`, stSmall)
	}
	out.HTML = b.String()
	out.PlainText = plain.String()
	return out
}

// PreviewPage wraps rendered HTML in a phone-width page for local preview.
func PreviewPage(title, body string) string {
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + html.EscapeString(title) + `</title></head><body style="margin:0;background:#fff;">` +
		`<div style="max-width:420px;margin:0 auto;background:#fff;padding:20px 16px;">` +
		`<h1 style="font-size:22px;line-height:1.4;margin:0 0 20px;color:#111;">` + html.EscapeString(title) + `</h1>` + body + `</div></body></html>`
}
