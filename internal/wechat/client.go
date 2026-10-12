package wechat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"time"
)

type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string {
	if e.Code == 0 {
		return e.Message
	}
	return fmt.Sprintf("微信接口错误 %d：%s", e.Code, e.Message)
}

func (e *Error) AuthFailed() bool {
	switch e.Code {
	case 40001, 40013, 40014, 40125, 41002, 41004:
		return true
	default:
		return strings.Contains(e.Message, "invalid credential") || strings.Contains(e.Message, "授权失效")
	}
}

// InvalidMedia 表示草稿或封面的 media_id 已经失效，例如草稿被删除或已发布。
func InvalidMedia(err error) bool {
	var we *Error
	if !errors.As(err, &we) || we == nil {
		return false
	}
	if we.Code == 40007 {
		return true
	}
	msg := strings.ToLower(we.Message)
	if strings.Contains(msg, "invalid media_id") || strings.Contains(msg, "invalid media id") || strings.Contains(msg, "thumb_media_id") {
		return true
	}
	return strings.Contains(we.Message, "media_id") && (strings.Contains(we.Message, "无效") || strings.Contains(msg, "invalid"))
}

func Explain(code int, errmsg string) string {
	reasons := map[int]string{
		-1:    "微信系统繁忙，请稍后重试",
		40001: "凭证无效或 access_token 不正确，请检查 AppID 与 AppSecret",
		40013: "AppID 无效",
		40014: "不合法的 access_token，请稍后重试",
		40125: "AppSecret 无效",
		40164: "服务器出口 IP 未加入公众号 IP 白名单，请到公众平台「设置与开发 - 基本配置 - IP 白名单」添加本机公网 IP",
		41001: "缺少 access_token",
		41002: "缺少 appid",
		41004: "缺少 secret",
		42001: "access_token 已过期",
		40007: "素材 media_id 无效",
		40009: "图片大小不合法",
		45001: "多媒体文件大小超过限制",
		45002: "消息内容超过限制",
		45009: "微信接口调用次数已达上限，请稍后再试",
		48001: "该公众号没有此接口权限",
		50002: "公众号用户受限，可能是账号被冻结或违规",
	}
	reason := reasons[code]
	if reason == "" {
		reason = strings.TrimSpace(errmsg)
		if reason == "" {
			reason = "微信接口返回错误"
		}
	}
	if errmsg != "" && !strings.Contains(reason, errmsg) {
		reason = reason + "（" + errmsg + "）"
	}
	return reason
}

type DraftArticle struct {
	Title            string
	Author           string
	Digest           string
	Content          string
	ContentSourceURL string
	ThumbMediaID     string
}

func (a DraftArticle) fields() map[string]any {
	author := a.Author
	if author == "" {
		author = "公号智写"
	}
	title := a.Title
	if title == "" {
		title = "未命名"
	}
	digest := a.Digest
	if len([]rune(digest)) > 120 {
		digest = string([]rune(digest)[:120])
	}
	return map[string]any{
		"title": title, "author": author, "digest": digest, "content": a.Content,
		"content_source_url": a.ContentSourceURL, "thumb_media_id": a.ThumbMediaID,
		"need_open_comment": 0, "only_fans_can_comment": 0,
	}
}

type Client interface {
	GetToken(ctx context.Context) (string, error)
	UploadThumb(ctx context.Context, image []byte, filename string) (string, error)
	UploadContentImage(ctx context.Context, image []byte, filename string) (string, error)
	AddDraft(ctx context.Context, article DraftArticle) (string, error)
	UpdateDraft(ctx context.Context, mediaID string, article DraftArticle) (string, error)
	GetDraft(ctx context.Context, mediaID string) (map[string]any, error)
	DeleteDraft(ctx context.Context, mediaID string) error
	DraftCount(ctx context.Context) (int, error)
	DraftSwitch(ctx context.Context) (open bool, supported bool, err error)
	Datacube(ctx context.Context, path, begin, end string) (map[string]any, error)
	ListDraftTitles(ctx context.Context, limit int) ([]string, error)
	ProbePublish(ctx context.Context) (bool, string, error)
	SubmitPublish(ctx context.Context, mediaID string) (string, error)
	GetPublish(ctx context.Context, publishID string) (PublishState, error)
}

