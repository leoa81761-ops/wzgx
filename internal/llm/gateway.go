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
	"sync"
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
	// FirstToken aborts a call that produces no bytes within this duration.
	// Outline and polish on a reasoning model use it so a silent call does not wait out the 60s cap.
	FirstToken time.Duration
}

type Completion struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	ProviderID       int64
	ProviderName     string
	Protocol         string
	Model            string
	ReasoningEffort  string
	Duration         time.Duration
	Attempts         []map[string]any
	ResearchSources  []map[string]any
}

type Gateway struct {
	Client              *http.Client
	Mock                bool
	Backoff             time.Duration
	MaxAttempts         int
	ResearchTimeout     time.Duration
	ResearchBackupModel string
	BackupSearchURL     string
	BackupSearchAlt     string
	BackupSearchBaidu   string
	SearchAPIURL        string
	SearchAPIKey        string
	SearchAPIProvider   string
	CacheTTL            time.Duration
	cacheMu             sync.Mutex
	cache               map[string]cachedCompletion
}

type cachedCompletion struct {
	until      time.Time
	completion Completion
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

func ResponsesURL(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/responses") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/responses"
	}
	return base + "/v1/responses"
}

func applyChatSchema(body map[string]any, schema *Schema) {
	if schema == nil || schema.Body == nil {
		return
	}
	if _, exists := body["response_format"]; exists {
		return
	}
	body["response_format"] = map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   schema.Name,
			"strict": true,
			"schema": schema.Body,
		},
	}
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

type callLimit struct {
	timeout  time.Duration
	attempts int
	research bool
}

// callTimeoutCap stops a model call before Cloudflare answers 524 at about 125s.
const callTimeoutCap = 60 * time.Second

// FastModel is the short topic and source-judge model.
const FastModel = "grok-chat-fast"

// FallbackModel is the model used as soon as a call times out or the gateway returns 524.
const FallbackModel = "grok-4.20-0309-non-reasoning"

// firstTokenTimeout is how long outline and polish wait for the first byte on grok-4.7.
const firstTokenTimeout = 20 * time.Second

func boundTimeout(requested, provider time.Duration) time.Duration {
	timeout := requested
	if timeout <= 0 {
		timeout = provider
	}
	if timeout <= 0 || timeout > callTimeoutCap {
		return callTimeoutCap
	}
	return timeout
}

// PreferFast keeps a fast or non-reasoning model and moves a reasoning model onto FastModel.
func PreferFast(list []Provider) []Provider {
	if len(list) == 0 {
		return nil
	}
	out := make([]Provider, len(list))
	copy(out, list)
	for i := range out {
		if reasoningModel(out[i].Model) {
			out[i].Model = FastModel
		}
	}
	return out
}

