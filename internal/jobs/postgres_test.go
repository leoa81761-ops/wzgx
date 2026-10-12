package jobs

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

func TestPostgresParallelGenerateStopsAtDailyCap(t *testing.T) {
	raw := os.Getenv("POSTGRES_TEST_URL")
	if raw == "" {
		t.Skip("未设置 POSTGRES_TEST_URL，跳过 Postgres 并发额度测试")
	}
	database, st := openPostgres(t, raw)
	ctx := context.Background()
	fallback := catalog.PlanFallback("premium")
	if err := st.UpsertPlanConfig(ctx, nil, model.PlanConfig{
		Tier: fallback.Tier, Name: fallback.Name, PriceLabel: fallback.PriceLabel,
		DailyQuota: 5, PeriodQuota: fallback.PeriodQuota, PeriodUnit: fallback.PeriodUnit,
		MaxAccounts: fallback.MaxAccounts, MaxPlans: fallback.MaxPlans, MaxWords: fallback.MaxWords,
		Summary: fallback.Summary,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdatePlanQuota(ctx, nil, "premium", 5, fallback.PeriodQuota, fallback.Summary); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)
	for round := 0; round < 3; round++ {
		user, plans, accounts := seedPlans(t, st, 10, now, "cap")
		ok, blocked, other := burstCreate(t, database, st, user, plans, accounts, now)
		if ok != 5 || blocked != 5 || len(other) != 0 {
			t.Fatalf("round %d: created %d, quota %d, other %v", round, ok, blocked, other)
		}
		tasks, holds := countUsage(t, database, user.ID)
		if tasks != 5 || holds != 5 {
			t.Fatalf("round %d: tasks %d holds %d", round, tasks, holds)
		}
	}

	user, plans, accounts := seedPlans(t, st, 1, now, "same")
	plans = repeatPlan(plans[0], 10)
	accounts = repeatAccount(accounts[0], 10)
	ok, blocked, other := burstCreate(t, database, st, user, plans, accounts, now)
	if ok != 1 || blocked != 0 || len(other) != 9 {
		t.Fatalf("same plan: created %d, quota %d, other %v", ok, blocked, other)
	}
	for _, err := range other {
		if !strings.Contains(err.Error(), "正在生成") {
			t.Fatal(err)
		}
	}
	tasks, holds := countUsage(t, database, user.ID)
	if tasks != 1 || holds != 1 {
		t.Fatalf("same plan stored tasks %d holds %d", tasks, holds)
	}
}

func burstCreate(t *testing.T, database *db.DB, st *store.Store, user *model.User, plans []*model.WritingPlan, accounts []*model.WxAccount, now time.Time) (ok, blocked int, other []error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var ready, done sync.WaitGroup
	release := make(chan struct{})
	ready.Add(len(plans))
	done.Add(len(plans))
	var mu sync.Mutex
	for i := range plans {
		go func(plan *model.WritingPlan, account *model.WxAccount) {
			defer done.Done()
			ready.Done()
			<-release
			copied := *user
			err := database.WithTx(ctx, func(tx *db.Tx) error {
				if _, err := tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL READ COMMITTED"); err != nil {
					return err
				}
				_, err := Create(ctx, st, tx, &copied, plan, account, "manual", now, nil)
				return err
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ok++
				return
			}
			if containsQuota(err) {
				blocked++
				return
			}
			other = append(other, err)
		}(plans[i], accounts[i])
	}
	ready.Wait()
	close(release)
	done.Wait()
	return ok, blocked, other
}

func containsQuota(err error) bool {
	return err != nil && strings.Contains(err.Error(), "额度已用完")
}

func countUsage(t *testing.T, database *db.DB, userID int64) (int64, int64) {
	t.Helper()
	var tasks, holds int64
	ctx := context.Background()
	if err := database.QueryRow(ctx, `SELECT COUNT(*) FROM article_task WHERE user_id = ?`, userID).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0) FROM quota_hold WHERE user_id = ? AND state IN ('held', 'confirmed') AND amount > 0`, userID).Scan(&holds); err != nil {
		t.Fatal(err)
	}
	return tasks, holds
}

func seedPlans(t *testing.T, st *store.Store, n int, now time.Time, tag string) (*model.User, []*model.WritingPlan, []*model.WxAccount) {
	t.Helper()
	ctx := context.Background()
	user := &model.User{Email: tag + "-" + randHex() + "@example.com", PasswordHash: "x", Tier: "premium", IsActive: true, CreatedAt: now}
	if err := st.InsertUser(ctx, nil, user); err != nil {
		t.Fatal(err)
	}
	var plans []*model.WritingPlan
	var accounts []*model.WxAccount
	for i := 0; i < n; i++ {
		account := &model.WxAccount{
			UserID: user.ID, Name: tag, AppID: "wx_" + tag + "_" + randHex(),
			BindStatus: "active", SubjectType: "personal", ServiceType: "subscription",
		}
		if err := st.InsertAccount(ctx, nil, account); err != nil {
			t.Fatal(err)
		}
		next := now.Add(-time.Minute)
		plan := &model.WritingPlan{
			UserID: user.ID, WxAccountID: account.ID, Direction: tag + itoa(int64(i)),
			StyleKey: "warm", TemplateKey: "fresh", WordTarget: 800, DailyCount: 1,
			ScheduleType: "daily", TimeWindow: "night", Status: "active", NextRunAt: &next,
			CreatedAt: now, Weekdays: []int{},
		}
		if err := st.InsertPlan(ctx, nil, plan); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, plan)
		accounts = append(accounts, account)
	}
	return user, plans, accounts
}

func repeatPlan(plan *model.WritingPlan, n int) []*model.WritingPlan {
	out := make([]*model.WritingPlan, n)
	for i := range out {
		out[i] = plan
	}
	return out
}

func repeatAccount(account *model.WxAccount, n int) []*model.WxAccount {
	out := make([]*model.WxAccount, n)
	for i := range out {
		out[i] = account
	}
	return out
}

func openPostgres(t *testing.T, raw string) (*db.DB, *store.Store) {
	t.Helper()
	admin, err := sql.Open("pgx", raw)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	name := "gonghao_q_" + randHex()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	var database *db.DB
	t.Cleanup(func() {
		if database != nil {
			_ = database.Close()
		}
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dropCancel()
		_, _ = admin.ExecContext(dropCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		if _, err := admin.ExecContext(dropCtx, "DROP DATABASE "+name); err != nil {
			t.Logf("删除测试库 %s 失败: %v", name, err)
		}
		_ = admin.Close()
	})
	database, err = db.Open(withDatabase(raw, name), 20, 10)
	if err != nil {
		t.Fatal(err)
	}
	return database, store.New(database)
}

func withDatabase(raw, name string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.Path = "/" + name
	return parsed.String()
}