// PublishState 是 freepublish/get 的结果。Status 0 成功，1 发布中，其余为失败。
type PublishState struct {
	Status int
	URL    string
}

type PermissionReport struct {
	OK           bool
	AuthFailed   bool
	CanDraft     bool
	CanPublish   bool
	DeliveryMode string
	Checks       []map[string]any
}

type mockBox struct {
	mu           sync.Mutex
	materials    map[string]int
	drafts       map[string]map[string]any
	images       map[string]string
	publishes    map[string]PublishState
	token        string
	switchClosed bool
	forcedCount  int
	datacube     map[string]map[string]any
}

var (
	mockMu    sync.Mutex
	mockStore = map[string]*mockBox{}
)

func ResetMock() {
	mockMu.Lock()
	mockStore = map[string]*mockBox{}
	mockMu.Unlock()
}

type MockClient struct {
	AppID  string
	Secret string
	Base   string
	box    *mockBox
}

func newMock(appID, secret, base string) *MockClient {
	mockMu.Lock()
	box := mockStore[appID]
	if box == nil {
		box = &mockBox{materials: map[string]int{}, drafts: map[string]map[string]any{}, images: map[string]string{}, publishes: map[string]PublishState{}, token: "mock-token-" + appID}
		mockStore[appID] = box
	}
	mockMu.Unlock()
	return &MockClient{AppID: appID, Secret: secret, Base: base, box: box}
}

func (c *MockClient) GetToken(context.Context) (string, error) {
	if c.AppID == "" || c.Secret == "" {
		return "", &Error{Message: "缺少 AppID 或 AppSecret"}
	}
	return c.box.token, nil
}

func (c *MockClient) UploadThumb(_ context.Context, image []byte, _ string) (string, error) {
	if _, err := c.GetToken(context.Background()); err != nil {
		return "", err
	}
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	id := "mock_thumb_" + shortID()
	c.box.materials[id] = len(image)
	return id, nil
}

func (c *MockClient) UploadContentImage(_ context.Context, image []byte, _ string) (string, error) {
	if _, err := c.GetToken(context.Background()); err != nil {
		return "", err
	}
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	id := shortID()
	url := "https://mmbiz.qpic.cn/mock/" + id
	c.box.images[url] = fmt.Sprint(len(image))
	return url, nil
}

func (c *MockClient) AddDraft(_ context.Context, article DraftArticle) (string, error) {
	if _, err := c.GetToken(context.Background()); err != nil {
		return "", err
	}
	if article.ThumbMediaID == "" {
		return "", &Error{Message: "草稿缺少封面素材"}
	}
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	id := "mock_draft_" + shortID()
	c.box.drafts[id] = article.fields()
	return id, nil
}

func (c *MockClient) UpdateDraft(_ context.Context, mediaID string, article DraftArticle) (string, error) {
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	if _, ok := c.box.drafts[mediaID]; !ok {
		return "", &Error{Message: "找不到要更新的草稿"}
	}
	c.box.drafts[mediaID] = article.fields()
	return mediaID, nil
}

func (c *MockClient) GetDraft(_ context.Context, mediaID string) (map[string]any, error) {
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	draft, ok := c.box.drafts[mediaID]
	if !ok {
		return nil, &Error{Message: "找不到草稿"}
	}
	out := map[string]any{}
	for k, v := range draft {
		out[k] = v
	}
	return out, nil
}

func (c *MockClient) DeleteDraft(_ context.Context, mediaID string) error {
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	delete(c.box.drafts, mediaID)
	return nil
}

func (c *MockClient) DraftCount(context.Context) (int, error) {
	if _, err := c.GetToken(context.Background()); err != nil {
		return 0, err
	}
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	if c.box.forcedCount > 0 {
		return c.box.forcedCount, nil
	}
	return len(c.box.drafts), nil
}

func (c *MockClient) DraftSwitch(context.Context) (bool, bool, error) {
	if _, err := c.GetToken(context.Background()); err != nil {
		return false, true, err
	}
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	return !c.box.switchClosed, true, nil
}

