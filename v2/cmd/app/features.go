package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"gonghao/internal/config"
	"gonghao/internal/server"
)

// registerFeatures mounts feature packages. Filled in as packages land.
func registerFeatures(ctx context.Context, srv *server.Server, pool *pgxpool.Pool, cfg config.Config, log *slog.Logger) error {
	return nil
}
