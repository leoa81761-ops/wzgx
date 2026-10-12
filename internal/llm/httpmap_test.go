package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
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

func TestConcurrentCacheDoesNotSwapCompletions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		msgs, _ := req["messages"].([]any)
		last, _ := msgs[len(msgs)-1].(map[string]any)
		text, _ := last["content"].(string)
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"` + text + `"}}]}`))
	}))
	defer srv.Close()
	g := NewGateway(false, srv.Client())
	g.CacheTTL = time.Hour
	provider := Provider{ID: 4, Name: "快", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "grok-chat-fast"}
	var wg sync.WaitGroup
	errCh := make(chan string, 2)
	for _, text := range []string{"家庭账号的开头", "弱电账号的开头"} {
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			got, err := g.CompleteText(context.Background(), []Provider{provider}, []map[string]string{{"role": "user", "content": text}}, nil, false)
			if err != nil || got.Content != text {
				errCh <- text + " -> " + got.Content
			}
		}(text)
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Fatal(msg)
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
