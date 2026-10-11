package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

type Provider struct {
	ID           int64
	Name         string
	Protocol     string
	BaseURL      string
	APIKey       string
	Model        string
	ExtraHeaders map[string]string
	ExtraParams  map[string]any
	Timeout      time.Duration
}

type Completion struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	ProviderID       int64
	ProviderName     string
	Protocol         string
	Model            string
	Attempts         []map[string]any
}

type Gateway struct {
	Client      *http.Client
	Mock        bool
	Backoff     time.Duration
	MaxAttempts int
}

func NewGateway(mock bool, client *http.Client) *Gateway {
	if client == nil {
		client = http.DefaultClient
	}
	return &Gateway{Client: client, Mock: mock, Backoff: 200 * time.Millisecond, MaxAttempts: 3}
}

func PublicParams(params map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range params {
		if strings.HasPrefix(key, "_") {
			continue
		}
		out[key] = value
	}
	return out
}

func OpenAIURL(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return base + "/chat/completions"
}

func AnthropicURL(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/messages") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/messages"
	}
	return base + "/v1/messages"
}

func (g *Gateway) CompleteJSON(ctx context.Context, providers []Provider, messages []map[string]string, validate func(map[string]any) (map[string]any, error)) (map[string]any, Completion, error) {
	if len(providers) == 0 {
		return nil, Completion{}, &Error{Message: "没有可用的模型服务。请管理员在配置中心添加供应商，或使用模拟模式。"}
	}
	var errorsFound []string
	var attempts []map[string]any
	for _, provider := range providers {
		if provider.Protocol != "openai" && provider.Protocol != "anthropic" {
			message := fmt.Sprintf("%s: 不支持的协议 %s", provider.Name, provider.Protocol)
			errorsFound = append(errorsFound, message)
			attempts = append(attempts, map[string]any{"provider_id": provider.ID, "protocol": provider.Protocol, "ok": false, "error": message})
			continue
		}
		completion, err := g.complete(ctx, provider, messages)
		if err != nil {
			message := fmt.Sprintf("%s: %s", provider.Name, err.Error())
			errorsFound = append(errorsFound, message)
			attempts = append(attempts, map[string]any{"provider_id": provider.ID, "provider_name": provider.Name, "protocol": provider.Protocol, "model": provider.Model, "ok": false, "error": clip(err.Error(), 300)})
			continue
		}
		data, err := ParseJSONContent(completion.Content)
		if err != nil {
			message := fmt.Sprintf("%s: %s", provider.Name, err.Error())
			errorsFound = append(errorsFound, message)
			attempts = append(attempts, map[string]any{"provider_id": provider.ID, "provider_name": provider.Name, "protocol": provider.Protocol, "model": provider.Model, "ok": false, "error": clip(err.Error(), 300)})
			continue
		}
		validated, err := validate(data)
		if err != nil {
			message := fmt.Sprintf("%s: %s", provider.Name, err.Error())
			errorsFound = append(errorsFound, message)
			attempts = append(attempts, map[string]any{"provider_id": provider.ID, "provider_name": provider.Name, "protocol": provider.Protocol, "model": provider.Model, "ok": false, "error": clip(err.Error(), 300)})
			continue
		}
		attempts = append(attempts, map[string]any{"provider_id": provider.ID, "provider_name": provider.Name, "protocol": provider.Protocol, "model": provider.Model, "ok": true})
		completion.Attempts = attempts
		return validated, completion, nil
	}
	return nil, Completion{}, &Error{Message: "全部模型服务失败：" + strings.Join(errorsFound, "；")}
}

func (g *Gateway) Complete(ctx context.Context, provider Provider, messages []map[string]string) (Completion, error) {
	return g.complete(ctx, provider, messages)
}

func (g *Gateway) complete(ctx context.Context, provider Provider, messages []map[string]string) (Completion, error) {
	if g.Mock || strings.HasPrefix(provider.BaseURL, "mock://") {
		return mockComplete(provider, messages)
	}
	attempts := g.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	var last error
	for try := 0; try < attempts; try++ {
		if try > 0 {
			wait := g.Backoff
			if wait > 0 {
				wait = wait * time.Duration(1<<(try-1))
				jitter := time.Duration(rand.Int63n(int64(wait/5 + 1)))
				timer := time.NewTimer(wait + jitter)
				select {
				case <-ctx.Done():
					timer.Stop()
					return Completion{}, ctx.Err()
				case <-timer.C:
				}
			}
		}
		completion, status, err := g.call(ctx, provider, messages)
		if err == nil {
			return completion, nil
		}
		last = err
		if !retryable(status, err) || try == attempts-1 {
			break
		}
	}
	if last == nil {
		last = &Error{Message: "模型调用失败"}
	}
	return Completion{}, last
}

