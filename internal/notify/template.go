package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// SendTemplate 用服务号模板消息接口发一条通知。没配齐调用方不应进来。
func SendTemplate(ctx context.Context, client *http.Client, base, appID, secret, templateID, fields, openid, title, body string) error {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = "https://api.weixin.qq.com"
	}
	token, err := templateToken(ctx, client, base, appID, secret)
	if err != nil {
		return err
	}
	keys := templateKeys(fields)
	data := map[string]any{}
	data[keys[0]] = map[string]string{"value": clip20(title)}
	if len(keys) > 1 {
		data[keys[1]] = map[string]string{"value": clip20(body)}
	}
	payload := map[string]any{
		"touser": openid, "template_id": templateID, "data": data,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/cgi-bin/message/template/send?access_token="+token, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return templateErr(resp)
}

func templateToken(ctx context.Context, client *http.Client, base, appID, secret string) (string, error) {
	raw, _ := json.Marshal(map[string]any{
		"grant_type": "client_credential", "appid": appID, "secret": secret, "force_refresh": false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/cgi-bin/stable_token", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var data map[string]any
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if json.Unmarshal(body, &data) != nil {
		return "", errTemplate("模板消息凭证无法解析")
	}
	if code := intNumber(data["errcode"]); code != 0 {
		return "", errTemplate("模板消息凭证被拒绝")
	}
	token, _ := data["access_token"].(string)
	if token == "" {
		return "", errTemplate("模板消息没有返回凭证")
	}
	return token, nil
}

func templateErr(resp *http.Response) error {
	var data map[string]any
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if json.Unmarshal(body, &data) != nil {
		return errTemplate("模板消息响应无法解析")
	}
	if code := intNumber(data["errcode"]); code != 0 {
		return errTemplate("模板消息发送被拒绝")
	}
	return nil
}

func templateKeys(fields string) []string {
	parts := strings.Split(fields, ",")
	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return []string{"thing1", "thing2"}
	}
	return out
}

func clip20(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "通知"
	}
	if utf8.RuneCountInString(s) <= 20 {
		return s
	}
	return string([]rune(s)[:20])
}

func intNumber(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

type errTemplate string

func (e errTemplate) Error() string { return string(e) }
