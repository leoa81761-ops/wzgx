package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"gonghao/internal/account"
	"gonghao/internal/audit"
	"gonghao/internal/auth"
	"gonghao/internal/config"
	"gonghao/internal/crypto"
	"gonghao/internal/gateway"
	"gonghao/internal/server"
	"gonghao/internal/svccfg"
)

// registerFeatures mounts feature packages.
func registerFeatures(ctx context.Context, srv *server.Server, pool *pgxpool.Pool, cfg config.Config, log *slog.Logger) error {
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
	return nil
}
