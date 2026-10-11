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

func TestFactCheckPromptForbidsInventedThresholds(t *testing.T) {
	fact := StageMessages("事实核对", map[string]any{"draft": map[string]any{"title": "弱电"}})
	system := fact[0]["content"]
	if !strings.Contains(system, "弱电") || !strings.Contains(system, "1000V") || !strings.Contains(system, "阈值") || !strings.Contains(system, "research_sources") {
		t.Fatal(system)
	}
	draft := StageMessages("正文", map[string]any{"word_target": 1500})
	if !strings.Contains(draft[0]["content"], "阈值") || !strings.Contains(draft[0]["content"], "标准编号") {
		t.Fatal(draft[0]["content"])
	}
}

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
	data, completion, err := gw.CompleteJSON(context.Background(), providers, []map[string]string{{"role": "user", "content": "{}"}}, nil, func(item map[string]any) (map[string]any, error) {
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
	_, _, err = gw.CompleteJSON(context.Background(), []Provider{{Name: "主模型", Protocol: "openai", BaseURL: fail.URL + "/v1", Model: "m", Timeout: 5 * time.Second}}, []map[string]string{{"role": "user", "content": "{}"}}, nil, func(item map[string]any) (map[string]any, error) {
		return item, nil
	})
	if err == nil || !strings.Contains(err.Error(), "全部模型服务失败") {
		t.Fatal(err)
	}
}

func TestStagePromptCarriesSchemaAndOpenAISendsIt(t *testing.T) {
	messages := StageMessages("选题", map[string]any{"direction": "职场成长"})
	if !strings.Contains(messages[0]["content"], "JSON Schema") || !strings.Contains(messages[0]["content"], `"title"`) {
		t.Fatal(messages[0]["content"])
	}
	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seen)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"好标题\",\"angle\":\"一个角度\"}"}}]}`))
	}))
	defer srv.Close()
	gw := &Gateway{Client: srv.Client(), Mock: false, MaxAttempts: 1}
	_, _, err := gw.CompleteJSON(context.Background(), []Provider{{
		Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "grok-4.7", Timeout: 5 * time.Second,
	}}, messages, StageSchema("选题"), func(item map[string]any) (map[string]any, error) {
		return item, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	format, _ := seen["response_format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("%#v", format)
	}
	spec, _ := format["json_schema"].(map[string]any)
	if spec["strict"] != true || spec["name"] != "topic" {
		t.Fatalf("%#v", spec)
	}
	if _, ok := seen["max_tokens"]; ok {
		t.Fatal("openai adapter should not invent max_tokens")
	}
	if _, ok := seen["reasoning_effort"]; ok {
		t.Fatalf("topic should not set reasoning_effort: %#v", seen["reasoning_effort"])
	}
}

func TestResponsesStreamingShape(t *testing.T) {
	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("auth %s", r.Header.Get("Authorization"))
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seen)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"{\\\"title\\\":\\\"好\\\"}\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":3}}}\n\n"))
	}))
	defer srv.Close()
	gw := &Gateway{Client: srv.Client(), Mock: false, MaxAttempts: 1}
	result, err := gw.Complete(context.Background(), Provider{
		Protocol: "responses", BaseURL: srv.URL + "/v1", APIKey: "sk-test", Model: "grok-4.7",
		ExtraParams: map[string]any{"reasoning_effort": "low"}, Timeout: 5 * time.Second,
	}, []map[string]string{{"role": "user", "content": "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if seen["stream"] != true || seen["model"] != "grok-4.7" || seen["reasoning_effort"] != "low" {
		t.Fatalf("%#v", seen)
	}
	input, _ := seen["input"].([]any)
	if len(input) != 1 {
		t.Fatal(input)
	}
	if result.Content != `{"title":"好"}` || result.PromptTokens != 2 || result.CompletionTokens != 3 || result.Protocol != "responses" {
		t.Fatalf("%#v", result)
	}
	if ResponsesURL("https://ai.1so.org/v1") != "https://ai.1so.org/v1/responses" {
		t.Fatal(ResponsesURL("https://ai.1so.org/v1"))
	}
}

func TestLongStageDefaultsEffortLow(t *testing.T) {
	seen := map[string]map[string]any{}
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		model, _ := body["model"].(string)
		seen[model] = body
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"好\"}"}}]}`))
	}))
	defer srv.Close()
	gw := &Gateway{Client: srv.Client(), Mock: false, MaxAttempts: 1}
	messages := StageMessages("正文", map[string]any{"word_target": 1500})
	accept := func(item map[string]any) (map[string]any, error) { return item, nil }
	_, _, err := gw.CompleteJSON(context.Background(), []Provider{{
		Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "grok-4.7", Timeout: 5 * time.Second,
	}}, messages, nil, accept)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = gw.CompleteJSON(context.Background(), []Provider{{
		Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "grok-chat-fast", Timeout: 5 * time.Second,
	}}, messages, nil, accept)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || !strings.Contains(paths[0], "/chat/completions") || !strings.Contains(paths[1], "/chat/completions") {
		t.Fatalf("paths %#v", paths)
	}
	if seen["grok-4.7"]["reasoning_effort"] != "low" || seen["grok-4.7"]["stream"] != true {
		t.Fatalf("%#v", seen["grok-4.7"])
	}
	if _, ok := seen["grok-chat-fast"]["reasoning_effort"]; ok {
		t.Fatalf("chat-fast should not receive reasoning_effort: %#v", seen["grok-chat-fast"]["reasoning_effort"])
	}
}

