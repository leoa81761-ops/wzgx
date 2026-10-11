package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Settings 从环境变量读取。不要在代码里写真实密钥。
type Settings struct {
	DatabaseURL       string
	MasterKey         string
	JWTSecret         string
	AdminEmail        string
	AdminPassword     string
	WeChatMode        string
	WeChatAPIBase     string
	LLMMode           string
	PublicEgressIP    string
	SchedulerEnabled  bool
	SchedulerInterval time.Duration
	MaxConcurrency    int
	StaleTaskSeconds  int
	DBMaxOpen         int
	DBMaxIdle         int
	HTTPAddr          string
	StaticDir         string
	HTTPIdleConns     int
	HTTPIdlePerHost   int
	NotifyWebhookURL  string
	SMTPHost          string
	SMTPPort          int
	SMTPUser          string
	SMTPPassword      string
	SMTPFrom          string
}

func Load() Settings {
	maxConc := envInt("MAX_CONCURRENCY", 128)
	if maxConc < 1 {
		maxConc = 1
	}
	if maxConc > 4096 {
		maxConc = 4096
	}
	interval := envInt("SCHEDULER_INTERVAL_SECONDS", 60)
	if interval < 5 {
		interval = 5
	}
	stale := envInt("STALE_TASK_SECONDS", 3600)
	if stale < 60 {
		stale = 60
	}
	addr := strings.TrimSpace(os.Getenv("ADDR"))
	if addr == "" {
		port := getenv("PORT", "8000")
		if strings.Contains(port, ":") {
			addr = port
		} else {
			addr = ":" + port
		}
	}
	return Settings{
		DatabaseURL:       getenv("DATABASE_URL", "sqlite:///./data/dev.db"),
		MasterKey:         getenv("MASTER_KEY", "dev-master-key-change-me"),
		JWTSecret:         getenv("JWT_SECRET", "dev-jwt-secret-change-me"),
		AdminEmail:        strings.ToLower(getenv("ADMIN_EMAIL", "admin@example.com")),
		AdminPassword:     getenv("ADMIN_PASSWORD", "admin123456"),
		WeChatMode:        getenv("WECHAT_MODE", "mock"),
		WeChatAPIBase:     getenv("WECHAT_API_BASE", "https://api.weixin.qq.com"),
		LLMMode:           getenv("LLM_MODE", "mock"),
		PublicEgressIP:    os.Getenv("PUBLIC_EGRESS_IP"),
		SchedulerEnabled:  envBool("SCHEDULER_ENABLED", true),
		SchedulerInterval: time.Duration(interval) * time.Second,
		MaxConcurrency:    maxConc,
		StaleTaskSeconds:  stale,
		DBMaxOpen:         envInt("DB_MAX_OPEN_CONNS", 0),
		DBMaxIdle:         envInt("DB_MAX_IDLE_CONNS", 0),
		HTTPAddr:          addr,
		StaticDir:         os.Getenv("STATIC_DIR"),
		HTTPIdleConns:     envInt("HTTP_CLIENT_MAX_IDLE_CONNS", 256),
		HTTPIdlePerHost:   envInt("HTTP_CLIENT_MAX_IDLE_PER_HOST", 64),
		NotifyWebhookURL:  strings.TrimSpace(os.Getenv("NOTIFY_WEBHOOK_URL")),
		SMTPHost:          strings.TrimSpace(os.Getenv("SMTP_HOST")),
		SMTPPort:          envInt("SMTP_PORT", 587),
		SMTPUser:          strings.TrimSpace(os.Getenv("SMTP_USER")),
		SMTPPassword:      os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:          strings.TrimSpace(os.Getenv("SMTP_FROM")),
	}
}

func getenv(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
