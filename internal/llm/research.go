package llm

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	markdownLink = regexp.MustCompile(`\[([^\]\n]{1,120})\]\((https?://[^)\s]+)\)`)
	bareURL      = regexp.MustCompile(`https?://[^\s)\]>"'\\,，。；]+`)
)

// InterpretResearch 从检索模型的输出里取出事实和来源。模型可能返回 JSON，也可能返回 Markdown。
func InterpretResearch(text string) (string, []map[string]any) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	if data, err := ParseJSONContent(text); err == nil {
		notes := clipRunes(strings.TrimSpace(asString(data["notes"])), 1500)
		sources := sourcesFromList(data["sources"])
		sources = appendSources(sources, sourcesFromList(data["citations"]))
		sources = appendSources(sources, sourcesFromList(data["search_results"]))
		if notes == "" && len(sources) == 0 {
			if summary := strings.TrimSpace(asString(data["summary"])); summary != "" {
				notes = clipRunes(summary, 1500)
			}
		}
		sources = appendSources(sources, sourcesFromMarkdown(text))
		if notes != "" {
			sources = appendSources(sources, sourcesFromMarkdown(notes))
		}
		return notes, sources
	}
	return clipRunes(text, 1500), sourcesFromMarkdown(text)
}

// AttachResearchCitations 把联网响应里正文以外的网址补进文本。
// grok-chat-fast 常把网址放在 citations、search_results 或 annotations，message.content 本身没有链接。
func AttachResearchCitations(text string, raw []byte) string {
	rows := citationRows(raw)
	if len(rows) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	added := 0
	for _, row := range rows {
		rawURL := row["url"]
		if rawURL == "" || strings.Contains(text, rawURL) {
			continue
		}
		title := strings.TrimSpace(row["title"])
		if title == "" {
			title = "来源"
		}
		title = strings.NewReplacer("[", "", "]", "", "\n", " ", "\r", " ").Replace(title)
		title = clipRunes(title, 80)
		b.WriteString("\n[")
		b.WriteString(title)
		b.WriteString("](")
		b.WriteString(rawURL)
		b.WriteString(")")
		added++
		if added >= 8 {
			break
		}
	}
	if added == 0 {
		return text
	}
	return b.String()
}

func citationRows(raw []byte) []map[string]string {
	if !isJSON(raw) {
		return nil
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return nil
	}
	var out []map[string]string
	collectCitationNodes(data, &out, 0)
	return out
}

func collectCitationNodes(v any, out *[]map[string]string, depth int) {
	if depth > 8 || len(*out) >= 16 {
		return
	}
	switch node := v.(type) {
	case map[string]any:
		for _, key := range []string{"citations", "search_results", "searchResults", "annotations", "references", "web_search_results"} {
			if child, ok := node[key]; ok {
				appendCitationList(child, out)
			}
		}
		for _, key := range []string{"choices", "message", "output", "response", "content", "delta"} {
			if child, ok := node[key]; ok {
				collectCitationNodes(child, out, depth+1)
			}
		}
	case []any:
		for _, item := range node {
			if len(*out) >= 16 {
				return
			}
			collectCitationNodes(item, out, depth+1)
		}
	}
}

func appendCitationList(raw any, out *[]map[string]string) {
	switch list := raw.(type) {
	case []any:
		for _, item := range list {
			appendCitationItem(item, out)
			if len(*out) >= 16 {
				return
			}
		}
	default:
		appendCitationItem(list, out)
	}
}

func appendCitationItem(item any, out *[]map[string]string) {
	switch v := item.(type) {
	case string:
		if rawURL := firstHTTP(v); rawURL != "" {
			*out = append(*out, map[string]string{"url": rawURL})
		}
	case map[string]any:
		rawURL, title := citationURL(v)
		if rawURL == "" {
			return
		}
		*out = append(*out, map[string]string{"url": rawURL, "title": title})
	}
}

func citationURL(source map[string]any) (string, string) {
	if rawURL := firstStringURL(source); rawURL != "" {
		return rawURL, firstNonEmpty(source, "title", "name")
	}
	for _, key := range []string{"url_citation", "citation", "web"} {
		nested, _ := source[key].(map[string]any)
		if nested == nil {
			continue
		}
		if rawURL := firstStringURL(nested); rawURL != "" {
			title := firstNonEmpty(nested, "title", "name")
			if title == "" {
				title = firstNonEmpty(source, "title", "name")
			}
			return rawURL, title
		}
	}
	return "", ""
}

