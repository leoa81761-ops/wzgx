package llm

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	markdownLink = regexp.MustCompile(`\[([^\]\n]{1,120})\]\((https?://[^)\s]+)\)`)
	bareURL      = regexp.MustCompile(`https?://[^\s)>\]]+`)
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
		if notes == "" && len(sources) == 0 {
			if summary := strings.TrimSpace(asString(data["summary"])); summary != "" {
				notes = clipRunes(summary, 1500)
			}
		}
		return notes, sources
	}
	return clipRunes(text, 1500), sourcesFromMarkdown(text)
}

func sourcesFromList(raw any) []map[string]any {
	list, _ := raw.([]any)
	var out []map[string]any
	seen := map[string]bool{}
	for _, item := range list {
		source, _ := item.(map[string]any)
		if source == nil {
			continue
		}
		row := normalizeSource(asString(source["name"]), asString(source["title"]), asString(source["published_on"]), asString(source["url"]), asString(source["source_type"]))
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
	add := func(name, title, rawURL string) {
		row := normalizeSource(name, title, "", rawURL, "other")
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
	for _, match := range markdownLink.FindAllStringSubmatch(text, 8) {
		if len(out) >= 8 {
			break
		}
		add("", strings.TrimSpace(match[1]), match[2])
	}
	if len(out) == 0 {
		for _, match := range bareURL.FindAllString(text, 8) {
			if len(out) >= 8 {
				break
			}
			add("", "", match)
		}
	}
	return out
}

func normalizeSource(name, title, published, rawURL, sourceType string) map[string]any {
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
	return map[string]any{
		"name": name, "title": title, "published_on": clipRunes(strings.TrimSpace(published), 32),
		"url": clipRunes(rawURL, 300), "source_type": sourceType,
	}
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
