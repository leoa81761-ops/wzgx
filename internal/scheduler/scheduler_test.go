package scheduler

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/pipeline"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/runtime"
	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/store"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
)

func TestScanIsIdempotentAndSkipsFuture(t *testing.T) {
	st, runner := openRunner(t)
	now := time.Now().UTC()
	_, _, plan := seed(t, st, "writer@example.com", now.Add(-time.Minute))
	ctx := context.Background()
	first, err := runner.ScanOnce(ctx, now)
	if err != nil || len(first) != 1 {
		t.Fatal(len(first), err)
	}
	second, err := runner.ScanOnce(ctx, now.Add(time.Minute))
	if err != nil || len(second) != 0 {
		t.Fatal(second, err)
	}
	task, err := st.GetTask(ctx, nil, first[0])
	if err != nil || task == nil || task.Status != "success" {
		t.Fatalf("%#v %v", task, err)
	}
	fresh, err := st.GetPlan(ctx, nil, plan.ID)
	if err != nil || fresh.NextRunAt == nil || !fresh.NextRunAt.After(now) {
		t.Fatal(fresh, err)
	}

	_, _, _ = seed(t, st, "future@example.com", now.Add(48*time.Hour))
	claimed, err := runner.ScanOnce(ctx, now)
	if err != nil || len(claimed) != 0 {
		t.Fatal(claimed, err)
	}
}

