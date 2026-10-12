package backup

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/leoa81761-ops/wzgx/internal/db"
)

func TestDumpRestoreAppSetting(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source, err := db.Open("sqlite://"+filepath.Join(dir, "a.db"), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	if _, err := source.Exec(ctx, `INSERT INTO app_setting (key, value, updated_at) VALUES (?, ?, ?)`, "site_name", "公号智写", "2026-10-12T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	raw, err := Dump(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	dest, err := db.Open("sqlite://"+filepath.Join(dir, "b.db"), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dest.Close() })
	if _, err := dest.Exec(ctx, `INSERT INTO app_setting (key, value, updated_at) VALUES (?, ?, ?)`, "site_name", "旧值", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, dest, raw); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := dest.QueryRow(ctx, `SELECT value FROM app_setting WHERE key = ?`, "site_name").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "公号智写" {
		t.Fatal(value)
	}
}
