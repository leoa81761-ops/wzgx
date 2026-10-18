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
	Layout                     string         // layout key; empty = classic
}

type Output struct {
	HTML      string
	Cited     []Source // official sources actually cited, in order of first use
	Tables    int
	Images    int
	PlainText string
}

// Layout is a set of inline styles. The layout changes only how the article looks; the words,
// the order of blocks and the table / image decisions stay the same.
type Layout struct {
	Key, Name, Note                                             string
	H2, P, List, Quote, Table, Td, Th, Img, Small, Caption, Sig string
}

// Layouts: a few clean templates; "classic" is the default and matches the original look.
var Layouts = []Layout{
	{Key: "classic", Name: "经典", Note: "黑灰正文，简洁小标题，适合大多数号",
		H2:      `margin:28px 0 12px;font-size:18px;font-weight:bold;color:#222;`,
		P:       `margin:0 0 16px;font-size:16px;line-height:1.8;color:#333;`,
		List:    `margin:0 0 16px;padding-left:22px;font-size:16px;line-height:1.8;color:#333;`,
		Quote:   `margin:0 0 16px;padding:10px 14px;border-left:3px solid #ccc;background:#f7f7f7;color:#555;font-size:15px;line-height:1.7;`,
		Table:   `width:100%;border-collapse:collapse;margin:0 0 16px;font-size:14px;`,
		Td:      `border:1px solid #ddd;padding:6px 8px;text-align:left;vertical-align:top;`,
		Th:      `border:1px solid #ddd;padding:6px 8px;text-align:left;background:#f2f2f2;font-weight:bold;`,
		Img:     `margin:0 0 16px;text-align:center;`,
		Small:   `margin:24px 0 0;font-size:13px;line-height:1.7;color:#888;`,
		Caption: `font-size:13px;color:#999;margin:6px 0 0;`,
		Sig:     `text-align:right;color:#666;`},
	{Key: "fresh", Name: "清新", Note: "绿色小标题竖条，适合生活、健康类",
		H2:      `margin:30px 0 14px;padding-left:10px;border-left:4px solid #3a9d6a;font-size:18px;font-weight:bold;color:#1f5f3f;line-height:1.4;`,
		P:       `margin:0 0 16px;font-size:16px;line-height:1.85;color:#333;letter-spacing:0.5px;`,
		List:    `margin:0 0 16px;padding-left:22px;font-size:16px;line-height:1.85;color:#333;`,
		Quote:   `margin:0 0 16px;padding:12px 14px;border-radius:6px;background:#eef7f1;color:#2f5d45;font-size:15px;line-height:1.75;`,
		Table:   `width:100%;border-collapse:collapse;margin:0 0 16px;font-size:14px;`,
		Td:      `border:1px solid #d5e9dc;padding:7px 8px;text-align:left;vertical-align:top;`,
		Th:      `border:1px solid #d5e9dc;padding:7px 8px;text-align:left;background:#e3f2e8;color:#1f5f3f;font-weight:bold;`,
		Img:     `margin:0 0 18px;text-align:center;`,
		Small:   `margin:26px 0 0;font-size:13px;line-height:1.7;color:#8a9a90;`,
		Caption: `font-size:13px;color:#8a9a90;margin:6px 0 0;`,
		Sig:     `text-align:right;color:#3a9d6a;`},
	{Key: "warm", Name: "暖色", Note: "暖橙色居中小标题，适合家庭、情感类",
		H2:      `margin:32px 0 16px;padding-bottom:6px;border-bottom:2px solid #e8a65d;text-align:center;font-size:18px;font-weight:bold;color:#8a4b14;`,
		P:       `margin:0 0 16px;font-size:16px;line-height:1.9;color:#3d3530;`,
		List:    `margin:0 0 16px;padding-left:22px;font-size:16px;line-height:1.9;color:#3d3530;`,
		Quote:   `margin:0 0 16px;padding:12px 16px;background:#fdf3e7;border-left:3px solid #e8a65d;color:#6b4a2b;font-size:15px;line-height:1.8;`,
		Table:   `width:100%;border-collapse:collapse;margin:0 0 16px;font-size:14px;`,
		Td:      `border:1px solid #f0dcc4;padding:7px 8px;text-align:left;vertical-align:top;`,
		Th:      `border:1px solid #f0dcc4;padding:7px 8px;text-align:left;background:#fbe9d4;color:#8a4b14;font-weight:bold;`,
		Img:     `margin:0 0 18px;text-align:center;`,
		Small:   `margin:26px 0 0;font-size:13px;line-height:1.7;color:#a08c7a;`,
		Caption: `font-size:13px;color:#a08c7a;margin:6px 0 0;`,
		Sig:     `text-align:center;color:#8a4b14;`},
	{Key: "plain", Name: "素雅", Note: "大留白、深灰细字，适合资讯、技术类",
		H2:      `margin:34px 0 14px;font-size:17px;font-weight:bold;color:#111;letter-spacing:1px;`,
		P:       `margin:0 0 18px;font-size:15px;line-height:2;color:#444;letter-spacing:0.5px;`,
		List:    `margin:0 0 18px;padding-left:20px;font-size:15px;line-height:2;color:#444;`,
		Quote:   `margin:0 0 18px;padding:4px 0 4px 14px;border-left:2px solid #999;color:#666;font-size:15px;line-height:1.9;`,
		Table:   `width:100%;border-collapse:collapse;margin:0 0 18px;font-size:14px;`,
		Td:      `border-bottom:1px solid #e5e5e5;padding:8px 6px;text-align:left;vertical-align:top;`,
		Th:      `border-bottom:2px solid #333;padding:8px 6px;text-align:left;font-weight:bold;color:#111;`,
		Img:     `margin:0 0 20px;text-align:center;`,
		Small:   `margin:28px 0 0;font-size:12px;line-height:1.8;color:#999;`,
		Caption: `font-size:12px;color:#999;margin:6px 0 0;`,
		Sig:     `text-align:right;color:#777;`},
}