func retryable(status int, err error) bool {
	if status == 429 || status >= 500 {
		return true
	}
	if status == 0 && err != nil {
		var pe *Error
		if errors.As(err, &pe) && strings.HasPrefix(pe.Message, "HTTP ") {
			return false
		}
		return true
	}
	return false
}

func (g *Gateway) call(ctx context.Context, provider Provider, messages []map[string]string) (Completion, int, error) {
	timeout := provider.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if provider.Protocol == "anthropic" {
		return g.callAnthropic(ctx, provider, messages)
	}
	return g.callOpenAI(ctx, provider, messages)
}

func (g *Gateway) callOpenAI(ctx context.Context, provider Provider, messages []map[string]string) (Completion, int, error) {
	body := PublicParams(provider.ExtraParams)
	msgs := make([]map[string]string, len(messages))
	copy(msgs, messages)
	body["model"] = provider.Model
	body["messages"] = msgs
	body["stream"] = true
	raw, status, err := g.post(ctx, OpenAIURL(provider.BaseURL), openAIHeaders(provider), body)
	if err != nil {
		return Completion{}, status, err
	}
	text, prompt, completion, err := readOpenAI(raw)
	if err != nil {
		return Completion{}, status, err
	}
	return Completion{Content: text, PromptTokens: prompt, CompletionTokens: completion, ProviderID: provider.ID, ProviderName: provider.Name, Protocol: "openai", Model: provider.Model}, status, nil
}

func (g *Gateway) callAnthropic(ctx context.Context, provider Provider, messages []map[string]string) (Completion, int, error) {
	var systemParts []string
	var chat []map[string]string
	for _, message := range messages {
		if message["role"] == "system" {
			systemParts = append(systemParts, message["content"])
			continue
		}
		role := "user"
		if message["role"] == "assistant" {
			role = "assistant"
		}
		chat = append(chat, map[string]string{"role": role, "content": message["content"]})
	}
	if len(chat) == 0 {
		chat = []map[string]string{{"role": "user", "content": "请继续"}}
	}
	body := PublicParams(provider.ExtraParams)
	delete(body, "messages")
	body["model"] = provider.Model
	if _, ok := body["max_tokens"]; !ok {
		body["max_tokens"] = 4096
	}
	body["messages"] = chat
	body["stream"] = true
	if len(systemParts) > 0 {
		body["system"] = strings.Join(systemParts, "\n")
	}
	raw, status, err := g.post(ctx, AnthropicURL(provider.BaseURL), anthropicHeaders(provider), body)
	if err != nil {
		return Completion{}, status, err
	}
	text, prompt, completion, err := readAnthropic(raw)
	if err != nil {
		return Completion{}, status, err
	}
	return Completion{Content: text, PromptTokens: prompt, CompletionTokens: completion, ProviderID: provider.ID, ProviderName: provider.Name, Protocol: "anthropic", Model: provider.Model}, status, nil
}

func openAIHeaders(provider Provider) map[string]string {
	headers := map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + provider.APIKey}
	for key, value := range provider.ExtraHeaders {
		headers[key] = value
	}
	return headers
}

func anthropicHeaders(provider Provider) map[string]string {
	headers := map[string]string{"Content-Type": "application/json", "x-api-key": provider.APIKey, "anthropic-version": "2023-06-01"}
	for key, value := range provider.ExtraHeaders {
		headers[key] = value
	}
	return headers
}

func (g *Gateway) post(ctx context.Context, url string, headers map[string]string, body map[string]any) ([]byte, int, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, &Error{Message: "网络错误：" + err.Error()}
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, 0, &Error{Message: "网络错误：" + err.Error()}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, &Error{Message: "网络错误：" + err.Error()}
	}
	if resp.StatusCode >= 400 {
		return data, resp.StatusCode, &Error{Message: fmt.Sprintf("HTTP %d：%s", resp.StatusCode, clip(string(data), 300))}
	}
	return data, resp.StatusCode, nil
}

func readOpenAI(raw []byte) (string, int, int, error) {
	if isJSON(raw) {
		return openAIJSON(raw)
	}
	var text strings.Builder
	prompt, completion := 0, 0
	err := eachSSE(raw, func(data []byte) error {
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			return nil
		}
		var chunk map[string]any
		if json.Unmarshal(data, &chunk) != nil {
			return nil
		}
		if usage, ok := chunk["usage"].(map[string]any); ok {
			prompt = intOf(usage["prompt_tokens"])
			completion = intOf(usage["completion_tokens"])
		}
		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			return nil
		}
		choice, _ := choices[0].(map[string]any)
		if delta, ok := choice["delta"].(map[string]any); ok {
			text.WriteString(textOf(delta["content"]))
		}
		if msg, ok := choice["message"].(map[string]any); ok {
			text.WriteString(textOf(msg["content"]))
		}
		return nil
	})
	if err != nil {
		return "", 0, 0, err
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", 0, 0, &Error{Message: "OpenAI 兼容接口没有返回文本"}
	}
	return text.String(), prompt, completion, nil
}

