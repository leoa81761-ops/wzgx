package publish

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/store"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
)

func TestAllowOnlyLowRiskVerifiedPaid(t *testing.T) {
	base := Gate{Enabled: true, Tier: "standard", SubjectType: "enterprise", Verified: true, DeliveryMode: "draft", BindStatus: "active", Risk: "low", ArticleStatus: "in_draft"}
	if ok, _ := Allow(base); !ok {
		t.Fatal("低风险认证号应该可以发布")
	}
	off := base
	off.Enabled = false
	if ok, reason := Allow(off); ok || reason != "" {
		t.Fatal(ok, reason)
	}
	for _, risk := range []string{"medium", "high"} {
		g := base
		g.Risk = risk
		ok, reason := Allow(g)
		if ok || !strings.Contains(reason, "低风险") || !strings.Contains(reason, "草稿箱") {
			t.Fatal(risk, ok, reason)
		}
	}
	personal := base
	personal.SubjectType = "personal"
	personal.Verified = false
	if ok, reason := Allow(personal); ok || !strings.Contains(reason, "认证") {
		t.Fatal(ok, reason)
	}
	free := base
	free.Tier = "free"
	if ok, reason := Allow(free); ok || !strings.Contains(reason, "档位") {
		t.Fatal(ok, reason)
	}
	blocked := base
	blocked.ProbeBlocked = true
	if ok, _ := Allow(blocked); ok {
		t.Fatal("探测失败不能发布")
	}
}

func TestTryArticlePublishesOnceAndSkipsMissingDraft(t *testing.T) {
	wechat.ResetMock()
	deps, user, account := seedPublish(t, "wx_mock_pub_once")
	ctx := context.Background()
	client := wechat.Build(account.AppID, "secret", "mock", "", nil, nil)
	media, err := client.AddDraft(ctx, wechat.DraftArticle{Title: "标题", Content: "<p>正文</p>", ThumbMediaID: "thumb"})
	if err != nil {
		t.Fatal(err)
	}
	article := insertReadyArticle(t, deps, user, account, media, "low")
	TryArticle(ctx, deps, article.ID)
	got, err := deps.Store.GetArticle(ctx, nil, article.ID)
	if err != nil || got == nil || got.Status != "published" || !strings.Contains(got.DeliveryNote, "mp.weixin.qq.com") {
		t.Fatalf("%#v %v", got, err)
	}
	if wechat.MockPublishCount(account.AppID) != 1 {
		t.Fatal(wechat.MockPublishCount(account.AppID))
	}
	TryArticle(ctx, deps, article.ID)
	if wechat.MockPublishCount(account.AppID) != 1 {
		t.Fatal("重复调用又提交了一次")
	}
	again, _ := deps.Store.GetArticle(ctx, nil, article.ID)
	if again.Status != "published" {
		t.Fatal(again.Status)
	}

	missing := insertReadyArticle(t, deps, user, account, "mock_draft_gone", "low")
	TryArticle(ctx, deps, missing.ID)
	missed, _ := deps.Store.GetArticle(ctx, nil, missing.ID)
	if missed.Status != "in_draft" || !strings.Contains(missed.DeliveryNote, "草稿已不在") {
		t.Fatal(missed.Status, missed.DeliveryNote)
	}
	if wechat.MockPublishCount(account.AppID) != 1 {
		t.Fatal("缺草稿不应该提交")
	}

	medium := insertReadyArticle(t, deps, user, account, media, "medium")
	TryArticle(ctx, deps, medium.ID)
	held, _ := deps.Store.GetArticle(ctx, nil, medium.ID)
	if held.Status != "in_draft" || !strings.Contains(held.DeliveryNote, "低风险") {
		t.Fatal(held.Status, held.DeliveryNote)
	}
}

func seedPublish(t *testing.T, appID string) (Deps, *model.User, *model.WxAccount) {
	t.Helper()
	database, err := db.Open("sqlite://"+filepath.Join(t.TempDir(), "t.db"), 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	st := store.New(database)
	cfg := config.Settings{MasterKey: "test-master-key", WeChatMode: "mock", PublishDailyCap: 20}
	exp := time.Now().UTC().Add(48 * time.Hour)
	user := &model.User{Email: appID + "@example.com", PasswordHash: "x", Tier: "standard", TierExpiresAt: &exp, IsActive: true, CreatedAt: time.Now().UTC()}
	if err := st.InsertUser(context.Background(), nil, user); err != nil {
		t.Fatal(err)
	}
	enc, err := security.EncryptSecret(cfg.MasterKey, "secret")
	if err != nil {
		t.Fatal(err)
	}
	account := &model.WxAccount{
		UserID: user.ID, Name: "企", AppID: appID, SecretEncrypted: enc, SubjectType: "enterprise", ServiceType: "subscription",
		Verified: true, DeliveryMode: "draft", BindStatus: "active", PermissionReport: map[string]any{"can_publish": true},
	}
	if err := st.InsertAccount(context.Background(), nil, account); err != nil {
		t.Fatal(err)
	}
	plan := &model.WritingPlan{UserID: user.ID, WxAccountID: account.ID, Direction: "职场成长", Mode: "auto", StyleKey: "warm", TemplateKey: "fresh", WordTarget: 800, ScheduleType: "daily", TimeWindow: "night", Status: "active", CreatedAt: time.Now().UTC()}
	if err := st.InsertPlan(context.Background(), nil, plan); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePlanPublish(context.Background(), nil, store.PlanPublish{PlanID: plan.ID, Enabled: true, DelayMinutes: 0, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	return Deps{Store: st, Settings: cfg}, user, account
}

func insertReadyArticle(t *testing.T, deps Deps, user *model.User, account *model.WxAccount, media, risk string) *model.Article {
	t.Helper()
	plans, err := deps.Store.ListPlansByUser(context.Background(), user.ID)
	if err != nil || len(plans) == 0 {
		t.Fatal(err, len(plans))
	}
	article := &model.Article{
		UserID: user.ID, PlanID: plans[0].ID, WxAccountID: account.ID, Title: "标题", DraftMediaID: media,
		RiskLevel: risk, Status: "publish_wait", CreatedAt: time.Now().UTC(),
	}
	if err := deps.Store.InsertArticle(context.Background(), nil, article); err != nil {
		t.Fatal(err)
	}
	row := store.ArticlePublish{ArticleID: article.ID, Status: "publish_wait", ScheduledAt: time.Now().UTC().Add(-time.Minute), Note: "等待"}
	if err := deps.Store.SaveArticlePublish(context.Background(), nil, row); err != nil {
		t.Fatal(err)
	}
	return article
}
