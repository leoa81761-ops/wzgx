package render

import (
	"html"
	"strings"
	"unicode/utf8"
)

var themes = map[string]map[string]string{
	"simple": {
		"wrap":  "font-size:16px;line-height:1.75;color:#1f2933;",
		"h2":    "font-size:18px;line-height:1.45;font-weight:700;margin:28px 0 12px;color:#1f2933;",
		"p":     "margin:0 0 14px;font-size:16px;line-height:1.75;color:#1f2933;",
		"lead":  "margin:0 0 18px;font-size:15px;line-height:1.7;color:#5c5148;",
		"ul":    "margin:0 0 16px;padding-left:1.2em;",
		"li":    "margin:0 0 8px;line-height:1.7;color:#1f2933;",
		"hr":    "border:none;border-top:1px solid #e6dccf;margin:22px 0;",
		"src_h": "font-size:15px;font-weight:700;margin:8px 0;color:#1f2933;",
		"src":   "font-size:13px;line-height:1.65;color:#6b6258;margin:0 0 8px;",
		"foot":  "font-size:13px;line-height:1.65;color:#8a8178;margin:8px 0 0;",
	},
	"fresh": {
		"wrap":  "font-size:16px;line-height:1.75;color:#24312c;",
		"h2":    "font-size:18px;line-height:1.45;font-weight:700;margin:28px 0 12px;color:#1f6a4d;",
		"p":     "margin:0 0 14px;font-size:16px;line-height:1.75;color:#24312c;",
		"lead":  "margin:0 0 18px;font-size:15px;line-height:1.7;color:#3e5c50;",
		"ul":    "margin:0 0 16px;padding-left:1.2em;",
		"li":    "margin:0 0 8px;line-height:1.7;color:#24312c;",
		"hr":    "border:none;border-top:1px solid #d5e6dc;margin:22px 0;",
		"src_h": "font-size:15px;font-weight:700;margin:8px 0;color:#1f6a4d;",
		"src":   "font-size:13px;line-height:1.65;color:#4d675c;margin:0 0 8px;",
		"foot":  "font-size:13px;line-height:1.65;color:#6d7f76;margin:8px 0 0;",
	},
	"business": {
		"wrap":  "font-size:16px;line-height:1.75;color:#1c2834;",
		"h2":    "font-size:18px;line-height:1.45;font-weight:700;margin:28px 0 12px;color:#1e3a5f;",
		"p":     "margin:0 0 14px;font-size:16px;line-height:1.75;color:#1c2834;",
		"lead":  "margin:0 0 18px;font-size:15px;line-height:1.7;color:#3d4d60;",
		"ul":    "margin:0 0 16px;padding-left:1.2em;",
		"li":    "margin:0 0 8px;line-height:1.7;color:#1c2834;",
		"hr":    "border:none;border-top:1px solid #d5deea;margin:22px 0;",
		"src_h": "font-size:15px;font-weight:700;margin:8px 0;color:#1e3a5f;",
		"src":   "font-size:13px;line-height:1.65;color:#546274;margin:0 0 8px;",
		"foot":  "font-size:13px;line-height:1.65;color:#6a7684;margin:8px 0 0;",
	},
}

func DisplayURL(url string) string {
	return strings.ReplaceAll(url, ".", "点")
}

