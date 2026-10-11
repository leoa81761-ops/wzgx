package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// GenerateCover 调用 OpenAI 兼容的 /v1/images/generations。封面默认 16:9。
// 上游文档写的是 b64_json，实际也可能返回 url，两种都接受。
func GenerateCover(ctx context.Context, client *http.Client, provider Provider, prompt string) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	timeout := provider.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body := PublicParams(provider.ExtraParams)
	if _, ok := body["aspect_ratio"]; !ok {
		body["aspect_ratio"] = "16:9"
	}
	if _, ok := body["n"]; !ok {
		body["n"] = 1
	}
	if _, ok := body["response_format"]; !ok {
		body["response_format"] = "b64_json"
	}
	body["model"] = provider.Model
	body["prompt"] = prompt
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ImagesURL(provider.BaseURL), strings.NewReader(string(payload)))
	if err != nil {
		return nil, &Error{Message: "网络错误：" + err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	for key, value := range provider.ExtraHeaders {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &Error{Message: "网络错误：" + err.Error()}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 12<<20))
	if err != nil {
		return nil, &Error{Message: "网络错误：" + err.Error()}
	}
	if resp.StatusCode >= 400 {
		return nil, &Error{Message: "生图失败：" + clip(string(raw), 200)}
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return nil, &Error{Message: "生图接口没有返回 JSON"}
	}
	if msg := apiError(data); msg != "" {
		return nil, &Error{Message: msg}
	}
	items, _ := data["data"].([]any)
	if len(items) == 0 {
		return nil, &Error{Message: "生图接口没有返回图片"}
	}
	first, _ := items[0].(map[string]any)
	if first == nil {
		return nil, &Error{Message: "生图接口没有返回图片"}
	}
	if encoded := strings.TrimSpace(asString(first["b64_json"])); encoded != "" {
		return decodeImage64(encoded)
	}
	if rawURL := strings.TrimSpace(asString(first["url"])); rawURL != "" {
		return downloadImage(ctx, client, rawURL)
	}
	return nil, &Error{Message: "生图接口没有返回图片数据"}
}

func ImagesURL(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/images/generations") {
		return base
	}
	return base + "/images/generations"
}

func decodeImage64(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if i := strings.Index(encoded, ","); i >= 0 && strings.Contains(encoded[:i], "base64") {
		encoded = encoded[i+1:]
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil || len(decoded) == 0 {
		return nil, &Error{Message: "生图返回的 base64 无法解码"}
	}
	return decoded, nil
}

func downloadImage(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
		return nil, &Error{Message: "生图返回的地址不可用"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, &Error{Message: "无法下载封面：" + err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &Error{Message: "无法下载封面：" + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, &Error{Message: "无法下载封面"}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil || len(data) == 0 {
		return nil, &Error{Message: "封面下载为空"}
	}
	return data, nil
}
