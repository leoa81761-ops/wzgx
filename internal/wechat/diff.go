package wechat

import (
	"math"
	"strings"
	"unicode/utf8"
)

// DraftView 是 draft/get 里能对上的标题和正文。找不到草稿时 Missing 为 true。
// 草稿消失只说明当前读不到，不能据此判断已经发布。
type DraftView struct {
	Title   string
	Content string
	Missing bool
}

type DraftDiff struct {
	State       string
	ChangeRatio float64
	RemoteTitle string
}

func ReadDraft(raw map[string]any) DraftView {
	if raw == nil {
		return DraftView{Missing: true}
	}
	if items, ok := raw["news_item"].([]any); ok && len(items) > 0 {
		if item, ok := items[0].(map[string]any); ok {
			return DraftView{Title: strOf(item["title"]), Content: strOf(item["content"])}
		}
	}
	if _, ok := raw["title"]; ok || raw["content"] != nil {
		return DraftView{Title: strOf(raw["title"]), Content: strOf(raw["content"])}
	}
	return DraftView{Missing: true}
}

func MissingDraft(err error) bool {
	if err == nil {
		return false
	}
	if InvalidMedia(err) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "找不到") || strings.Contains(strings.ToLower(msg), "not found")
}

func DiffDraft(localTitle, localHTML string, remote DraftView) DraftDiff {
	if remote.Missing {
		return DraftDiff{State: "missing", ChangeRatio: 1}
	}
	titleRatio := textRatio(localTitle, remote.Title)
	bodyRatio := textRatio(localHTML, remote.Content)
	ratio := titleRatio
	if bodyRatio > ratio {
		ratio = bodyRatio
	}
	state := "unchanged"
	if ratio > 0 {
		state = "modified"
	}
	return DraftDiff{State: state, ChangeRatio: ratio, RemoteTitle: remote.Title}
}

func textRatio(a, b string) float64 {
	if a == b {
		return 0
	}
	an := utf8.RuneCountInString(a)
	bn := utf8.RuneCountInString(b)
	max := an
	if bn > max {
		max = bn
	}
	if max == 0 {
		return 0
	}
	delta := an - bn
	if delta < 0 {
		delta = -delta
	}
	ratio := float64(delta) / float64(max)
	if ratio < 0.05 {
		ratio = 0.05
	}
	if ratio > 1 {
		ratio = 1
	}
	return math.Round(ratio*1000) / 1000
}

func strOf(v any) string {
	s, _ := v.(string)
	return s
}
