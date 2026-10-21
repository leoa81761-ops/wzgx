package db_test

import (
	"context"
	"testing"

	"gonghao/internal/db"
	"gonghao/internal/testdb"
)

func TestMigrateIdempotent(t *testing.T) {
	pool := testdb.Open(t) // already migrated once
	v, err := db.Migrate(context.Background(), pool)
	if err != nil || v != db.LatestVersion() {
		t.Fatalf("second migrate: v=%d err=%v", v, err)
	}
	var n int
	pool.QueryRow(context.Background(), `SELECT count(*) FROM schema_migrations`).Scan(&n)
	if n != len(mustMigrations(t)) {
		t.Fatalf("schema_migrations rows=%d", n)
	}
	// role check constraint is enforced by the DB
	_, err = pool.Exec(context.Background(), `INSERT INTO users (email, password_hash, role) VALUES ('x@y.z','h','admin')`)
	if err == nil {
		t.Fatal("bad role accepted")
	}
}

func mustMigrations(t *testing.T) []db.Migration {
	ms, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	return ms
}
