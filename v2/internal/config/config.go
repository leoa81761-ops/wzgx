// Package config loads process configuration from environment variables.
// Business settings (models, keys, quotas) live in the database and are edited
// in the admin console; only bootstrapping values come from the environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Addr            string        // listen address, V2_ADDR (default :8090)
	DatabaseURL     string        // V2_DATABASE_URL
	MasterKey       string        // V2_MASTER_KEY, encrypts third-party credentials
	SessionTTL      time.Duration // V2_SESSION_TTL (default 720h)
	CookieSecure    bool          // V2_COOKIE_SECURE (default true)
	WebDir          string        // V2_WEB_DIR, built frontend on disk (default: the copy embedded in the binary)
	DefaultCoverURL string        // V2_DEFAULT_COVER_URL, image slots use it until image generation lands
	WorkerOff       bool          // V2_WORKER_OFF=true disables the job worker in this process
	ServerIP        string        // V2_SERVER_IP, this server's outbound IP, shown as the WeChat IP-whitelist hint
	WeChatBase      string        // V2_WECHAT_BASE, WeChat API base URL (default https://api.weixin.qq.com; local tests use a fake)

	// Optional first super admin, created only when no super admin exists.
	BootstrapAdminEmail    string // V2_BOOTSTRAP_ADMIN_EMAIL
	BootstrapAdminPassword string // V2_BOOTSTRAP_ADMIN_PASSWORD
}

func Load() (Config, error) { return load(os.Getenv) }

func load(get func(string) string) (Config, error) {
	c := Config{
		Addr:                   or(get("V2_ADDR"), ":8090"),
		DatabaseURL:            get("V2_DATABASE_URL"),
		MasterKey:              get("V2_MASTER_KEY"),
		SessionTTL:             720 * time.Hour,
		CookieSecure:           true,
		WebDir:                 get("V2_WEB_DIR"),
		DefaultCoverURL:        get("V2_DEFAULT_COVER_URL"),
		WorkerOff:              get("V2_WORKER_OFF") == "true",
		ServerIP:               get("V2_SERVER_IP"),
		WeChatBase:             get("V2_WECHAT_BASE"),
		BootstrapAdminEmail:    get("V2_BOOTSTRAP_ADMIN_EMAIL"),
		BootstrapAdminPassword: get("V2_BOOTSTRAP_ADMIN_PASSWORD"),
	}
	if v := get("V2_SESSION_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return c, fmt.Errorf("V2_SESSION_TTL: invalid duration %q", v)
		}
		c.SessionTTL = d
	}
	if v := get("V2_COOKIE_SECURE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return c, fmt.Errorf("V2_COOKIE_SECURE: %w", err)
		}
		c.CookieSecure = b
	}
	if c.DatabaseURL == "" {
		return c, errors.New("V2_DATABASE_URL is required")
	}
	if len(c.MasterKey) < 32 {
		return c, errors.New("V2_MASTER_KEY must be at least 32 characters")
	}
	if (c.BootstrapAdminEmail == "") != (c.BootstrapAdminPassword == "") {
		return c, errors.New("V2_BOOTSTRAP_ADMIN_EMAIL and V2_BOOTSTRAP_ADMIN_PASSWORD must be set together")
	}
	return c, nil
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