func (g *Gateway) CompleteJSON(ctx context.Context, providers []Provider, messages []map[string]string, schema *Schema, validate func(map[string]any) (map[string]any, error)) (map[string]any, Completion, error) {
	if len(providers) == 0 {
		return nil, Completion{}, &Error{Message: "没有可用的模型服务。请管理员在配置中心添加供应商，或使用模拟模式。"}
	}
	var errorsFound []string
	var attempts []map[string]any
	for _, provider := range providers {
		if provider.Protocol != "openai" && provider.Protocol != "anthropic" && provider.Protocol != "responses" && provider.Protocol != "http" {
			message := fmt.Sprintf("%s: 不支持的协议 %s", provider.Name, provider.Protocol)
			errorsFound = append(errorsFound, message)
			attempts = append(attempts, map[string]any{"provider_id": provider.ID, "protocol": provider.Protocol, "ok": false, "error": message})
			continue
		}
		completion, err := g.complete(ctx, provider, messages, schema, true, callLimit{})
		if err != nil {
			message := fmt.Sprintf("%s: %s", provider.Name, err.Error())
			errorsFound = append(errorsFound, message)
			attempts = append(attempts, failureAttempts(provider, completion, err)...)
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
		attempts = append(attempts, completion.Attempts...)
		attempts = append(attempts, successAttempt(provider, completion))
		completion.Attempts = attempts
		return validated, completion, nil
	}
	return nil, Completion{Attempts: attempts}, &Error{Message: "全部模型服务失败：" + strings.Join(errorsFound, "；")}
}

func (g *Gateway) Complete(ctx context.Context, provider Provider, messages []map[string]string) (Completion, error) {
	return g.complete(ctx, provider, messages, nil, true, callLimit{})
}

// CompleteFirst 按顺序调用供应商，返回第一份文本。不要求输出是 JSON。默认流式。
func (g *Gateway) CompleteFirst(ctx context.Context, providers []Provider, messages []map[string]string, schema *Schema) (Completion, error) {
	return g.CompleteText(ctx, providers, messages, schema, true)
}

// CompleteResearch 单次调用不超过 60 秒。同一次调用里，40 秒内失败的 502、503 和连接错误会短暂停后再试，最多两次。
// 超时或 524 立刻改用 grok-4.20-0309-non-reasoning。资料检索自己还会做一次更短的补救检索。
// grok-chat-fast 和 non-reasoning 走流式。
func (g *Gateway) CompleteResearch(ctx context.Context, providers []Provider, messages []map[string]string) (Completion, error) {
	return g.completeText(ctx, providers, messages, nil, false, callLimit{timeout: g.researchTimeout(), attempts: 1, research: true})
}

func researchTransport(provider Provider, configured time.Duration) (bool, time.Duration) {
	if configured <= 0 {
		configured = 150 * time.Second
	}
	if configured > callTimeoutCap {
		configured = callTimeoutCap
	}
	return researchCanStream(provider), configured
}

func researchCanStream(provider Provider) bool {
	if provider.Protocol == "anthropic" || provider.Protocol == "responses" {
		return false
	}
	name := strings.ToLower(provider.Model)
	return strings.Contains(name, "chat-fast") || strings.Contains(name, "non-reasoning")
}

func (g *Gateway) researchTimeout() time.Duration {
	if g != nil && g.ResearchTimeout > 0 {
		return g.ResearchTimeout
	}
	return 150 * time.Second
}

// CompleteText 按顺序调用供应商。stream 为 false 时请求体里写 stream:false。schema 为空时不附带 json_schema。
func (g *Gateway) CompleteText(ctx context.Context, providers []Provider, messages []map[string]string, schema *Schema, stream bool) (Completion, error) {
	return g.completeText(ctx, providers, messages, schema, stream, callLimit{})
}

func (g *Gateway) completeText(ctx context.Context, providers []Provider, messages []map[string]string, schema *Schema, stream bool, limit callLimit) (Completion, error) {
	if len(providers) == 0 {
		return Completion{}, &Error{Message: "没有可用的模型服务"}
	}
	var errorsFound []string
	var attempts []map[string]any
	for _, provider := range providers {
		if provider.Protocol != "openai" && provider.Protocol != "anthropic" && provider.Protocol != "responses" && provider.Protocol != "http" {
			message := provider.Name + ": 不支持的协议 " + provider.Protocol
			errorsFound = append(errorsFound, message)
			attempts = append(attempts, map[string]any{"provider_id": provider.ID, "provider_name": provider.Name, "protocol": provider.Protocol, "model": provider.Model, "ok": false, "error": message})
			continue
		}
		completion, err := g.complete(ctx, provider, messages, schema, stream, limit)
		if err != nil {
			message := provider.Name + ": " + err.Error()
			errorsFound = append(errorsFound, message)
			attempts = append(attempts, failureAttempts(provider, completion, err)...)
			continue
		}
		attempts = append(attempts, completion.Attempts...)
		attempts = append(attempts, successAttempt(provider, completion))
		completion.Attempts = attempts
		return completion, nil
	}
	return Completion{Attempts: attempts}, &Error{Message: "全部模型服务失败：" + strings.Join(errorsFound, "；")}
}

func (g *Gateway) complete(ctx context.Context, provider Provider, messages []map[string]string, schema *Schema, stream bool, limit callLimit) (Completion, error) {
	if g.Mock || strings.HasPrefix(provider.BaseURL, "mock://") {
		return mockComplete(provider, messages)
	}
	provider = routeStage(provider, messages)
	if provider.FirstToken > 0 {
		stream = true
	}
	cacheKey := ""
	if g.CacheTTL > 0 && !stream {
		cacheKey = completionKey(provider, messages)
		if hit, ok := g.cached(cacheKey); ok {
			return hit, nil
		}
	}
	attempts := g.MaxAttempts
	if limit.attempts > 0 {
		attempts = limit.attempts
	}
	if attempts < 1 {
		attempts = 1
	}
	effort := requestEffort(provider, messages)
	protocol := provider.Protocol
	var failed []map[string]any
	var last error
	retriedIdle := false
	researchQuick := 0
	switched := false
	pendingFallback := false
	for try := 0; ; try++ {
		if !pendingFallback {
			if limit.research {
				if try > 0 && researchQuick == 0 {
					break
				}
				if researchQuick > 2 {
					break
				}
			} else if try >= attempts {
				break
			}
		}
		pendingFallback = false
		if try > 0 && !retriedIdle {
			wait := g.backoffWait(try, limit.research)
			if wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return Completion{Attempts: failed}, ctx.Err()
				case <-timer.C:
				}
			}
		}
		callStream := stream
		callTimeout := limit.timeout
		if limit.research {
			callStream, callTimeout = researchTransport(provider, limit.timeout)
		}
		started := time.Now()
		completion, status, err := g.call(ctx, provider, messages, schema, callStream, callTimeout, effort, protocol)
		if err == nil {
			completion.Attempts = failed
			completion.ReasoningEffort = effort
			completion.Duration = time.Since(started)
			if cacheKey != "" {
				g.remember(cacheKey, completion)
			}
			return completion, nil
		}
		last = err
		failed = append(failed, map[string]any{
			"provider_id": provider.ID, "provider_name": provider.Name, "protocol": protocol,
			"model": provider.Model, "reasoning_effort": effort, "ok": false, "error": clip(err.Error(), 300),
			"ms": time.Since(started).Milliseconds(),
		})
		if ctx.Err() != nil {
			break
		}
		if fastFallback(status, err) && !switched && !strings.EqualFold(strings.TrimSpace(provider.Model), FallbackModel) {
			switched = true
			provider.Model = FallbackModel
			provider.FirstToken = 0
			effort = ""
			retriedIdle = true
			pendingFallback = true
			if limit.research {
				researchQuick = 1
			}
			continue
		}
		if fastFallback(status, err) {
			break
		}
		if limit.research {
			if researchQuick < 2 && quickResearchError(status, err, time.Since(started)) {
				researchQuick++
				continue
			}
			break
		}
		if switched {
			break
		}
		if idleCut(status, err) {
			if grokStreamInternal(provider.Model, err) {
				stream = false
				limit.timeout = fasterTimeout(limit.timeout, provider.Timeout)
			}
			if !retriedIdle && try+1 < attempts && canDegrade(effort, protocol, provider) {
				retriedIdle = true
				effort, protocol = degrade(effort, protocol, provider)
				continue
			}
			break
		}
		retriedIdle = false
		if !retryable(status, err) || try == attempts-1 {
			break
		}
	}
	if last == nil {
		last = &Error{Message: "模型调用失败"}
	}
	return Completion{Attempts: failed, ProviderID: provider.ID, ProviderName: provider.Name, Protocol: protocol, Model: provider.Model, ReasoningEffort: effort}, last
}

