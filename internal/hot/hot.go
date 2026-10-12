package hot

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Item struct {
	Title       string
	Summary     string
	Source      string
	SourceName  string
	URL         string
	PublishedOn string
	Heat        int
	Vertical    string
	Reports     int
}

type Source struct {
	Name     string
	Protocol string
	BaseURL  string
	APIKey   string
	Params   map[string]any
	Timeout  time.Duration
}

func Fetch(ctx context.Context, client *http.Client, src Source) ([]Item, error) {
	if client == nil {
		client = http.DefaultClient
	}
	timeout := src.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var req *http.Request
	var err error
	if src.Protocol == "mcp" {
		tool := strParam(src.Params, "tool")
		if tool == "" {
			tool = "hot_topics"
		}
		body, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": tool, "arguments": map[string]any{}},
		})
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, src.BaseURL, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, src.BaseURL, nil)
	}
	if err != nil {
		return nil, err
	}
	if src.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+src.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, &urlError{msg: "热点源返回 " + resp.Status}
	}
	return Parse(raw, src.Name, src.Params), nil
}

type urlError struct{ msg string }

func (e *urlError) Error() string { return e.msg }

func Parse(raw []byte, sourceName string, params map[string]any) []Item {
	var data any
	if json.Unmarshal(raw, &data) != nil {
		return nil
	}
	data = unwrapMCP(data)
	listKey := strParam(params, "list")
	if listKey != "" {
		if obj, ok := data.(map[string]any); ok {
			data = obj[listKey]
		}
	} else if obj, ok := data.(map[string]any); ok {
		for _, key := range []string{"data", "items", "list", "result"} {
			if child, ok := obj[key]; ok {
				data = child
				break
			}
		}
	}
	rows, _ := data.([]any)
	titleKey := orKey(params, "title", "title")
	urlKey := orKey(params, "url", "url")
	summaryKey := orKey(params, "summary", "summary")
	sourceKey := orKey(params, "source", "source")
	timeKey := orKey(params, "time", "published_on")
	heatKey := orKey(params, "heat", "heat")
	var out []Item
	for _, row := range rows {
		obj, _ := row.(map[string]any)
		if obj == nil {
			continue
		}
		item := Item{
			Title: asString(obj[titleKey]), Summary: asString(obj[summaryKey]), Source: asString(obj[sourceKey]),
			SourceName: sourceName, URL: asString(obj[urlKey]), PublishedOn: asString(obj[timeKey]),
			Heat: asInt(obj[heatKey]), Vertical: strParam(params, "vertical"), Reports: 1,
		}
		if item.Title == "" || item.URL == "" || !strings.HasPrefix(item.URL, "http") {
			continue
		}
		out = append(out, item)
	}
	return out
}

func unwrapMCP(data any) any {
	obj, ok := data.(map[string]any)
	if !ok {
		return data
	}
	result, _ := obj["result"].(map[string]any)
	if result == nil {
		return data
	}
	if structured, ok := result["structuredContent"]; ok {
		return structured
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		return data
	}
	first, _ := content[0].(map[string]any)
	text := asString(first["text"])
	if text == "" {
		return data
	}
	var inner any
	if json.Unmarshal([]byte(text), &inner) == nil {
		return inner
	}
	return data
}

// Merge 按网址合并，同一网址被多个源报道时累加次数。
func Merge(groups ...[]Item) []Item {
	order := []string{}
	byURL := map[string]*Item{}
	for _, group := range groups {
		for _, item := range group {
			key := canonical(item.URL)
			if key == "" {
				continue
			}
			prev, ok := byURL[key]
			if !ok {
				copyItem := item
				copyItem.Reports = 1
				byURL[key] = &copyItem
				order = append(order, key)
				continue
			}
			prev.Reports++
			if item.Heat > prev.Heat {
				prev.Heat = item.Heat
			}
			if prev.Source == "" {
				prev.Source = item.Source
			}
		}
	}
	out := make([]Item, 0, len(order))
	for _, key := range order {
		out = append(out, *byURL[key])
	}
	return out
}

func Score(item Item) int {
	score := item.Heat
	host := hostOf(item.URL)
	if host == "gov.cn" || strings.HasSuffix(host, ".gov.cn") {
		score += 100
	}
	if item.Reports >= 2 {
		score += 50
	}
	return score
}

// Choose 给普通档第一条可用热点，给高级档优先两个来源以上的条目。
func Choose(items []Item, tier string, used map[string]bool) (Item, bool) {
	best := Item{}
	found := false
	bestScore := -1
	for _, item := range items {
		tag := Tag(item)
		if used[tag] || item.Title == "" {
			continue
		}
		if tier != "premium" && tier != "lifetime" && found {
			break
		}
		score := Score(item)
		if tier == "premium" || tier == "lifetime" {
			if !found || score > bestScore || (score == bestScore && item.Reports > best.Reports) {
				best = item
				bestScore = score
				found = true
			}
			continue
		}
		return item, true
	}
	return best, found
}

func CacheHours(params map[string]any) int {
	hours := 12
	if params != nil {
		switch n := params["cache_hours"].(type) {
		case float64:
			if int(n) > 0 {
				hours = int(n)
			}
		case int:
			if n > 0 {
				hours = n
			}
		case string:
			if v, err := strconv.Atoi(strings.TrimSpace(n)); err == nil && v > 0 {
				hours = v
			}
		}
	}
	if hours < 1 {
		return 1
	}
	if hours > 168 {
		return 168
	}
	return hours
}

func ForVertical(items []Item, key, direction string) []Item {
	key = strings.TrimSpace(key)
	direction = strings.TrimSpace(direction)
	if key == "" && direction == "" {
		return items
	}
	out := make([]Item, 0, len(items))
	for _, item := range items {
		vertical := strings.TrimSpace(item.Vertical)
		if vertical == "" || vertical == key || vertical == direction {
			out = append(out, item)
		}
	}
	return out
}

func Tag(item Item) string {
	title := strings.TrimSpace(item.Title)
	if len([]rune(title)) > 40 {
		title = string([]rune(title)[:40])
	}
	return "热点|热点|" + title
}

func canonical(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.Host = strings.TrimPrefix(strings.ToLower(parsed.Host), "www.")
	parsed.Fragment = ""
	return parsed.Scheme + "://" + parsed.Host + parsed.Path
}

func hostOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
}

func strParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	return strings.TrimSpace(asString(params[key]))
}

func orKey(params map[string]any, key, fallback string) string {
	if v := strParam(params, key); v != "" {
		return v
	}
	return fallback
}

func asString(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}
