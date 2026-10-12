package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Settings 从环境变量读取。不要在代码里写真实密钥。
type Settings struct {
	DatabaseURL             string
	MasterKey               string
	JWTSecret               string
	AdminEmail              string
	AdminPassword           string
	WeChatMode              string
	WeChatAPIBase           string
	LLMMode                 string
	PublicEgressIP          string
	SchedulerEnabled        bool
	SchedulerInterval       time.Duration
	MaxConcurrency          int
	StaleTaskSeconds        int
	DBMaxOpen               int
	DBMaxIdle               int
	HTTPAddr                string
	StaticDir               string
	HTTPIdleConns           int
	HTTPIdlePerHost         int
	NotifyWebhookURL        string
	SMTPHost                string
	SMTPPort                int
	SMTPUser                string
	SMTPPassword            string
	SMTPFrom                string
	PaySandbox              bool
	PublishDailyCap         int
	WxPayMchID              string
	WxPayAppID              string
	WxPayMchSerial          string
	WxPayAPIv3Key           string
	WxPayPrivateKey         string
	WxPayPlatformCert       string
	WxPayNotifyURL          string
	WxPayAPIBase            string
	AlipayAppID             string
	AlipayPrivateKey        string
	AlipayPublicKey         string
	AlipayNotifyURL         string
	AlipayReturnURL         string
	AlipayGateway           string
	PriceStandardMonth      int
	PriceStandardYear       int
	PricePremiumMonth       int
	PricePremiumYear        int
	PriceLifetime           int
	ResearchTimeout         time.Duration
	ResearchBackupModel     string
	SMSProvider             string
	SMSAccessKey            string
	SMSAccessSecret         string
	SMSSignName             string
	SMSTemplateCode         string
	SMSSdkAppID             string
	SMSRegion               string
	SMSAPIBase              string
	WeChatOpenAppID         string
	WeChatOpenSecret        string
	WeChatOpenRedirect      string
	WeChatComponentAppID    string
	WeChatComponentSecret   string
	WeChatComponentToken    string
	WeChatComponentAESKey   string
	WeChatComponentRedirect string
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
		DatabaseURL:             getenv("DATABASE_URL", "sqlite:///./data/dev.db"),
		MasterKey:               getenv("MASTER_KEY", "dev-master-key-change-me"),
		JWTSecret:               getenv("JWT_SECRET", "dev-jwt-secret-change-me"),
		AdminEmail:              strings.ToLower(getenv("ADMIN_EMAIL", "admin@example.com")),
		AdminPassword:           getenv("ADMIN_PASSWORD", "admin123456"),
		WeChatMode:              getenv("WECHAT_MODE", "mock"),
		WeChatAPIBase:           getenv("WECHAT_API_BASE", "https://api.weixin.qq.com"),
		LLMMode:                 getenv("LLM_MODE", "mock"),
		PublicEgressIP:          os.Getenv("PUBLIC_EGRESS_IP"),
		SchedulerEnabled:        envBool("SCHEDULER_ENABLED", true),
		SchedulerInterval:       time.Duration(interval) * time.Second,
		MaxConcurrency:          maxConc,
		StaleTaskSeconds:        stale,
		DBMaxOpen:               envInt("DB_MAX_OPEN_CONNS", 0),
		DBMaxIdle:               envInt("DB_MAX_IDLE_CONNS", 0),
		HTTPAddr:                addr,
		StaticDir:               os.Getenv("STATIC_DIR"),
		HTTPIdleConns:           envInt("HTTP_CLIENT_MAX_IDLE_CONNS", 256),
		HTTPIdlePerHost:         envInt("HTTP_CLIENT_MAX_IDLE_PER_HOST", 64),
		NotifyWebhookURL:        strings.TrimSpace(os.Getenv("NOTIFY_WEBHOOK_URL")),
		SMTPHost:                strings.TrimSpace(os.Getenv("SMTP_HOST")),
		SMTPPort:                envInt("SMTP_PORT", 587),
		SMTPUser:                strings.TrimSpace(os.Getenv("SMTP_USER")),
		SMTPPassword:            os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:                strings.TrimSpace(os.Getenv("SMTP_FROM")),
		PaySandbox:              envBool("PAY_SANDBOX", false),
		PublishDailyCap:         publishCap(),
		WxPayMchID:              strings.TrimSpace(os.Getenv("WXPAY_MCH_ID")),
		WxPayAppID:              strings.TrimSpace(os.Getenv("WXPAY_APP_ID")),
		WxPayMchSerial:          strings.TrimSpace(os.Getenv("WXPAY_MCH_SERIAL")),
		WxPayAPIv3Key:           strings.TrimSpace(os.Getenv("WXPAY_API_V3_KEY")),
		WxPayPrivateKey:         loadPEM("WXPAY_PRIVATE_KEY", "WXPAY_PRIVATE_KEY_FILE"),
		WxPayPlatformCert:       loadPEM("WXPAY_PLATFORM_CERT", "WXPAY_PLATFORM_CERT_FILE"),
		WxPayNotifyURL:          strings.TrimSpace(os.Getenv("WXPAY_NOTIFY_URL")),
		WxPayAPIBase:            getenv("WXPAY_API_BASE", "https://api.mch.weixin.qq.com"),
		AlipayAppID:             strings.TrimSpace(os.Getenv("ALIPAY_APP_ID")),
		AlipayPrivateKey:        loadPEM("ALIPAY_PRIVATE_KEY", "ALIPAY_PRIVATE_KEY_FILE"),
		AlipayPublicKey:         loadPEM("ALIPAY_PUBLIC_KEY", "ALIPAY_PUBLIC_KEY_FILE"),
		AlipayNotifyURL:         strings.TrimSpace(os.Getenv("ALIPAY_NOTIFY_URL")),
		AlipayReturnURL:         strings.TrimSpace(os.Getenv("ALIPAY_RETURN_URL")),
		AlipayGateway:           getenv("ALIPAY_GATEWAY", "https://openapi.alipay.com/gateway.do"),
		PriceStandardMonth:      positiveOr("PAY_PRICE_STANDARD_MONTH", 3000),
		PriceStandardYear:       positiveOr("PAY_PRICE_STANDARD_YEAR", 29900),
		PricePremiumMonth:       positiveOr("PAY_PRICE_PREMIUM_MONTH", 9900),
		PricePremiumYear:        positiveOr("PAY_PRICE_PREMIUM_YEAR", 99000),
		PriceLifetime:           positiveOr("PAY_PRICE_LIFETIME", 300000),
		ResearchTimeout:         researchTimeout(),
		ResearchBackupModel:     getenv("RESEARCH_BACKUP_MODEL", "grok-chat-fast"),
		SMSProvider:             strings.ToLower(strings.TrimSpace(os.Getenv("SMS_PROVIDER"))),
		SMSAccessKey:            strings.TrimSpace(os.Getenv("SMS_ACCESS_KEY")),
		SMSAccessSecret:         os.Getenv("SMS_ACCESS_SECRET"),
		SMSSignName:             strings.TrimSpace(os.Getenv("SMS_SIGN_NAME")),
		SMSTemplateCode:         strings.TrimSpace(os.Getenv("SMS_TEMPLATE_CODE")),
		SMSSdkAppID:             strings.TrimSpace(os.Getenv("SMS_SDK_APP_ID")),
		SMSRegion:               getenv("SMS_REGION", "ap-guangzhou"),
		SMSAPIBase:              strings.TrimSpace(os.Getenv("SMS_API_BASE")),
		WeChatOpenAppID:         strings.TrimSpace(os.Getenv("WECHAT_OPEN_APP_ID")),
		WeChatOpenSecret:        os.Getenv("WECHAT_OPEN_APP_SECRET"),
		WeChatOpenRedirect:      strings.TrimSpace(os.Getenv("WECHAT_OPEN_REDIRECT_URL")),
		WeChatComponentAppID:    strings.TrimSpace(os.Getenv("WECHAT_COMPONENT_APP_ID")),
		WeChatComponentSecret:   os.Getenv("WECHAT_COMPONENT_APP_SECRET"),
		WeChatComponentToken:    os.Getenv("WECHAT_COMPONENT_TOKEN"),
		WeChatComponentAESKey:   strings.TrimSpace(os.Getenv("WECHAT_COMPONENT_AES_KEY")),
		WeChatComponentRedirect: strings.TrimSpace(os.Getenv("WECHAT_COMPONENT_REDIRECT_URL")),
	}
}

