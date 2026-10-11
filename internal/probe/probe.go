package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/content"
	"github.com/leoa81761-ops/wzgx/internal/cover"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/pipeline"
	"github.com/leoa81761-ops/wzgx/internal/render"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
)

type Step struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type Config struct {
	AppID      string
	Secret     string
	WeChatBase string
	Protocol   string
	LLMBase    string
	LLMKey     string
	LLMModel   string
	Headers    map[string]string
	Timeout    time.Duration
	HTTP       *http.Client
	Direction  string
}

func FromEnv() Config {
	timeout := 120 * time.Second
	if raw := strings.TrimSpace(os.Getenv("LLM_TIMEOUT_SECONDS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			timeout = time.Duration(n) * time.Second
		}
	}
	headers := map[string]string{}
	if raw := strings.TrimSpace(os.Getenv("LLM_EXTRA_HEADERS")); raw != "" {
		_ = json.Unmarshal([]byte(raw), &headers)
	}
	protocol := strings.TrimSpace(os.Getenv("LLM_PROTOCOL"))
	if protocol == "" {
		protocol = "openai"
	}
	base := strings.TrimSpace(os.Getenv("WECHAT_API_BASE"))
	if base == "" {
		base = "https://api.weixin.qq.com"
	}
	direction := strings.TrimSpace(os.Getenv("PROBE_DIRECTION"))
	if direction == "" {
		direction = "职场成长"
	}
	return Config{
		AppID: os.Getenv("WX_APP_ID"), Secret: os.Getenv("WX_APP_SECRET"), WeChatBase: base,
		Protocol: protocol, LLMBase: os.Getenv("LLM_BASE_URL"), LLMKey: os.Getenv("LLM_API_KEY"),
		LLMModel: os.Getenv("LLM_MODEL"), Headers: headers, Timeout: timeout, Direction: direction,
	}
}

func (c Config) Missing() string {
	var miss []string
	if strings.TrimSpace(c.AppID) == "" || strings.TrimSpace(c.Secret) == "" {
		miss = append(miss, "WX_APP_ID / WX_APP_SECRET")
	}
	if strings.TrimSpace(c.LLMBase) == "" || strings.TrimSpace(c.LLMKey) == "" || strings.TrimSpace(c.LLMModel) == "" {
		miss = append(miss, "LLM_BASE_URL / LLM_API_KEY / LLM_MODEL")
	}
	if c.Protocol != "openai" && c.Protocol != "anthropic" && c.Protocol != "responses" {
		miss = append(miss, "LLM_PROTOCOL（openai、responses 或 anthropic）")
	}
	return strings.Join(miss, "、")
}

// Run 用真实微信和模型接口走一遍：取 token、四段写作、封面、正文图、草稿新增、草稿读取。
func Run(ctx context.Context, cfg Config) ([]Step, error) {
	if msg := cfg.Missing(); msg != "" {
		return []Step{{Name: "检查配置", OK: false, Detail: "缺少 " + msg}}, fmt.Errorf("缺少 %s", msg)
	}
	client := wechat.NewLive(cfg.AppID, cfg.Secret, cfg.WeChatBase, cfg.HTTP, wechat.NewTokenCache())
	gateway := &llm.Gateway{Client: cfg.HTTP, Mock: false, Backoff: 300 * time.Millisecond, MaxAttempts: 2}
	if gateway.Client == nil {
		gateway.Client = http.DefaultClient
	}
	var steps []Step
	add := func(name, detail string, err error) error {
		if err != nil {
			steps = append(steps, Step{Name: name, OK: false, Detail: err.Error()})
			return err
		}
		steps = append(steps, Step{Name: name, OK: true, Detail: detail})
		return nil
	}
	token, err := client.GetToken(ctx)
	if err := add("获取 access_token", fmt.Sprintf("已获取，长度 %d", len(token)), err); err != nil {
		return steps, err
	}
	provider := llm.Provider{
		ID: 1, Name: "连通测试", Protocol: cfg.Protocol, BaseURL: cfg.LLMBase, APIKey: cfg.LLMKey,
		Model: cfg.LLMModel, ExtraHeaders: cfg.Headers, ExtraParams: map[string]any{}, Timeout: cfg.Timeout,
	}
	providers := map[string][]llm.Provider{"topic": {provider}, "outline": {provider}, "draft": {provider}, "review": {provider}}
	work := pipeline.Input{Direction: cfg.Direction, StyleKey: "warm", TemplateKey: "simple", WordTarget: 800, Tier: "free"}
	built, err := pipeline.Build(ctx, gateway, work, nil, providers, content.DefaultSourcePolicy(), time.Now())
	if err != nil {
		_ = add("模型写作", "", err)
		return steps, err
	}
	steps = append(steps,
		Step{Name: "选题", OK: true, Detail: built.Topic.TopicTag},
		Step{Name: "大纲 / 正文 / 审核", OK: true, Detail: "标题：" + str(built.Draft["title"])},
	)
	thumb, err := client.UploadThumb(ctx, cover.CoverPNG("simple"), "cover.png")
	if err := add("上传封面 add_material", "media_id="+thumb, err); err != nil {
		return steps, err
	}
	imageURL, err := client.UploadContentImage(ctx, cover.ContentPNG(), "inline.png")
	if err := add("上传正文图片 uploadimg", imageURL, err); err != nil {
		return steps, err
	}
	html := render.InjectImage(built.HTML, imageURL)
	draft := wechat.DraftArticle{Title: str(built.Draft["title"]), Digest: str(built.Draft["summary"]), Content: html, Author: "公号智写", ThumbMediaID: thumb}
	mediaID, err := client.AddDraft(ctx, draft)
	if err := add("新增草稿 draft/add", "media_id="+mediaID, err); err != nil {
		return steps, err
	}
	got, err := client.GetDraft(ctx, mediaID)
	if err != nil {
		_ = add("读取草稿 draft/get", "", err)
		return steps, err
	}
	title := draftTitle(got)
	if title == "" {
		title = "已读到草稿"
	}
	steps = append(steps, Step{Name: "读取草稿 draft/get", OK: true, Detail: "标题：" + title + "；media_id=" + mediaID})
	return steps, nil
}

func draftTitle(data map[string]any) string {
	if title := str(data["title"]); title != "" {
		return title
	}
	items, _ := data["news_item"].([]any)
	if len(items) == 0 {
		return ""
	}
	first, _ := items[0].(map[string]any)
	return str(first["title"])
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