func (c *MockClient) Datacube(_ context.Context, path, _, _ string) (map[string]any, error) {
	if _, err := c.GetToken(context.Background()); err != nil {
		return nil, err
	}
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	if c.box.datacube == nil {
		return map[string]any{"list": []any{}}, nil
	}
	if row, ok := c.box.datacube[path]; ok {
		return row, nil
	}
	return map[string]any{"list": []any{}}, nil
}

func (c *MockClient) ListDraftTitles(_ context.Context, limit int) ([]string, error) {
	if _, err := c.GetToken(context.Background()); err != nil {
		return nil, err
	}
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	if limit < 1 {
		limit = 20
	}
	out := make([]string, 0, len(c.box.drafts))
	for _, draft := range c.box.drafts {
		title, _ := draft["title"].(string)
		if title == "" {
			continue
		}
		out = append(out, title)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (c *MockClient) ProbePublish(context.Context) (bool, string, error) {
	return true, "模拟环境不调用发布接口，是否可发布以绑定的企业已认证信息为准", nil
}

func (c *MockClient) SubmitPublish(_ context.Context, mediaID string) (string, error) {
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	if _, ok := c.box.drafts[mediaID]; !ok {
		return "", &Error{Message: "找不到草稿"}
	}
	delete(c.box.drafts, mediaID)
	id := "mock_pub_" + shortID()
	c.box.publishes[id] = PublishState{Status: 0, URL: "https://mp.weixin.qq.com/s/" + id}
	return id, nil
}

func (c *MockClient) GetPublish(_ context.Context, publishID string) (PublishState, error) {
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	state, ok := c.box.publishes[publishID]
	if !ok {
		return PublishState{}, &Error{Message: "找不到发布单"}
	}
	return state, nil
}

var idMu sync.Mutex
var idN int

func shortID() string {
	idMu.Lock()
	idN++
	n := idN
	idMu.Unlock()
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d-%d", time.Now().UnixNano(), n)))
	return hex.EncodeToString(sum[:6])
}

type TokenCache struct {
	mu sync.Mutex
	m  map[string]*tokenEntry
}

type tokenEntry struct {
	token string
	until time.Time
}

func NewTokenCache() *TokenCache { return &TokenCache{m: map[string]*tokenEntry{}} }

func (c *TokenCache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item := c.m[key]
	if item == nil || time.Now().After(item.until) || item.token == "" {
		return "", false
	}
	return item.token, true
}

func (c *TokenCache) Set(key, token string, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = &tokenEntry{token: token, until: until}
}

func (c *TokenCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, key)
}

type LiveClient struct {
	AppID           string
	Secret          string
	BaseURL         string
	HTTP            *http.Client
	Timeout         time.Duration
	Cache           *TokenCache
	RefreshToken    string
	ComponentAppID  string
	ComponentSecret string
	ComponentTicket string
	key             string
}

func NewLive(appID, secret, base string, client *http.Client, cache *TokenCache) *LiveClient {
	base = strings.TrimRight(base, "/")
	if base == "" {
		base = "https://api.weixin.qq.com"
	}
	if client == nil {
		client = http.DefaultClient
	}
	if cache == nil {
		cache = NewTokenCache()
	}
	sum := sha256.Sum256([]byte(secret))
	return &LiveClient{AppID: appID, Secret: secret, BaseURL: base, HTTP: client, Timeout: 30 * time.Second, Cache: cache, key: appID + "|" + base + "|" + hex.EncodeToString(sum[:8])}
}

func (c *LiveClient) GetToken(ctx context.Context) (string, error) {
	return c.token(ctx, false)
}

func (c *LiveClient) authorizerToken(ctx context.Context) (string, error) {
	compKey := "component|" + c.ComponentAppID
	componentToken, ok := c.Cache.Get(compKey)
	if !ok {
		data, err := c.do(ctx, http.MethodPost, "/cgi-bin/component/api_component_token", nil, map[string]any{
			"component_appid": c.ComponentAppID, "component_appsecret": c.ComponentSecret, "component_verify_ticket": c.ComponentTicket,
		})
		if err != nil {
			return "", err
		}
		componentToken, _ = data["component_access_token"].(string)
		if componentToken == "" {
			return "", &Error{Message: "没有拿到第三方平台调用凭证"}
		}
		expires := intOf(data["expires_in"])
		if expires < 120 {
			expires = 7200
		}
		c.Cache.Set(compKey, componentToken, time.Now().Add(time.Duration(expires-300)*time.Second))
	}
	data, err := c.do(ctx, http.MethodPost, "/cgi-bin/component/api_authorizer_token", map[string]string{"component_access_token": componentToken}, map[string]any{
		"component_appid": c.ComponentAppID, "authorizer_appid": c.AppID, "authorizer_refresh_token": c.RefreshToken,
	})
	if err != nil {
		return "", err
	}
	token, _ := data["authorizer_access_token"].(string)
	if token == "" {
		return "", &Error{Message: "没有拿到授权方调用凭证"}
	}
	expires := intOf(data["expires_in"])
	if expires < 120 {
		expires = 7200
	}
	c.Cache.Set(c.key, token, time.Now().Add(time.Duration(expires-300)*time.Second))
	return token, nil
}

func NewAuthorizer(appID, refresh, componentAppID, componentSecret, ticket, base string, client *http.Client, cache *TokenCache) *LiveClient {
	c := NewLive(appID, refresh, base, client, cache)
	c.Secret = ""
	c.RefreshToken = refresh
	c.ComponentAppID = componentAppID
	c.ComponentSecret = componentSecret
	c.ComponentTicket = ticket
	c.key = appID + "|authorizer|" + c.BaseURL
	return c
}

func (c *LiveClient) token(ctx context.Context, force bool) (string, error) {
	if !force {
		if token, ok := c.Cache.Get(c.key); ok {
			return token, nil
		}
	} else {
		c.Cache.Delete(c.key)
	}
	if c.RefreshToken != "" {
		return c.authorizerToken(ctx)
	}
	data, err := c.request(ctx, http.MethodPost, "/cgi-bin/stable_token", map[string]any{
		"grant_type": "client_credential", "appid": c.AppID, "secret": c.Secret, "force_refresh": force,
	}, false)
	if err != nil {
		return "", err
	}
	token, _ := data["access_token"].(string)
	if token == "" {
		return "", &Error{Message: "未返回 access_token"}
	}
	expires := intOf(data["expires_in"])
	if expires < 120 {
		expires = 7200
	}
	c.Cache.Set(c.key, token, time.Now().Add(time.Duration(expires-300)*time.Second))
	return token, nil
}

func (c *LiveClient) UploadThumb(ctx context.Context, image []byte, filename string) (string, error) {
	data, err := c.upload(ctx, "/cgi-bin/material/add_material", map[string]string{"type": "image"}, image, filename)
	if err != nil {
		return "", err
	}
	id, _ := data["media_id"].(string)
	if id == "" {
		return "", &Error{Message: "上传封面未返回 media_id"}
	}
	return id, nil
}

func (c *LiveClient) UploadContentImage(ctx context.Context, image []byte, filename string) (string, error) {
	data, err := c.upload(ctx, "/cgi-bin/media/uploadimg", nil, image, filename)
	if err != nil {
		return "", err
	}
	url, _ := data["url"].(string)
	if url == "" {
		return "", &Error{Message: "上传正文图片未返回 url"}
	}
	return url, nil
}

func (c *LiveClient) AddDraft(ctx context.Context, article DraftArticle) (string, error) {
	data, err := c.request(ctx, http.MethodPost, "/cgi-bin/draft/add", map[string]any{"articles": []any{article.fields()}}, true)
	if err != nil {
		return "", err
	}
	id, _ := data["media_id"].(string)
	if id == "" {
		return "", &Error{Message: "新增草稿未返回 media_id"}
	}
	return id, nil
}

func (c *LiveClient) UpdateDraft(ctx context.Context, mediaID string, article DraftArticle) (string, error) {
	_, err := c.request(ctx, http.MethodPost, "/cgi-bin/draft/update", map[string]any{"media_id": mediaID, "index": 0, "articles": article.fields()}, true)
	if err != nil {
		return "", err
	}
	return mediaID, nil
}

func (c *LiveClient) GetDraft(ctx context.Context, mediaID string) (map[string]any, error) {
	return c.request(ctx, http.MethodPost, "/cgi-bin/draft/get", map[string]any{"media_id": mediaID}, true)
}

func (c *LiveClient) DeleteDraft(ctx context.Context, mediaID string) error {
	_, err := c.request(ctx, http.MethodPost, "/cgi-bin/draft/delete", map[string]any{"media_id": mediaID}, true)
	return err
}

func (c *LiveClient) ProbePublish(ctx context.Context) (bool, string, error) {
	data, err := c.request(ctx, http.MethodPost, "/cgi-bin/account/getaccountbasicinfo", map[string]any{}, true)
	if err != nil {
		return false, err.Error(), nil
	}
	principal := intOf(data["principal_type"])
	verified := false
	if info, ok := data["wx_verify_info"].(map[string]any); ok {
		verified, _ = info["qualification_verify"].(bool)
	}
	if verified && principal != 0 {
		return true, "已认证且不是个人主体", nil
	}
	return false, "未认证或个人主体，不能自动发布", nil
}

func (c *LiveClient) SubmitPublish(ctx context.Context, mediaID string) (string, error) {
	data, err := c.request(ctx, http.MethodPost, "/cgi-bin/freepublish/submit", map[string]any{"media_id": mediaID}, true)
	if err != nil {
		return "", err
	}
	switch id := data["publish_id"].(type) {
	case string:
		if id != "" {
			return id, nil
		}
	case float64:
		return fmt.Sprintf("%.0f", id), nil
	}
	return "", &Error{Message: "发布接口没有返回 publish_id，为避免重复发布不再重试"}
}

func (c *LiveClient) GetPublish(ctx context.Context, publishID string) (PublishState, error) {
	data, err := c.request(ctx, http.MethodPost, "/cgi-bin/freepublish/get", map[string]any{"publish_id": publishID}, true)
	if err != nil {
		return PublishState{}, err
	}
	state := PublishState{Status: intOf(data["publish_status"])}
	if detail, ok := data["article_detail"].(map[string]any); ok {
		if items, ok := detail["item"].([]any); ok && len(items) > 0 {
			if item, ok := items[0].(map[string]any); ok {
				state.URL, _ = item["article_url"].(string)
			}
		}
	}
	return state, nil
}

func (c *LiveClient) DraftSwitch(ctx context.Context) (bool, bool, error) {
	token, err := c.token(ctx, false)
	if err != nil {
		return false, true, err
	}
	data, err := c.do(ctx, http.MethodPost, "/cgi-bin/draft/switch", map[string]string{"access_token": token, "checkonly": "1"}, nil)
	if err != nil {
		var wx *Error
		if errors.As(err, &wx) && (wx.Code == 40066 || strings.Contains(wx.Message, "废弃") || strings.Contains(wx.Message, "not exist")) {
			return true, false, nil
		}
		return false, true, err
	}
	if intOf(data["is_open"]) == 0 {
		return false, true, nil
	}
	return true, true, nil
}

func (c *LiveClient) Datacube(ctx context.Context, path, begin, end string) (map[string]any, error) {
	if !strings.HasPrefix(path, "/datacube/") {
		return nil, &Error{Message: "未知的数据接口"}
	}
	return c.request(ctx, http.MethodPost, path, map[string]any{"begin_date": begin, "end_date": end}, true)
}

func (c *LiveClient) ListDraftTitles(ctx context.Context, limit int) ([]string, error) {
	if limit < 1 {
		limit = 20
	}
	if limit > 20 {
		limit = 20
	}
	data, err := c.request(ctx, http.MethodPost, "/cgi-bin/draft/batchget", map[string]any{"offset": 0, "count": limit, "no_content": 1}, true)
	if err != nil {
		return nil, err
	}
	items, _ := data["item"].([]any)
	var titles []string
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		content, _ := item["content"].(map[string]any)
		news, _ := content["news_item"].([]any)
		if len(news) == 0 {
			continue
		}
		first, _ := news[0].(map[string]any)
		title, _ := first["title"].(string)
		if title != "" {
			titles = append(titles, title)
		}
	}
	return titles, nil
}