func (g *Gateway) backoffWait(try int, research bool) time.Duration {
	wait := g.Backoff
	if research {
		if wait <= 0 {
			wait = 300 * time.Millisecond
		}
		if wait > time.Second {
			wait = time.Second
		}
		return wait * time.Duration(try)
	}
	if wait <= 0 {
		return 0
	}
	wait = wait * time.Duration(1<<(try-1))
	jitter := time.Duration(rand.Int63n(int64(wait/5 + 1)))
	return wait + jitter
}

// quickResearchError 判断这次失败是不是可以马上再试的网关错误。超过 40 秒的失败不再重试。
func quickResearchError(status int, err error, elapsed time.Duration) bool {
	if err == nil || elapsed >= 40*time.Second {
		return false
	}
	if status == 502 || status == 503 {
		return true
	}
	if status != 0 {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "deadline") || strings.Contains(msg, "timeout") || strings.Contains(msg, "超时") {
		return false
	}
	var pe *Error
	if errors.As(err, &pe) && strings.HasPrefix(pe.Message, "HTTP ") {
		return false
	}
	return true
}

func successAttempt(provider Provider, completion Completion) map[string]any {
	model := completion.Model
	if model == "" {
		model = provider.Model
	}
	attempt := map[string]any{
		"provider_id": provider.ID, "provider_name": provider.Name, "protocol": completion.Protocol,
		"model": model, "reasoning_effort": completion.ReasoningEffort, "ok": true,
	}
	if completion.Duration > 0 {
		attempt["ms"] = completion.Duration.Milliseconds()
	}
	return attempt
}

