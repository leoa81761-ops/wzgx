package quota

import (
	"context"
	"fmt"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

const GraceDays = 7

type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

type Limits struct {
	Tier        string
	Name        string
	DailyQuota  int
	PeriodQuota int
	PeriodUnit  string
	MaxAccounts int
	MaxPlans    int
	MaxWords    int
	Summary     string
	PriceLabel  string
}

func Membership(user *model.User, now time.Time) (tier, state string) {
	now = schedule.AsUTC(now)
	tier = user.Tier
	if tier == "" {
		tier = "free"
	}
	if tier == "free" {
		return "free", "active"
	}
	if tier == "lifetime" {
		return "lifetime", "active"
	}
	if user.TierExpiresAt == nil || !schedule.AsUTC(*user.TierExpiresAt).Before(now) {
		return tier, "active"
	}
	if !now.After(schedule.AsUTC(*user.TierExpiresAt).Add(GraceDays * 24 * time.Hour)) {
		return tier, "grace"
	}
	return "free", "expired"
}

func LifetimePeriodKey(start, today time.Time) string {
	sy, sm, sd := start.Date()
	ty, _, _ := today.Date()
	years := ty - sy
	ann := dateOr28(sy+years, sm, sd)
	todayD := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	if todayD.Before(ann) {
		years--
	}
	if years < 0 {
		years = 0
	}
	return fmt.Sprintf("life:%04d-%02d-%02d:%d", sy, int(sm), sd, years)
}

func dateOr28(y int, m time.Month, d int) time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	if t.Day() != d {
		return time.Date(y, m, 28, 0, 0, 0, 0, time.UTC)
	}
	return t
}

func PeriodKey(user *model.User, tier string, now time.Time, unit string) string {
	local := schedule.ShanghaiNow(now)
	if tier == "lifetime" || unit == "year" {
		started := user.TierStartedAt
		if started == nil || started.IsZero() {
			if !user.CreatedAt.IsZero() {
				started = &user.CreatedAt
			} else {
				n := now
				started = &n
			}
		}
		return LifetimePeriodKey(started.In(schedule.Shanghai), local)
	}
	return local.Format("2006-01")
}

func LimitsFor(ctx context.Context, st *store.Store, q db.Q, tier string) (Limits, error) {
	row, err := st.GetPlanConfig(ctx, q, tier)
	if err != nil {
		return Limits{}, err
	}
	if row == nil {
		fb := catalog.PlanFallback(tier)
		return Limits{Tier: fb.Tier, Name: fb.Name, DailyQuota: fb.DailyQuota, PeriodQuota: fb.PeriodQuota, PeriodUnit: fb.PeriodUnit, MaxAccounts: fb.MaxAccounts, MaxPlans: fb.MaxPlans, MaxWords: fb.MaxWords, Summary: fb.Summary, PriceLabel: fb.PriceLabel}, nil
	}
	return Limits{Tier: row.Tier, Name: row.Name, DailyQuota: row.DailyQuota, PeriodQuota: row.PeriodQuota, PeriodUnit: row.PeriodUnit, MaxAccounts: row.MaxAccounts, MaxPlans: row.MaxPlans, MaxWords: row.MaxWords, Summary: row.Summary, PriceLabel: row.PriceLabel}, nil
}

func Usage(ctx context.Context, st *store.Store, q db.Q, userID int64, dayKey, periodKey string) (int, int, error) {
	daily, err := st.SumHold(ctx, q, userID, "day_key", dayKey)
	if err != nil {
		return 0, 0, err
	}
	period, err := st.SumHold(ctx, q, userID, "period_key", periodKey)
	return daily, period, err
}

func BlockReason(ctx context.Context, st *store.Store, q db.Q, user *model.User, now time.Time) (string, error) {
	tier, _ := Membership(user, now)
	limits, err := LimitsFor(ctx, st, q, tier)
	if err != nil {
		return "", err
	}
	day := schedule.ShanghaiDay(now)
	period := PeriodKey(user, tier, now, limits.PeriodUnit)
	daily, monthly, err := Usage(ctx, st, q, user.ID, day, period)
	if err != nil {
		return "", err
	}
	if daily >= limits.DailyQuota {
		return "今日额度已用完", nil
	}
	if monthly >= limits.PeriodQuota {
		unit := "本月"
		if limits.PeriodUnit == "year" {
			unit = "本周年"
		}
		return unit + "额度已用完", nil
	}
	return "", nil
}

