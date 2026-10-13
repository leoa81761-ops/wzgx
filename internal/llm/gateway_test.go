package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFactCheckPromptForbidsInventedThresholds(t *testing.T) {
	fact := StageMessages("事实核对", map[string]any{"draft": map[string]any{"title": "弱电"}})
	system := fact[0]["content"]
	if !strings.Contains(system, "弱电") || !strings.Contains(system, "1000V") || !strings.Contains(system, "阈值") || !strings.Contains(system, "research_sources") || !strings.Contains(system, "不要编造网址") || !strings.Contains(system, "不要编造标准编号") {
		t.Fatal(system)
	}
	home := StageMessages("事实核对", map[string]any{"vertical_key": "family", "direction": "家庭生活", "draft": map[string]any{"title": "周末收纳"}})
	if strings.Contains(home[0]["content"], "1000V") || strings.Contains(home[0]["content"], "弱电") {
		t.Fatal(home[0]["content"])
	}
	draft := StageMessages("正文", map[string]any{"word_target": 1500, "vertical_key": "elv", "direction": "弱电智能化"})
	if !strings.Contains(draft[0]["content"], "阈值") || !strings.Contains(draft[0]["content"], "标准编号") || !strings.Contains(draft[0]["content"], "[1]") || !strings.Contains(draft[0]["content"], "假设") || !strings.Contains(draft[0]["content"], "我在现场见过") {
		t.Fatal(draft[0]["content"])
	}
	polish := StageMessages("结构润色", map[string]any{"vertical_key": "family", "direction": "家庭生活"})
	if !strings.Contains(polish[0]["content"], "40") || !strings.Contains(polish[0]["content"], "假设") || strings.Contains(polish[0]["content"], "我在现场见过") {
		t.Fatal(polish[0]["content"])
	}
	review := StageMessages("审核", map[string]any{"direction": "职场成长"})
	if !strings.Contains(review[0]["content"], "反复") || !strings.Contains(review[0]["content"], "quality.motif") {
		t.Fatal(review[0]["content"])
	}
	titles := StageMessages("标题", map[string]any{"summary": "两个人的周末"})
	if !strings.Contains(titles[0]["content"], "consistent") || !strings.Contains(titles[0]["content"], "具体场景") || !strings.Contains(titles[0]["content"], "28") || !strings.Contains(titles[0]["content"], "叙述句") {
		t.Fatal(titles[0]["content"])
	}
	if !strings.Contains(draft[0]["content"], "reader_profile") || !strings.Contains(titles[0]["content"], "读者画像") {
		t.Fatal("reader profile instruction missing")
	}
	condense := StageMessages("缩写", map[string]any{"word_target": 800})
	if !strings.Contains(condense[0]["content"], "不要短于") || strings.Contains(condense[0]["content"], "85% 到 130%") {
		t.Fatal(condense[0]["content"])
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
	messages := StageMessages("大纲", map[string]any{"word_target": 1500})
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
	_, _, err = gw.CompleteJSON(context.Background(), []Provider{{
		Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "grok-4.7", Timeout: 5 * time.Second,
	}}, StageMessages("正文", map[string]any{"word_target": 1500}), nil, accept)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 || !strings.Contains(paths[0], "/chat/completions") || !strings.Contains(paths[1], "/chat/completions") {
		t.Fatalf("paths %#v", paths)
	}
	if seen[FallbackModel]["model"] != FallbackModel {
		t.Fatalf("draft should start on the non-reasoning model: %#v", seen[FallbackModel])
	}
	if _, ok := seen[FallbackModel]["reasoning_effort"]; ok {
		t.Fatalf("draft model should not receive reasoning_effort: %#v", seen[FallbackModel]["reasoning_effort"])
	}
	if seen["grok-4.7"]["reasoning_effort"] != "low" || seen["grok-4.7"]["stream"] != true {
		t.Fatalf("%#v", seen["grok-4.7"])
	}
	if _, ok := seen["grok-chat-fast"]["reasoning_effort"]; ok {
		t.Fatalf("chat-fast should not receive reasoning_effort: %#v", seen["grok-chat-fast"]["reasoning_effort"])
	}
}

func TestIdleCutFallsBackToFastModel(t *testing.T) {
	var calls int
	var models []string
	var efforts []any
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		paths = append(paths, r.URL.Path)
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		model, _ := body["model"].(string)
		models = append(models, model)
		efforts = append(efforts, body["reasoning_effort"])
		if calls == 1 {
			if !strings.Contains(r.URL.Path, "/chat/completions") {
				t.Errorf("first path %s", r.URL.Path)
			}
			w.WriteHeader(524)
			_, _ = w.Write([]byte("error code: 524"))
			return
		}
		if !strings.Contains(r.URL.Path, "/chat/completions") {
			t.Errorf("fallback path %s", r.URL.Path)
		}
		if model != FallbackModel {
			t.Errorf("fallback model %s", model)
		}
		if _, ok := body["reasoning_effort"]; ok {
			t.Errorf("fallback effort %#v", body["reasoning_effort"])
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"好\"}"}}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	}))
	defer srv.Close()
	gw := &Gateway{Client: srv.Client(), Mock: false, MaxAttempts: 1, Backoff: time.Second}
	messages := StageMessages("大纲", map[string]any{"word_target": 1500})
	started := time.Now()
	_, completion, err := gw.CompleteJSON(context.Background(), []Provider{{
		ID: 7, Name: "推理", Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "grok-4.7",
		ExtraParams: map[string]any{"reasoning_effort": "high"}, Timeout: 5 * time.Second,
	}}, messages, nil, func(item map[string]any) (map[string]any, error) {
		return item, nil
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed > time.Second {
		t.Fatalf("fallback waited %s", elapsed)
	}
	if calls != 2 || models[0] != "grok-4.7" || models[1] != FallbackModel || efforts[0] != "high" || efforts[1] != nil {
		t.Fatalf("calls %d models %#v efforts %#v paths %#v", calls, models, efforts, paths)
	}
	if len(completion.Attempts) != 2 || completion.Attempts[0]["ok"] != false || completion.Attempts[1]["ok"] != true {
		t.Fatalf("%#v", completion.Attempts)
	}
	if completion.Attempts[0]["model"] != "grok-4.7" || completion.Attempts[1]["model"] != FallbackModel {
		t.Fatalf("%#v", completion.Attempts)
	}
	if completion.Model != FallbackModel || completion.Protocol != "openai" || completion.ReasoningEffort != "" {
		t.Fatalf("%#v", completion)
	}
}

func TestSlowCallFallsBackWithinCap(t *testing.T) {
	if callTimeoutCap != 60*time.Second {
		t.Fatal(callTimeoutCap)
	}
	if boundTimeout(150*time.Second, 0) != 60*time.Second {
		t.Fatal(boundTimeout(150*time.Second, 0))
	}
	if boundTimeout(80*time.Millisecond, 30*time.Second) != 80*time.Millisecond {
		t.Fatal(boundTimeout(80*time.Millisecond, 30*time.Second))
	}
	var mu sync.Mutex
	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		model, _ := body["model"].(string)
		mu.Lock()
		models = append(models, model)
		mu.Unlock()
		if model != FallbackModel {
			time.Sleep(2 * time.Second)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	gw := &Gateway{Client: srv.Client(), Mock: false, MaxAttempts: 1, Backoff: time.Second}
	started := time.Now()
	result, err := gw.Complete(context.Background(), Provider{
		Name: "推理", Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "grok-4.7", Timeout: 80 * time.Millisecond,
	}, []map[string]string{{"role": "user", "content": "写一段"}})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed > time.Second {
		t.Fatalf("slow call waited %s", elapsed)
	}
	if len(models) != 2 || models[0] != "grok-4.7" || models[1] != FallbackModel || result.Content != "ok" {
		t.Fatalf("models %#v content %q", models, result.Content)
	}
}

func TestGrokInternalErrorFallsBackFaster(t *testing.T) {
	if fasterTimeout(90*time.Second, 0) != 20*time.Second {
		t.Fatal(fasterTimeout(90*time.Second, 0))
	}
	if fasterTimeout(0, 5*time.Second) != 5*time.Second {
		t.Fatal(fasterTimeout(0, 5*time.Second))
	}
	if !grokStreamInternal("grok-4.7", &Error{Message: "http2: stream error: INTERNAL_ERROR"}) {
		t.Fatal("detect")
	}
	if grokStreamInternal("grok-4", &Error{Message: "INTERNAL_ERROR"}) || grokStreamInternal("grok-4.7", &Error{Message: "HTTP 524"}) {
		t.Fatal("scope")
	}
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if calls == 1 {
			w.WriteHeader(500)
			_, _ = w.Write([]byte("stream error: INTERNAL_ERROR; received from peer"))
			return
		}
		if body["stream"] != false {
			t.Errorf("retry should not stream %#v", body["stream"])
		}
		_, _ = w.Write([]byte(`{"output_text":"{\"title\":\"好\"}","usage":{"input_tokens":1,"output_tokens":2}}`))
	}))
	defer srv.Close()
	gw := &Gateway{Client: srv.Client(), Mock: false, MaxAttempts: 3, Backoff: time.Millisecond}
	_, _, err := gw.CompleteJSON(context.Background(), []Provider{{
		Name: "推理", Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "grok-4.7",
		ExtraParams: map[string]any{"reasoning_effort": "high"}, Timeout: 90 * time.Second,
	}}, StageMessages("大纲", map[string]any{"word_target": 800}), nil, func(item map[string]any) (map[string]any, error) {
		return item, nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("calls %d err %v", calls, err)
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

func TestCompleteResearchUsesConfiguredTimeout(t *testing.T) {
	var calls int
	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		model, _ := body["model"].(string)
		models = append(models, model)
		time.Sleep(400 * time.Millisecond)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	gw := &Gateway{Client: srv.Client(), Mock: false, MaxAttempts: 3, Backoff: time.Second, ResearchTimeout: 150 * time.Millisecond}
	started := time.Now()
	_, err := gw.CompleteResearch(context.Background(), []Provider{{
		Name: "检索", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "grok-chat-fast", Timeout: 30 * time.Second,
	}}, []map[string]string{{"role": "user", "content": `{"环节":"资料检索"}`}})
	elapsed := time.Since(started)
	if err == nil || calls != 2 || models[0] != "grok-chat-fast" || models[1] != FallbackModel || elapsed > time.Second {
		t.Fatalf("calls %d models %#v elapsed %s err %v", calls, models, elapsed, err)
	}
}

func TestResearchChatFastStreamsAndKeepsFinalCitations(t *testing.T) {
	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seen)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"参见 https://made-up.invalid/gb\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{}}],\"citations\":[\"https://www.gov.cn/zhengce\"]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	gw := NewGateway(false, srv.Client())
	gw.ResearchTimeout = 150 * time.Second
	result, err := gw.CompleteResearch(context.Background(), []Provider{{
		Name: "检索", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "grok-chat-fast", Timeout: 30 * time.Second,
	}}, StageMessages("资料检索", map[string]any{"topic_title": "弱电"}))
	if err != nil {
		t.Fatal(err)
	}
	if seen["stream"] != true || seen["model"] != "grok-chat-fast" {
		t.Fatalf("%#v", seen)
	}
	if _, ok := seen["tools"]; ok {
		t.Fatalf("tools %#v", seen["tools"])
	}
	if len(result.ResearchSources) != 1 || result.ResearchSources[0]["url"] != "https://www.gov.cn/zhengce" {
		t.Fatalf("%#v", result.ResearchSources)
	}
	if strings.Contains(asString(result.ResearchSources[0]["url"]), "made-up") {
		t.Fatal(result.ResearchSources)
	}
}

func TestResearchCallsCapAt60Seconds(t *testing.T) {
	stream, timeout := researchTransport(Provider{Protocol: "openai", Model: "grok-chat-fast"}, 150*time.Second)
	if !stream || timeout != 60*time.Second {
		t.Fatalf("chat-fast %v %s", stream, timeout)
	}
	stream, timeout = researchTransport(Provider{Protocol: "openai", Model: "grok-4.7"}, 150*time.Second)
	if stream || timeout != 60*time.Second {
		t.Fatalf("grok-4.7 %v %s", stream, timeout)
	}
	stream, timeout = researchTransport(Provider{Protocol: "responses", Model: "grok-4.7"}, 90*time.Second)
	if stream || timeout != 60*time.Second {
		t.Fatalf("short %v %s", stream, timeout)
	}
	stream, timeout = researchTransport(Provider{Protocol: "openai", Model: "grok-chat-fast"}, 30*time.Second)
	if !stream || timeout != 30*time.Second {
		t.Fatalf("configured short %v %s", stream, timeout)
	}
}

func TestResearchRetriesQuickGatewayErrors(t *testing.T) {
	if !quickResearchError(502, &Error{Message: "HTTP 502：bad gateway"}, 32*time.Second) {
		t.Fatal("fast 502")
	}
	if !quickResearchError(503, &Error{Message: "HTTP 503：unavailable"}, time.Second) {
		t.Fatal("503")
	}
	if quickResearchError(502, &Error{Message: "HTTP 502：bad gateway"}, 40*time.Second) {
		t.Fatal("slow 502 should not retry")
	}
	if quickResearchError(500, &Error{Message: "HTTP 500：upstream"}, time.Second) {
		t.Fatal("500")
	}
	if !quickResearchError(0, &Error{Message: "网络错误：connection reset by peer"}, time.Second) {
		t.Fatal("connection")
	}
	if quickResearchError(0, &Error{Message: "网络错误：context deadline exceeded"}, 150*time.Millisecond) {
		t.Fatal("timeout")
	}
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 2 {
			w.WriteHeader(502)
			_, _ = w.Write([]byte("bad gateway"))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"检索完成"}}],"citations":["https://www.gov.cn/zhengce"]}`))
	}))
	defer srv.Close()
	gw := NewGateway(false, srv.Client())
	gw.Backoff = time.Millisecond
	gw.MaxAttempts = 1
	started := time.Now()
	result, err := gw.CompleteResearch(context.Background(), []Provider{{
		Name: "检索", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "grok-chat-fast", Timeout: 5 * time.Second,
	}}, StageMessages("资料检索", map[string]any{"topic_title": "弱电"}))
	if err != nil || calls != 3 || len(result.ResearchSources) != 1 {
		t.Fatalf("calls %d elapsed %s err %v sources %#v", calls, time.Since(started), err, result.ResearchSources)
	}
	if len(result.Attempts) < 2 || result.Attempts[0]["ok"] != false {
		t.Fatalf("%#v", result.Attempts)
	}
}

func TestResearchResponsesDoesNotAddWebSearch(t *testing.T) {
	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seen)
		_, _ = w.Write([]byte(`{"output_text":"参见 https://www.mohurd.gov.cn/fake","usage":{"num_sources_used":0}}`))
	}))
	defer srv.Close()
	gw := NewGateway(false, srv.Client())
	result, err := gw.CompleteResearch(context.Background(), []Provider{{
		Name: "推理", Protocol: "responses", BaseURL: srv.URL + "/v1", Model: "grok-4.7", Timeout: 5 * time.Second,
	}}, StageMessages("资料检索", map[string]any{"topic_title": "弱电"}))
	if err != nil {
		t.Fatal(err)
	}
	if seen["stream"] != false {
		t.Fatalf("non chat-fast should not stream: %#v", seen["stream"])
	}
	if _, ok := seen["tools"]; ok {
		t.Fatalf("tools %#v", seen["tools"])
	}
	if len(result.ResearchSources) != 0 {
		t.Fatalf("%#v", result.ResearchSources)
	}
}

func TestDraftStartsOnNonReasoningModel(t *testing.T) {
	var calls int
	var model string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		model, _ = body["model"].(string)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	gw := &Gateway{Client: srv.Client(), Mock: false, MaxAttempts: 1}
	_, err := gw.Complete(context.Background(), Provider{
		Name: "推理", Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "grok-4.7", Timeout: 5 * time.Second,
	}, StageMessages("正文", map[string]any{"word_target": 1500}))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || model != FallbackModel {
		t.Fatalf("calls %d model %s", calls, model)
	}
}

func TestOutlineFirstTokenFallsBack(t *testing.T) {
	if firstTokenTimeout != 20*time.Second {
		t.Fatal(firstTokenTimeout)
	}
	var mu sync.Mutex
	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		model, _ := body["model"].(string)
		mu.Lock()
		models = append(models, model)
		mu.Unlock()
		if model != FallbackModel {
			time.Sleep(200 * time.Millisecond)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	gw := &Gateway{Client: srv.Client(), Mock: false, MaxAttempts: 1, Backoff: time.Second}
	started := time.Now()
	result, err := gw.Complete(context.Background(), Provider{
		Name: "推理", Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "grok-4.7",
		Timeout: 5 * time.Second, FirstToken: 40 * time.Millisecond,
	}, StageMessages("大纲", map[string]any{"word_target": 1500}))
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed > time.Second {
		t.Fatalf("first token waited %s", elapsed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(models) != 2 || models[0] != "grok-4.7" || models[1] != FallbackModel || result.Content != "ok" {
		t.Fatalf("models %#v content %q", models, result.Content)
	}
}

func TestOutlineFirstByteIsNotCut(t *testing.T) {
	var calls int
	var model string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		model, _ = body["model"].(string)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	gw := &Gateway{Client: srv.Client(), Mock: false, MaxAttempts: 1}
	result, err := gw.Complete(context.Background(), Provider{
		Name: "推理", Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "grok-4.7",
		Timeout: 5 * time.Second, FirstToken: 40 * time.Millisecond,
	}, StageMessages("结构润色", map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || model != "grok-4.7" || result.Content != "ok" {
		t.Fatalf("calls %d model %s content %q", calls, model, result.Content)
	}
}
