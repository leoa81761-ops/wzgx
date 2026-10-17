package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"gonghao/internal/auth"
	"gonghao/internal/config"
	"gonghao/internal/server"
)

// registerFeatures mounts feature packages.
func registerFeatures(ctx context.Context, srv *server.Server, pool *pgxpool.Pool, cfg config.Config, log *slog.Logger) error {
	authSvc := auth.NewService(auth.NewPGStore(pool), cfg.SessionTTL)
	if created, err := authSvc.Bootstrap(ctx, cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword); err != nil {
		return err
	} else if created {
		log.Info("bootstrap super admin created")
	}
	auth.NewHandlers(authSvc, cfg.CookieSecure).Register(srv)
	return nil
}