func Reserve(ctx context.Context, st *store.Store, q db.Q, user *model.User, taskID int64, now time.Time, free bool, note string) error {
	tier, _ := Membership(user, now)
	limits, err := LimitsFor(ctx, st, q, tier)
	if err != nil {
		return err
	}
	day := schedule.ShanghaiDay(now)
	period := PeriodKey(user, tier, now, limits.PeriodUnit)
	if free {
		if err := st.InsertHold(ctx, q, &model.QuotaHold{UserID: user.ID, TaskID: taskID, DayKey: day, PeriodKey: period, Amount: 0, State: "confirmed", CreatedAt: now}); err != nil {
			return err
		}
		if note == "" {
			note = "首次重写不占额度"
		}
		return st.InsertLedger(ctx, q, user.ID, "free_regen", 0, &taskID, note, schedule.Format(now))
	}
	reason, err := BlockReason(ctx, st, q, user, now)
	if err != nil {
		return err
	}
	if reason != "" {
		return &Error{Message: reason}
	}
	if err := st.InsertHold(ctx, q, &model.QuotaHold{UserID: user.ID, TaskID: taskID, DayKey: day, PeriodKey: period, Amount: 1, State: "held", CreatedAt: now}); err != nil {
		return err
	}
	if note == "" {
		note = "预占额度"
	}
	return st.InsertLedger(ctx, q, user.ID, "reserve", 1, &taskID, note, schedule.Format(now))
}

func Confirm(ctx context.Context, st *store.Store, q db.Q, taskID int64) error {
	hold, err := st.GetHoldByTask(ctx, q, taskID)
	if err != nil || hold == nil || hold.State != "held" {
		return err
	}
	if err := st.UpdateHoldState(ctx, q, hold.ID, "confirmed"); err != nil {
		return err
	}
	return st.InsertLedger(ctx, q, hold.UserID, "confirm", hold.Amount, &taskID, "确认扣除", schedule.Format(time.Now().UTC()))
}

func Release(ctx context.Context, st *store.Store, q db.Q, taskID int64, note string) error {
	hold, err := st.GetHoldByTask(ctx, q, taskID)
	if err != nil || hold == nil || hold.State != "held" {
		return err
	}
	if note == "" {
		note = "失败退回"
	}
	if err := st.UpdateHoldState(ctx, q, hold.ID, "released"); err != nil {
		return err
	}
	return st.InsertLedger(ctx, q, hold.UserID, "release", hold.Amount, &taskID, note, schedule.Format(time.Now().UTC()))
}

func Snapshot(ctx context.Context, st *store.Store, q db.Q, user *model.User, now time.Time) (map[string]any, error) {
	tier, state := Membership(user, now)
	limits, err := LimitsFor(ctx, st, q, tier)
	if err != nil {
		return nil, err
	}
	day := schedule.ShanghaiDay(now)
	period := PeriodKey(user, tier, now, limits.PeriodUnit)
	daily, used, err := Usage(ctx, st, q, user.ID, day, period)
	if err != nil {
		return nil, err
	}
	stateLabel := map[string]string{"active": "有效", "grace": "宽限期", "expired": "已降为免费"}[state]
	if stateLabel == "" {
		stateLabel = state
	}
	periodLabel := "本月"
	if limits.PeriodUnit == "year" {
		periodLabel = "本周年"
	}
	var expires any
	if user.TierExpiresAt != nil {
		expires = user.TierExpiresAt.UTC().Format(time.RFC3339)
	}
	label := catalog.TierLabels[tier]
	if label == "" {
		label = tier
	}
	return map[string]any{
		"tier": tier, "tier_label": label, "stored_tier": user.Tier, "state": state, "state_label": stateLabel,
		"expires_at": expires, "daily_used": daily, "daily_limit": limits.DailyQuota, "period_used": used, "period_limit": limits.PeriodQuota,
		"period_unit": limits.PeriodUnit, "period_label": periodLabel, "max_words": limits.MaxWords, "max_accounts": limits.MaxAccounts,
		"max_plans": limits.MaxPlans, "summary": limits.Summary,
	}, nil
}

func EnforceFreeLimits(ctx context.Context, st *store.Store, q db.Q, user *model.User, now time.Time) error {
	tier, _ := Membership(user, now)
	if tier != "free" {
		return nil
	}
	limits, err := LimitsFor(ctx, st, q, "free")
	if err != nil {
		return err
	}
	accounts, err := st.ListActiveAccounts(ctx, q, user.ID)
	if err != nil {
		return err
	}
	kept := map[int64]bool{}
	for i, account := range accounts {
		if i >= limits.MaxAccounts {
			break
		}
		kept[account.ID] = true
	}
	plans, err := st.ListPlansForLimit(ctx, q, user.ID)
	if err != nil {
		return err
	}
	activeKept := 0
	for i := range plans {
		plan := &plans[i]
		if !kept[plan.WxAccountID] || activeKept >= limits.MaxPlans {
			if plan.Status == "active" {
				plan.Status = "paused"
				plan.PauseReason = "会员降级"
				if err := st.UpdatePlan(ctx, q, plan); err != nil {
					return err
				}
			}
			continue
		}
		if plan.Status == "paused" && plan.PauseReason == "会员降级" {
			plan.Status = "active"
			plan.PauseReason = ""
			if err := st.UpdatePlan(ctx, q, plan); err != nil {
				return err
			}
			activeKept++
			continue
		}
		if plan.Status == "active" {
			activeKept++
		}
	}
	return nil
}

func Priority(tier, trigger string) int {
	base := map[string]int{"premium": 30, "lifetime": 30, "standard": 20, "free": 10}[tier]
	if base == 0 {
		base = 10
	}
	if trigger == "manual" {
		base += 5
	}
	if trigger == "regenerate" {
		base += 6
	}
	return base
}