func (c *LiveClient) DraftCount(ctx context.Context) (int, error) {
	data, err := c.request(ctx, http.MethodGet, "/cgi-bin/draft/count", nil, true)
	if err != nil {
		return 0, err
	}
	if n := intOf(data["total_count"]); n > 0 {
		return n, nil
	}
	return intOf(data["item_count"]), nil
}

func (c *LiveClient) upload(ctx context.Context, path string, query map[string]string, image []byte, filename string) (map[string]any, error) {
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.token(ctx, attempt == 1)
		if err != nil {
			return nil, err
		}
		data, err := c.postFile(ctx, path, token, query, image, filename)
		if err == nil {
			return data, nil
		}
		last = err
		if !tokenExpired(err) || attempt == 1 {
			return nil, err
		}
		c.Cache.Delete(c.key)
	}
	return nil, last
}

func (c *LiveClient) request(ctx context.Context, method, path string, body map[string]any, auth bool) (map[string]any, error) {
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		q := map[string]string{}
		if auth {
			token, err := c.token(ctx, attempt == 1)
			if err != nil {
				return nil, err
			}
			q["access_token"] = token
		}
		data, err := c.do(ctx, method, path, q, body)
		if err == nil {
			return data, nil
		}
		last = err
		if !auth || !tokenExpired(err) || attempt == 1 {
			return nil, err
		}
		c.Cache.Delete(c.key)
	}
	return nil, last
}

