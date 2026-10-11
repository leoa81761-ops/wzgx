package runtime

import (
	"context"
	"sync"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/content"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

const ttl = 15 * time.Second

type WeChat struct {
	Mode string
	Base string
	IP   string
}

type Cache struct {
	mu       sync.Mutex
	st       *store.Store
	cfg      config.Settings
	wechat   WeChat
	wechatAt time.Time
	words    []string
	wordsAt  time.Time
	routes   map[string][]llm.Provider
	routesAt time.Time
}

func New(st *store.Store, cfg config.Settings) *Cache {
	return &Cache{st: st, cfg: cfg, routes: map[string][]llm.Provider{}}
}

func (c *Cache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wechatAt = time.Time{}
	c.wordsAt = time.Time{}
	c.routesAt = time.Time{}
	c.routes = map[string][]llm.Provider{}
}

func (c *Cache) WeChat(ctx context.Context, q db.Q) (WeChat, error) {
	c.mu.Lock()
	if time.Since(c.wechatAt) < ttl && c.wechat.Base != "" {
		item := c.wechat
		c.mu.Unlock()
		return item, nil
	}
	c.mu.Unlock()
	item, err := loadWeChat(ctx, c.st, q, c.cfg)
	if err != nil {
		return WeChat{}, err
	}
	c.mu.Lock()
	c.wechat = item
	c.wechatAt = time.Now()
	c.mu.Unlock()
	return item, nil
}

func (c *Cache) Words(ctx context.Context, q db.Q) ([]string, error) {
	c.mu.Lock()
	if time.Since(c.wordsAt) < ttl && c.words != nil {
		out := append([]string{}, c.words...)
		c.mu.Unlock()
		return out, nil
	}
	c.mu.Unlock()
	words, err := loadWords(ctx, c.st, q)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.words = words
	c.wordsAt = time.Now()
	c.mu.Unlock()
	return append([]string{}, words...), nil
}

func (c *Cache) Providers(ctx context.Context, q db.Q, stage, tier string) ([]llm.Provider, error) {
	key := stage + "|" + tier
	c.mu.Lock()
	if time.Since(c.routesAt) < ttl {
		if list, ok := c.routes[key]; ok {
			out := append([]llm.Provider{}, list...)
			c.mu.Unlock()
			return out, nil
		}
	}
	c.mu.Unlock()
	list, err := resolve(ctx, c.st, q, c.cfg, stage, tier)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if time.Since(c.routesAt) >= ttl {
		c.routes = map[string][]llm.Provider{}
		c.routesAt = time.Now()
	}
	c.routes[key] = list
	c.mu.Unlock()
	return append([]llm.Provider{}, list...), nil
}

func loadWeChat(ctx context.Context, st *store.Store, q db.Q, cfg config.Settings) (WeChat, error) {
	row, err := st.FindService(ctx, q, "wechat", "wechat")
	if err != nil {
		return WeChat{}, err
	}
	if row == nil {
		return WeChat{Mode: cfg.WeChatMode, Base: cfg.WeChatAPIBase, IP: cfg.PublicEgressIP}, nil
	}
	mode, _ := row.ExtraParams["mode"].(string)
	if mode == "" {
		mode = cfg.WeChatMode
	}
	base := row.BaseURL
	if base == "" {
		base = cfg.WeChatAPIBase
	}
	ip, _ := row.ExtraParams["public_egress_ip"].(string)
	if ip == "" {
		ip = cfg.PublicEgressIP
	}
	return WeChat{Mode: mode, Base: base, IP: ip}, nil
}

func loadWords(ctx context.Context, st *store.Store, q db.Q) ([]string, error) {
	row, err := st.FindService(ctx, q, "audit", "local_words")
	if err != nil {
		return nil, err
	}
	var extra []string
	if row != nil {
		if raw, ok := row.ExtraParams["words"].([]any); ok {
			for _, item := range raw {
				if s, ok := item.(string); ok {
					extra = append(extra, s)
				}
			}
		}
	}
	return content.EffectiveWords(extra), nil
}

func resolve(ctx context.Context, st *store.Store, q db.Q, cfg config.Settings, stage, tier string) ([]llm.Provider, error) {
	chosen, err := routes(ctx, st, q, cfg.MasterKey, stage, tier)
	if err != nil {
		return nil, err
	}
	if len(chosen) == 0 && tier != "*" {
		chosen, err = routes(ctx, st, q, cfg.MasterKey, stage, "*")
		if err != nil {
			return nil, err
		}
	}
	if len(chosen) > 0 {
		return chosen, nil
	}
	rows, err := st.ListTextModels(ctx, q, true)
	if err != nil {
		return nil, err
	}
	var providers []llm.Provider
	for _, row := range rows {
		providers = append(providers, ToProvider(row, cfg.MasterKey))
	}
	if len(providers) > 0 {
		return providers, nil
	}
	if cfg.LLMMode == "mock" {
		return []llm.Provider{Builtin()}, nil
	}
	return nil, nil
}

func routes(ctx context.Context, st *store.Store, q db.Q, master, stage, tier string) ([]llm.Provider, error) {
	list, err := st.RoutesFor(ctx, q, stage, tier)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	var providers []llm.Provider
	for _, route := range list {
		row, err := st.GetService(ctx, q, route.ServiceConfigID)
		if err != nil {
			return nil, err
		}
		if row != nil && row.Enabled && row.ConfigType == "text_model" {
			providers = append(providers, ToProvider(*row, master))
		}
	}
	return providers, nil
}

func ToProvider(row model.ServiceConfig, master string) llm.Provider {
	secret := ""
	if row.APIKeyEncrypted != "" {
		if plain, err := security.DecryptSecret(master, row.APIKeyEncrypted); err == nil {
			secret = plain
		}
	}
	timeout := time.Duration(row.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	headers := row.ExtraHeaders
	if headers == nil {
		headers = map[string]string{}
	}
	params := row.ExtraParams
	if params == nil {
		params = map[string]any{}
	}
	return llm.Provider{ID: row.ID, Name: row.Name, Protocol: row.Protocol, BaseURL: row.BaseURL, APIKey: secret, Model: row.ModelName, ExtraHeaders: headers, ExtraParams: params, Timeout: timeout}
}

func Builtin() llm.Provider {
	return llm.Provider{ID: 0, Name: "内置模拟", Protocol: "openai", BaseURL: "mock://local", Model: "mock-writer", ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{}, Timeout: 30 * time.Second}
}
