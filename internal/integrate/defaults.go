package integrate

import (
	"strings"

	"github.com/leoa81761-ops/wzgx/internal/config"
)

const (
	// PublicBaseHint 只出现在配置页的提示里。运行时不会用它代替环境变量。
	PublicBaseHint = "https://mp.nbn168.uk"

	PathWechatPayNotify = "/api/billing/wechat/notify"
	PathAlipayNotify    = "/api/billing/alipay/notify"
	PathAlipayReturn    = "/pricing"
	PathWechatScan      = "/api/auth/wechat/callback"
	PathWechatComponent = "/api/auth/wechat/component/callback"
)

// CallbackDefault builds a callback from the configured public base.
// An empty base stays empty. The request host is never used.
func CallbackDefault(base, section, key string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return ""
	}
	path := ""
	switch section + "." + key {
	case "wxpay.notify_url":
		path = PathWechatPayNotify
	case "alipay.notify_url":
		path = PathAlipayNotify
	case "alipay.return_url":
		path = PathAlipayReturn
	case "wechat_open.redirect_url":
		path = PathWechatScan
	case "wechat_component.redirect_url":
		path = PathWechatComponent
	default:
		return ""
	}
	return base + path
}

// WithCallbackDefaults fills callback URLs that are still empty.
// A value already set from the database or from its own environment variable stays.
func WithCallbackDefaults(cfg config.Settings) config.Settings {
	cfg.PublicBaseURL = strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
	if cfg.WxPayNotifyURL == "" {
		cfg.WxPayNotifyURL = CallbackDefault(cfg.PublicBaseURL, "wxpay", "notify_url")
	}
	if cfg.AlipayNotifyURL == "" {
		cfg.AlipayNotifyURL = CallbackDefault(cfg.PublicBaseURL, "alipay", "notify_url")
	}
	if cfg.AlipayReturnURL == "" {
		cfg.AlipayReturnURL = CallbackDefault(cfg.PublicBaseURL, "alipay", "return_url")
	}
	if cfg.WeChatOpenRedirect == "" {
		cfg.WeChatOpenRedirect = CallbackDefault(cfg.PublicBaseURL, "wechat_open", "redirect_url")
	}
	if cfg.WeChatComponentRedirect == "" {
		cfg.WeChatComponentRedirect = CallbackDefault(cfg.PublicBaseURL, "wechat_component", "redirect_url")
	}
	return cfg
}