func (s Settings) SMSEnabled() bool {
	switch s.SMSProvider {
	case "aliyun":
		return s.SMSAccessKey != "" && s.SMSAccessSecret != "" && s.SMSSignName != "" && s.SMSTemplateCode != ""
	case "tencent":
		return s.SMSAccessKey != "" && s.SMSAccessSecret != "" && s.SMSSignName != "" && s.SMSTemplateCode != "" && s.SMSSdkAppID != ""
	default:
		return false
	}
}

func (s Settings) WeChatScanEnabled() bool {
	return s.WeChatOpenAppID != "" && s.WeChatOpenSecret != "" && s.WeChatOpenRedirect != ""
}

func (s Settings) WeChatComponentEnabled() bool {
	return s.WeChatComponentAppID != "" && s.WeChatComponentSecret != "" && s.WeChatComponentToken != "" && len(s.WeChatComponentAESKey) == 43 && s.WeChatComponentRedirect != ""
}

func researchTimeout() time.Duration {
	n := envInt("RESEARCH_TIMEOUT_SECONDS", 150)
	if n < 30 {
		n = 30
	}
	if n > 300 {
		n = 300
	}
	return time.Duration(n) * time.Second
}

func publishCap() int {
	n := envInt("PUBLISH_DAILY_CAP", 20)
	if n < 1 {
		n = 1
	}
	if n > 100 {
		n = 100
	}
	return n
}

func positiveOr(key string, fallback int) int {
	n := envInt(key, 0)
	if n <= 0 {
		return fallback
	}
	return n
}

func loadPEM(envKey, fileKey string) string {
	v := os.Getenv(envKey)
	v = strings.ReplaceAll(v, `\n`, "\n")
	if strings.TrimSpace(v) != "" {
		return v
	}
	path := strings.TrimSpace(os.Getenv(fileKey))
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func (s Settings) WeChatPayReady() bool {
	return s.WxPayMchID != "" && s.WxPayAppID != "" && s.WxPayMchSerial != "" && len(s.WxPayAPIv3Key) == 32 &&
		strings.TrimSpace(s.WxPayPrivateKey) != "" && strings.TrimSpace(s.WxPayPlatformCert) != "" && s.WxPayNotifyURL != ""
}

func (s Settings) AlipayReady() bool {
	return s.AlipayAppID != "" && strings.TrimSpace(s.AlipayPrivateKey) != "" && strings.TrimSpace(s.AlipayPublicKey) != "" && s.AlipayNotifyURL != ""
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