func failureAttempts(provider Provider, completion Completion, err error) []map[string]any {
	if len(completion.Attempts) > 0 {
		return completion.Attempts
	}
	return []map[string]any{{
		"provider_id": provider.ID, "provider_name": provider.Name, "protocol": provider.Protocol,
		"model": provider.Model, "ok": false, "error": clip(err.Error(), 300),
	}}
}

func applyEffort(body map[string]any, effort string) {
	if effort == "" {
		delete(body, "reasoning_effort")
		return
	}
	body["reasoning_effort"] = effort
}

func requestEffort(provider Provider, messages []map[string]string) string {
	effort := ""
	if provider.ExtraParams != nil {
		effort, _ = provider.ExtraParams["reasoning_effort"].(string)
	}
	effort = strings.ToLower(strings.TrimSpace(effort))
	switch effort {
	case "low", "medium", "high":
	default:
		effort = ""
	}
	if effort == "" && longStage(stageFromMessages(messages)) && !skipsEffort(provider.Model) {
		return "low"
	}
	return effort
}

// routeStage keeps grok-4.7 for outline and polish, and starts every other writing
// stage on the non-reasoning model so a 60s timeout is not spent before the fallback.
// Models that are not reasoning models, including test fixtures named "writer", stay as configured.
func routeStage(provider Provider, messages []map[string]string) Provider {
	if !reasoningModel(provider.Model) {
		return provider
	}
	switch stageFromMessages(messages) {
	case "大纲", "结构润色":
		if provider.FirstToken <= 0 {
			provider.FirstToken = firstTokenTimeout
		}
	case "正文", "扩写", "审核", "标题", "缩写", "事实核对", "栏目结构", "意象改写", "套话改写", "开头比较", "标题比较", "禁忌改写":
		provider.Model = FallbackModel
		provider.FirstToken = 0
	}
	return provider
}

func stageFromMessages(messages []map[string]string) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i]["role"] != "user" {
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(messages[i]["content"]), &payload) != nil || payload == nil {
			return ""
		}
		return asString(payload["环节"])
	}
	return ""
}

func longStage(stage string) bool {
	switch stage {
	case "大纲", "正文", "审核", "扩写", "缩写", "事实核对":
		return true
	default:
		return false
	}
}

func skipsEffort(model string) bool {
	name := strings.ToLower(model)
	return strings.Contains(name, "chat-fast") || strings.Contains(name, "non-reasoning")
}

