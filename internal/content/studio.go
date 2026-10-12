package content

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// CopiedSpan reports whether the draft repeats 40 or more runes from a source text.
func CopiedSpan(draft string, sources []string) bool {
	body := strings.TrimSpace(draft)
	if utf8.RuneCountInString(body) < 40 {
		return false
	}
	for _, source := range sources {
		runes := []rune(strings.TrimSpace(source))
		if len(runes) < 40 {
			continue
		}
		for i := 0; i+40 <= len(runes); i += 20 {
			window := string(runes[i : i+40])
			if strings.Contains(body, window) {
				return true
			}
		}
	}
	return false
}

func DraftText(draft map[string]any) string {
	return flatten(draft)
}

func SourceTexts(draft map[string]any) []string {
	var out []string
	for _, raw := range asList(draft["sources"]) {
		source, _ := raw.(map[string]any)
		if source == nil {
			continue
		}
		out = append(out, asString(source["title"])+" "+asString(source["summary"])+" "+asString(source["excerpt"]))
	}
	return out
}

func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

func asString(v any) string {
	text, _ := v.(string)
	return text
}

// StyleFeatures keeps counts and averages. It does not keep the sample text.
func StyleFeatures(samples []string) (map[string]any, error) {
	if len(samples) < 3 || len(samples) > 5 {
		return nil, errSamples
	}
	var sentences, questions, firstPerson, runes int
	for _, sample := range samples {
		text := strings.TrimSpace(sample)
		if utf8.RuneCountInString(text) < 80 {
			return nil, errSamples
		}
		runes += utf8.RuneCountInString(text)
		sentences += strings.Count(text, "。") + strings.Count(text, "！") + strings.Count(text, "？")
		questions += strings.Count(text, "？")
		firstPerson += strings.Count(text, "我")
	}
	if sentences < 1 {
		sentences = 1
	}
	return map[string]any{
		"samples": len(samples), "avg_sentence_runes": runes / sentences, "questions": questions, "first_person": firstPerson,
	}, nil
}

// DiffStyleFeatures keeps only counts from an edit. It does not keep either text.
func DiffStyleFeatures(local, remote string) map[string]any {
	return map[string]any{
		"source":             "edit_diff",
		"local_runes":        utf8.RuneCountInString(local),
		"remote_runes":       utf8.RuneCountInString(remote),
		"question_delta":     strings.Count(remote, "？") - strings.Count(local, "？"),
		"first_person_delta": strings.Count(remote, "我") - strings.Count(local, "我"),
	}
}

type sampleError struct{}

func (sampleError) Error() string { return "请提供 3 到 5 篇至少 80 字的范文" }

var errSamples = sampleError{}

// Reachable marks an http(s) URL. Timeouts and other network errors stay unconfirmed so a blip does not drop a source.
func Reachable(ctx context.Context, client *http.Client, rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if client == nil || (!strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://")) {
		return "未校验"
	}
	call, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(call, http.MethodHead, rawURL, nil)
	if err != nil {
		return "未确认"
	}
	resp, err := client.Do(req)
	if err != nil {
		return "未确认"
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented {
		return reachableGet(ctx, client, rawURL)
	}
	return reachLabel(resp.StatusCode)
}

func reachableGet(ctx context.Context, client *http.Client, rawURL string) string {
	call, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(call, http.MethodGet, rawURL, nil)
	if err != nil {
		return "未确认"
	}
	resp, err := client.Do(req)
	if err != nil {
		return "未确认"
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
	return reachLabel(resp.StatusCode)
}

func reachLabel(status int) string {
	if status >= 200 && status < 400 {
		return "可访问"
	}
	if status == http.StatusNotFound || status == http.StatusGone {
		return "打不开"
	}
	return "未确认"
}

// AnnotateAccess writes 可访问、打不开 or 未确认 onto each source and keeps the source.
func AnnotateAccess(ctx context.Context, client *http.Client, draft map[string]any) {
	for _, raw := range asList(draft["sources"]) {
		source, _ := raw.(map[string]any)
		if source == nil {
			continue
		}
		source["accessible"] = Reachable(ctx, client, asString(source["url"]))
	}
}

// DropUnreachable removes sources whose URL is marked 打不开 and writes the label onto the rest.
func DropUnreachable(ctx context.Context, client *http.Client, draft map[string]any) {
	list := asList(draft["sources"])
	if len(list) == 0 {
		return
	}
	kept := make([]any, 0, len(list))
	for _, raw := range list {
		source, _ := raw.(map[string]any)
		if source == nil {
			continue
		}
		label := Reachable(ctx, client, asString(source["url"]))
		source["accessible"] = label
		if label == "打不开" {
			continue
		}
		kept = append(kept, source)
	}
	draft["sources"] = kept
}

// PickVariant prefers the label with the highest adoption rate. explore picks rows[exploreAt].
func PickVariant(rows [][2]int, explore bool, exploreAt int) int {
	if len(rows) == 0 {
		return -1
	}
	if explore {
		if exploreAt < 0 || exploreAt >= len(rows) {
			exploreAt = 0
		}
		return exploreAt
	}
	best := 0
	bestRate := -1.0
	seen := false
	for i, row := range rows {
		uses, adopted := row[0], row[1]
		if uses <= 0 {
			continue
		}
		rate := float64(adopted) / float64(uses)
		if !seen || rate > bestRate {
			best = i
			bestRate = rate
			seen = true
		}
	}
	return best
}
