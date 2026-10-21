// Package testdb gives each test a fresh, migrated PostgreSQL schema.
// Tests are skipped unless V2_TEST_DATABASE_URL is set, e.g.
//
//	V2_TEST_DATABASE_URL=postgres://gh2test:gh2test@127.0.0.1:5432/gh2test?sslmode=disable go test ./...
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"gonghao/internal/db"
)

func Open(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("V2_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("V2_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	b := make([]byte, 6)
	rand.Read(b)
	schema := "t_" + hex.EncodeToString(b)

	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	if _, err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}
