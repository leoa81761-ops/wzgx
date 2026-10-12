package integrate

import (
	"testing"

	"github.com/leoa81761-ops/wzgx/internal/config"
)

func TestCallbackDefaultsUsePublicBaseOnly(t *testing.T) {
	empty := WithCallbackDefaults(config.Settings{})
	if empty.WxPayNotifyURL != "" || empty.AlipayNotifyURL != "" || empty.AlipayReturnURL != "" || empty.WeChatOpenRedirect != "" || empty.WeChatComponentRedirect != "" {
		t.Fatalf("%#v", empty)
	}
	filled := WithCallbackDefaults(config.Settings{PublicBaseURL: "https://mp.nbn168.uk/"})
	if filled.PublicBaseURL != "https://mp.nbn168.uk" {
		t.Fatal(filled.PublicBaseURL)
	}
	if filled.WxPayNotifyURL != "https://mp.nbn168.uk"+PathWechatPayNotify {
		t.Fatal(filled.WxPayNotifyURL)
	}
	if filled.AlipayNotifyURL != "https://mp.nbn168.uk"+PathAlipayNotify {
		t.Fatal(filled.AlipayNotifyURL)
	}
	if filled.AlipayReturnURL != "https://mp.nbn168.uk"+PathAlipayReturn {
		t.Fatal(filled.AlipayReturnURL)
	}
	if filled.WeChatOpenRedirect != "https://mp.nbn168.uk"+PathWechatScan {
		t.Fatal(filled.WeChatOpenRedirect)
	}
	if filled.WeChatComponentRedirect != "https://mp.nbn168.uk"+PathWechatComponent {
		t.Fatal(filled.WeChatComponentRedirect)
	}
	kept := WithCallbackDefaults(config.Settings{
		PublicBaseURL: "https://mp.nbn168.uk", WxPayNotifyURL: "https://pay.example/hook",
	})
	if kept.WxPayNotifyURL != "https://pay.example/hook" {
		t.Fatal(kept.WxPayNotifyURL)
	}
	if CallbackDefault("", "wxpay", "notify_url") != "" {
		t.Fatal("empty base invented a host")
	}
}