func firstStringURL(source map[string]any) string {
	for _, key := range []string{"url", "link", "href", "uri"} {
		text, _ := source[key].(string)
		if rawURL := firstHTTP(text); rawURL != "" {
			return rawURL
		}
	}
	return ""
}

func firstNonEmpty(source map[string]any, keys ...string) string {
	for _, key := range keys {
		text, _ := source[key].(string)
		text = strings.TrimSpace(text)
		if text != "" {
			return text
		}
	}
	return ""
}

func firstHTTP(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !(strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")) {
		if urls := bareURL.FindAllString(s, 1); len(urls) > 0 {
			s = urls[0]
		} else {
			return ""
		}
	}
	return strings.TrimRight(s, ".,;，。；)>\"'")
}

func sourcesHaveURL(sources []map[string]any) bool {
	for _, source := range sources {
		if asString(source["url"]) != "" {
			return true
		}
	}
	return false
}

func sourcesFromList(raw any) []map[string]any {
	list, _ := raw.([]any)
	var out []map[string]any
	seen := map[string]bool{}
	for _, item := range list {
		var row map[string]any
		switch source := item.(type) {
		case string:
			rawURL := source
			if urls := bareURL.FindAllString(source, 1); len(urls) > 0 {
				rawURL = urls[0]
			}
			row = normalizeSource("", "", "", rawURL, "other", "")
		case map[string]any:
			rawURL, nestedTitle := citationURL(source)
			name := asString(source["name"])
			title := asString(source["title"])
			if title == "" {
				title = nestedTitle
			}
			row = normalizeSource(name, title, asString(source["published_on"]), rawURL, asString(source["source_type"]), asString(source["note"]))
		}
		if row == nil {
			continue
		}
		key := asString(row["url"]) + "|" + asString(row["title"])
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, row)
		if len(out) >= 8 {
			break
		}
	}
	return out
}

func sourcesFromMarkdown(text string) []map[string]any {
	var out []map[string]any
	seen := map[string]bool{}
	add := func(name, title, rawURL, note string) {
		row := normalizeSource(name, title, "", rawURL, "other", note)
		if row == nil {
			return
		}
		key := asString(row["url"])
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, row)
	}
	for _, match := range markdownLink.FindAllStringSubmatchIndex(text, 8) {
		if len(out) >= 8 || len(match) < 6 {
			break
		}
		title := strings.TrimSpace(text[match[2]:match[3]])
		rawURL := text[match[4]:match[5]]
		rest := text[match[1]:]
		if nl := strings.IndexAny(rest, "\r\n"); nl >= 0 {
			rest = rest[:nl]
		}
		rest = strings.TrimSpace(rest)
		note := ""
		if rest != "" && !strings.Contains(rest, "http") && utf8.RuneCountInString(rest) <= 200 {
			note = rest
		}
		add("", title, rawURL, note)
	}
	if len(out) == 0 {
		for _, match := range bareURL.FindAllString(text, 8) {
			if len(out) >= 8 {
				break
			}
			add("", "", match, "")
		}
	}
	return out
}

func appendSources(base, extra []map[string]any) []map[string]any {
	seen := map[string]bool{}
	for _, row := range base {
		if url := asString(row["url"]); url != "" {
			seen[url] = true
		}
	}
	for _, row := range extra {
		url := asString(row["url"])
		if url != "" && seen[url] {
			continue
		}
		if url != "" {
			seen[url] = true
		}
		base = append(base, row)
		if len(base) >= 8 {
			break
		}
	}
	return base
}

func normalizeSource(name, title, published, rawURL, sourceType, note string) map[string]any {
	name = clipRunes(strings.TrimSpace(name), 80)
	title = clipRunes(strings.TrimSpace(title), 120)
	rawURL = strings.TrimRight(strings.TrimSpace(rawURL), ".,;，。；")
	if rawURL != "" && !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = ""
	}
	if name == "" && title == "" && rawURL == "" {
		return nil
	}
	if name == "" && rawURL != "" {
		if parsed, err := url.Parse(rawURL); err == nil {
			name = parsed.Hostname()
		}
	}
	switch sourceType {
	case "news", "policy", "standard", "other":
	default:
		sourceType = "other"
	}
	row := map[string]any{
		"name": name, "title": title, "published_on": clipRunes(strings.TrimSpace(published), 32),
		"url": clipRunes(rawURL, 300), "source_type": sourceType,
	}
	if note = clipRunes(strings.TrimSpace(note), 200); note != "" {
		row["note"] = note
	}
	return row
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