func reasoningModel(model string) bool {
	if skipsEffort(model) {
		return false
	}
	name := strings.ToLower(model)
	if strings.Contains(name, "imagine") || strings.Contains(name, "image") {
		return false
	}
	return strings.Contains(name, "grok-4") || strings.Contains(name, "grok-composer") || strings.Contains(name, "grok-build")
}

func canDegrade(effort, protocol string, provider Provider) bool {
	if effort == "high" || effort == "medium" {
		return true
	}
	return protocol == "openai" && reasoningModel(provider.Model)
}

func degrade(effort, protocol string, provider Provider) (string, string) {
	if effort == "high" || effort == "medium" {
		effort = "low"
	}
	if protocol == "openai" && reasoningModel(provider.Model) {
		protocol = "responses"
	}
	return effort, protocol
}

func grokStreamInternal(model string, err error) bool {
	if err == nil || !strings.Contains(strings.ToLower(model), "grok-4.7") {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "internal_error")
}

// fasterTimeout caps the next grok-4.7 attempt after a stream INTERNAL_ERROR.
func fasterTimeout(current, providerTimeout time.Duration) time.Duration {
	if current <= 0 {
		current = providerTimeout
	}
	const cap = 20 * time.Second
	if current <= 0 || current > cap {
		return cap
	}
	return current
}

func fastFallback(status int, err error) bool {
	if status == 522 || status == 524 {
		return true
	}
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "deadline") || strings.Contains(msg, "timeout") || strings.Contains(msg, "超时") {
		return true
	}
	return strings.Contains(msg, "524") || strings.Contains(msg, "522")
}

func idleCut(status int, err error) bool {
	if status == 522 || status == 524 {
		return true
	}
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "524") || strings.Contains(msg, "522") {
		return true
	}
	return strings.Contains(msg, "internal_error") || strings.Contains(msg, "stream error")
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

func (g *Gateway) call(ctx context.Context, provider Provider, messages []map[string]string, schema *Schema, stream bool, timeout time.Duration, effort, protocol string) (Completion, int, error) {
	timeout = boundTimeout(timeout, provider.Timeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch protocol {
	case "anthropic":
		return g.callAnthropic(ctx, provider, messages, stream, effort)
	case "responses":
		return g.callResponses(ctx, provider, messages, schema, stream, effort)
	case "http":
		return g.callMapped(ctx, provider, messages)
	default:
		return g.callOpenAI(ctx, provider, messages, schema, stream, effort)
	}
}

func (g *Gateway) callOpenAI(ctx context.Context, provider Provider, messages []map[string]string, schema *Schema, stream bool, effort string) (Completion, int, error) {
	body := PublicParams(provider.ExtraParams)
	msgs := make([]map[string]string, len(messages))
	copy(msgs, messages)
	body["model"] = provider.Model
	body["messages"] = msgs
	body["stream"] = stream
	applyEffort(body, effort)
	applyChatSchema(body, schema)
	raw, status, err := g.post(ctx, OpenAIURL(provider.BaseURL), openAIHeaders(provider), body, provider.FirstToken)
	if err != nil {
		return Completion{}, status, err
	}
	text, prompt, completion, err := readOpenAI(raw)
	if err != nil {
		return Completion{}, status, err
	}
	var cited []map[string]any
	if stageFromMessages(messages) == "资料检索" {
		cited = StructuredResearchSources(raw)
	}
	return Completion{Content: text, PromptTokens: prompt, CompletionTokens: completion, ProviderID: provider.ID, ProviderName: provider.Name, Protocol: "openai", Model: provider.Model, ReasoningEffort: effort, ResearchSources: cited}, status, nil
}

func (g *Gateway) callAnthropic(ctx context.Context, provider Provider, messages []map[string]string, stream bool, effort string) (Completion, int, error) {
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
		body["max_tokens"] = 16384
	}
	body["messages"] = chat
	body["stream"] = stream
	applyEffort(body, effort)
	if len(systemParts) > 0 {
		body["system"] = strings.Join(systemParts, "\n")
	}
	raw, status, err := g.post(ctx, AnthropicURL(provider.BaseURL), anthropicHeaders(provider), body, provider.FirstToken)
	if err != nil {
		return Completion{}, status, err
	}
	text, prompt, completion, err := readAnthropic(raw)
	if err != nil {
		return Completion{}, status, err
	}
	return Completion{Content: text, PromptTokens: prompt, CompletionTokens: completion, ProviderID: provider.ID, ProviderName: provider.Name, Protocol: "anthropic", Model: provider.Model, ReasoningEffort: effort}, status, nil
}

