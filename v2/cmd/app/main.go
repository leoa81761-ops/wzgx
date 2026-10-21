// Command app runs the 公号智写 v2 server (HTTP; scheduler and workers join later).
package main

import (
	"context"
	"errors"
	"gonghao/web"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gonghao/internal/config"
	"gonghao/internal/db"
	"gonghao/internal/server"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	v, err := db.Migrate(ctx, pool)
	if err != nil {
		return err
	}
	log.Info("schema ready", "version", v)

	srv := server.New(server.Options{
		DB: pool,
		Schema: func(ctx context.Context) (int, error) {
			var n int
			err := pool.QueryRow(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&n)
			return n, err
		},
		WebDir: cfg.WebDir,
		Web:    web.Dist(),
		Log:    log,
	})
	if err := registerFeatures(ctx, srv, pool, cfg, log); err != nil {
		return err
	}

	hs := &http.Server{Addr: cfg.Addr, Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	log.Info("listening", "addr", cfg.Addr)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
