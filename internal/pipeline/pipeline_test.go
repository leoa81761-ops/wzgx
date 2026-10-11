package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/jobs"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/runtime"
	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/store"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
)

func TestPipelineReachesDraftAndRotatesTopics(t *testing.T) {
	st, deps := openDeps(t, "mock", nil)
	ctx := context.Background()
	user, account, plan := seedPlan(t, st, "writer@example.com")
	yesterday := time.Now().UTC().Add(-24 * time.Hour)
	first := runTask(t, st, deps, user, account, plan, yesterday)
	second := runTask(t, st, deps, user, account, plan, time.Now().UTC())
	if first == second {
		t.Fatal("topics should rotate")
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 10)
	if err != nil || len(articles) != 2 {
		t.Fatal(len(articles), err)
	}
	older := articles[1]
	if older.Status != "in_draft" || !strings.HasPrefix(older.DraftMediaID, "mock_draft_") {
		t.Fatalf("%s %s", older.Status, older.DraftMediaID)
	}
	if !strings.Contains(older.BodyHTML, "style=") || !strings.Contains(older.BodyHTML, "温暖亲和") || !strings.Contains(older.BodyHTML, "#1f6a4d") {
		t.Fatal(older.BodyHTML)
	}
	coverage, err := st.ListCoverage(ctx, nil, plan.ID)
	if err != nil || len(coverage) != 2 || coverage[0].TopicTag == coverage[1].TopicTag {
		t.Fatal(len(coverage), err)
	}
}

