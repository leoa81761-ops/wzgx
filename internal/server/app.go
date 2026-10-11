package server

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/pipeline"
	"github.com/leoa81761-ops/wzgx/internal/runtime"
	"github.com/leoa81761-ops/wzgx/internal/scheduler"
	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/store"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
)

type App struct {
	Cfg     config.Settings
	DB      *db.DB
	Store   *store.Store
	HTTP    *http.Client
	Tokens  *wechat.TokenCache
	Cache   *runtime.Cache
	Gateway *llm.Gateway
	Pool    *scheduler.Pool
	Runner  *scheduler.Runner
	cancel  context.CancelFunc
}

func Open(cfg config.Settings) (*App, error) {
	database, err := db.Open(cfg.DatabaseURL, cfg.DBMaxOpen, cfg.DBMaxIdle)
	if err != nil {
		return nil, err
	}
	st := store.New(database)
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          cfg.HTTPIdleConns,
		MaxIdleConnsPerHost:   cfg.HTTPIdlePerHost,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if transport.MaxIdleConns <= 0 {
		transport.MaxIdleConns = 256
	}
	if transport.MaxIdleConnsPerHost <= 0 {
		transport.MaxIdleConnsPerHost = 64
	}
	client := &http.Client{Transport: transport}
	app := &App{
		Cfg: cfg, DB: database, Store: st, HTTP: client, Tokens: wechat.NewTokenCache(),
		Cache: runtime.New(st, cfg), Gateway: llm.NewGateway(cfg.LLMMode == "mock", client),
		Pool: scheduler.NewPool(cfg.MaxConcurrency),
	}
	app.Runner = &scheduler.Runner{Deps: app.deps(), Pool: app.Pool}
	ctx := context.Background()
	if err := app.seed(ctx); err != nil {
		_ = database.Close()
		return nil, err
	}
	return app, nil
}

func (a *App) deps() pipeline.Deps {
	return pipeline.Deps{Store: a.Store, Settings: a.Cfg, Gateway: a.Gateway, HTTP: a.HTTP, Tokens: a.Tokens, Cache: a.Cache}
}

func (a *App) Close() error {
	if a.cancel != nil {
		a.cancel()
	}
	a.HTTP.CloseIdleConnections()
	return a.DB.Close()
}

func (a *App) StartScheduler() {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	go func() {
		interval := a.Cfg.SchedulerInterval
		if interval < 5*time.Second {
			interval = 5 * time.Second
		}
		for {
			start := time.Now()
			if _, err := a.Runner.ScanOnce(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil {
				log.Printf("调度扫描失败: %v", err)
			}
			wait := interval - time.Since(start)
			if wait < 0 {
				wait = 0
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

func (a *App) seed(ctx context.Context) error {
	return a.DB.WithTx(ctx, func(tx *db.Tx) error {
		for _, item := range catalog.PlanDefaults {
			if err := a.Store.UpsertPlanConfig(ctx, tx, model.PlanConfig{
				Tier: item.Tier, Name: item.Name, PriceLabel: item.PriceLabel, DailyQuota: item.DailyQuota,
				PeriodQuota: item.PeriodQuota, PeriodUnit: item.PeriodUnit, MaxAccounts: item.MaxAccounts,
				MaxPlans: item.MaxPlans, MaxWords: item.MaxWords, Summary: item.Summary,
			}); err != nil {
				return err
			}
		}
		for _, item := range catalog.Styles {
			ok, err := a.Store.HasStyle(ctx, tx, item.Key)
			if err != nil {
				return err
			}
			if !ok {
				if err := a.Store.InsertStyle(ctx, tx, item.Key, item.Name, item.Description); err != nil {
					return err
				}
			}
		}
		for _, item := range catalog.Templates {
			ok, err := a.Store.HasTemplate(ctx, tx, item.Key)
			if err != nil {
				return err
			}
			if !ok {
				if err := a.Store.InsertTemplate(ctx, tx, item.Key, item.Name, item.Description); err != nil {
					return err
				}
			}
		}
		for _, doc := range catalog.LegalDocs {
			ok, err := a.Store.HasLegal(ctx, tx, doc.Type, catalog.LegalVersion)
			if err != nil {
				return err
			}
			if !ok {
				if err := a.Store.InsertLegal(ctx, tx, model.LegalDoc{DocType: doc.Type, Version: catalog.LegalVersion, Title: doc.Title, Content: doc.Content, Status: "active", EffectiveDate: "2026-10-11"}); err != nil {
					return err
				}
			}
		}
		row, err := a.Store.FindService(ctx, tx, "wechat", "wechat")
		if err != nil {
			return err
		}
		if row == nil {
			now := time.Now().UTC()
			if err := a.Store.InsertService(ctx, tx, &model.ServiceConfig{
				ConfigType: "wechat", Name: "wechat", Protocol: "wechat", BaseURL: a.Cfg.WeChatAPIBase,
				ExtraParams: map[string]any{"mode": a.Cfg.WeChatMode, "public_egress_ip": a.Cfg.PublicEgressIP},
				Enabled:     true, UpdatedAt: now,
			}); err != nil {
				return err
			}
		}
		admin, err := a.Store.FindUserByEmail(ctx, tx, a.Cfg.AdminEmail)
		if err != nil {
			return err
		}
		if admin == nil {
			hash, err := security.HashPassword(a.Cfg.AdminPassword)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			user := &model.User{Email: a.Cfg.AdminEmail, PasswordHash: hash, Tier: "lifetime", TierStartedAt: &now, IsAdmin: true, IsActive: true, CreatedAt: now}
			return a.Store.InsertUser(ctx, tx, user)
		}
		return nil
	})
}
