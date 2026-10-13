package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/content"
	"github.com/leoa81761-ops/wzgx/internal/editorial"
	"github.com/leoa81761-ops/wzgx/internal/llm"
)

func TestSearchQueryUsesTopicEntities(t *testing.T) {
	payload := map[string]any{
		"topic_title":        "综合布线：对比两种常见做法",
		"subtopic":           "综合布线",
		"column":             "知识FAQ",
		"angle":              "对比两种常见做法",
		"direction":          "弱电智能化",
		"keywords":           "弱电,线槽",
		"column_positioning": "用问答把一个具体问题讲清楚",
	}
	queries := append(backupQueries(payload), shortSearchQuery(payload), focusedQuery(payload))
	for _, query := range queries {
		if strings.Contains(query, "两种做法") || strings.Contains(query, "对比两种") {
			t.Fatal(query)
		}
	}
	if !strings.Contains(shortSearchQuery(payload), "综合布线") {
		t.Fatal(shortSearchQuery(payload))
	}
}

func TestExpandKeepsOriginalAndStopsWhenItDoesNotGrow(t *testing.T) {
	original := "只有一句现场记录。"
	added := strings.Repeat("先打开检修口再看线槽。", 80)
	var calls int
	srv := expandServer(t, &calls, map[string]any{
		"title": "短稿", "summary": "摘要",
		"sections": []any{map[string]any{"heading": "新的一节", "paragraphs": []any{added}, "list": []any{}}},
		"closing":  "结束", "sources": []any{},
	})
	defer srv.Close()
	draft := map[string]any{
		"title": "短稿", "summary": "摘要",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{original}, "list": []any{}}},
		"closing":  "结束", "sources": []any{},
	}
	gw := llm.NewGateway(false, srv.Client())
	out, _, _, _ := fitBody(context.Background(), gw, []llm.Provider{{
		Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 10 * time.Second,
	}}, map[string]any{"word_target": 800}, draft, 800)
	blob := bodyPlain(out)
	if !strings.Contains(blob, original) || !strings.Contains(blob, "先打开检修口再看线槽。") {
		t.Fatal(blob)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestThinFactsUseTheColumnTemplate(t *testing.T) {
	var calls int
	srv := expandServer(t, &calls, map[string]any{
		"title": "短稿", "summary": "摘要",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"只有一句。"}, "list": []any{}}},
		"closing":  "结束", "sources": []any{},
	})
	defer srv.Close()
	draft := map[string]any{
		"title": "短稿", "summary": "摘要",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"只有一句现场记录。"}, "list": []any{}}},
		"closing":  "结束", "sources": []any{},
	}
	gw := llm.NewGateway(false, srv.Client())
	out, _, _, still := fitBody(context.Background(), gw, []llm.Provider{{
		Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 10 * time.Second,
	}}, map[string]any{"word_target": 800, "writing_mode": "experience", "column_structure": "qa", "subtopic": "综合布线"}, draft, 800)
	if calls != 0 || still {
		t.Fatalf("calls %d still %v runes %d", calls, still, content.BodyRunes(out))
	}
	blob := bodyPlain(out)
	if !strings.Contains(blob, "只有一句现场记录。") {
		t.Fatal(blob)
	}
	for _, heading := range []string{"一个现场案例", "可以照做的步骤", "容易踩的坑", "读者还会问"} {
		if !strings.Contains(blob, heading) {
			t.Fatal(heading, blob)
		}
	}
	if content.BodyRunes(out) < 800*85/100 {
		t.Fatal(content.BodyRunes(out))
	}
}

func TestSourcedPieceIsNotLabeledExperience(t *testing.T) {
	draft := map[string]any{
		"title":    "综合布线验收",
		"sections": []any{map[string]any{"paragraphs": []any{"合适的正文写了好几句。"}, "list": []any{}}},
		"closing":  "结束",
		"sources":  []any{map[string]any{"title": "住宅综合布线说明", "url": "https://www.gov.cn/a"}},
	}
	out := ensureInlineCitations(draft)
	if !strings.Contains(bodyPlain(out), "[1]") || !strings.Contains(bodyPlain(out), "住宅综合布线说明") {
		t.Fatal(bodyPlain(out))
	}
	warm := editorial.EnsureConcreteTips(editorial.StripWarmFiller(map[string]any{
		"sections": []any{map[string]any{"paragraphs": []any{"今晚很温馨。", "彼此很有默契。"}}},
	}))
	if editorial.ConcreteTips(warm) < 5 || strings.Contains(editorialFiller(warm), "温馨") {
		t.Fatal(editorial.ConcreteTips(warm))
	}
}

func editorialFiller(draft map[string]any) string {
	return bodyPlain(draft)
}

func expandServer(t *testing.T, calls *int, data map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		raw, _ := json.Marshal(data)
		out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(raw)}}}})
		_, _ = w.Write(out)
	}))
}
