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
	uddgLink   = regexp.MustCompile(`uddg=([^&"]+)`)
	htmlLink   = regexp.MustCompile(`href="(https?://[^"]+)"`)
	bingItem   = regexp.MustCompile(`(?s)<li[^>]*class="[^"]*b_algo[^"]*"[^>]*>(.*?)</li>`)
	bingAnchor = regexp.MustCompile(`(?s)<h2[^>]*>\s*<a[^>]*href="(https?://[^"]+)"[^>]*>(.*?)</a>`)
	sogouItem  = regexp.MustCompile(`(?s)<h3[^>]*class="[^"]*vr-title[^"]*"[^>]*>\s*<a[^>]*href="(https?://[^"]+)"[^>]*>(.*?)</a>(.{0,400})`)
	baiduBlock = regexp.MustCompile(`(?s)mu="(https?://[^"]+)"(.{0,700})`)
	baiduTitle = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	htmlTag    = regexp.MustCompile(`(?s)<[^>]+>`)
)

// SearchTarget is one backup search endpoint. An API key selects the JSON provider.
type SearchTarget struct {
	URL    string
	APIKey string
}

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
	setSearchQuery(target.Hostname(), values, query)
	target.RawQuery = values.Encode()
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; gonghao-research/1.0)")
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

// SearchChain tries each endpoint until one returns a result. An API key uses the JSON provider.
func SearchChain(ctx context.Context, client *http.Client, targets []SearchTarget, query string) ([]map[string]any, error) {
	var last error
	for _, target := range targets {
		var rows []map[string]any
		var err error
		if strings.TrimSpace(target.APIKey) != "" {
			rows, err = SearchAPI(ctx, client, target.URL, target.APIKey, query)
		} else {
			rows, err = SearchWeb(ctx, client, target.URL, query)
		}
		if err != nil {
			last = err
			continue
		}
		if len(rows) > 0 {
			return rows, nil
		}
	}
	return nil, last
}

// SearchAPI calls an optional key-based search such as Bocha.
func SearchAPI(ctx context.Context, client *http.Client, endpoint, apiKey, query string) ([]map[string]any, error) {
	endpoint = strings.TrimSpace(endpoint)
	query = strings.TrimSpace(query)
	apiKey = strings.TrimSpace(apiKey)
	if endpoint == "" || query == "" || apiKey == "" {
		return nil, nil
	}
	if client == nil {
		client = http.DefaultClient
	}
	payload, _ := json.Marshal(map[string]any{"query": query, "count": 5, "summary": true})
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
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
		return nil, &Error{Message: "检索接口返回 " + resp.Status}
	}
	return ParseSearchBody(body), nil
}

func setSearchQuery(host string, values url.Values, query string) {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	switch {
	case strings.Contains(host, "baidu.com"):
		if values.Get("wd") == "" {
			values.Set("wd", query)
		}
	case strings.Contains(host, "sogou.com"):
		if values.Get("query") == "" {
			values.Set("query", query)
		}
	default:
		if values.Get("q") == "" && values.Get("query") == "" {
			values.Set("q", query)
		}
	}
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
	if rows := parseEngineHTML(string(body)); len(rows) > 0 {
		return rows
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
		Data    struct {
			WebPages struct {
				Value []map[string]any `json:"value"`
			} `json:"webPages"`
		} `json:"data"`
	}
	var list []map[string]any
	if json.Unmarshal(body, &wrapped) == nil && len(wrapped.Results) > 0 {
		list = wrapped.Results
	} else if json.Unmarshal(body, &wrapped) == nil && len(wrapped.Data.WebPages.Value) > 0 {
		list = wrapped.Data.WebPages.Value
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
		if title == "" {
			title, _ = row["name"].(string)
		}
		name, _ := row["name"].(string)
		if name == "" || name == title {
			name = hostLabel(raw)
		}
		snippet, _ := row["snippet"].(string)
		if snippet == "" {
			snippet, _ = row["summary"].(string)
		}
		item := map[string]any{"url": raw, "name": name, "title": strings.TrimSpace(title), "source_type": "web"}
		if text := strings.TrimSpace(snippet); text != "" {
			item["snippet"] = clipSearch(text, 180)
		}
		out = append(out, item)
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
	case "", "duckduckgo.com", "duck.com", "google.com", "bing.com", "cn.bing.com", "sogou.com", "baidu.com":
		return true
	default:
		return false
	}
}

func parseEngineHTML(body string) []map[string]any {
	if rows := parseBing(body); len(rows) > 0 {
		return rows
	}
	if rows := parseSogou(body); len(rows) > 0 {
		return rows
	}
	return parseBaidu(body)
}

func parseBing(body string) []map[string]any {
	var out []map[string]any
	for _, block := range bingItem.FindAllStringSubmatch(body, 8) {
		anchor := bingAnchor.FindStringSubmatch(block[1])
		if anchor == nil {
			continue
		}
		snippet := ""
		if parts := regexp.MustCompile(`(?s)<p[^>]*>(.*?)</p>`).FindStringSubmatch(block[1]); parts != nil {
			snippet = plainHTML(parts[1])
		}
		if item := searchItem(anchor[1], plainHTML(anchor[2]), snippet); item != nil {
			out = append(out, item)
		}
		if len(out) >= 5 {
			break
		}
	}
	return out
}

func parseSogou(body string) []map[string]any {
	var out []map[string]any
	for _, match := range sogouItem.FindAllStringSubmatch(body, 8) {
		snippet := plainHTML(match[3])
		if item := searchItem(match[1], plainHTML(match[2]), snippet); item != nil {
			out = append(out, item)
		}
		if len(out) >= 5 {
			break
		}
	}
	return out
}

func parseBaidu(body string) []map[string]any {
	var out []map[string]any
	for _, match := range baiduBlock.FindAllStringSubmatch(body, 8) {
		title := ""
		if anchor := baiduTitle.FindStringSubmatch(match[2]); anchor != nil {
			title = plainHTML(anchor[1])
		}
		if item := searchItem(match[1], title, plainHTML(match[2])); item != nil {
			out = append(out, item)
		}
		if len(out) >= 5 {
			break
		}
	}
	return out
}

func searchItem(raw, title, snippet string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" || searchHostSkipped(raw) {
		return nil
	}
	item := map[string]any{"url": raw, "name": hostLabel(raw), "title": strings.TrimSpace(title), "source_type": "web"}
	if text := strings.TrimSpace(snippet); text != "" && text != strings.TrimSpace(title) {
		item["snippet"] = clipSearch(text, 180)
	}
	return item
}

func plainHTML(text string) string {
	text = htmlTag.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&#34;", "\"")
	return strings.TrimSpace(strings.Join(strings.Fields(text), " "))
}

func clipSearch(text string, n int) string {
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	return string([]rune(text)[:n])
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