func TestIdleCutRetriesWithResponses(t *testing.T) {
	var calls int
	var efforts []any
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		paths = append(paths, r.URL.Path)
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		efforts = append(efforts, body["reasoning_effort"])
		if calls == 1 {
			if !strings.Contains(r.URL.Path, "/chat/completions") {
				t.Errorf("first path %s", r.URL.Path)
			}
			w.WriteHeader(524)
			_, _ = w.Write([]byte("error code: 524"))
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			t.Errorf("retry path %s", r.URL.Path)
		}
		if body["stream"] != true || body["reasoning_effort"] != "low" {
			t.Errorf("retry body %#v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"先想一下\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"{\\\"title\\\":\\\"好\\\"}\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":2}}}\n\n"))
	}))
	defer srv.Close()
	gw := &Gateway{Client: srv.Client(), Mock: false, MaxAttempts: 3, Backoff: time.Second}
	messages := StageMessages("正文", map[string]any{"word_target": 1500})
	_, completion, err := gw.CompleteJSON(context.Background(), []Provider{{
		ID: 7, Name: "推理", Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "grok-4.7",
		ExtraParams: map[string]any{"reasoning_effort": "high"}, Timeout: 5 * time.Second,
	}}, messages, nil, func(item map[string]any) (map[string]any, error) {
		return item, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || efforts[0] != "high" || efforts[1] != "low" {
		t.Fatalf("calls %d efforts %#v paths %#v", calls, efforts, paths)
	}
	if len(completion.Attempts) != 2 || completion.Attempts[0]["ok"] != false || completion.Attempts[1]["ok"] != true {
		t.Fatalf("%#v", completion.Attempts)
	}
	if completion.Attempts[0]["protocol"] != "openai" || completion.Attempts[1]["protocol"] != "responses" {
		t.Fatalf("%#v", completion.Attempts)
	}
	if completion.Protocol != "responses" || completion.ReasoningEffort != "low" {
		t.Fatalf("%#v", completion)
	}
}

func TestIdleCutDetection(t *testing.T) {
	if !idleCut(524, nil) || !idleCut(522, nil) {
		t.Fatal("status")
	}
	if !idleCut(0, &Error{Message: "网络错误：http2: stream error: stream ID 1; INTERNAL_ERROR; received from peer"}) {
		t.Fatal("stream")
	}
	if idleCut(400, &Error{Message: "HTTP 400：bad request"}) {
		t.Fatal("plain 400 should not look like an idle cut")
	}
}

func TestCompleteResearchDoesNotRetryInsideGateway(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(500)
		_, _ = w.Write([]byte("upstream"))
	}))
	defer srv.Close()
	gw := NewGateway(false, srv.Client())
	gw.MaxAttempts = 3
	gw.Backoff = time.Millisecond
	_, err := gw.CompleteResearch(context.Background(), []Provider{{
		Name: "检索", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "grok-chat-fast", Timeout: 30 * time.Second,
	}}, []map[string]string{{"role": "user", "content": "{}"}})
	if err == nil || calls != 1 {
		t.Fatalf("calls %d err %v", calls, err)
	}
}