func tokenExpired(err error) bool {
	we, ok := err.(*Error)
	if !ok {
		return false
	}
	return we.Code == 40001 || we.Code == 42001 || we.Code == 40014
}

func (c *LiveClient) do(ctx context.Context, method, path string, query map[string]string, body map[string]any) (map[string]any, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return nil, &Error{Message: "微信接口网络错误：" + err.Error()}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	q := req.URL.Query()
	for k, v := range query {
		q.Set(k, v)
	}
	req.URL.RawQuery = q.Encode()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, &Error{Message: "微信接口网络错误：" + err.Error()}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, &Error{Message: "微信接口网络错误：" + err.Error()}
	}
	return parse(resp.StatusCode, raw)
}

func imageContentType(filename string) string {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	default:
		return "image/png"
	}
}

func (c *LiveClient) postFile(ctx context.Context, path, token string, extra map[string]string, image []byte, filename string) (map[string]any, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	if filename == "" {
		filename = "image.png"
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="media"; filename="%s"`, filename))
	hdr.Set("Content-Type", imageContentType(filename))
	part, err := w.CreatePart(hdr)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(image); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, &buf)
	if err != nil {
		return nil, &Error{Message: "上传失败：" + err.Error()}
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	q := req.URL.Query()
	q.Set("access_token", token)
	for k, v := range extra {
		q.Set(k, v)
	}
	req.URL.RawQuery = q.Encode()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, &Error{Message: "上传失败：" + err.Error()}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, &Error{Message: "上传失败：" + err.Error()}
	}
	return parse(resp.StatusCode, raw)
}

func (c *LiveClient) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return context.WithTimeout(ctx, timeout)
}

func parse(status int, raw []byte) (map[string]any, error) {
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil || data == nil {
		return nil, &Error{Message: fmt.Sprintf("微信接口返回无法解析（HTTP %d）", status)}
	}
	code := intOf(data["errcode"])
	if _, ok := data["errcode"]; ok && code != 0 {
		errmsg, _ := data["errmsg"].(string)
		return nil, &Error{Code: code, Message: Explain(code, errmsg)}
	}
	if status >= 400 {
		return nil, &Error{Message: fmt.Sprintf("微信接口 HTTP %d", status)}
	}
	return data, nil
}

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

func Build(appID, secret, mode, base string, client *http.Client, cache *TokenCache) Client {
	if mode == "mock" || strings.HasPrefix(appID, "wx_mock") {
		return newMock(appID, secret, base)
	}
	return NewLive(appID, secret, base, client, cache)
}

func tokenAuthFailed(err error) bool {
	var wx *Error
	if errors.As(err, &wx) {
		return wx.AuthFailed()
	}
	return err != nil
}

func CheckPermissions(ctx context.Context, client Client, cover []byte) PermissionReport {
	checks := []map[string]any{}
	if _, err := client.GetToken(ctx); err != nil {
		checks = append(checks, map[string]any{"name": "access_token", "ok": false, "detail": err.Error()})
		return PermissionReport{OK: false, AuthFailed: tokenAuthFailed(err), CanDraft: false, DeliveryMode: "export", Checks: checks}
	}
	checks = append(checks, map[string]any{"name": "access_token", "ok": true, "detail": "已获取调用凭证"})
	count, err := client.DraftCount(ctx)
	if err != nil {
		checks = append(checks, map[string]any{"name": "draft_count", "ok": false, "detail": err.Error()})
		return PermissionReport{OK: true, AuthFailed: false, CanDraft: false, DeliveryMode: "export", Checks: checks}
	}
	checks = append(checks, map[string]any{"name": "draft_count", "ok": true, "detail": fmt.Sprintf("当前草稿数量 %d", count)})
	mediaID, err := client.UploadThumb(ctx, cover, "cover.png")
	if err != nil {
		checks = append(checks, map[string]any{"name": "material", "ok": false, "detail": err.Error()})
		return PermissionReport{OK: true, AuthFailed: false, CanDraft: false, DeliveryMode: "export", Checks: checks}
	}
	checks = append(checks, map[string]any{"name": "material", "ok": true, "detail": "封面素材上传成功"})
	probeID, err := client.AddDraft(ctx, DraftArticle{Title: "绑定检测可删除", Author: "公号智写", Digest: "权限检测草稿，可删除", Content: "<p>这是一条绑定检测草稿，系统会尝试删除。</p>", ThumbMediaID: mediaID})
	if err == nil {
		_, err = client.GetDraft(ctx, probeID)
	}
	if err != nil {
		checks = append(checks, map[string]any{"name": "draft_write", "ok": false, "detail": err.Error()})
		if probeID != "" {
			_ = client.DeleteDraft(ctx, probeID)
		}
		return PermissionReport{OK: true, AuthFailed: false, CanDraft: false, DeliveryMode: "export", Checks: checks}
	}
	checks = append(checks, map[string]any{"name": "draft_write", "ok": true, "detail": "草稿写入与读取成功"})
	canPublish, detail, err := client.ProbePublish(ctx)
	if err != nil {
		detail = err.Error()
	}
	if detail == "" {
		detail = "未探测发布权限"
	}
	checks = append(checks, map[string]any{"name": "publish_probe", "ok": canPublish, "detail": detail})
	if err := client.DeleteDraft(ctx, probeID); err != nil {
		checks = append(checks, map[string]any{"name": "draft_cleanup", "ok": false, "detail": "检测草稿未能删除：" + err.Error()})
	} else {
		checks = append(checks, map[string]any{"name": "draft_cleanup", "ok": true, "detail": "检测草稿已删除"})
	}
	return PermissionReport{OK: true, AuthFailed: false, CanDraft: true, CanPublish: canPublish, DeliveryMode: "draft", Checks: checks}
}

// SetMockDraftClosed 让模拟公众号的草稿箱开关读成关闭。
func SetMockDraftClosed(appID string, closed bool) {
	box := newMock(appID, "mock-secret", "").box
	box.mu.Lock()
	box.switchClosed = closed
	box.mu.Unlock()
}

// SetMockDraftCount 让模拟环境的草稿总数接口返回固定数字。
func SetMockDraftCount(appID string, n int) {
	box := newMock(appID, "mock-secret", "").box
	box.mu.Lock()
	box.forcedCount = n
	box.mu.Unlock()
}

// SetMockDatacube 写入模拟的发表内容或用户分析响应。path 用 /datacube/ 开头。
func SetMockDatacube(appID, path string, payload map[string]any) {
	box := newMock(appID, "mock-secret", "").box
	box.mu.Lock()
	if box.datacube == nil {
		box.datacube = map[string]map[string]any{}
	}
	box.datacube[path] = payload
	box.mu.Unlock()
}

// MockPublishCount 返回模拟环境里已经提交过的发布单数量，测试用来确认没有重复提交。
func MockPublishCount(appID string) int {
	mockMu.Lock()
	box := mockStore[appID]
	mockMu.Unlock()
	if box == nil {
		return 0
	}
	box.mu.Lock()
	defer box.mu.Unlock()
	return len(box.publishes)
}
