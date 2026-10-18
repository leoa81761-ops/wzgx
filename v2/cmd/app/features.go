package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"gonghao/internal/account"
	"gonghao/internal/audit"
	"gonghao/internal/auth"
	"gonghao/internal/config"
	"gonghao/internal/crypto"
	"gonghao/internal/gateway"
	"gonghao/internal/gen"
	"gonghao/internal/job"
	"gonghao/internal/publish"
	"gonghao/internal/search"
	"gonghao/internal/server"
	"gonghao/internal/svccfg"
	"gonghao/internal/wechat"
)

// registerFeatures mounts feature packages.
func registerFeatures(ctx context.Context, srv *server.Server, pool *pgxpool.Pool, cfg config.Config, log *slog.Logger) error {
	if err := auth.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		return err
	}
	authSvc := auth.NewService(auth.NewPGStore(pool), cfg.SessionTTL)
	if created, err := authSvc.Bootstrap(ctx, cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword); err != nil {
		return err
	} else if created {
		log.Info("bootstrap super admin created")
	}
	ah := auth.NewHandlers(authSvc, cfg.CookieSecure)
	ah.Register(srv)

	box, err := crypto.NewBox(cfg.MasterKey)
	if err != nil {
		return err
	}
	aud := audit.PG{DB: pool}
	account.New(pool, box, aud).Register(srv, ah)
	gw := gateway.New(gateway.PGRecorder{DB: pool, Log: log})
	(&svccfg.Service{DB: pool, Box: box, Audit: aud, GW: gw}).Register(srv, ah)

	pipe := &gen.Pipeline{DB: pool, GW: gw, Box: box, Tavily: search.Tavily{BaseURL: cfg.TavilyBase}, DefaultImageURL: cfg.DefaultCoverURL, Log: log}
	jobs := &job.Service{DB: pool, Runner: pipe, Render: pipe, Auth: authSvc, Audit: aud, Log: log, ServerIP: cfg.ServerIP}
	jobs.Register(srv, ah)
	pub := &publish.Service{DB: pool, Box: box, Audit: aud, WX: &wechat.Client{Base: cfg.WeChatBase}, ServerIP: cfg.ServerIP,
		ReadBackAfter: cfg.ReadBackAfter, AutoPublish: cfg.AutoPublish}
	pub.Register(srv, ah)
	jobs.Tick = append(jobs.Tick, pub.ReadBackDue, pub.PublishDue, jobs.GrowthTick)
	if cfg.AutoPublish {
		log.Info("auto-publish worker on", "wechat_base", pub.WX.Base)
	}
	if !cfg.WorkerOff {
		go jobs.Work(ctx, 15*time.Second)
	}
	return nil
}
