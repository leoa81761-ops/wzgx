package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestMappedHTTPFieldsAndCache(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"payload":{"text":"映射成功","in":3,"out":4}}`))
	}))
	defer srv.Close()
	g := NewGateway(false, srv.Client())
	g.CacheTTL = time.Hour
	provider := Provider{
		Name: "custom", Protocol: "http", BaseURL: srv.URL, Model: "m",
		ExtraParams: map[string]any{"response_text": "payload.text", "response_prompt_tokens": "payload.in", "response_completion_tokens": "payload.out"},
	}
	messages := []map[string]string{{"role": "user", "content": "你好"}}
	first, err := g.CompleteText(context.Background(), []Provider{provider}, messages, nil, false)
	if err != nil || first.Content != "映射成功" || first.PromptTokens != 3 || first.CompletionTokens != 4 {
		t.Fatal(first, err)
	}
	second, err := g.CompleteText(context.Background(), []Provider{provider}, messages, nil, false)
	if err != nil || second.Content != "映射成功" || atomic.LoadInt32(&hits) != 1 {
		t.Fatal(second, err, hits)
	}
}

func TestCompleteBatchOneHTTPCall(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"[\"第一段\",\"标题\"]"}}]}`))
	}))
	defer srv.Close()
	g := NewGateway(false, srv.Client())
	out, err := g.CompleteBatch(context.Background(), []Provider{{Name: "batch", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "m"}}, [][]map[string]string{
		{{"role": "user", "content": "写一段"}},
		{{"role": "user", "content": "写标题"}},
	})
	if err != nil || len(out) != 2 || out[0].Content != "第一段" || out[1].Content != "标题" || atomic.LoadInt32(&hits) != 1 {
		t.Fatal(out, err, hits)
	}
}