func cloneTheme(key string) map[string]string {
	src := themes[key]
	if src == nil {
		src = themes["simple"]
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func RenderHTML(article map[string]any, templateKey string, dark, compact bool) string {
	theme := cloneTheme(templateKey)
	if compact {
		theme["p"] = "margin:0 0 10px;font-size:15px;line-height:1.65;color:#1f2933;"
		theme["h2"] = "font-size:17px;font-weight:700;margin:16px 0 8px;color:#1f2933;"
	}
	if dark {
		theme["wrap"] = "font-size:16px;line-height:1.75;color:#f5f0e8;background:#1c1917;padding:12px;"
		theme["h2"] = strings.NewReplacer("#1f2933", "#e7b8a8", "#1f6a4d", "#e7b8a8", "#1e3a5f", "#e7b8a8").Replace(theme["h2"])
		theme["p"] = "margin:0 0 14px;font-size:16px;line-height:1.75;color:#f5f0e8;"
		theme["lead"] = "margin:0 0 18px;font-size:15px;line-height:1.7;color:#d9cfc3;"
		theme["li"] = "margin:0 0 8px;line-height:1.7;color:#f5f0e8;"
		theme["hr"] = "border:none;border-top:1px solid #3a332c;margin:22px 0;"
		theme["src_h"] = "font-size:15px;font-weight:700;margin:8px 0;color:#e7b8a8;"
		theme["src"] = "font-size:13px;line-height:1.65;color:#d9cfc3;margin:0 0 8px;"
		theme["foot"] = "font-size:13px;line-height:1.65;color:#d9cfc3;margin:8px 0 0;"
	}
	var parts []string
	parts = append(parts, `<section style="`+theme["wrap"]+`">`)
	if summary := strings.TrimSpace(asString(article["summary"])); summary != "" {
		parts = append(parts, `<p style="`+theme["lead"]+`">`+html.EscapeString(summary)+`</p>`)
	}
	for _, raw := range asList(article["sections"]) {
		section, _ := raw.(map[string]any)
		if section == nil {
			continue
		}
		if heading := strings.TrimSpace(asString(section["heading"])); heading != "" {
			parts = append(parts, `<h2 style="`+theme["h2"]+`">`+html.EscapeString(heading)+`</h2>`)
		}
		for _, paragraph := range asList(section["paragraphs"]) {
			text := strings.TrimSpace(asString(paragraph))
			if text != "" {
				parts = append(parts, `<p style="`+theme["p"]+`">`+html.EscapeString(text)+`</p>`)
			}
		}
		var items []string
		for _, item := range asList(section["list"]) {
			text := strings.TrimSpace(asString(item))
			if text != "" {
				items = append(items, text)
			}
		}
		if len(items) > 0 && !compact {
			var lis strings.Builder
			for _, item := range items {
				lis.WriteString(`<li style="` + theme["li"] + `">` + html.EscapeString(item) + `</li>`)
			}
			parts = append(parts, `<ul style="`+theme["ul"]+`">`+lis.String()+`</ul>`)
		}
	}
	if closing := strings.TrimSpace(asString(article["closing"])); closing != "" {
		parts = append(parts, `<p style="`+theme["p"]+`">`+html.EscapeString(closing)+`</p>`)
	}
	sources := asList(article["sources"])
	if len(sources) > 0 {
		parts = append(parts, `<hr style="`+theme["hr"]+`" />`)
		parts = append(parts, `<p style="`+theme["src_h"]+`">参考来源</p>`)
		for i, raw := range sources {
			source, _ := raw.(map[string]any)
			if source == nil {
				continue
			}
			parts = append(parts, `<p style="`+theme["src"]+`">`+html.EscapeString(sourceLine(i+1, source))+`</p>`)
		}
	}
	footnotes := asList(article["footnotes"])
	if len(footnotes) > 0 {
		parts = append(parts, `<hr style="`+theme["hr"]+`" />`)
		for _, note := range footnotes {
			parts = append(parts, `<p style="`+theme["foot"]+`">`+html.EscapeString(asString(note))+`</p>`)
		}
	}
	parts = append(parts, "</section>")
	return strings.Join(parts, "")
}

func RenderWithinLimit(article map[string]any, templateKey string, dark bool) (string, error) {
	htmlText := RenderHTML(article, templateKey, dark, false)
	if utf8.RuneCountInString(htmlText) <= 20000 {
		return htmlText, nil
	}
	compact := RenderHTML(article, "simple", dark, true)
	if utf8.RuneCountInString(compact) <= 20000 {
		return compact, nil
	}
	return "", errTooLong
}

type tooLong struct{}

func (tooLong) Error() string {
	return "正文超过微信草稿长度上限（2 万字符），请缩短篇幅后重试"
}

var errTooLong = tooLong{}

func InjectImage(htmlText, imageURL string) string {
	if strings.TrimSpace(imageURL) == "" {
		return htmlText
	}
	tag := `<img src="` + html.EscapeString(imageURL) + `" alt="配图" style="max-width:100%;height:auto;display:block;margin:0 0 16px;" />`
	const marker = "</p>"
	if i := strings.Index(htmlText, marker); i >= 0 {
		return htmlText[:i+len(marker)] + tag + htmlText[i+len(marker):]
	}
	return tag + htmlText
}

func sourceLine(index int, source map[string]any) string {
	name := asString(source["name"])
	title := asString(source["title"])
	date := asString(source["published_on"])
	url := DisplayURL(asString(source["url"]))
	bits := []string{"[" + itoa(index) + "]"}
	if name != "" {
		bits = append(bits, name)
	}
	if title != "" {
		bits = append(bits, "《"+title+"》")
	}
	if date != "" {
		bits = append(bits, date)
	}
	if note := strings.TrimSpace(asString(source["note"])); note != "" {
		bits = append(bits, note)
	}
	if url != "" {
		bits = append(bits, url)
	}
	return strings.Join(bits, " ")
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return ""
	}
}

func asList(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case nil:
		return nil
	default:
		return nil
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
