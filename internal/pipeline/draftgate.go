package pipeline

import (
	"context"
	"strconv"
	"strings"

	"github.com/leoa81761-ops/wzgx/internal/wechat"
)

func draftWaitMessage(ctx context.Context, deps Deps, w *work) string {
	if w == nil || deps.Cache == nil || w.DeliveryMode != "draft" || w.ExistingMediaID != "" {
		return ""
	}
	wx, err := deps.Cache.WeChat(ctx, nil)
	if err != nil {
		return ""
	}
	mode := "live"
	if wx.Mode == "mock" || strings.HasPrefix(w.AppID, "wx_mock") {
		mode = "mock"
	}
	client := wechat.Build(w.AppID, w.AppSecret, mode, wx.Base, deps.HTTP, deps.Tokens)
	msg, wait := wechat.DraftWait(ctx, client, draftCap(ctx, deps))
	if !wait {
		return ""
	}
	return msg
}

func draftCap(ctx context.Context, deps Deps) int {
	raw, err := deps.Store.GetSetting(ctx, nil, "draft_box_cap")
	if err != nil || strings.TrimSpace(raw) == "" {
		return 1000
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 1000
	}
	return n
}
