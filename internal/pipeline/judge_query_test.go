package pipeline

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/llm"
)

func judgeCandidates() []map[string]any {
	titles := []string{"面板高度对不上", "信息点标签先核对", "线槽盖板留在旁边", "配线架端口写上号", "弱电间门禁要登记"}
	var out []map[string]any
	for _, title := range titles {
		out = append(out, map[string]any{"title": title, "angle": title, "pain": title})
	}
	return out
}

func TestJudgeReadsIndexTitleAndJSON(t *testing.T) {
	cands := judgeCandidates()
	second := "信息点标签先核对"
	third := "线槽盖板留在旁边"
	cases := []struct {
		name string
		raw  string
		data map[string]any
		want string
	}{
		{"number", "", map[string]any{"winner": float64(2)}, second},
		{"numeric string", `{"winner":"2"}`, map[string]any{"winner": "2"}, second},
		{"ordinal", "", map[string]any{"winner": "第2个"}, second},
		{"exact title", "", map[string]any{"winner": third}, third},
		{"nested title", "", map[string]any{"winner": map[string]any{"title": third}}, third},
		{"json string", `{"winner":{"index":3}}`, nil, third},
		{"out of range 20", `{"winner":20}`, map[string]any{"winner": float64(20)}, ""},
		{"out of range 36", `{"winner":36}`, map[string]any{"winner": float64(36)}, ""},
		{"twenty characters", strings.Repeat("甲", 20), nil, ""},
		{"thirty six characters", strings.Repeat("乙", 36), nil, ""},
		{"scores", "", map[string]any{"winner": "没有这个标题", "scores": []any{float64(1), float64(9), float64(2)}}, ""},
	}
	if utf8.RuneCountInString(strings.Repeat("甲", 20)) != 20 || utf8.RuneCountInString(strings.Repeat("乙", 36)) != 36 {
		t.Fatal("fixture length")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := interpretJudge(tc.raw, tc.data, cands)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	if bestScoredTitle(map[string]any{"scores": []any{float64(1), float64(9), float64(0)}}, nil, cands) != second {
		t.Fatal("scores")
	}
	if chooseJudgedTitle(t.Context(), nil, nil, nil, map[string]any{"candidates": []any{cands[0], cands[1]}}, "", nil, nil) != "面板高度对不上" {
		t.Fatal("first candidate")
	}
}

func TestJudgeHTTPShapesPickACandidate(t *testing.T) {
	cands := judgeCandidates()
	replies := []struct {
		body string
		want string
	}{
		{`{"winner":2}`, "信息点标签先核对"},
		{`{"winner":"线槽盖板留在旁边"}`, "线槽盖板留在旁边"},
		{`{"winner":{"title":"配线架端口写上号"}}`, "配线架端口写上号"},
		{strings.Repeat("甲", 20), "面板高度对不上"},
		{strings.Repeat("乙", 36), "面板高度对不上"},
		{`{"winner":20}`, "面板高度对不上"},
		{`{"winner":36}`, "面板高度对不上"},
	}
	for _, reply := range replies {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := json.Marshal(map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{"content": reply.body}}},
			})
			_, _ = w.Write(raw)
		}))
		gateway := llm.NewGateway(false, srv.Client())
		providers := []llm.Provider{{
			Name: "选题", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 5 * time.Second,
		}}
		got := chooseJudgedTitle(t.Context(), gateway, providers, map[string]any{"column": "现场笔记"}, map[string]any{"candidates": candidateAny(cands)}, "备用标题", nil, nil)
		srv.Close()
		if got != reply.want {
			t.Fatalf("body %q got %q want %q", reply.body, got, reply.want)
		}
	}
}

func TestSearchQueriesStayOnTheChosenTopic(t *testing.T) {
	dropped := queriesOnTopic([]string{"弱电条文", "弱电标准", "弱电项目"}, map[string]any{"topic_title": "面板高度对不上", "direction": "弱电智能化"})
	if len(dropped) != 0 {
		t.Fatal(dropped)
	}
	kept := queriesOnTopic([]string{"弱电条文", "面板高度现场"}, map[string]any{"topic_title": "面板高度对不上"})
	if len(kept) != 1 || kept[0] != "面板高度现场" {
		t.Fatal(kept)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeChoice(w, map[string]any{"queries": []any{"弱电条文", "弱电标准", "弱电项目"}})
	}))
	defer srv.Close()
	gateway := llm.NewGateway(false, srv.Client())
	providers := []llm.Provider{{
		Name: "检索", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 5 * time.Second,
	}}
	seen := searchQueries(t.Context(), gateway, providers, map[string]any{
		"topic_title": "面板高度对不上", "direction": "弱电智能化",
	})
	for _, query := range seen {
		if strings.Contains(query, "弱电") {
			t.Fatal(seen)
		}
	}
	if len(seen) == 0 || !strings.Contains(strings.Join(seen, " "), "面板高度对不上") {
		t.Fatal(seen)
	}
	var completion llm.Completion
	stampSourceTrace(&completion, nil, nil, false, 0, nil, seen)
	last := completion.Attempts[len(completion.Attempts)-1]
	logged, _ := last["queries"].([]string)
	if len(logged) != len(seen) || logged[0] != seen[0] {
		t.Fatal(last)
	}
}

func candidateAny(rows []map[string]any) []any {
	out := make([]any, len(rows))
	for i, row := range rows {
		out[i] = row
	}
	return out
}
