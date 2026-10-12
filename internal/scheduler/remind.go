package scheduler

import (
	"context"
	"strconv"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

func (r *Runner) remindExpiring(ctx context.Context, now time.Time) error {
	if r.Deps.Store == nil {
		return nil
	}
	users, err := r.Deps.Store.ListExpiringUsers(ctx)
	if err != nil {
		return err
	}
	today := dateOnly(schedule.ShanghaiNow(now))
	for i := range users {
		user := &users[i]
		if user.TierExpiresAt == nil {
			continue
		}
		exp := dateOnly(schedule.AsUTC(*user.TierExpiresAt).In(schedule.Shanghai))
		days := int(exp.Sub(today).Hours() / 24)
		if days != 7 && days != 3 && days != 1 {
			continue
		}
		kind := strconv.Itoa(days)
		on := exp.Format("2006-01-02")
		fresh, err := r.Deps.Store.InsertExpiryNotice(ctx, user.ID, kind, on, now)
		if err != nil {
			return err
		}
		if !fresh {
			continue
		}
		note := &store.Notification{
			UserID: user.ID, Kind: "expiry", Title: "会员还剩 " + kind + " 天",
			Body: "会员将在 " + on + " 到期。到期后有 7 天宽限期，宽限期内不能新增公众号和方向。可以提前续费。",
			CreatedAt: now,
		}
		if err := r.Deps.Store.InsertNotification(ctx, nil, note); err != nil {
			return err
		}
	}
	return nil
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
