package scheduler

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/stats"
	"github.com/leoa81761-ops/wzgx/internal/store"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
)

func (r *Runner) pullStats(ctx context.Context, now time.Time) error {
	accounts, err := r.Deps.Store.ListVerifiedAccounts(ctx)
	if err != nil {
		return err
	}
	day := stats.Yesterday(now)
	for i := range accounts {
		account := accounts[i]
		if !stats.PullDue(now, account.ID) {
			continue
		}
		agreed, err := r.Deps.Store.HasConsentVersion(ctx, account.UserID, "analytics", "1")
		if err != nil || !agreed {
			continue
		}
		seen, err := r.Deps.Store.HasWeChatStat(ctx, account.ID, day)
		if err != nil || seen {
			continue
		}
		secret, err := security.DecryptSecret(r.Deps.Settings.MasterKey, account.SecretEncrypted)
		if err != nil {
			continue
		}
		wx, err := r.Deps.Cache.WeChat(ctx, nil)
		if err != nil {
			return err
		}
		mode := "live"
		if wx.Mode == "mock" || strings.HasPrefix(account.AppID, "wx_mock") {
			mode = "mock"
		}
		client := wechat.Build(account.AppID, secret, mode, wx.Base, r.Deps.HTTP, r.Deps.Tokens)
		rows, err := stats.Collect(ctx, client, day)
		if err != nil {
			log.Printf("认证号 %d 统计未拉到: %v", account.ID, err)
			continue
		}
		if len(rows) == 0 {
			rows = []stats.Row{{Date: day, Key: "empty"}}
		}
		for _, row := range rows {
			if row.Date == "" {
				row.Date = day
			}
			if err := r.Deps.Store.SaveStat(ctx, nil, store.StatRow{
				AccountID: account.ID, Date: row.Date, Key: row.Key, Reads: row.Reads, Shares: row.Shares,
				Favors: row.Favors, Follows: row.Follows, Source: "wechat", CreatedAt: now,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