// LayoutByKey returns the layout, or the default one for an unknown key.
func LayoutByKey(k string) Layout {
	for _, l := range Layouts {
		if l.Key == k {
			return l
		}
	}
	return Layouts[0]
}

// ValidLayout reports whether k names a layout.
func ValidLayout(k string) bool {
	for _, l := range Layouts {
		if l.Key == k {
			return true
		}
	}
	return false
}

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
	L := LayoutByKey(o.Layout)

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
			fmt.Fprintf(&b, `<p style="%s">%s</p>`, L.P, inline(s))
		}
	}
	image := func(caption string) {
		if o.DefaultImageURL == "" || out.Images >= 3 {
			return
		}
		out.Images++
		fmt.Fprintf(&b, `<section style="%s"><img src="%s" alt="%s" style="max-width:100%%;"/>`, L.Img, html.EscapeString(o.DefaultImageURL), html.EscapeString(caption))
		if caption != "" {
			fmt.Fprintf(&b, `<p style="%s">%s</p>`, L.Caption, html.EscapeString(caption))
		}
		b.WriteString(`</section>`)
	}
	table := func(rows [][]string, header bool) {
		out.Tables++
		fmt.Fprintf(&b, `<table style="%s">`, L.Table)
		for i, r := range rows {
			b.WriteString("<tr>")
			for _, c := range r {
				st, tag := L.Td, "td"
				if header && i == 0 {
					st, tag = L.Th, "th"
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
				fmt.Fprintf(&b, `<h2 style="%s">%s</h2>`, L.H2, inline(t))
			}
		case "list":
			if rows, ok := TableFit(bl.Items); ok {
				table(append([][]string{{"项目", "说明"}}, rows...), true)
				continue
			}
			fmt.Fprintf(&b, `<ul style="%s">`, L.List)
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
				fmt.Fprintf(&b, `<ul style="%s">`, L.List)
				for _, r := range bl.Rows {
					fmt.Fprintf(&b, `<li>%s</li>`, inline(strings.Join(r, "，")))
				}
				b.WriteString(`</ul>`)
			}
		case "quote":
			fmt.Fprintf(&b, `<blockquote style="%s">%s</blockquote>`, L.Quote, inline(bl.Text))
		case "image":
			image(bl.Caption)
		default:
			para(bl.Text)
		}
	}
	para(o.Ending)
	if s := strings.TrimSpace(o.Signature); s != "" {
		fmt.Fprintf(&b, `<p style="%s%s">%s</p>`, L.P, L.Sig, html.EscapeString(s))
	}
	if len(out.Cited) > 0 {
		fmt.Fprintf(&b, `<section style="%s"><p style="margin:0 0 4px;">依据</p>`, L.Small)
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
		fmt.Fprintf(&b, `<p style="%s">本文由 AI 辅助生成</p>`, L.Small)
	}
	out.HTML = b.String()
	out.PlainText = plain.String()
	return out
}

// PreviewPage wraps rendered HTML in a phone-width page for local preview. dark approximates
// WeChat's dark mode, which recolours the article itself: light backgrounds turn dark and dark text
// turns light, while pictures keep their colours.
func PreviewPage(title, body string, dark bool) string {
	bg, h1 := "#fff", "#111"
	wrap := ""
	if dark {
		bg = "#191919"
		wrap = `<style>.dk{filter:invert(0.9) hue-rotate(180deg);background:#fff}.dk img{filter:invert(1) hue-rotate(180deg)}</style>`
	}
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + html.EscapeString(title) + `</title>` + wrap + `</head><body style="margin:0;background:` + bg + `;">` +
		`<div class="dk" style="max-width:420px;margin:0 auto;padding:20px 16px;">` +
		`<h1 style="font-size:22px;line-height:1.4;margin:0 0 20px;color:` + h1 + `;">` + html.EscapeString(title) + `</h1>` + body + `</div></body></html>`
}
