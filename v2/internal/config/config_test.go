package config

import (
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadDefaults(t *testing.T) {
	c, err := load(env(map[string]string{
		"V2_DATABASE_URL": "postgres://x",
		"V2_MASTER_KEY":   "0123456789abcdef0123456789abcdef",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8090" || c.SessionTTL != 720*time.Hour || !c.CookieSecure {
		t.Fatalf("bad defaults: %+v", c)
	}
}

func TestLoadRejects(t *testing.T) {
	cases := []map[string]string{
		{"V2_MASTER_KEY": "0123456789abcdef0123456789abcdef"},
		{"V2_DATABASE_URL": "x", "V2_MASTER_KEY": "short"},
		{"V2_DATABASE_URL": "x", "V2_MASTER_KEY": "0123456789abcdef0123456789abcdef", "V2_SESSION_TTL": "nope"},
		{"V2_DATABASE_URL": "x", "V2_MASTER_KEY": "0123456789abcdef0123456789abcdef", "V2_BOOTSTRAP_ADMIN_EMAIL": "a@b"},
	}
	for i, m := range cases {
		if _, err := load(env(m)); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}
