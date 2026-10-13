package jobs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

func TestManualDrawsTheUserDailyCap(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)
	user, plans := seedTwo(t, st, "premium", now)
	var made int
	for i := 0; i < 5; i++ {
		plan := plans[i%2]
		task, err := Create(ctx, st, nil, user, plan, accountOf(t, st, plan), "manual", now.Add(time.Duration(i)*time.Minute), nil)
		if err != nil {
			t.Fatal(i, err)
		}
		task.Status = "success"
		if err := st.UpdateTask(ctx, nil, task); err != nil {
			t.Fatal(err)
		}
		made++
	}
	if made != 5 {
		t.Fatal(made)
	}
	_, err := Create(ctx, st, nil, user, plans[0], accountOf(t, st, plans[0]), "manual", now.Add(time.Hour), nil)
	if err == nil || !strings.Contains(err.Error(), "今日额度已用完") {
		t.Fatal(err)
	}
}

func TestSamePlanCannotRunConcurrently(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)
	user, plans := seedTwo(t, st, "premium", now)
	if _, err := Create(ctx, st, nil, user, plans[0], accountOf(t, st, plans[0]), "manual", now, nil); err != nil {
		t.Fatal(err)
	}
	_, err := Create(ctx, st, nil, user, plans[0], accountOf(t, st, plans[0]), "manual", now.Add(time.Minute), nil)
	if err == nil || !strings.Contains(err.Error(), "正在生成") {
		t.Fatal(err)
	}
	if _, err := Create(ctx, st, nil, user, plans[1], accountOf(t, st, plans[1]), "manual", now.Add(time.Minute), nil); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleIdempotencyUsesTimePoint(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)
	user, plans := seedTwo(t, st, "premium", now)
	plan := plans[0]
	account := accountOf(t, st, plan)
	firstAt := now
	plan.NextRunAt = &firstAt
	first, err := Create(ctx, st, nil, user, plan, account, "schedule", now, nil)
	if err != nil {
		t.Fatal(err)
	}
	first.Status = "success"
	if err := st.UpdateTask(ctx, nil, first); err != nil {
		t.Fatal(err)
	}
	secondAt := now.Add(time.Hour)
	plan.NextRunAt = &secondAt
	second, err := Create(ctx, st, nil, user, plan, account, "schedule", secondAt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.IdempotencyKey == second.IdempotencyKey || !strings.Contains(second.IdempotencyKey, schedule.Format(secondAt)) {
		t.Fatal(first.IdempotencyKey, second.IdempotencyKey)
	}
	second.Status = "success"
	if err := st.UpdateTask(ctx, nil, second); err != nil {
		t.Fatal(err)
	}
	plan.NextRunAt = &secondAt
	_, err = Create(ctx, st, nil, user, plan, account, "schedule", secondAt, nil)
	if err == nil || !db.IsUnique(err) {
		t.Fatal(err)
	}
}

func seedTwo(t *testing.T, st *store.Store, tier string, now time.Time) (*model.User, []*model.WritingPlan) {
	t.Helper()
	ctx := context.Background()
	user := &model.User{Email: tier + "@example.com", PasswordHash: "x", Tier: tier, IsActive: true, CreatedAt: now}
	if err := st.InsertUser(ctx, nil, user); err != nil {
		t.Fatal(err)
	}
	var plans []*model.WritingPlan
	for i, name := range []string{"甲", "乙"} {
		account := &model.WxAccount{UserID: user.ID, Name: name, AppID: "wx_" + tier + name, BindStatus: "active", SubjectType: "personal", ServiceType: "subscription"}
		if err := st.InsertAccount(ctx, nil, account); err != nil {
			t.Fatal(err)
		}
		next := now.Add(-time.Minute)
		plan := &model.WritingPlan{UserID: user.ID, WxAccountID: account.ID, Direction: name, StyleKey: "warm", TemplateKey: "fresh", WordTarget: 800, DailyCount: 1, ScheduleType: "daily", TimeWindow: "night", Status: "active", NextRunAt: &next, CreatedAt: now, Weekdays: []int{}}
		if err := st.InsertPlan(ctx, nil, plan); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, plan)
		_ = i
	}
	return user, plans
}

func accountOf(t *testing.T, st *store.Store, plan *model.WritingPlan) *model.WxAccount {
	t.Helper()
	account, err := st.GetAccount(context.Background(), nil, plan.WxAccountID)
	if err != nil || account == nil {
		t.Fatal(err)
	}
	return account
}

func TestCreateRejectsAnotherUsersAccount(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)
	owner, plans := seedTwo(t, st, "premium", now)
	other := &model.User{Email: "other@example.com", PasswordHash: "x", Tier: "premium", IsActive: true, CreatedAt: now}
	if err := st.InsertUser(ctx, nil, other); err != nil {
		t.Fatal(err)
	}
	_, err := Create(ctx, st, nil, other, plans[0], accountOf(t, st, plans[0]), "manual", now, nil)
	if err == nil || !strings.Contains(err.Error(), "不属于") {
		t.Fatal(err)
	}
	if _, err := Create(ctx, st, nil, owner, plans[0], accountOf(t, st, plans[0]), "manual", now, nil); err != nil {
		t.Fatal(err)
	}
}

func TestOpenTasksAreUniquePerPlan(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)
	user, plans := seedTwo(t, st, "premium", now)
	plan := plans[0]
	account := accountOf(t, st, plan)
	day := schedule.ShanghaiDay(now)
	first := &model.ArticleTask{
		PlanID: plan.ID, UserID: user.ID, WxAccountID: account.ID, IdempotencyKey: "open-a",
		TriggerType: "manual", ScheduleDate: day, RunAt: now, Status: "running", CreatedAt: now,
	}
	if err := st.InsertTask(ctx, nil, first); err != nil {
		t.Fatal(err)
	}
	second := &model.ArticleTask{
		PlanID: plan.ID, UserID: user.ID, WxAccountID: account.ID, IdempotencyKey: "open-b",
		TriggerType: "manual", ScheduleDate: day, RunAt: now, Status: "pending", CreatedAt: now,
	}
	err := st.InsertTask(ctx, nil, second)
	if err == nil || !db.IsUnique(err) || !planOpenConflict(err) {
		t.Fatal(err)
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