func TestSensitiveFailureReleasesQuota(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		stage := stageOf(req)
		var data map[string]any
		switch stage {
		case "选题":
			data = map[string]any{"title": "一个正常选题"}
		case "大纲":
			data = map[string]any{"points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"}
		default:
			data = map[string]any{"title": "震惊内幕", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"稳赚不赔"}, "list": []any{}}}}
		}
		body, _ := json.Marshal(data)
		out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(body)}}}})
		_, _ = w.Write(out)
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "坏模型", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "bad",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: time.Now().UTC(), ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	user, account, plan := seedPlan(t, st, "bad@example.com")
	now := time.Now().UTC()
	var taskID int64
	if err := st.DB.WithTx(ctx, func(tx *db.Tx) error {
		task, err := jobs.Create(ctx, st, tx, user, plan, account, "manual", now, nil)
		if err != nil {
			return err
		}
		taskID = task.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	Execute(ctx, deps, taskID, false, now)
	task, err := st.GetTask(ctx, nil, taskID)
	if err != nil || task == nil || task.Status != "failed" || !strings.Contains(task.FailureReason, "敏感词") {
		t.Fatalf("%#v %v", task, err)
	}
	reason, err := quota.BlockReason(ctx, st, nil, user, now)
	if err != nil || reason != "" {
		t.Fatalf("quota should be released, got %q %v", reason, err)
	}
}

func TestDurationMeasuresTheWholeTask(t *testing.T) {
	st, deps := openDeps(t, "mock", nil)
	user, account, plan := seedPlan(t, st, "slow@example.com")
	past := time.Now().UTC().Add(-2 * time.Minute)
	ctx := context.Background()
	var taskID int64
	if err := st.DB.WithTx(ctx, func(tx *db.Tx) error {
		task, err := jobs.Create(ctx, st, tx, user, plan, account, "manual", past, nil)
		if err != nil {
			return err
		}
		taskID = task.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	Execute(ctx, deps, taskID, false, past)
	task, err := st.GetTask(ctx, nil, taskID)
	if err != nil || task == nil || task.Status != "success" {
		t.Fatalf("%#v %v", task, err)
	}
	if task.DurationMs < 60_000 {
		t.Fatalf("duration_ms = %d, want the whole run", task.DurationMs)
	}
}

func TestResearchMarkdownBecomesSources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		var data map[string]any
		switch stageOf(req) {
		case "选题":
			data = map[string]any{"title": "远程协作的一件小事", "angle": "今天就能做"}
		case "资料检索":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"参见 [人社部](https://www.example.com/notice)。"}}]}`))
			return
		case "大纲":
			data = map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"}
		default:
			data = map[string]any{"title": "远程协作的一件小事", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"先做最小的一步。"}, "list": []any{}}}, "closing": "结束", "sources": []any{}}
		}
		body, _ := json.Marshal(data)
		out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(body)}}}})
		_, _ = w.Write(out)
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	user, account, plan := seedPlan(t, st, "search@example.com")
	user.Tier = "standard"
	if err := st.UpdateUser(ctx, nil, user); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "检索", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "grok-chat-fast",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: time.Now().UTC(), ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	models, err := st.ListTextModels(ctx, nil, true)
	if err != nil || len(models) != 1 {
		t.Fatal(len(models), err)
	}
	if err := st.ReplaceRoutes(ctx, nil, "research", "*", []int64{models[0].ID}); err != nil {
		t.Fatal(err)
	}
	tag := runTask(t, st, deps, user, account, plan, time.Now().UTC())
	if tag == "" {
		t.Fatal("empty tag")
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 || !strings.Contains(articles[0].BodyHTML, "参考来源") || !strings.Contains(articles[0].BodyHTML, "example点com") {
		html := ""
		if len(articles) > 0 {
			html = articles[0].BodyHTML
		}
		t.Fatal(html, err)
	}
}

func stageOf(req map[string]any) string {
	msgs, _ := req["messages"].([]any)
	if len(msgs) == 0 {
		return ""
	}
	last, _ := msgs[len(msgs)-1].(map[string]any)
	content, _ := last["content"].(string)
	var payload map[string]any
	_ = json.Unmarshal([]byte(content), &payload)
	stage, _ := payload["环节"].(string)
	return stage
}

func runTask(t *testing.T, st *store.Store, deps Deps, user *model.User, account *model.WxAccount, plan *model.WritingPlan, now time.Time) string {
	t.Helper()
	ctx := context.Background()
	var taskID int64
	if err := st.DB.WithTx(ctx, func(tx *db.Tx) error {
		task, err := jobs.Create(ctx, st, tx, user, plan, account, "manual", now, nil)
		if err != nil {
			return err
		}
		taskID = task.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	Execute(ctx, deps, taskID, false, now)
	article, err := st.GetArticleByTask(ctx, nil, taskID)
	if err != nil || article == nil || article.Status != "in_draft" {
		status := ""
		if article != nil {
			status = article.Status + " " + article.FailureReason
		}
		task, _ := st.GetTask(ctx, nil, taskID)
		reason := ""
		if task != nil {
			reason = task.FailureReason
		}
		t.Fatalf("article %s task %s err %v", status, reason, err)
	}
	return article.TopicTag
}

func seedPlan(t *testing.T, st *store.Store, email string) (*model.User, *model.WxAccount, *model.WritingPlan) {
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
	account := &model.WxAccount{UserID: user.ID, Name: "测试号", AppID: "wx_mock_" + email, SecretEncrypted: secret, SubjectType: "personal", ServiceType: "subscription", DeliveryMode: "draft", BindStatus: "active", BoundAt: &bound}
	if err := st.InsertAccount(ctx, nil, account); err != nil {
		t.Fatal(err)
	}
	next := now.Add(-time.Minute)
	plan := &model.WritingPlan{UserID: user.ID, WxAccountID: account.ID, Direction: "职场成长", Mode: "managed", StyleKey: "warm", TemplateKey: "fresh", WordTarget: 800, ScheduleType: "daily", TimeWindow: "night", Status: "active", NextRunAt: &next, CreatedAt: now, Weekdays: []int{}}
	if err := st.InsertPlan(ctx, nil, plan); err != nil {
		t.Fatal(err)
	}
	return user, account, plan
}

func openDeps(t *testing.T, llmMode string, client *http.Client) (*store.Store, Deps) {
	t.Helper()
	database, err := db.Open("sqlite://"+filepath.Join(t.TempDir(), "t.db"), 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	st := store.New(database)
	cfg := config.Settings{MasterKey: "test-master-key", LLMMode: llmMode, WeChatMode: "mock", WeChatAPIBase: "https://api.weixin.qq.com", StaleTaskSeconds: 900}
	if client == nil {
		client = http.DefaultClient
	}
	return st, Deps{Store: st, Settings: cfg, Gateway: llm.NewGateway(llmMode == "mock", client), HTTP: client, Tokens: wechat.NewTokenCache(), Cache: runtime.New(st, cfg)}
}
