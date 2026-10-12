package quota

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

func TestDailyMonthlyReleaseAndGrace(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	user := &model.User{Email: "quota@example.com", PasswordHash: "x", Tier: "free", IsActive: true, CreatedAt: now}
	if err := st.InsertUser(ctx, nil, user); err != nil {
		t.Fatal(err)
	}
	if err := Reserve(ctx, st, nil, user, 1, now, false, ""); err != nil {
		t.Fatal(err)
	}
	reason, err := BlockReason(ctx, st, nil, user, now)
	if err != nil || reason != "今日额度已用完" {
		t.Fatal(reason, err)
	}
	if err := Reserve(ctx, st, nil, user, 2, now, false, ""); err == nil {
		t.Fatal("second reserve should fail")
	}
	if err := Release(ctx, st, nil, 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := Reserve(ctx, st, nil, user, 3, now, false, ""); err != nil {
		t.Fatal(err)
	}

	freeUser := &model.User{Email: "free-regen@example.com", PasswordHash: "x", Tier: "free", IsActive: true, CreatedAt: now}
	if err := st.InsertUser(ctx, nil, freeUser); err != nil {
		t.Fatal(err)
	}
	if err := Reserve(ctx, st, nil, freeUser, 9, now, true, "首次重写"); err != nil {
		t.Fatal(err)
	}
	reason, err = BlockReason(ctx, st, nil, freeUser, now)
	if err != nil || reason != "" {
		t.Fatalf("free regen should not consume quota, got %q %v", reason, err)
	}

	start := time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)
	monthUser := &model.User{Email: "month@example.com", PasswordHash: "x", Tier: "free", IsActive: true, CreatedAt: start}
	if err := st.InsertUser(ctx, nil, monthUser); err != nil {
		t.Fatal(err)
	}
	for offset := 0; offset < 10; offset++ {
		if err := Reserve(ctx, st, nil, monthUser, int64(100+offset), start.AddDate(0, 0, offset), false, ""); err != nil {
			t.Fatal(offset, err)
		}
	}
	blocked, err := BlockReason(ctx, st, nil, monthUser, start.AddDate(0, 0, 20))
	if err != nil || blocked != "本月额度已用完" {
		t.Fatal(blocked, err)
	}

	graceNow := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	expires := graceNow.Add(-3 * 24 * time.Hour)
	member := &model.User{Tier: "standard", TierExpiresAt: &expires}
	tier, state := Membership(member, graceNow)
	if tier != "standard" || state != "grace" {
		t.Fatal(tier, state)
	}
	tier, state = Membership(member, expires.Add(8*24*time.Hour))
	if tier != "free" || state != "expired" {
		t.Fatal(tier, state)
	}
}

func TestPackUsedAfterPeriodIsFull(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)
	user := &model.User{Email: "pack@example.com", PasswordHash: "x", Tier: "free", IsActive: true, CreatedAt: start}
	if err := st.InsertUser(ctx, nil, user); err != nil {
		t.Fatal(err)
	}
	for offset := 0; offset < 10; offset++ {
		if err := Reserve(ctx, st, nil, user, int64(200+offset), start.AddDate(0, 0, offset), false, ""); err != nil {
			t.Fatal(offset, err)
		}
	}
	if err := st.InsertPack(ctx, nil, user.ID, "pack-order", 2, start.Add(90*24*time.Hour), start); err != nil {
		t.Fatal(err)
	}
	day := start.AddDate(0, 0, 20)
	if err := Reserve(ctx, st, nil, user, 300, day, false, ""); err != nil {
		t.Fatal(err)
	}
	left, err := st.PackRemaining(ctx, nil, user.ID, day)
	if err != nil || left != 1 {
		t.Fatal(left, err)
	}
	snap, err := Snapshot(ctx, st, nil, user, day)
	if err != nil || snap["pack_remaining"] != 1 {
		t.Fatal(snap, err)
	}
	if err := Release(ctx, st, nil, 300, ""); err != nil {
		t.Fatal(err)
	}
	left, err = st.PackRemaining(ctx, nil, user.ID, day)
	if err != nil || left != 2 {
		t.Fatal(left, err)
	}
}

func TestPremiumDailyIsFiveAcrossTheUser(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 2, 0, 0, 0, time.UTC)
	user := &model.User{Email: "premium@example.com", PasswordHash: "x", Tier: "premium", IsActive: true, CreatedAt: now}
	if err := st.InsertUser(ctx, nil, user); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		if err := Reserve(ctx, st, nil, user, int64(i), now, false, ""); err != nil {
			t.Fatal(i, err)
		}
	}
	reason, err := BlockReason(ctx, st, nil, user, now)
	if err != nil || reason != "今日额度已用完" {
		t.Fatal(reason, err)
	}
	snap, err := Snapshot(ctx, st, nil, user, now)
	if err != nil || snap["daily_remaining"] != 0 || snap["daily_limit"] != 5 || snap["period_limit"] != 150 {
		t.Fatal(snap, err)
	}
	if err := Release(ctx, st, nil, 5, ""); err != nil {
		t.Fatal(err)
	}
	snap, err = Snapshot(ctx, st, nil, user, now)
	if err != nil || snap["daily_remaining"] != 1 || snap["daily_used"] != 4 {
		t.Fatal(snap, err)
	}
}

func TestPlanDailyCountsCannotExceedTier(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 2, 0, 0, 0, time.UTC)
	user := &model.User{Email: "split@example.com", PasswordHash: "x", Tier: "premium", IsActive: true, CreatedAt: now}
	if err := st.InsertUser(ctx, nil, user); err != nil {
		t.Fatal(err)
	}
	account := &model.WxAccount{UserID: user.ID, Name: "甲", AppID: "wx_split", BindStatus: "active", SubjectType: "personal", ServiceType: "subscription"}
	if err := st.InsertAccount(ctx, nil, account); err != nil {
		t.Fatal(err)
	}
	first := &model.WritingPlan{UserID: user.ID, WxAccountID: account.ID, Direction: "甲", StyleKey: "warm", TemplateKey: "fresh", WordTarget: 800, DailyCount: 3, ScheduleType: "daily", TimeWindow: "night", Status: "active", CreatedAt: now, Weekdays: []int{}}
	second := &model.WritingPlan{UserID: user.ID, WxAccountID: account.ID, Direction: "乙", StyleKey: "warm", TemplateKey: "fresh", WordTarget: 800, DailyCount: 2, ScheduleType: "daily", TimeWindow: "night", Status: "active", CreatedAt: now, Weekdays: []int{}}
	if err := st.InsertPlan(ctx, nil, first); err != nil || st.InsertPlan(ctx, nil, second) != nil {
		t.Fatal(err)
	}
	if err := CheckDailyCount(ctx, st, nil, user, now, 0, 1); err == nil {
		t.Fatal("6th article per day should be rejected")
	}
	if err := CheckDailyCount(ctx, st, nil, user, now, first.ID, 3); err != nil {
		t.Fatal(err)
	}
	free := &model.User{Email: "one@example.com", PasswordHash: "x", Tier: "free", IsActive: true, CreatedAt: now}
	if err := st.InsertUser(ctx, nil, free); err != nil {
		t.Fatal(err)
	}
	if err := CheckDailyCount(ctx, st, nil, free, now, 0, 2); err == nil {
		t.Fatal("free tier is 1 per day")
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	database, err := db.Open("sqlite://"+filepath.Join(t.TempDir(), "t.db"), 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return store.New(database)
}
