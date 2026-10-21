package db

import (
	"strings"
	"testing"
)

func TestMigrationsEmbedded(t *testing.T) {
	ms, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 || ms[0].Version != 1 {
		t.Fatalf("expected migration 1 first, got %+v", ms)
	}
	for i := 1; i < len(ms); i++ {
		if ms[i].Version <= ms[i-1].Version {
			t.Fatal("migrations not strictly ordered")
		}
	}
	if LatestVersion() != ms[len(ms)-1].Version {
		t.Fatal("LatestVersion mismatch")
	}
	sql := ms[0].SQL
	for _, must := range []string{"CREATE TABLE users", "'super_admin'", "CREATE TABLE service_config", "idle_timeout_s", "article_type_id", "wx_account_app_bound_uq"} {
		if !strings.Contains(sql, must) {
			t.Errorf("schema missing %q", must)
		}
	}
	// No model names hardcoded in the schema.
	for _, banned := range []string{"grok", "gpt-", "claude"} {
		if strings.Contains(strings.ToLower(sql), banned) {
			t.Errorf("schema mentions model name %q", banned)
		}
	}
}
