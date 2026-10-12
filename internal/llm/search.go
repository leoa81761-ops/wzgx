package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	uddgLink = regexp.MustCompile(`uddg=([^&"]+)`)
	htmlLink = regexp.MustCompile(`href="(https?://[^"]+)"`)
)

// SearchWeb is the non-model backup when the research model returns 502 or no text.
// The endpoint may be a JSON {"results":[{"title","url"}]} API or an HTML results page.
func SearchWeb(ctx context.Context, client *http.Client, endpoint, query string) ([]map[string]any, error) {
	endpoint = strings.TrimSpace(endpoint)
	query = strings.TrimSpace(query)
	if endpoint == "" || query == "" {
		return nil, nil
	}
	if client == nil {
		client = http.DefaultClient
	}
	target, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	values := target.Query()
	if values.Get("q") == "" && values.Get("query") == "" {
		values.Set("q", query)
	}
	target.RawQuery = values.Encode()
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gonghao-research/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, &Error{Message: "检索页面返回 " + resp.Status}
	}
	return ParseSearchBody(body), nil
}

// ParseSearchBody reads JSON results or HTML links, including DuckDuckGo uddg redirects.
func ParseSearchBody(body []byte) []map[string]any {
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return nil
	}
	if body[0] == '{' || body[0] == '[' {
		if rows := parseSearchJSON(body); len(rows) > 0 {
			return rows
		}
	}
	seen := map[string]bool{}
	var out []map[string]any
	add := func(raw, title string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || seen[raw] || searchHostSkipped(raw) {
			return
		}
		seen[raw] = true
		item := map[string]any{"url": raw, "name": hostLabel(raw), "title": strings.TrimSpace(title), "source_type": "web"}
		out = append(out, item)
	}
	for _, match := range uddgLink.FindAllSubmatch(body, 12) {
		raw, err := url.QueryUnescape(string(match[1]))
		if err != nil {
			continue
		}
		add(raw, "")
		if len(out) >= 5 {
			return out
		}
	}
	for _, match := range htmlLink.FindAllSubmatch(body, 20) {
		add(string(match[1]), "")
		if len(out) >= 5 {
			return out
		}
	}
	return out
}

func parseSearchJSON(body []byte) []map[string]any {
	var wrapped struct {
		Results []map[string]any `json:"results"`
	}
	var list []map[string]any
	if json.Unmarshal(body, &wrapped) == nil && len(wrapped.Results) > 0 {
		list = wrapped.Results
	} else if json.Unmarshal(body, &list) != nil {
		return nil
	}
	var out []map[string]any
	for _, row := range list {
		raw, _ := row["url"].(string)
		if raw == "" {
			raw, _ = row["link"].(string)
		}
		raw = strings.TrimSpace(raw)
		if raw == "" || searchHostSkipped(raw) {
			continue
		}
		title, _ := row["title"].(string)
		name, _ := row["name"].(string)
		if name == "" {
			name = hostLabel(raw)
		}
		out = append(out, map[string]any{"url": raw, "name": name, "title": strings.TrimSpace(title), "source_type": "web"})
		if len(out) >= 5 {
			break
		}
	}
	return out
}

func searchHostSkipped(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return true
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	switch host {
	case "", "duckduckgo.com", "duck.com", "google.com", "bing.com":
		return true
	default:
		return false
	}
}

func hostLabel(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	if utf8.RuneCountInString(host) > 40 {
		return string([]rune(host)[:40])
	}
	return host
}
