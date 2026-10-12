package llm

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

var bareURL = regexp.MustCompile(`https?://[^\s)\]>"'\\,，。；]+`)

// InterpretResearch 只取出检索说明。正文、Markdown 和模型自己写的 sources 里的网址一律不当来源。
func InterpretResearch(text string) (string, []map[string]any) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	if data, err := ParseJSONContent(text); err == nil {
		notes := clipRunes(strings.TrimSpace(asString(data["notes"])), 1500)
		if notes == "" {
			if summary := strings.TrimSpace(asString(data["summary"])); summary != "" {
				notes = clipRunes(summary, 1500)
			}
		}
		return notes, nil
	}
	return clipRunes(text, 1500), nil
}

// StructuredResearchSources 只采用接口返回的引用，并且全部返回。
// 接受 citations、search_results、annotations；Responses 还要 num_sources_used > 0 才采信 url 注释。
// 流式响应里的引用出现在最后一块时同样算数。模型正文里的链接不算。
// 屏蔽、去重和排序在后面做，这里不提前截成 8 条。
func StructuredResearchSources(raw []byte) []map[string]any {
	rows := trustedCitationRows(raw)
	var out []map[string]any
	seen := map[string]bool{}
	for _, row := range rows {
		rawURL := row["url"]
		if rawURL == "" || seen[rawURL] {
			continue
		}
		seen[rawURL] = true
		item := normalizeSource(row["name"], row["title"], "", rawURL, "other", "")
		if item == nil {
			continue
		}
		if snippet := strings.TrimSpace(row["snippet"]); snippet != "" {
			item["snippet"] = clipRunes(snippet, 180)
		}
		out = append(out, item)
	}
	return out
}

func trustedCitationRows(raw []byte) []map[string]string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	var out []map[string]string
	if isJSON(raw) {
		var data map[string]any
		if json.Unmarshal(raw, &data) != nil {
			return nil
		}
		collectTrustedCitations(data, &out, 0)
		return out
	}
	_ = eachSSE(raw, func(data []byte) error {
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			return nil
		}
		var chunk map[string]any
		if json.Unmarshal(data, &chunk) != nil {
			return nil
		}
		collectTrustedCitations(chunk, &out, 0)
		return nil
	})
	return out
}

func collectTrustedCitations(v any, out *[]map[string]string, depth int) {
	if depth > 8 {
		return
	}
	switch node := v.(type) {
	case map[string]any:
		if used, ok := sourcesUsed(node); ok && used <= 0 {
			return
		}
		for _, key := range []string{"citations", "search_results", "searchResults", "annotations", "web_search_results"} {
			if child, ok := node[key]; ok {
				appendCitationList(child, out)
			}
		}
		for _, key := range []string{"choices", "message", "output", "response", "content", "delta"} {
			if child, ok := node[key]; ok {
				collectTrustedCitations(child, out, depth+1)
			}
		}
	case []any:
		for _, item := range node {
			collectTrustedCitations(item, out, depth+1)
		}
	}
}

func sourcesUsed(node map[string]any) (int, bool) {
	if n, ok := numberAt(node["num_sources_used"]); ok {
		return n, true
	}
	usage, _ := node["usage"].(map[string]any)
	if usage == nil {
		return 0, false
	}
	if n, ok := numberAt(usage["num_sources_used"]); ok {
		return n, true
	}
	return 0, false
}

func numberAt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

func appendCitationList(raw any, out *[]map[string]string) {
	switch list := raw.(type) {
	case []any:
		for _, item := range list {
			appendCitationItem(item, out)
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
		snippet := firstNonEmpty(v, "snippet", "summary")
		*out = append(*out, map[string]string{"url": rawURL, "title": title, "snippet": snippet})
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
