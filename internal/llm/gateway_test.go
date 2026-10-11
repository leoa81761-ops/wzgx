package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestURLNormalization(t *testing.T) {
	if OpenAIURL("https://api.openai.com/v1") != "https://api.openai.com/v1/chat/completions" {
		t.Fatal(OpenAIURL("https://api.openai.com/v1"))
	}
	if OpenAIURL("https://llm.example.com/proxy/chat/completions") != "https://llm.example.com/proxy/chat/completions" {
		t.Fatal("openai already complete")
	}
	if AnthropicURL("https://api.anthropic.com") != "https://api.anthropic.com/v1/messages" {
		t.Fatal(AnthropicURL("https://api.anthropic.com"))
	}
	if AnthropicURL("https://claude.example.com/v1") != "https://claude.example.com/v1/messages" {
		t.Fatal(AnthropicURL("https://claude.example.com/v1"))
	}
	if AnthropicURL("https://claude.example.com/v1/messages") != "https://claude.example.com/v1/messages" {
		t.Fatal("anthropic already complete")
	}
}

func TestOpenAIRequestShapeAndSSE(t *testing.T) {
	var seenBody map[string]any
	var auth, custom string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		custom = r.Header.Get("X-Custom")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seenBody)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"title\\\":\\\"好\\\"}\"}}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()
	gw := &Gateway{Client: srv.Client(), Mock: false, MaxAttempts: 1}
	result, err := gw.Complete(context.Background(), Provider{
		ID: 1, Name: "主模型", Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "sk-test-key", Model: "demo-model",
		ExtraHeaders: map[string]string{"X-Custom": "abc"}, ExtraParams: map[string]any{"temperature": 0.2, "model": "should-not-win"}, Timeout: 5 * time.Second,
	}, []map[string]string{{"role": "user", "content": "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk-test-key" || custom != "abc" {
		t.Fatalf("headers %s %s", auth, custom)
	}
	if seenBody["model"] != "demo-model" || seenBody["stream"] != true || seenBody["temperature"] != 0.2 {
		t.Fatalf("body %#v", seenBody)
	}
	if result.Content != `{"title":"好"}` || result.PromptTokens != 3 || result.Protocol != "openai" {
		t.Fatalf("%#v", result)
	}
}

func TestOpenAIJSONFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"好\"}"}}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`))
	}))
	defer srv.Close()
	gw := &Gateway{Client: srv.Client(), Mock: false, MaxAttempts: 1}
	result, err := gw.Complete(context.Background(), Provider{
		Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "m", Timeout: 5 * time.Second,
	}, []map[string]string{{"role": "user", "content": "hi"}})
	if err != nil || result.Content != `{"title":"好"}` || result.CompletionTokens != 4 {
		t.Fatalf("%v %#v", err, result)
	}
}

func TestAnthropicRequestShape(t *testing.T) {
	var seenBody map[string]any
	var key, version, custom string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path %s", r.URL.Path)
		}
		key = r.Header.Get("x-api-key")
		version = r.Header.Get("anthropic-version")
		custom = r.Header.Get("X-Custom")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seenBody)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"{\"title\":\"好\"}"}],"usage":{"input_tokens":5,"output_tokens":6}}`))
	}))
	defer srv.Close()
	gw := &Gateway{Client: srv.Client(), Mock: false, MaxAttempts: 1}
	result, err := gw.Complete(context.Background(), Provider{
		Protocol: "anthropic", BaseURL: srv.URL, APIKey: "sk-test-key", Model: "demo-model",
		ExtraHeaders: map[string]string{"X-Custom": "abc"}, ExtraParams: map[string]any{"temperature": 0.2, "model": "should-not-win"}, Timeout: 5 * time.Second,
	}, []map[string]string{{"role": "system", "content": "只输出 JSON"}, {"role": "user", "content": "写一段"}})
	if err != nil {
		t.Fatal(err)
	}
	if key != "sk-test-key" || version != "2023-06-01" || custom != "abc" {
		t.Fatalf("headers %s %s %s", key, version, custom)
	}
	if seenBody["model"] != "demo-model" || seenBody["stream"] != true || seenBody["system"] != "只输出 JSON" {
		t.Fatalf("%#v", seenBody)
	}
	msgs, _ := seenBody["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatal(msgs)
	}
	if result.CompletionTokens != 6 {
		t.Fatal(result.CompletionTokens)
	}
	if _, ok := seenBody["max_tokens"]; !ok {
		t.Fatal("missing max_tokens")
	}
}

func TestGatewayFallsBackAndAllFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/messages") {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"error":{"message":"overloaded"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"备用模型\"}"}}]}`))
	}))
	defer srv.Close()
	gw := &Gateway{Client: srv.Client(), Mock: false, MaxAttempts: 1, Backoff: time.Millisecond}
	providers := []Provider{
		{ID: 1, Name: "Claude", Protocol: "anthropic", BaseURL: srv.URL, APIKey: "k", Model: "c", Timeout: 5 * time.Second},
		{ID: 2, Name: "兼容接口", Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "o", Timeout: 5 * time.Second},
	}
	data, completion, err := gw.CompleteJSON(context.Background(), providers, []map[string]string{{"role": "user", "content": "{}"}}, func(item map[string]any) (map[string]any, error) {
		return item, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if data["title"] != "备用模型" {
		t.Fatal(data)
	}
	if len(completion.Attempts) != 2 || completion.Attempts[0]["ok"] != false || completion.Attempts[1]["ok"] != true || completion.Attempts[1]["protocol"] != "openai" {
		t.Fatalf("%#v", completion.Attempts)
	}

	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte("rate limit"))
	}))
	defer fail.Close()
	gw.Client = fail.Client()
	_, _, err = gw.CompleteJSON(context.Background(), []Provider{{Name: "主模型", Protocol: "openai", BaseURL: fail.URL + "/v1", Model: "m", Timeout: 5 * time.Second}}, []map[string]string{{"role": "user", "content": "{}"}}, func(item map[string]any) (map[string]any, error) {
		return item, nil
	})
	if err == nil || !strings.Contains(err.Error(), "全部模型服务失败") {
		t.Fatal(err)
	}
}
