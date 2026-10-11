package llm

import (
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
		if notes == "" && len(sources) == 0 {
			if summary := strings.TrimSpace(asString(data["summary"])); summary != "" {
				notes = clipRunes(summary, 1500)
			}
		}
		if !sourcesHaveURL(sources) {
			sources = appendSources(sources, sourcesFromMarkdown(text))
			if notes != "" {
				sources = appendSources(sources, sourcesFromMarkdown(notes))
			}
		}
		return notes, sources
	}
	return clipRunes(text, 1500), sourcesFromMarkdown(text)
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
			rawURL := asString(source["url"])
			if rawURL == "" {
				rawURL = asString(source["link"])
			}
			row = normalizeSource(asString(source["name"]), asString(source["title"]), asString(source["published_on"]), rawURL, asString(source["source_type"]), asString(source["note"]))
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
