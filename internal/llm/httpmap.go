package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

func (g *Gateway) callMapped(ctx context.Context, provider Provider, messages []map[string]string) (Completion, int, error) {
	body := PublicParams(provider.ExtraParams)
	textPath, _ := body["response_text"].(string)
	promptPath, _ := body["response_prompt_tokens"].(string)
	completionPath, _ := body["response_completion_tokens"].(string)
	delete(body, "response_text")
	delete(body, "response_prompt_tokens")
	delete(body, "response_completion_tokens")
	body["model"] = provider.Model
	body["messages"] = messages
	raw, status, err := g.post(ctx, strings.TrimRight(provider.BaseURL, "/"), openAIHeaders(provider), body)
	if err != nil {
		return Completion{}, status, err
	}
	if strings.TrimSpace(textPath) == "" {
		text, prompt, completion, err := readOpenAI(raw)
		if err != nil {
			return Completion{}, status, err
		}
		return Completion{Content: text, PromptTokens: prompt, CompletionTokens: completion, ProviderID: provider.ID, ProviderName: provider.Name, Protocol: "http", Model: provider.Model}, status, nil
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Completion{}, status, &Error{Message: "响应不是 JSON"}
	}
	text, _ := lookupPath(payload, textPath).(string)
	if text == "" {
		return Completion{}, status, &Error{Message: "响应里没有映射的正文字段"}
	}
	return Completion{
		Content: text, PromptTokens: pathInt(payload, promptPath), CompletionTokens: pathInt(payload, completionPath),
		ProviderID: provider.ID, ProviderName: provider.Name, Protocol: "http", Model: provider.Model,
	}, status, nil
}

func lookupPath(root any, path string) any {
	cur := root
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			return nil
		}
		switch node := cur.(type) {
		case map[string]any:
			cur = node[part]
		default:
			return nil
		}
	}
	return cur
}

func pathInt(root any, path string) int {
	if path == "" {
		return 0
	}
	switch n := lookupPath(root, path).(type) {
	case float64:
		return int(n)
	case string:
		v, _ := strconv.Atoi(n)
		return v
	default:
		return 0
	}
}

func completionKey(provider Provider, messages []map[string]string) string {
	raw, _ := json.Marshal(struct {
		Protocol string
		Model    string
		Messages []map[string]string
	}{provider.Protocol, provider.Model, messages})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (g *Gateway) cached(key string) (Completion, bool) {
	g.cacheMu.Lock()
	defer g.cacheMu.Unlock()
	hit, ok := g.cache[key]
	if !ok || time.Now().After(hit.until) {
		return Completion{}, false
	}
	return hit.completion, true
}

func (g *Gateway) remember(key string, completion Completion) {
	g.cacheMu.Lock()
	defer g.cacheMu.Unlock()
	if g.cache == nil {
		g.cache = map[string]cachedCompletion{}
	}
	g.cache[key] = cachedCompletion{until: time.Now().Add(g.CacheTTL), completion: completion}
}

func (g *Gateway) CompleteBatch(ctx context.Context, providers []Provider, groups [][]map[string]string) ([]Completion, error) {
	if len(groups) == 0 {
		return nil, &Error{Message: "没有要批量调用的内容"}
	}
	if len(providers) == 0 {
		return nil, &Error{Message: "没有可用的模型服务"}
	}
	provider := providers[0]
	if g.Mock || strings.HasPrefix(provider.BaseURL, "mock://") || provider.Protocol != "openai" || len(groups) == 1 {
		out := make([]Completion, 0, len(groups))
		for _, group := range groups {
			item, err := g.CompleteText(ctx, providers, group, nil, false)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil
	}
	var b strings.Builder
	b.WriteString("请按顺序回答下面每个问题。只返回一个 JSON 字符串数组，不要解释。\n")
	for i, group := range groups {
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(". ")
		for _, message := range group {
			if message["role"] == "user" {
				b.WriteString(message["content"])
			}
		}
		b.WriteByte('\n')
	}
	completion, err := g.CompleteText(ctx, providers, []map[string]string{{"role": "user", "content": b.String()}}, nil, false)
	if err != nil {
		return nil, err
	}
	var texts []string
	if json.Unmarshal([]byte(strings.TrimSpace(completion.Content)), &texts) != nil || len(texts) != len(groups) {
		out := make([]Completion, 0, len(groups))
		for _, group := range groups {
			item, err := g.CompleteText(ctx, providers, group, nil, false)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil
	}
	out := make([]Completion, len(texts))
	for i, text := range texts {
		out[i] = Completion{Content: text, ProviderID: provider.ID, ProviderName: provider.Name, Protocol: provider.Protocol, Model: provider.Model}
	}
	return out, nil
}