func (g *Gateway) callResponses(ctx context.Context, provider Provider, messages []map[string]string, schema *Schema, stream bool, effort string) (Completion, int, error) {
	body := PublicParams(provider.ExtraParams)
	input := make([]map[string]string, 0, len(messages))
	for _, message := range messages {
		role := message["role"]
		if role == "" {
			role = "user"
		}
		input = append(input, map[string]string{"role": role, "content": message["content"]})
	}
	body["model"] = provider.Model
	body["input"] = input
	body["stream"] = stream
	applyEffort(body, effort)
	delete(body, "messages")
	if schema != nil && schema.Body != nil {
		if _, exists := body["text"]; !exists {
			body["text"] = map[string]any{
				"format": map[string]any{
					"type":   "json_schema",
					"name":   schema.Name,
					"strict": true,
					"schema": schema.Body,
				},
			}
		}
	}
	raw, status, err := g.post(ctx, ResponsesURL(provider.BaseURL), openAIHeaders(provider), body, provider.FirstToken)
	if err != nil {
		return Completion{}, status, err
	}
	text, prompt, completion, err := readResponses(raw)
	if err != nil {
		return Completion{}, status, err
	}
	var cited []map[string]any
	if stageFromMessages(messages) == "资料检索" {
		cited = StructuredResearchSources(raw)
	}
	return Completion{Content: text, PromptTokens: prompt, CompletionTokens: completion, ProviderID: provider.ID, ProviderName: provider.Name, Protocol: "responses", Model: provider.Model, ReasoningEffort: effort, ResearchSources: cited}, status, nil
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

func (g *Gateway) post(ctx context.Context, url string, headers map[string]string, body map[string]any, firstToken time.Duration) ([]byte, int, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, &Error{Message: "网络错误：" + err.Error()}
	}
	if firstToken > 0 {
		req.Close = true
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	started := time.Now()
	resp, err := g.do(ctx, req, firstToken)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	remain := time.Duration(0)
	if firstToken > 0 {
		remain = firstToken - time.Since(started)
		if remain <= 0 {
			return nil, resp.StatusCode, &Error{Message: "首字超时"}
		}
	}
	data, err := readLimited(resp.Body, remain)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		return data, resp.StatusCode, &Error{Message: fmt.Sprintf("HTTP %d：%s", resp.StatusCode, clip(string(data), 300))}
	}
	return data, resp.StatusCode, nil
}

// do waits for response headers. A first-token limit covers that wait, because a silent
// model often sends nothing at all until the call cap.
func (g *Gateway) do(ctx context.Context, req *http.Request, firstToken time.Duration) (*http.Response, error) {
	if firstToken <= 0 {
		resp, err := g.Client.Do(req)
		if err != nil {
			return nil, &Error{Message: "网络错误：" + err.Error()}
		}
		return resp, nil
	}
	type result struct {
		resp *http.Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := g.Client.Do(req)
		done <- result{resp, err}
	}()
	timer := time.NewTimer(firstToken)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, &Error{Message: "首字超时"}
	case res := <-done:
		if res.err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(res.err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(res.err.Error()), "timeout") {
				return nil, &Error{Message: "首字超时"}
			}
			return nil, &Error{Message: "网络错误：" + res.err.Error()}
		}
		return res.resp, nil
	}
}