func openAIJSON(raw []byte) (string, int, int, error) {
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return "", 0, 0, &Error{Message: "模型接口没有返回 JSON"}
	}
	if msg := apiError(data); msg != "" {
		return "", 0, 0, &Error{Message: msg}
	}
	usage, _ := data["usage"].(map[string]any)
	text := openAIText(data)
	if strings.TrimSpace(text) == "" {
		return "", 0, 0, &Error{Message: "OpenAI 兼容接口没有返回文本"}
	}
	return text, intOf(usage["prompt_tokens"]), intOf(usage["completion_tokens"]), nil
}

func openAIText(data map[string]any) string {
	choices, _ := data["choices"].([]any)
	if len(choices) == 0 {
		return ""
	}
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	if message == nil {
		return ""
	}
	return textOf(message["content"])
}

func readAnthropic(raw []byte) (string, int, int, error) {
	if isJSON(raw) {
		return anthropicJSON(raw)
	}
	var text strings.Builder
	prompt, completion := 0, 0
	err := eachSSE(raw, func(data []byte) error {
		var chunk map[string]any
		if json.Unmarshal(data, &chunk) != nil {
			return nil
		}
		switch chunk["type"] {
		case "message_start":
			if msg, ok := chunk["message"].(map[string]any); ok {
				if usage, ok := msg["usage"].(map[string]any); ok {
					prompt = intOf(usage["input_tokens"])
				}
			}
		case "content_block_delta":
			if delta, ok := chunk["delta"].(map[string]any); ok {
				text.WriteString(asString(delta["text"]))
			}
		case "message_delta":
			if usage, ok := chunk["usage"].(map[string]any); ok {
				if n := intOf(usage["output_tokens"]); n > 0 {
					completion = n
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", 0, 0, err
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", 0, 0, &Error{Message: "Anthropic 接口没有返回文本"}
	}
	return text.String(), prompt, completion, nil
}

func anthropicJSON(raw []byte) (string, int, int, error) {
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return "", 0, 0, &Error{Message: "模型接口没有返回 JSON"}
	}
	if msg := apiError(data); msg != "" {
		return "", 0, 0, &Error{Message: msg}
	}
	usage, _ := data["usage"].(map[string]any)
	text := anthropicText(data)
	if strings.TrimSpace(text) == "" {
		return "", 0, 0, &Error{Message: "Anthropic 接口没有返回文本"}
	}
	return text, intOf(usage["input_tokens"]), intOf(usage["output_tokens"]), nil
}

func anthropicText(data map[string]any) string {
	blocks, _ := data["content"].([]any)
	var texts []string
	for _, block := range blocks {
		switch b := block.(type) {
		case map[string]any:
			if b["type"] == "text" || asString(b["text"]) != "" {
				texts = append(texts, asString(b["text"]))
			}
		case string:
			texts = append(texts, b)
		}
	}
	return strings.Join(texts, "\n")
}

func apiError(data map[string]any) string {
	raw, ok := data["error"]
	if !ok || raw == nil {
		return ""
	}
	if m, ok := raw.(map[string]any); ok {
		if msg := asString(m["message"]); msg != "" {
			return msg
		}
	}
	return fmt.Sprint(raw)
}

func isJSON(raw []byte) bool {
	trim := bytes.TrimSpace(raw)
	return len(trim) > 0 && trim[0] == '{'
}

func eachSSE(raw []byte, fn func([]byte) error) error {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 64*1024), 2<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if err := fn([]byte(data)); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

func textOf(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var parts []string
		for _, part := range t {
			switch p := part.(type) {
			case string:
				parts = append(parts, p)
			case map[string]any:
				if p["text"] != nil {
					parts = append(parts, asString(p["text"]))
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func ParseJSONContent(text string) (map[string]any, error) {
	raw := strings.TrimSpace(text)
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```")
		raw = strings.TrimPrefix(raw, "json")
		raw = strings.TrimSpace(raw)
		raw = strings.TrimSuffix(raw, "```")
		raw = strings.TrimSpace(raw)
	}
	var data map[string]any
	if json.Unmarshal([]byte(raw), &data) != nil {
		start := strings.Index(raw, "{")
		end := strings.LastIndex(raw, "}")
		if start < 0 || end <= start {
			return nil, &Error{Message: "模型输出不是 JSON 对象"}
		}
		if json.Unmarshal([]byte(raw[start:end+1]), &data) != nil || data == nil {
			return nil, &Error{Message: "模型输出不是 JSON 对象"}
		}
	}
	if data == nil {
		return nil, &Error{Message: "模型输出不是 JSON 对象"}
	}
	return data, nil
}
