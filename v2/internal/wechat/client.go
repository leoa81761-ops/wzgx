// Package wechat talks to the WeChat Official Account API: access tokens (bind check), image
// uploads and draft creation. It never publishes; a draft only lands in the account's draft box.
package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const DefaultBase = "https://api.weixin.qq.com"

type Client struct {
	Base string       // default DefaultBase; tests point it at a fake server
	HTTP *http.Client // default 20s timeout
	Now  func() time.Time

	mu     sync.Mutex
	tokens map[string]cached // appID -> token
}

type cached struct {
	token string
	until time.Time
}

// APIError is an errcode/errmsg answer from WeChat.
type APIError struct {
	Code int    `json:"errcode"`
	Msg  string `json:"errmsg"`
}

func (e *APIError) Error() string { return fmt.Sprintf("微信接口错误 %d：%s", e.Code, e.Msg) }

// Hint turns the common errcodes into an actionable Chinese explanation.
func (e *APIError) Hint() string {
	switch e.Code {
	case 40013:
		return "AppID 不正确"
	case 40125, 40001:
		return "AppSecret 不正确或已重置"
	case 40164, 61004:
		return "服务器 IP 不在公众号的 IP 白名单里，请在公众号后台「设置与开发 → 基本配置」中添加"
	case 48001:
		return "公众号没有这个接口的权限（个人订阅号或未认证账号可能无法使用草稿接口）"
	case 45009:
		return "接口调用次数已达今日上限"
	}
	return ""
}

func (c *Client) base() string {
	if c.Base != "" {
		return c.Base
	}
	return DefaultBase
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Token returns an access token from the stable_token endpoint (which does not invalidate tokens other
// systems hold for the same account), cached until shortly before it expires.
func (c *Client) Token(ctx context.Context, appID, secret string, force bool) (string, error) {
	c.mu.Lock()
	if t, ok := c.tokens[appID]; ok && !force && c.now().Before(t.until) {
		c.mu.Unlock()
		return t.token, nil
	}
	c.mu.Unlock()
	body, _ := json.Marshal(map[string]any{"grant_type": "client_credential", "appid": appID, "secret": secret, "force_refresh": false})
	var out struct {
		APIError
		Token   string `json:"access_token"`
		Expires int    `json:"expires_in"`
	}
	if err := c.do(ctx, "POST", c.base()+"/cgi-bin/stable_token", "application/json", bytes.NewReader(body), &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("微信没有返回 access_token")
	}
	ttl := time.Duration(out.Expires)*time.Second - 5*time.Minute
	if ttl < time.Minute {
		ttl = time.Minute
	}
	c.mu.Lock()
	if c.tokens == nil {
		c.tokens = map[string]cached{}
	}
	c.tokens[appID] = cached{out.Token, c.now().Add(ttl)}
	c.mu.Unlock()
	return out.Token, nil
}

// Forget drops a cached token (after WeChat says it is invalid).
func (c *Client) Forget(appID string) {
	c.mu.Lock()
	delete(c.tokens, appID)
	c.mu.Unlock()
}

// UploadContentImage uploads an image used inside the article body and returns its mmbiz URL.
func (c *Client) UploadContentImage(ctx context.Context, token string, name string, img []byte) (string, error) {
	var out struct {
		APIError
		URL string `json:"url"`
	}
	if err := c.upload(ctx, "/cgi-bin/media/uploadimg?access_token="+url.QueryEscape(token), name, img, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

// UploadThumb uploads the cover as a permanent image material and returns its media_id.
func (c *Client) UploadThumb(ctx context.Context, token string, name string, img []byte) (string, error) {
	var out struct {
		APIError
		MediaID string `json:"media_id"`
	}
	if err := c.upload(ctx, "/cgi-bin/material/add_material?type=image&access_token="+url.QueryEscape(token), name, img, &out); err != nil {
		return "", err
	}
	return out.MediaID, nil
}

type Draft struct {
	Title        string `json:"title"`
	Author       string `json:"author,omitempty"`
	Digest       string `json:"digest,omitempty"`
	Content      string `json:"content"`
	ThumbMediaID string `json:"thumb_media_id"`
	OpenComment  int    `json:"need_open_comment"`
}

// AddDraft creates a draft in the account's draft box. It does not publish.
func (c *Client) AddDraft(ctx context.Context, token string, d Draft) (string, error) {
	body, _ := json.Marshal(map[string]any{"articles": []Draft{d}})
	var out struct {
		APIError
		MediaID string `json:"media_id"`
	}
	if err := c.do(ctx, "POST", c.base()+"/cgi-bin/draft/add?access_token="+url.QueryEscape(token), "application/json", bytes.NewReader(body), &out); err != nil {
		return "", err
	}
	if out.MediaID == "" {
		return "", errors.New("微信没有返回草稿 media_id")
	}
	return out.MediaID, nil
}

func (c *Client) upload(ctx context.Context, path, name string, img []byte, out apiErr) error {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("media", name)
	fw.Write(img)
	mw.Close()
	return c.do(ctx, "POST", c.base()+path, mw.FormDataContentType(), &b, out)
}

type apiErr interface{ apiError() *APIError }

func (e *APIError) apiError() *APIError { return e }

func (c *Client) do(ctx context.Context, method, u, ctype string, body io.Reader, out apiErr) error {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", ctype)
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("连接微信失败：%w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("微信接口 HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("微信返回无法解析：%w", err)
	}
	if e := out.apiError(); e.Code != 0 {
		return &APIError{Code: e.Code, Msg: e.Msg}
	}
	return nil
}