// readLimited returns the body. When firstToken is set, the first byte must arrive
// within that window; a silent reasoning model then falls back instead of waiting out the call cap.
func readLimited(body io.Reader, firstToken time.Duration) ([]byte, error) {
	src := io.LimitReader(body, 4<<20)
	if firstToken <= 0 {
		data, err := io.ReadAll(src)
		if err != nil {
			return nil, &Error{Message: "网络错误：" + err.Error()}
		}
		return data, nil
	}
	buf := make([]byte, 1)
	type chunk struct {
		n   int
		err error
	}
	done := make(chan chunk, 1)
	go func() {
		n, err := src.Read(buf)
		done <- chunk{n, err}
	}()
	timer := time.NewTimer(firstToken)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil, &Error{Message: "首字超时"}
	case part := <-done:
		if part.n == 0 && part.err != nil {
			return nil, &Error{Message: "网络错误：" + part.err.Error()}
		}
		rest, err := io.ReadAll(src)
		if err != nil {
			return nil, &Error{Message: "网络错误：" + err.Error()}
		}
		return append(buf[:part.n], rest...), nil
	}
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

func readResponses(raw []byte) (string, int, int, error) {
	if isJSON(raw) {
		return responsesJSON(raw)
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
		switch asString(chunk["type"]) {
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			// 思考过程的增量会刷新 Cloudflare 的空闲窗口。正文只取 output_text。
		case "response.output_text.delta":
			text.WriteString(asString(chunk["delta"]))
		case "response.output_text.done":
			if text.Len() == 0 {
				text.WriteString(asString(chunk["text"]))
			}
		case "response.completed":
			if resp, ok := chunk["response"].(map[string]any); ok {
				prompt, completion = usagePair(resp["usage"], prompt, completion)
				if text.Len() == 0 {
					text.WriteString(responsesText(resp))
				}
			}
		}
		if usage, ok := chunk["usage"].(map[string]any); ok {
			prompt, completion = usagePair(usage, prompt, completion)
		}
		return nil
	})
	if err != nil {
		return "", 0, 0, err
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", 0, 0, &Error{Message: "Responses 接口没有返回文本"}
	}
	return text.String(), prompt, completion, nil
}

func responsesJSON(raw []byte) (string, int, int, error) {
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return "", 0, 0, &Error{Message: "模型接口没有返回 JSON"}
	}
	if msg := apiError(data); msg != "" {
		return "", 0, 0, &Error{Message: msg}
	}
	text := responsesText(data)
	if strings.TrimSpace(text) == "" {
		if resp, ok := data["response"].(map[string]any); ok {
			text = responsesText(resp)
			data = resp
		}
	}
	if strings.TrimSpace(text) == "" {
		return "", 0, 0, &Error{Message: "Responses 接口没有返回文本"}
	}
	prompt, completion := usagePair(data["usage"], 0, 0)
	return text, prompt, completion, nil
}

func responsesText(data map[string]any) string {
	if data == nil {
		return ""
	}
	if text := asString(data["output_text"]); text != "" {
		return text
	}
	output, _ := data["output"].([]any)
	var parts []string
	for _, item := range output {
		block, _ := item.(map[string]any)
		if block == nil {
			continue
		}
		if text := asString(block["text"]); text != "" && block["content"] == nil {
			parts = append(parts, text)
		}
		content, _ := block["content"].([]any)
		for _, piece := range content {
			part, _ := piece.(map[string]any)
			if part == nil {
				continue
			}
			if text := asString(part["text"]); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "")
}

func usagePair(raw any, prompt, completion int) (int, int) {
	usage, _ := raw.(map[string]any)
	if usage == nil {
		return prompt, completion
	}
	if n := intOf(usage["input_tokens"]); n > 0 {
		prompt = n
	}
	if n := intOf(usage["prompt_tokens"]); n > 0 {
		prompt = n
	}
	if n := intOf(usage["output_tokens"]); n > 0 {
		completion = n
	}
	if n := intOf(usage["completion_tokens"]); n > 0 {
		completion = n
	}
	return prompt, completion
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