func TestTwoPlansShareTheDay(t *testing.T) {
	st, runner := openRunner(t)
	now := time.Now().UTC()
	ctx := context.Background()
	user := &model.User{Email: "two@example.com", PasswordHash: "x", Tier: "premium", IsActive: true, CreatedAt: now}
	if err := st.InsertUser(ctx, nil, user); err != nil {
		t.Fatal(err)
	}
	secret, err := security.EncryptSecret("test-master-key", "mock-secret")
	if err != nil {
		t.Fatal(err)
	}
	bound := now
	account := &model.WxAccount{UserID: user.ID, Name: "测试号", AppID: "wx_mock_two", SecretEncrypted: secret, DeliveryMode: "draft", BindStatus: "active", BoundAt: &bound, SubjectType: "personal", ServiceType: "subscription"}
	if err := st.InsertAccount(ctx, nil, account); err != nil {
		t.Fatal(err)
	}
	next := now.Add(-time.Minute)
	for _, direction := range []string{"甲", "乙"} {
		plan := &model.WritingPlan{UserID: user.ID, WxAccountID: account.ID, Direction: direction, Mode: "managed", StyleKey: "warm", TemplateKey: "fresh", WordTarget: 800, DailyCount: 1, ScheduleType: "daily", TimeWindow: "night", Status: "active", NextRunAt: &next, CreatedAt: now, Weekdays: []int{}}
		if err := st.InsertPlan(ctx, nil, plan); err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := runner.ScanOnce(ctx, now)
	if err != nil || len(claimed) != 2 {
		t.Fatal(len(claimed), err)
	}
}

func TestStuckGeneratingIsFailed(t *testing.T) {
	st, runner := openRunner(t)
	now := time.Now().UTC()
	user, account, plan := seed(t, st, "stuck@example.com", now.Add(48*time.Hour))
	ctx := context.Background()
	started := now.Add(-20 * time.Minute)
	task := &model.ArticleTask{
		PlanID: plan.ID, UserID: user.ID, WxAccountID: account.ID, IdempotencyKey: "stuck-task",
		TriggerType: "manual", Status: "running", RunAt: started, StartedAt: &started, CreatedAt: started,
	}
	if err := st.InsertTask(ctx, nil, task); err != nil {
		t.Fatal(err)
	}
	article := &model.Article{
		TaskID: task.ID, UserID: user.ID, PlanID: plan.ID, WxAccountID: account.ID,
		Title: "正在生成", Status: "generating", CreatedAt: started,
	}
	if err := st.InsertArticle(ctx, nil, article); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.ScanOnce(ctx, now); err != nil {
		t.Fatal(err)
	}
	fresh, err := st.GetTask(ctx, nil, task.ID)
	if err != nil || fresh == nil || fresh.Status != "failed" || !strings.Contains(fresh.FailureReason, "10 分钟") {
		t.Fatalf("%#v %v", fresh, err)
	}
	got, err := st.GetArticle(ctx, nil, article.ID)
	if err != nil || got == nil || got.Status != "failed" || !strings.Contains(got.FailureReason, "10 分钟") {
		t.Fatalf("%#v %v", got, err)
	}
}

func TestRecentProgressIsNotStuck(t *testing.T) {
	st, runner := openRunner(t)
	now := time.Now().UTC()
	user, account, plan := seed(t, st, "moving@example.com", now.Add(48*time.Hour))
	ctx := context.Background()
	started := now.Add(-12 * time.Minute)
	task := &model.ArticleTask{
		PlanID: plan.ID, UserID: user.ID, WxAccountID: account.ID, IdempotencyKey: "moving-task",
		TriggerType: "manual", Status: "running", RunAt: started, StartedAt: &started, CreatedAt: started,
	}
	if err := st.InsertTask(ctx, nil, task); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchProgress(ctx, nil, task.ID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	article := &model.Article{
		TaskID: task.ID, UserID: user.ID, PlanID: plan.ID, WxAccountID: account.ID,
		Title: "正在生成", Status: "generating", CreatedAt: started,
	}
	if err := st.InsertArticle(ctx, nil, article); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.ScanOnce(ctx, now); err != nil {
		t.Fatal(err)
	}
	fresh, err := st.GetTask(ctx, nil, task.ID)
	if err != nil || fresh == nil || fresh.Status != "running" {
		t.Fatalf("%#v %v", fresh, err)
	}
}

func TestStuckDraftIsHeld(t *testing.T) {
	st, runner := openRunner(t)
	now := time.Now().UTC()
	user, account, plan := seed(t, st, "held@example.com", now.Add(48*time.Hour))
	ctx := context.Background()
	started := now.Add(-12 * time.Minute)
	task := &model.ArticleTask{
		PlanID: plan.ID, UserID: user.ID, WxAccountID: account.ID, IdempotencyKey: "held-task",
		TriggerType: "manual", Status: "running", RunAt: started, StartedAt: &started, CreatedAt: started,
	}
	if err := st.InsertTask(ctx, nil, task); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchProgress(ctx, nil, task.ID, now.Add(-11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	article := &model.Article{
		TaskID: task.ID, UserID: user.ID, PlanID: plan.ID, WxAccountID: account.ID,
		Title: "已经写好", Summary: "摘要", BodyHTML: "<p>已经写好的正文</p>", Status: "generating", CreatedAt: started,
		BodyJSON: map[string]any{"title": "已经写好", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"已经写好的正文"}}}},
	}
	if err := st.InsertArticle(ctx, nil, article); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.ScanOnce(ctx, now); err != nil {
		t.Fatal(err)
	}
	fresh, err := st.GetTask(ctx, nil, task.ID)
	if err != nil || fresh == nil || fresh.Status != "success" || !strings.Contains(fresh.FailureReason, "10 分钟") {
		t.Fatalf("%#v %v", fresh, err)
	}
	got, err := st.GetArticle(ctx, nil, article.ID)
	if err != nil || got == nil || got.Status != "held" || !strings.Contains(got.BodyHTML, "已经写好的正文") || !strings.Contains(got.DeliveryNote, "待核对") {
		t.Fatalf("%#v %v", got, err)
	}
}

func TestHardCapHoldsEvenWithProgress(t *testing.T) {
	st, runner := openRunner(t)
	now := time.Now().UTC()
	user, account, plan := seed(t, st, "hardcap@example.com", now.Add(48*time.Hour))
	ctx := context.Background()
	started := now.Add(-26 * time.Minute)
	task := &model.ArticleTask{
		PlanID: plan.ID, UserID: user.ID, WxAccountID: account.ID, IdempotencyKey: "hard-task",
		TriggerType: "manual", Status: "running", RunAt: started, StartedAt: &started, CreatedAt: started,
	}
	if err := st.InsertTask(ctx, nil, task); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchProgress(ctx, nil, task.ID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	article := &model.Article{
		TaskID: task.ID, UserID: user.ID, PlanID: plan.ID, WxAccountID: account.ID,
		Title: "超时稿", BodyHTML: "<p>超时前的正文</p>", Status: "generating", CreatedAt: started,
	}
	if err := st.InsertArticle(ctx, nil, article); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.ScanOnce(ctx, now); err != nil {
		t.Fatal(err)
	}
	fresh, err := st.GetTask(ctx, nil, task.ID)
	if err != nil || fresh == nil || fresh.Status != "success" || !strings.Contains(fresh.FailureReason, "25 分钟") {
		t.Fatalf("%#v %v", fresh, err)
	}
	got, err := st.GetArticle(ctx, nil, article.ID)
	if err != nil || got == nil || got.Status != "held" || !strings.Contains(got.BodyHTML, "超时前的正文") {
		t.Fatalf("%#v %v", got, err)
	}
}

func TestQuotaExhaustionPausesPlan(t *testing.T) {
	st, runner := openRunner(t)
	now := time.Now().UTC()
	user, _, plan := seed(t, st, "paused@example.com", now.Add(-time.Minute))
	ctx := context.Background()
	if err := quota.Reserve(ctx, st, nil, user, 1, now, false, ""); err != nil {
		t.Fatal(err)
	}
	claimed, err := runner.ScanOnce(ctx, now)
	if err != nil || len(claimed) != 0 {
		t.Fatal(claimed, err)
	}
	fresh, err := st.GetPlan(ctx, nil, plan.ID)
	if err != nil || fresh.Status != "paused" || fresh.PauseReason != "额度已用完" {
		t.Fatalf("%#v %v", fresh, err)
	}
}

func seed(t *testing.T, st *store.Store, email string, next time.Time) (*model.User, *model.WxAccount, *model.WritingPlan) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	user := &model.User{Email: email, PasswordHash: "x", Tier: "free", IsActive: true, CreatedAt: now}
	if err := st.InsertUser(ctx, nil, user); err != nil {
		t.Fatal(err)
	}
	secret, err := security.EncryptSecret("test-master-key", "mock-secret")
	if err != nil {
		t.Fatal(err)
	}
	bound := now
	account := &model.WxAccount{UserID: user.ID, Name: "测试号", AppID: "wx_mock_" + email, SecretEncrypted: secret, DeliveryMode: "draft", BindStatus: "active", BoundAt: &bound, SubjectType: "personal", ServiceType: "subscription"}
	if err := st.InsertAccount(ctx, nil, account); err != nil {
		t.Fatal(err)
	}
	plan := &model.WritingPlan{UserID: user.ID, WxAccountID: account.ID, Direction: "职场成长", Mode: "managed", StyleKey: "warm", TemplateKey: "fresh", WordTarget: 800, ScheduleType: "daily", TimeWindow: "night", Status: "active", NextRunAt: &next, CreatedAt: now, Weekdays: []int{}}
	if err := st.InsertPlan(ctx, nil, plan); err != nil {
		t.Fatal(err)
	}
	return user, account, plan
}

func openRunner(t *testing.T) (*store.Store, *Runner) {
	t.Helper()
	database, err := db.Open("sqlite://"+filepath.Join(t.TempDir(), "t.db"), 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	st := store.New(database)
	cfg := config.Settings{MasterKey: "test-master-key", LLMMode: "mock", WeChatMode: "mock", WeChatAPIBase: "https://api.weixin.qq.com", StaleTaskSeconds: 900, MaxConcurrency: 4}
	deps := pipeline.Deps{Store: st, Settings: cfg, Gateway: llm.NewGateway(true, nil), HTTP: &http.Client{}, Tokens: wechat.NewTokenCache(), Cache: runtime.New(st, cfg)}
	return st, &Runner{Deps: deps, Pool: NewPool(4)}
}
