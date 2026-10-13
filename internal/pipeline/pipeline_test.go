package pipeline

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/columns"
	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/content"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/jobs"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/quality"
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
	if len(task.ModelTrace) == 0 {
		t.Fatal("failed task should keep model_trace")
	}
	if traceStage(task.ModelTrace, "topic")["ok"] != true {
		t.Fatalf("%#v", task.ModelTrace)
	}
	notes, _, err := st.ListNotifications(ctx, user.ID, 20)
	if err != nil || len(notes) == 0 || notes[0].Kind != "failure" {
		t.Fatalf("failure should notify, %#v %v", notes, err)
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
	var researchCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		var data map[string]any
		switch stageOf(req) {
		case "选题":
			data = map[string]any{"title": "远程协作的一件小事", "angle": "今天就能做"}
		case "资料检索":
			researchCalls++
			if req["stream"] != true {
				t.Errorf("research stream %#v", req["stream"])
			}
			if _, ok := req["response_format"]; ok {
				t.Errorf("research should not force json_schema: %#v", req["response_format"])
			}
			msgs, _ := req["messages"].([]any)
			if len(msgs) > 0 {
				first, _ := msgs[0].(map[string]any)
				system, _ := first["content"].(string)
				if !strings.Contains(system, "搜索") || strings.Contains(system, "空数组") {
					t.Errorf("research prompt %s", system)
				}
			}
			writeCited(w, "参见 [编造](https://made-up.invalid/gb)。", "https://www.example.com/notice")
			return
		case "来源判断":
			writeSourceJudge(w, req)
			return
		case "大纲":
			data = map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"}
		default:
			data = map[string]any{"title": "远程协作的一件小事", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{strings.Repeat("合适的正文。", 120) + "职场成长说明写了这一步。[1]"}, "list": []any{}}}, "closing": "结束", "sources": []any{}}
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
	if err != nil || len(articles) == 0 || !strings.Contains(articles[0].BodyHTML, "经验分享") || !strings.Contains(articles[0].BodyHTML, "www.example.com") || strings.Contains(articles[0].BodyHTML, "example点com") || strings.Contains(articles[0].BodyHTML, "made-up") {
		html := ""
		if len(articles) > 0 {
			html = articles[0].BodyHTML
		}
		t.Fatal(html, err)
	}
	if researchCalls != 1 {
		t.Fatalf("research calls %d", researchCalls)
	}
	task, err := st.GetTask(ctx, nil, articles[0].TaskID)
	if err != nil || task == nil {
		t.Fatal(err)
	}
	var sawResearch bool
	for _, row := range task.ModelTrace {
		if row["stage"] != "research" {
			continue
		}
		sawResearch = true
		if row["provider_name"] != "检索" || row["model"] != "grok-chat-fast" {
			t.Fatalf("%#v", row)
		}
	}
	if !sawResearch {
		t.Fatal(task.ModelTrace)
	}
}

func TestResearchRetriesWhenEmpty(t *testing.T) {
	var researchCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "远程协作的一件小事", "angle": "今天就能做"})
		case "资料检索":
			researchCalls++
			if researchCalls == 1 {
				writeChoice(w, map[string]any{"notes": "", "sources": []any{}})
				return
			}
			writeCited(w, "参见近期通知，正文不写网址。", "https://www.example.com/notice")
		case "来源判断":
			writeSourceJudge(w, req)
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		default:
			writeChoice(w, map[string]any{"title": "远程协作的一件小事", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{strings.Repeat("合适的正文。", 120) + "职场成长说明写了这一步。[1]"}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	user, account, plan := seedPaid(t, st, "retry@example.com", srv.URL)
	runTask(t, st, deps, user, account, plan, time.Now().UTC())
	if researchCalls != 2 {
		t.Fatalf("research calls %d", researchCalls)
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 || !strings.Contains(articles[0].BodyHTML, "经验分享") || !strings.Contains(articles[0].BodyHTML, "www.example.com") || strings.Contains(articles[0].BodyHTML, "example点com") {
		t.Fatal(articles, err)
	}
}

func TestDraftExpandsOnceWhenShort(t *testing.T) {
	var expandCalls int
	long := strings.Repeat("步骤写清楚。", 140)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "一个够长的题目", "angle": "今天就能做"})
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		case "扩写":
			expandCalls++
			writeChoice(w, map[string]any{"title": "一个够长的题目", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{long}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		case "审核":
			writeChoice(w, map[string]any{"title": "一个够长的题目", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{long}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		default:
			writeChoice(w, map[string]any{"title": "一个够长的题目", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"只有一句。"}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "writer",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: time.Now().UTC(), ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	user, account, plan := seedPlan(t, st, "short@example.com")
	plan.WordTarget = 800
	runTask(t, st, deps, user, account, plan, time.Now().UTC())
	if expandCalls != 3 {
		t.Fatalf("expand calls %d", expandCalls)
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 || !strings.Contains(articles[0].BodyHTML, "步骤写清楚。") {
		t.Fatal(articles, err)
	}
}

func TestResearchStopsAfterOneRetry(t *testing.T) {
	var researchCalls int
	body := strings.Repeat("合适的正文。", 120)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "检索失败也能写完", "angle": "今天就能做"})
		case "资料检索":
			researchCalls++
			w.WriteHeader(500)
			_, _ = w.Write([]byte("upstream"))
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		default:
			writeChoice(w, map[string]any{"title": "检索失败也能写完", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{body}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	user, account, plan := seedPaid(t, st, "research-524@example.com", srv.URL)
	if _, err := st.DB.Exec(ctx, `UPDATE service_config SET timeout_seconds = 600 WHERE config_type = 'text_model'`); err != nil {
		t.Fatal(err)
	}
	var taskID int64
	if err := st.DB.WithTx(ctx, func(tx *db.Tx) error {
		task, err := jobs.Create(ctx, st, tx, user, plan, account, "manual", time.Now().UTC(), nil)
		if err != nil {
			return err
		}
		taskID = task.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	Execute(ctx, deps, taskID, false, time.Now().UTC())
	if researchCalls != 2 {
		t.Fatalf("research calls %d", researchCalls)
	}
	article, err := st.GetArticleByTask(ctx, nil, taskID)
	if err != nil || article == nil || strings.Contains(article.BodyHTML, "参考来源") {
		t.Fatal(article, err)
	}
	task, err := st.GetTask(ctx, nil, taskID)
	if err != nil || task == nil || traceStage(task.ModelTrace, "research")["ok"] != false {
		t.Fatalf("%#v %v", task, err)
	}
	if article.Status != "in_draft" || article.DraftMediaID == "" || strings.Contains(article.DeliveryNote, "无参考来源") || strings.Contains(article.BodyHTML, "参考来源") || strings.Contains(article.BodyHTML, "[1]") {
		t.Fatalf("%s %s %s %s", article.Status, article.DraftMediaID, article.DeliveryNote, article.BodyHTML)
	}
	notes, _, err := st.ListNotifications(ctx, user.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var drafted bool
	for _, note := range notes {
		if note.Title == "有一篇稿没有写入草稿" {
			t.Fatalf("%#v", notes)
		}
		if note.Title == "有一篇稿已在草稿箱" {
			drafted = true
		}
	}
	if !drafted {
		t.Fatalf("%#v", notes)
	}
}

func TestBackupResearchRetriesChatFast(t *testing.T) {
	var researchCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		if strings.Contains(r.URL.Path, "/responses") {
			t.Errorf("backup should stay on chat completions: %s", r.URL.Path)
		}
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "补救检索成功", "angle": "今天就能做"})
		case "资料检索":
			researchCalls++
			if researchCalls == 1 && (req["stream"] != true || req["model"] != "grok-chat-fast") {
				t.Errorf("stream %#v model %#v", req["stream"], req["model"])
			}
			if researchCalls > 1 && req["model"] == "grok-chat-fast" {
				t.Errorf("backup should leave grok-chat-fast, got %#v", req["model"])
			}
			if _, ok := req["tools"]; ok {
				t.Errorf("research should not send web_search: %#v", req["tools"])
			}
			msgs, _ := req["messages"].([]any)
			blob, _ := json.Marshal(msgs)
			if researchCalls == 1 {
				if strings.Contains(string(blob), "这是补救检索") {
					t.Error("first pass should not be the short query")
				}
				writeCited(w, "参见 [编造](https://www.mohurd.gov.cn/fake)。")
				return
			}
			if !strings.Contains(string(blob), "这是补救检索") {
				t.Errorf("short prompt %s", blob)
			}
			writeCited(w, "住建部有公开说明。", "https://www.mohurd.gov.cn/gongkai/1")
		case "来源判断":
			writeSourceJudge(w, req)
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		default:
			writeChoice(w, map[string]any{"title": "补救检索成功", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{strings.Repeat("合适的正文。", 120) + "职场成长说明写了这一步。[1]"}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	user, account, plan := seedPaid(t, st, "backup@example.com", srv.URL)
	runTask(t, st, deps, user, account, plan, time.Now().UTC())
	if researchCalls != 2 {
		t.Fatalf("research calls %d", researchCalls)
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 {
		t.Fatal(err)
	}
	if !strings.Contains(articles[0].BodyHTML, "www.mohurd.gov.cn") || strings.Contains(articles[0].BodyHTML, "mohurd点gov点cn") || strings.Contains(articles[0].BodyHTML, "fake") || strings.Contains(articles[0].DeliveryNote, "无参考来源") {
		t.Fatal(articles[0].BodyHTML, articles[0].DeliveryNote)
	}
	notes, _, err := st.ListNotifications(ctx, user.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range notes {
		if note.Title == "无参考来源" {
			t.Fatalf("%#v", notes)
		}
	}
}

func TestExpandAsksToCondenseInsteadOfCutting(t *testing.T) {
	long := strings.Repeat("步骤写清楚。", 400)
	short := strings.Repeat("压短后仍是完整的句子。", 80)
	var sawGoal bool
	var condense int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		body := long
		switch stageOf(req) {
		case "扩写":
			msgs, _ := req["messages"].([]any)
			if len(msgs) > 0 {
				last, _ := msgs[len(msgs)-1].(map[string]any)
				content, _ := last["content"].(string)
				if strings.Contains(content, `"expand_goal":840`) || strings.Contains(content, `"expand_goal": 840`) {
					sawGoal = true
				}
			}
		case "缩写":
			condense++
			body = short
		default:
			t.Errorf("stage %s", stageOf(req))
		}
		writeChoice(w, map[string]any{
			"title": "短稿", "summary": "摘要",
			"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{body}, "list": []any{}}},
			"closing":  "结束", "sources": []any{},
		})
	}))
	defer srv.Close()
	gw := llm.NewGateway(false, srv.Client())
	draft := map[string]any{
		"title": "短稿", "summary": "摘要",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"只有一句。"}, "list": []any{}}},
		"closing":  "结束", "sources": []any{},
	}
	out, _, trace, stillShort := fitBody(context.Background(), gw, []llm.Provider{{
		Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 10 * time.Second,
	}}, map[string]any{"word_target": 800}, draft, 800)
	if stillShort {
		t.Fatal("expanded draft should clear the short flag")
	}
	if !sawGoal || condense < 1 {
		t.Fatalf("goal %v condense %d", sawGoal, condense)
	}
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), "压短后仍是完整的句子。") || strings.Contains(string(raw), "步骤写清楚。") {
		t.Fatalf("runes %d trace %#v body %s", content.BodyRunes(out), trace, raw)
	}
}

func TestBackupResearchUsesADifferentModel(t *testing.T) {
	providers := []llm.Provider{
		{Name: "推理", Protocol: "openai", Model: "grok-4.7"},
		{Name: "快", Protocol: "openai", Model: "grok-chat-fast"},
	}
	out := backupResearchProviders(providers, "grok-chat-fast", "grok-chat-fast")
	if len(out) != 1 || out[0].Model != "grok-4.7" || out[0].Protocol != "openai" {
		t.Fatalf("%#v", out)
	}
	only := backupResearchProviders([]llm.Provider{{Name: "快", Protocol: "openai", Model: "grok-chat-fast"}}, "", "grok-chat-fast")
	if len(only) != 1 || only[0].Model == "grok-chat-fast" || only[0].Protocol != "openai" {
		t.Fatalf("%#v", only)
	}
	custom := backupResearchProviders([]llm.Provider{{Name: "快", Protocol: "openai", Model: "grok-chat-fast", BaseURL: "https://x/v1"}}, "grok-4.7", "grok-chat-fast")
	if len(custom) != 1 || custom[0].Protocol != "openai" || custom[0].Model != "grok-4.7" || custom[0].BaseURL != "https://x/v1" {
		t.Fatalf("%#v", custom)
	}
}

func TestResearchDropsAggregatorDomains(t *testing.T) {
	var researchCalls int
	body := strings.Repeat("合适的正文。", 120)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "只要官方来源", "angle": "今天就能做"})
		case "资料检索":
			researchCalls++
			msgs, _ := req["messages"].([]any)
			last, _ := msgs[len(msgs)-1].(map[string]any)
			content, _ := last["content"].(string)
			if !strings.Contains(content, "xilu.com") {
				t.Errorf("payload should list blocked domains: %s", content)
			}
			if researchCalls == 1 {
				writeCited(w, "转载站。", "https://www.xilu.com/news/1")
				return
			}
			writeCited(w, "官方页面。", "https://www.gov.cn/zhengce/2024")
		case "来源判断":
			writeSourceJudge(w, req)
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		default:
			writeChoice(w, map[string]any{
				"title": "只要官方来源", "summary": "摘要",
				"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{body + "职场成长说明写了这一步。[1]"}, "list": []any{}}},
				"closing":  "结束",
				"sources":  []any{map[string]any{"name": "西陆", "title": "转载", "url": "https://fyun.com/a", "source_type": "news"}},
			})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	user, account, plan := seedPaid(t, st, "domains@example.com", srv.URL)
	runTask(t, st, deps, user, account, plan, time.Now().UTC())
	if researchCalls != 2 {
		t.Fatalf("research calls %d", researchCalls)
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 {
		t.Fatal(err)
	}
	html := articles[0].BodyHTML
	if !strings.Contains(html, "经验分享") || !strings.Contains(html, "www.gov.cn") || strings.Contains(html, "gov点cn") || strings.Contains(html, "xilu") || strings.Contains(html, "fyun") {
		t.Fatal(html)
	}
}

func TestResearchReadsChatCitations(t *testing.T) {
	var researchCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "弱电入门", "angle": "先分清概念"})
		case "资料检索":
			researchCalls++
			if researchCalls == 1 {
				w.WriteHeader(500)
				_, _ = w.Write([]byte("upstream"))
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"检索到公开资料，正文不写网址。"}}],"citations":[{"url":"https://www.gov.cn/zhengce/2024/elv","title":"弱电入门公开说明","snippet":"弱电入门要先看公开说明"},{"url":"https://openstd.samr.gov.cn/std/1","title":"弱电入门综合布线标准","snippet":"弱电入门的综合布线标准"}]}`))
		case "来源判断":
			writeSourceJudge(w, req)
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		default:
			writeChoice(w, map[string]any{"title": "弱电入门", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{strings.Repeat("合适的正文。", 120) + "弱电入门公开说明和综合布线标准都写了这一步。[1][2]"}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	user, account, plan := seedPaid(t, st, "citations@example.com", srv.URL)
	runTask(t, st, deps, user, account, plan, time.Now().UTC())
	if researchCalls != 2 {
		t.Fatalf("research calls %d", researchCalls)
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 {
		t.Fatal(err)
	}
	html := articles[0].BodyHTML
	if !strings.Contains(html, "参考来源") || !strings.Contains(html, "www.gov.cn") || !strings.Contains(html, "openstd.samr.gov.cn") || strings.Contains(html, "点gov") {
		t.Fatal(html)
	}
}

func TestResearchRelaxesAllowlist(t *testing.T) {
	var researchCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "允许名单外的来源", "angle": "今天就能做"})
		case "资料检索":
			researchCalls++
			writeCited(w, "参见 [媒体](https://news.example.com/a)。", "https://news.example.com/a")
		case "来源判断":
			writeSourceJudge(w, req)
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		default:
			writeChoice(w, map[string]any{"title": "允许名单外的来源", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{strings.Repeat("合适的正文。", 120) + "职场成长说明写了这一步。[1]"}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	user, account, plan := seedPaid(t, st, "relax@example.com", srv.URL)
	saveSourceDomains(t, st, []any{"xilu.com", "fyun.com", "huanancj.com"}, []any{"gov.cn"})
	runTask(t, st, deps, user, account, plan, time.Now().UTC())
	if researchCalls != 1 {
		t.Fatalf("research calls %d", researchCalls)
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 {
		t.Fatal(err)
	}
	if !strings.Contains(articles[0].BodyHTML, "经验分享") || !strings.Contains(articles[0].BodyHTML, "news.example.com") || strings.Contains(articles[0].BodyHTML, "example点com") {
		t.Fatal(articles[0].BodyHTML)
	}
	task, err := st.GetTask(ctx, nil, articles[0].TaskID)
	if err != nil || task == nil || !traceHasDrop(task.ModelTrace, "不在允许名单") || !traceRelaxed(task.ModelTrace) {
		t.Fatalf("%#v %v", task, err)
	}
}

func TestResearchRetryDropsBlocklistThenRelaxes(t *testing.T) {
	var researchCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "屏蔽后再放宽", "angle": "今天就能做"})
		case "资料检索":
			researchCalls++
			if researchCalls == 1 {
				writeCited(w, "转载。", "https://www.xilu.com/news/1")
				return
			}
			writeCited(w, "媒体。", "https://news.example.com/a")
		case "来源判断":
			writeSourceJudge(w, req)
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		default:
			writeChoice(w, map[string]any{"title": "屏蔽后再放宽", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{strings.Repeat("合适的正文。", 120) + "职场成长说明写了这一步。[1]"}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	user, account, plan := seedPaid(t, st, "relax-retry@example.com", srv.URL)
	saveSourceDomains(t, st, []any{"xilu.com", "fyun.com", "huanancj.com"}, []any{"gov.cn"})
	runTask(t, st, deps, user, account, plan, time.Now().UTC())
	if researchCalls != 2 {
		t.Fatalf("research calls %d", researchCalls)
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 {
		t.Fatal(err)
	}
	html := articles[0].BodyHTML
	if !strings.Contains(html, "经验分享") || !strings.Contains(html, "news.example.com") || strings.Contains(html, "example点com") || strings.Contains(html, "xilu") {
		t.Fatal(html)
	}
	task, err := st.GetTask(ctx, nil, articles[0].TaskID)
	if err != nil || task == nil || !traceHasDrop(task.ModelTrace, "屏蔽域名") || !traceHasDrop(task.ModelTrace, "不在允许名单") || !traceRelaxed(task.ModelTrace) {
		t.Fatalf("%#v %v", task, err)
	}
}

func TestFactCheckExpansionIsTrimmed(t *testing.T) {
	inBand := strings.Repeat("合适的正文。", 130)
	cell := strings.Repeat("表", 40)
	var rows []any
	for i := 0; i < 8; i++ {
		rows = append(rows, []any{cell, cell})
	}
	longDraft := map[string]any{
		"title": "核对后变长", "summary": "摘要",
		"sections": []any{map[string]any{
			"heading": "甲", "paragraphs": []any{inBand}, "list": []any{},
			"table": map[string]any{"headers": []any{"项目", "说明"}, "rows": rows},
		}},
		"closing": "结束", "sources": []any{},
	}
	var sawFact bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "核对后变长", "angle": "今天就能做"})
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		case "扩写":
			writeChoice(w, map[string]any{"title": "核对后变长", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{inBand}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		case "缩写":
			writeChoice(w, map[string]any{
				"title": "核对后变长", "summary": "摘要",
				"sections": []any{map[string]any{
					"heading": "甲", "paragraphs": []any{inBand}, "list": []any{},
					"table": map[string]any{"headers": []any{"项目", "说明"}, "rows": []any{[]any{cell, cell}}},
				}},
				"closing": "结束", "sources": []any{},
			})
		case "事实核对":
			sawFact = true
			writeChoice(w, longDraft)
		case "审核":
			writeChoice(w, longDraft)
		default:
			writeChoice(w, map[string]any{"title": "核对后变长", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{inBand}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "writer",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: time.Now().UTC(), ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	user, account, plan := seedPlan(t, st, "refit@example.com")
	plan.WordTarget = 800
	runTask(t, st, deps, user, account, plan, time.Now().UTC())
	if !sawFact {
		t.Fatal("fact check was not called")
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 {
		t.Fatal(err)
	}
	high := 800 * 130 / 100
	if n := content.BodyRunes(articles[0].BodyJSON); n > high || n < 800*85/100 {
		t.Fatalf("body runes %d", n)
	}
	if strings.Count(articles[0].BodyHTML, cell) >= 16 || !strings.Contains(articles[0].BodyHTML, "合适的正文。") {
		t.Fatal(articles[0].BodyHTML)
	}
}

func TestDraftCondensesWhenLong(t *testing.T) {
	var expandCalls, condenseCalls int
	long := strings.Repeat("这是一段很长的说明文字。", 200)
	short := strings.Repeat("压短后的说明。", 120)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "写得太长了", "angle": "今天就能做"})
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		case "扩写":
			expandCalls++
			writeChoice(w, map[string]any{"title": "写得太长了", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{long}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		case "缩写":
			condenseCalls++
			writeChoice(w, map[string]any{"title": "写得太长了", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{short}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		default:
			writeChoice(w, map[string]any{"title": "写得太长了", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{long}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "writer",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: time.Now().UTC(), ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	user, account, plan := seedPlan(t, st, "long@example.com")
	plan.WordTarget = 800
	runTask(t, st, deps, user, account, plan, time.Now().UTC())
	if expandCalls != 2 || condenseCalls < 1 {
		t.Fatalf("expand %d condense %d", expandCalls, condenseCalls)
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 {
		t.Fatal(err)
	}
	if !strings.Contains(articles[0].BodyHTML, short) || strings.Contains(articles[0].BodyHTML, "很长的说明文字") {
		t.Fatal(articles[0].BodyHTML)
	}
}

func TestDraftSkipsRewriteWhenInBand(t *testing.T) {
	var expandCalls, condenseCalls int
	text := strings.Repeat("合适的正文。", 147)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "长度刚好", "angle": "今天就能做"})
			return
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
			return
		case "扩写":
			expandCalls++
		case "缩写":
			condenseCalls++
		}
		writeChoice(w, map[string]any{"title": "长度刚好", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{text}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "writer",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: time.Now().UTC(), ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	user, account, plan := seedPlan(t, st, "band@example.com")
	plan.WordTarget = 800
	runTask(t, st, deps, user, account, plan, time.Now().UTC())
	if expandCalls != 0 || condenseCalls != 0 {
		t.Fatalf("expand %d condense %d", expandCalls, condenseCalls)
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 {
		t.Fatal(err)
	}
	if n := content.BodyRunes(articles[0].BodyJSON); n < 800*85/100 || n > 800*130/100 {
		t.Fatalf("body runes %d", n)
	}
}

func TestSensitiveWordFromFinishPieceFailsTheDraft(t *testing.T) {
	text := strings.Repeat("合适的正文。", 120)
	clean := map[string]any{"title": "长度刚好", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{text}, "list": []any{}}}, "closing": "结束", "sources": []any{}}
	var reviews int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "长度刚好", "angle": "今天就能做"})
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		case "结构润色":
			writeChoice(w, map[string]any{"hook": "为什么这里出现稳赚不赔？", "headings": []any{"先看问题", "一个做法"}, "closing": "可以先记住。欢迎留言。稳赚不赔。"})
		case "审核":
			reviews++
			if reviews >= 2 {
				writeChoice(w, map[string]any{"title": "长度刚好", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"这里留下了稳赚不赔。"}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
				return
			}
			writeChoice(w, clean)
		default:
			writeChoice(w, clean)
		}
	}))
	defer srv.Close()
	gateway := llm.NewGateway(false, srv.Client())
	provider := llm.Provider{Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 10 * time.Second}
	providers := map[string][]llm.Provider{"topic": {provider}, "outline": {provider}, "draft": {provider}, "review": {provider}}
	_, err := Build(context.Background(), gateway, Input{
		Direction: "职场成长", StyleKey: "warm", TemplateKey: "fresh", WordTarget: 800, Tier: "free",
	}, []string{"稳赚不赔", "内部消息"}, providers, content.DefaultSourcePolicy(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "审核未通过") || !strings.Contains(err.Error(), "稳赚不赔") {
		t.Fatalf("reviews %d err %v", reviews, err)
	}
	if reviews < 2 {
		t.Fatalf("reviews %d", reviews)
	}
}

func TestCondenseKeepsSourcesAndRejectsNewClaims(t *testing.T) {
	long := strings.Repeat("原文说明引用这里[1]。", 40)
	original := func() map[string]any {
		return map[string]any{
			"title":    "验收记录",
			"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{long}, "list": []any{}}},
			"closing":  "结束。",
			"sources":  []any{map[string]any{"name": "官网", "title": "原文", "url": "https://www.gov.cn/a"}},
		}
	}
	short := map[string]any{
		"title":    "验收记录",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"压短后的说明仍见[1]。"}, "list": []any{}}},
		"closing":  "结束。",
		"sources":  []any{map[string]any{"name": "坏站", "title": "替换", "url": "https://evil.example/a"}},
	}
	changed := map[string]any{
		"title":    "验收记录",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"压短后出现了99这个新数字。"}, "list": []any{}}},
		"closing":  "结束。",
		"sources":  []any{},
	}
	var mode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "reject" {
			writeChoice(w, changed)
			return
		}
		writeChoice(w, short)
	}))
	defer srv.Close()
	gateway := llm.NewGateway(false, srv.Client())
	providers := map[string][]llm.Provider{"review": {{Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 10 * time.Second}}}
	kept, _, _ := condenseOnce(context.Background(), gateway, providers, original(), map[string]any{})
	raw, _ := json.Marshal(kept)
	text := string(raw)
	if !strings.Contains(text, "压短后的说明仍见[1]。") || !strings.Contains(text, "https://www.gov.cn/a") || strings.Contains(text, "evil.example") {
		t.Fatal(text)
	}
	mode = "reject"
	rejected, _, trace := condenseOnce(context.Background(), gateway, providers, original(), map[string]any{})
	raw, _ = json.Marshal(rejected)
	text = string(raw)
	if !strings.Contains(text, long) || strings.Contains(text, "99") || !strings.Contains(text, "https://www.gov.cn/a") {
		t.Fatal(text)
	}
	if len(trace) == 0 || trace[0]["stage"] != "condense" || trace[0]["ok"] != true {
		t.Fatalf("%#v", trace)
	}
}

func TestDegradeSkipsRewriteAndTitles(t *testing.T) {
	long := strings.Repeat("这是一段很长的说明文字。", 200)
	short := strings.Repeat("压短后的说明。", 120)
	var polish, titles, condense int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "写得太长了", "angle": "今天就能做"})
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		case "结构润色":
			polish++
			writeChoice(w, map[string]any{"hook": "为什么先看这一步？", "headings": []any{"甲", "乙"}, "closing": "可以先记住。欢迎留言。"})
		case "标题":
			titles++
			writeChoice(w, map[string]any{"titles": []any{"这是一个足够长的备用标题用来占位"}})
		case "缩写":
			condense++
			writeChoice(w, map[string]any{"title": "写得太长了", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{short}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		default:
			writeChoice(w, map[string]any{"title": "写得太长了", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{long}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		}
	}))
	defer srv.Close()
	gateway := llm.NewGateway(false, srv.Client())
	provider := llm.Provider{Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 10 * time.Second}
	providers := map[string][]llm.Provider{"topic": {provider}, "outline": {provider}, "draft": {provider}, "review": {provider}}
	note := "本月生成成本已达上限，这篇没有使用联网检索和 AI 配图。"
	built, err := Build(context.Background(), gateway, Input{
		Direction: "职场成长", StyleKey: "warm", TemplateKey: "fresh", WordTarget: 800, Tier: "free", CostNote: note,
	}, nil, providers, content.DefaultSourcePolicy(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if polish != 0 || titles != 0 || condense < 1 {
		t.Fatalf("polish %d titles %d condense %d", polish, titles, condense)
	}
	raw, _ := json.Marshal(built.Draft)
	if !strings.Contains(string(raw), short) {
		t.Fatal(string(raw))
	}
	beforePolish, beforeTitles, beforeCondense := polish, titles, condense
	inBand := map[string]any{
		"title":    "长度刚好",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{strings.Repeat("合适的正文。", 120)}}},
		"closing":  "结束",
	}
	out, _, _ := finishPiece(context.Background(), gateway, providers, inBand, Input{WordTarget: 800, CostNote: note}, "长度刚好", map[string]any{}, nil)
	if polish != beforePolish || titles != beforeTitles || condense != beforeCondense {
		t.Fatalf("in-band polish %d titles %d condense %d", polish, titles, condense)
	}
	if !strings.Contains(string(mustJSON(out)), "合适的正文。") {
		t.Fatal(out)
	}
}

func mustJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func TestFailedStageKeepsTrace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "选题成功", "angle": "今天就能做"})
		default:
			w.WriteHeader(500)
			_, _ = w.Write([]byte("outline down"))
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "writer",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: time.Now().UTC(), ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	user, account, plan := seedPlan(t, st, "trace@example.com")
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
	if err != nil || task == nil || task.Status != "failed" {
		t.Fatalf("%#v %v", task, err)
	}
	if traceStage(task.ModelTrace, "topic")["ok"] != true || traceStage(task.ModelTrace, "outline")["ok"] != false {
		t.Fatalf("%#v", task.ModelTrace)
	}
}

func TestFactCheckRewritesWrongDefinition(t *testing.T) {
	wrong := strings.Repeat("合适的正文。", 110) + "弱电是交流1000V以下的电力系统。"
	fixed := strings.Repeat("合适的正文。", 110) + "弱电指通信、安防和楼宇自控等电子系统。"
	var sawFact bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "弱电入门", "angle": "先分清概念"})
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		case "事实核对":
			sawFact = true
			msgs, _ := req["messages"].([]any)
			first, _ := msgs[0].(map[string]any)
			system, _ := first["content"].(string)
			if !strings.Contains(system, "弱电") || !strings.Contains(system, "1000V") || !strings.Contains(system, "research_sources") {
				t.Errorf("fact prompt %s", system)
			}
			writeChoice(w, map[string]any{"title": "弱电入门", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{fixed}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		case "审核":
			writeChoice(w, map[string]any{"title": "弱电入门", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{fixed}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		default:
			writeChoice(w, map[string]any{"title": "弱电入门", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{wrong}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "writer",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: time.Now().UTC(), ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	user, account, plan := seedPlan(t, st, "facts@example.com")
	runTask(t, st, deps, user, account, plan, time.Now().UTC())
	if !sawFact {
		t.Fatal("fact check was not called")
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 {
		t.Fatal(err)
	}
	if strings.Contains(articles[0].BodyHTML, "1000") || !strings.Contains(articles[0].BodyHTML, "通信") {
		t.Fatal(articles[0].BodyHTML)
	}
	task, err := st.GetTask(ctx, nil, articles[0].TaskID)
	if err != nil || task == nil || traceStage(task.ModelTrace, "factcheck")["ok"] != true {
		t.Fatalf("%#v %v", task, err)
	}
}

func TestResearchPrefersChatFast(t *testing.T) {
	var researchModels []string
	body := strings.Repeat("合适的正文。", 120)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		if stageOf(req) == "资料检索" {
			researchModels = append(researchModels, req["model"].(string))
			writeCited(w, "官方说明。", "https://www.gov.cn/zhengce/2024")
			return
		}
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "先用快模型", "angle": "今天就能做"})
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		default:
			writeChoice(w, map[string]any{"title": "先用快模型", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{body + "职场成长说明写了这一步。[1]"}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	now := time.Now().UTC()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "慢", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "grok-4.7",
		Enabled: true, TimeoutSeconds: 90, UpdatedAt: now, ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "快", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "grok-chat-fast",
		Enabled: true, TimeoutSeconds: 90, UpdatedAt: now, ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	models, err := st.ListTextModels(ctx, nil, true)
	if err != nil || len(models) != 2 {
		t.Fatal(len(models), err)
	}
	var slowID, fastID int64
	for _, item := range models {
		if item.ModelName == "grok-4.7" {
			slowID = item.ID
		}
		if item.ModelName == "grok-chat-fast" {
			fastID = item.ID
		}
	}
	if err := st.ReplaceRoutes(ctx, nil, "research", "*", []int64{slowID, fastID}); err != nil {
		t.Fatal(err)
	}
	user, account, plan := seedPlan(t, st, "fast-research@example.com")
	user.Tier = "standard"
	if err := st.UpdateUser(ctx, nil, user); err != nil {
		t.Fatal(err)
	}
	runTask(t, st, deps, user, account, plan, now)
	if len(researchModels) != 1 || researchModels[0] != "grok-chat-fast" {
		t.Fatalf("research models %#v", researchModels)
	}
}

func TestRegenCreatesDraftWhenMediaIDInvalid(t *testing.T) {
	body := strings.Repeat("合适的正文。", 120)
	var thumbN, draftN, adds, updates int
	var updatedMedia string
	var addedThumbs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/chat/completions") {
			raw, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(raw, &req)
			switch stageOf(req) {
			case "选题":
				writeChoice(w, map[string]any{"title": "重写封面", "angle": "今天就能做"})
			case "大纲":
				writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
			default:
				writeChoice(w, map[string]any{"title": "重写封面", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{body}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
			}
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/stable_token"):
			_, _ = w.Write([]byte(`{"access_token":"token-1","expires_in":7200}`))
		case strings.HasSuffix(r.URL.Path, "/add_material"):
			thumbN++
			_, _ = w.Write([]byte(`{"media_id":"thumb-` + strconv.Itoa(thumbN) + `"}`))
		case strings.HasSuffix(r.URL.Path, "/uploadimg"):
			_, _ = w.Write([]byte(`{"url":"https://mmbiz.qpic.cn/demo"}`))
		case strings.HasSuffix(r.URL.Path, "/draft/switch"):
			_, _ = w.Write([]byte(`{"errcode":0,"is_open":1}`))
		case strings.HasSuffix(r.URL.Path, "/draft/count"):
			_, _ = w.Write([]byte(`{"total_count":1}`))
		case strings.HasSuffix(r.URL.Path, "/draft/update"):
			updates++
			var payload map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &payload)
			updatedMedia, _ = payload["media_id"].(string)
			_, _ = w.Write([]byte(`{"errcode":40007,"errmsg":"invalid media_id"}`))
		case strings.HasSuffix(r.URL.Path, "/draft/add"):
			adds++
			draftN++
			var payload map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &payload)
			articles, _ := payload["articles"].([]any)
			if len(articles) == 1 {
				article, _ := articles[0].(map[string]any)
				if thumb, _ := article["thumb_media_id"].(string); thumb != "" {
					addedThumbs = append(addedThumbs, thumb)
				}
			}
			_, _ = w.Write([]byte(`{"media_id":"draft-` + strconv.Itoa(draftN) + `"}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	now := time.Now().UTC()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "writer",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: now, ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "wechat", Name: "wechat", Protocol: "local", BaseURL: srv.URL, Enabled: true,
		UpdatedAt: now, ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{"mode": "live"},
	}); err != nil {
		t.Fatal(err)
	}
	user, account, plan := seedPlan(t, st, "regen-media@example.com")
	if _, err := st.DB.Exec(ctx, `UPDATE wx_account SET app_id = ? WHERE id = ?`, "wx_live_regen", account.ID); err != nil {
		t.Fatal(err)
	}
	runTask(t, st, deps, user, account, plan, now)
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) != 1 || articles[0].DraftMediaID != "draft-1" || articles[0].ThumbMediaID != "thumb-1" {
		t.Fatalf("%#v %v", articles, err)
	}
	article := &articles[0]
	var regenID int64
	if err := st.DB.WithTx(ctx, func(tx *db.Tx) error {
		task, err := jobs.Create(ctx, st, tx, user, plan, account, "regenerate", now.Add(time.Minute), article)
		if err != nil {
			return err
		}
		regenID = task.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	Execute(ctx, deps, regenID, false, now.Add(time.Minute))
	article, err = st.GetArticle(ctx, nil, article.ID)
	if err != nil || article == nil {
		t.Fatal(err)
	}
	if article.Status != "in_draft" || article.DraftMediaID != "draft-2" || article.ThumbMediaID == "thumb-1" || article.ThumbMediaID == "" {
		t.Fatalf("status %s draft %s thumb %s note %s", article.Status, article.DraftMediaID, article.ThumbMediaID, article.DeliveryNote)
	}
	if updates != 1 || updatedMedia != "draft-1" || adds != 2 {
		t.Fatalf("updates %d media %s adds %d thumbs %#v", updates, updatedMedia, adds, addedThumbs)
	}
	if addedThumbs[len(addedThumbs)-1] != article.ThumbMediaID {
		t.Fatalf("stored thumb %s added %#v", article.ThumbMediaID, addedThumbs)
	}
	task, err := st.GetTask(ctx, nil, regenID)
	if err != nil || task == nil || traceStage(task.ModelTrace, "cover")["ok"] != true {
		t.Fatalf("%#v %v", task, err)
	}
}

func TestAddRetriesWhenThumbInvalid(t *testing.T) {
	body := strings.Repeat("合适的正文。", 120)
	var adds, thumbs int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/chat/completions") {
			raw, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(raw, &req)
			switch stageOf(req) {
			case "选题":
				writeChoice(w, map[string]any{"title": "封面重传", "angle": "今天就能做"})
			case "大纲":
				writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
			default:
				writeChoice(w, map[string]any{"title": "封面重传", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{body}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
			}
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/stable_token"):
			_, _ = w.Write([]byte(`{"access_token":"token-1","expires_in":7200}`))
		case strings.HasSuffix(r.URL.Path, "/add_material"):
			thumbs++
			_, _ = w.Write([]byte(`{"media_id":"thumb-` + strconv.Itoa(thumbs) + `"}`))
		case strings.HasSuffix(r.URL.Path, "/uploadimg"):
			_, _ = w.Write([]byte(`{"url":"https://mmbiz.qpic.cn/demo"}`))
		case strings.HasSuffix(r.URL.Path, "/draft/switch"):
			_, _ = w.Write([]byte(`{"errcode":0,"is_open":1}`))
		case strings.HasSuffix(r.URL.Path, "/draft/count"):
			_, _ = w.Write([]byte(`{"total_count":1}`))
		case strings.HasSuffix(r.URL.Path, "/draft/add"):
			adds++
			if adds == 1 {
				_, _ = w.Write([]byte(`{"errcode":40007,"errmsg":"invalid media_id hint: thumb_media_id"}`))
				return
			}
			_, _ = w.Write([]byte(`{"media_id":"draft-ok"}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	now := time.Now().UTC()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "writer",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: now, ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "wechat", Name: "wechat", Protocol: "local", BaseURL: srv.URL, Enabled: true,
		UpdatedAt: now, ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{"mode": "live"},
	}); err != nil {
		t.Fatal(err)
	}
	user, account, plan := seedPlan(t, st, "thumb-retry@example.com")
	if _, err := st.DB.Exec(ctx, `UPDATE wx_account SET app_id = ? WHERE id = ?`, "wx_live_thumb", account.ID); err != nil {
		t.Fatal(err)
	}
	runTask(t, st, deps, user, account, plan, now)
	if adds != 2 || thumbs != 2 {
		t.Fatalf("adds %d thumbs %d", adds, thumbs)
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 || articles[0].Status != "in_draft" || articles[0].DraftMediaID != "draft-ok" || articles[0].ThumbMediaID != "thumb-2" {
		t.Fatalf("%#v %v", articles, err)
	}
}

func TestCoverTraceRecordsSource(t *testing.T) {
	jpegRaw := tinyJPEG(t)
	var sawFormat any
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		sawFormat = body["response_format"]
		encoded := base64Encode(jpegRaw)
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + encoded + `"}]}`))
	}))
	defer imgSrv.Close()
	textSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		long := strings.Repeat("封面旁边的正文。", 80)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "封面测试", "angle": "今天就能做"})
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		default:
			long = strings.Repeat("封面旁边的正文。", 100)
			writeChoice(w, map[string]any{"title": "封面测试", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{long}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		}
	}))
	defer textSrv.Close()
	st, deps := openDeps(t, "live", http.DefaultClient)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "写作", Protocol: "openai", BaseURL: textSrv.URL + "/v1", ModelName: "writer",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: now, ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "image_model", Name: "生图", Protocol: "openai", BaseURL: imgSrv.URL + "/v1", ModelName: "grok-imagine-image-2.0",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: now, ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	user, account, plan := seedPlan(t, st, "cover@example.com")
	user.Tier = "standard"
	if err := st.UpdateUser(ctx, nil, user); err != nil {
		t.Fatal(err)
	}
	runTask(t, st, deps, user, account, plan, now)
	if sawFormat != "b64_json" {
		t.Fatal(sawFormat)
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 {
		t.Fatal(err)
	}
	task, err := st.GetTask(ctx, nil, articles[0].TaskID)
	if err != nil || task == nil {
		t.Fatal(err)
	}
	coverRow := traceStage(task.ModelTrace, "cover")
	if coverRow["source"] != "ai" || coverRow["filename"] != "cover.jpg" || coverRow["content_type"] != "image/jpeg" || coverRow["ok"] != true {
		t.Fatalf("%#v", coverRow)
	}
	if n, _ := coverRow["bytes"].(float64); n <= 0 {
		t.Fatal(coverRow)
	}

	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte("bad gateway"))
	}))
	defer failSrv.Close()
	if _, err := st.DB.Exec(ctx, `UPDATE service_config SET base_url = ? WHERE config_type = 'image_model'`, failSrv.URL+"/v1"); err != nil {
		t.Fatal(err)
	}
	user2, account2, plan2 := seedPlan(t, st, "cover-fallback@example.com")
	user2.Tier = "standard"
	if err := st.UpdateUser(ctx, nil, user2); err != nil {
		t.Fatal(err)
	}
	runTask(t, st, deps, user2, account2, plan2, now)
	articles, err = st.ListArticlesByUser(ctx, user2.ID, 5)
	if err != nil || len(articles) == 0 || articles[0].Status != "in_draft" {
		t.Fatal(articles, err)
	}
	task, err = st.GetTask(ctx, nil, articles[0].TaskID)
	if err != nil || task == nil {
		t.Fatal(err)
	}
	coverRow = traceStage(task.ModelTrace, "cover")
	if coverRow["source"] != "builtin" || coverRow["filename"] != "cover.png" || coverRow["ok"] != false || coverRow["error"] == nil || coverRow["error"] == "" {
		t.Fatalf("%#v", coverRow)
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

func writeSourceJudge(w http.ResponseWriter, req map[string]any) {
	payload := map[string]any{}
	msgs, _ := req["messages"].([]any)
	if len(msgs) > 0 {
		last, _ := msgs[len(msgs)-1].(map[string]any)
		content, _ := last["content"].(string)
		_ = json.Unmarshal([]byte(content), &payload)
	}
	var items []any
	cands, _ := payload["candidates"].([]any)
	for i, raw := range cands {
		row, _ := raw.(map[string]any)
		fact := ""
		if row != nil {
			fact, _ = row["title"].(string)
			if fact == "" {
				fact, _ = row["snippet"].(string)
			}
		}
		facts := []any{}
		if fact != "" {
			facts = []any{fact}
		}
		items = append(items, map[string]any{"index": i + 1, "score": 3, "facts": facts})
	}
	if items == nil {
		items = []any{}
	}
	writeChoice(w, map[string]any{"items": items})
}

func writeCited(w http.ResponseWriter, content string, citations ...string) {
	payload := map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
	}
	title := "职场成长说明"
	if strings.Contains(content, "弱电") {
		title = "弱电智能化说明"
	}
	if len(citations) > 0 {
		list := make([]any, len(citations))
		for i, item := range citations {
			list[i] = map[string]any{"url": item, "title": title, "snippet": title}
		}
		payload["citations"] = list
	}
	raw, _ := json.Marshal(payload)
	_, _ = w.Write(raw)
}

func writeChoice(w http.ResponseWriter, data map[string]any) {
	body, _ := json.Marshal(data)
	out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(body)}}}})
	_, _ = w.Write(out)
}

func seedPaid(t *testing.T, st *store.Store, email, base string) (*model.User, *model.WxAccount, *model.WritingPlan) {
	t.Helper()
	ctx := context.Background()
	user, account, plan := seedPlan(t, st, email)
	user.Tier = "standard"
	if err := st.UpdateUser(ctx, nil, user); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "检索", Protocol: "openai", BaseURL: base + "/v1", ModelName: "grok-chat-fast",
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
	return user, account, plan
}

func tinyJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 18))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestHighRiskStaysInAppAndNotifies(t *testing.T) {
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
			data = map[string]any{"title": "用药备忘", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"这里写出了成人剂量，不应进草稿箱。"}, "list": []any{}}}}
		}
		body, _ := json.Marshal(data)
		out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(body)}}}})
		_, _ = w.Write(out)
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "风险模型", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "risk",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: time.Now().UTC(), ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	user, account, plan := seedPlan(t, st, "risk@example.com")
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
	if err != nil || task == nil || task.Status != "success" {
		t.Fatalf("%#v %v", task, err)
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 || articles[0].Status != "held" || articles[0].RiskLevel != "high" || articles[0].DraftMediaID != "" {
		t.Fatalf("%#v %v", articles, err)
	}
	notes, _, err := st.ListNotifications(ctx, user.ID, 10)
	if err != nil || len(notes) == 0 || notes[0].Kind != "risk" {
		t.Fatalf("%#v %v", notes, err)
	}
}

func base64Encode(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}

func saveSourceDomains(t *testing.T, st *store.Store, block, allow []any) {
	t.Helper()
	if err := st.InsertService(context.Background(), nil, &model.ServiceConfig{
		ConfigType: "audit", Name: "source_domains", Protocol: "local", Enabled: true,
		UpdatedAt: time.Now().UTC(), ExtraHeaders: map[string]string{},
		ExtraParams: map[string]any{"block": block, "allow": allow},
	}); err != nil {
		t.Fatal(err)
	}
}

func traceHasDrop(rows []map[string]any, reason string) bool {
	for _, row := range rows {
		if row["stage"] != "research" {
			continue
		}
		switch dropped := row["dropped"].(type) {
		case []any:
			for _, item := range dropped {
				entry, _ := item.(map[string]any)
				if entry["reason"] == reason {
					return true
				}
			}
		case []map[string]any:
			for _, entry := range dropped {
				if entry["reason"] == reason {
					return true
				}
			}
		}
	}
	return false
}

func traceRelaxed(rows []map[string]any) bool {
	for _, row := range rows {
		if row["stage"] == "research" && row["relaxed"] == true {
			return true
		}
	}
	return false
}

func traceStage(rows []map[string]any, stage string) map[string]any {
	for _, row := range rows {
		if row["stage"] == stage {
			return row
		}
	}
	return map[string]any{}
}

func TestHardDropIgnoresTermOverlap(t *testing.T) {
	_, kept, dropped, _ := takeSources(t.Context(), nil, "笔记", []map[string]any{
		{"title": "", "snippet": "综合布线施工前要核对材料清单", "url": "https://www.mohurd.gov.cn/gongkai/untitled"},
		{"title": "办公室网络布线的现场记录", "snippet": "线槽先看清楚再放线", "url": "https://www.example.com/office"},
		{"title": "奶奶的养生茶", "url": "https://www.example.com/tea"},
		{"title": "", "url": "https://www.example.com/blank"},
		{"title": "综合布线验收 Windows ISO 系统之家", "url": "https://www.xitongzhijia.net/win10.iso"},
		{"title": "综合布线验收电视剧全集在线观看", "url": "https://www.iqiyi.com/v/1"},
		{"title": "综合布线验收", "url": "https://apps.apple.com/app/id1"},
		{"title": "PPT模板下载", "url": "https://www.1ppt.com/moban/1"},
		{"title": "弱电验收词典", "url": "https://www.json.cn/ci"},
		{"title": "家里（汉语词语）_百度百科", "url": "https://baike.baidu.com/item/%E5%AE%B6%E9%87%8C"},
		{"title": "弱电验收检索", "url": "https://std.samr.gov.cn/search/orgOthers?q=1"},
	}, content.DefaultSourcePolicy(), "弱电智能化 综合布线验收")
	seen := map[string]bool{}
	for _, row := range kept {
		seen[str(row["url"])] = true
	}
	for _, url := range []string{
		"https://www.mohurd.gov.cn/gongkai/untitled",
		"https://www.example.com/office",
		"https://www.example.com/tea",
	} {
		if !seen[url] {
			t.Fatal("missing", url, kept, dropped)
		}
	}
	for _, url := range []string{
		"https://www.example.com/blank",
		"https://www.xitongzhijia.net/win10.iso",
		"https://www.iqiyi.com/v/1",
		"https://apps.apple.com/app/id1",
		"https://www.1ppt.com/moban/1",
		"https://www.json.cn/ci",
		"https://baike.baidu.com/item/%E5%AE%B6%E9%87%8C",
		"https://std.samr.gov.cn/search/orgOthers?q=1",
	} {
		if seen[url] {
			t.Fatal("kept junk", url, kept)
		}
	}
}

func TestJudgeKeepsRelevantFactsOnly(t *testing.T) {
	sources := []map[string]any{
		{"title": "综合布线验收说明", "snippet": "施工前要核对材料清单", "url": "https://www.mohurd.gov.cn/a"},
		{"title": "综合布线现场记录", "snippet": "线槽先看清楚", "url": "https://www.example.com/office"},
		{"title": "奶奶的养生茶", "url": "https://www.example.com/tea"},
	}
	items := []any{
		map[string]any{"index": float64(1), "score": float64(3), "facts": []any{"施工前要核对材料清单"}},
		map[string]any{"index": float64(2), "score": float64(2), "facts": []any{"线槽先看清楚"}},
		map[string]any{"index": float64(3), "score": float64(1), "facts": []any{"茶"}},
	}
	kept, dropped := applyJudgeScores(sources, items)
	if len(kept) != 2 {
		t.Fatal(kept, dropped)
	}
	if str(kept[0]["url"]) != "https://www.mohurd.gov.cn/a" {
		t.Fatal(kept)
	}
	facts, _ := kept[0]["facts"].([]any)
	if len(facts) != 1 || facts[0] != "施工前要核对材料清单" {
		t.Fatal(kept[0])
	}
	sheet := factSheet([]any{kept[0], kept[1]})
	if !strings.Contains(sheet, "1. 综合布线验收说明") || !strings.Contains(sheet, "施工前要核对材料清单") || !strings.Contains(sheet, "2. 综合布线现场记录") {
		t.Fatal(sheet)
	}
	gateway := llm.NewGateway(true, nil)
	judged, extra, _ := judgeAndKeep(t.Context(), gateway, []llm.Provider{{
		Name: "判断", Protocol: "openai", BaseURL: "mock://", Model: "grok-chat-fast",
	}}, "弱电智能化 综合布线验收", sources)
	seen := map[string]bool{}
	for _, row := range judged {
		seen[str(row["url"])] = true
		if len(asList(row["facts"])) == 0 {
			t.Fatal("missing facts", row)
		}
	}
	if !seen["https://www.mohurd.gov.cn/a"] || !seen["https://www.example.com/office"] || seen["https://www.example.com/tea"] {
		t.Fatal(judged, extra)
	}
}

func TestUntitledSourcesUseFetchedTitle(t *testing.T) {
	var hits int
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>布线施工记录</title><meta name="description" content="弱电验收要看的公开说明"></head></html>`))
	}))
	defer page.Close()
	_, kept, dropped, _ := takeSources(t.Context(), page.Client(), "笔记", []map[string]any{
		{"url": page.URL + "/std"},
		{"url": "http://127.0.0.1/secret"},
		{"title": "GB 50311 综合布线", "url": "https://www.bzfyw.com/std/50311"},
		{"title": "GB 50311 条文", "url": "https://bbs.co188.com/thread/1"},
		{"title": "标准全文", "url": "https://www.biaozhun.org/gb/50311"},
	}, content.DefaultSourcePolicy(), "弱电智能化综合布线验收 GB 50311")
	if hits != 0 {
		t.Fatal(hits)
	}
	seen := map[string]bool{}
	for _, row := range kept {
		seen[str(row["url"])] = true
		if strings.Contains(str(row["url"]), "127.0.0.1") || strings.Contains(str(row["title"]), "布线施工记录") {
			t.Fatal(row)
		}
	}
	if seen[page.URL+"/std"] || seen["http://127.0.0.1/secret"] || !seen["https://www.bzfyw.com/std/50311"] || !seen["https://bbs.co188.com/thread/1"] || !seen["https://www.biaozhun.org/gb/50311"] {
		t.Fatal(kept, dropped)
	}
}

func TestBackupSearchUsesConfiguredAPIFirst(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		if r.URL.Path == "/html" {
			t.Fatal("html search ran before the api returned rows")
		}
		_, _ = w.Write([]byte(`{"results":[{"title":"综合布线验收说明","url":"https://www.gov.cn/api"}]}`))
	}))
	defer srv.Close()
	gateway := llm.NewGateway(false, srv.Client())
	gateway.SearchAPIProvider = "tavily"
	gateway.SearchAPIKey = "tvly"
	gateway.SearchAPIURL = srv.URL + "/tavily"
	gateway.BackupSearchURL = srv.URL + "/html"
	rows, engines, err := backupHTMLSearch(t.Context(), gateway, "综合布线验收")
	if err != nil || len(rows) != 1 || rows[0]["url"] != "https://www.gov.cn/api" {
		t.Fatal(rows, err, hits)
	}
	if len(hits) != 1 || hits[0] != "/tavily" || len(engines) != 1 {
		t.Fatal(hits, engines)
	}
}

func TestFetchResearchSearchesWhenFewerThanThree(t *testing.T) {
	var query string
	var searches int
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searches++
		query = r.URL.Query().Get("q")
		_, _ = w.Write([]byte(`{"results":[{"title":"综合布线验收的另一条说明","url":"https://www.mohurd.gov.cn/gongkai/backup"},{"title":"弱电验收记录","url":"https://www.gov.cn/zhengce/backup"}]}`))
	}))
	defer search.Close()
	var researchCalls int
	research := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		if stageOf(req) == "来源判断" {
			writeSourceJudge(w, req)
			return
		}
		researchCalls++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"检索到一条公开说明。"}}],"citations":[{"url":"https://www.mohurd.gov.cn/gongkai/1","title":"综合布线验收说明"}]}`))
	}))
	defer research.Close()
	gateway := llm.NewGateway(false, research.Client())
	gateway.BackupSearchURL = search.URL
	gateway.BackupSearchAlt = search.URL
	gateway.ResearchTimeout = 5 * time.Second
	gateway.MaxAttempts = 1
	providers := []llm.Provider{{
		Name: "检索", Protocol: "openai", BaseURL: research.URL + "/v1", Model: "grok-4", Timeout: 5 * time.Second,
	}}
	notes, sources, _, err := fetchResearch(t.Context(), gateway, providers, map[string]any{
		"topic_title": "综合布线验收", "direction": "弱电智能化", "keywords": "验收",
	}, content.DefaultSourcePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if researchCalls != 1 || searches < 2 {
		t.Fatalf("research %d searches %d notes %s", researchCalls, searches, notes)
	}
	if !strings.Contains(query, "综合布线验收") || utf8.RuneCountInString(query) > 32 {
		t.Fatal(query)
	}
	seen := map[string]bool{}
	for _, raw := range sources {
		source, _ := raw.(map[string]any)
		seen[str(source["url"])] = true
	}
	if !seen["https://www.mohurd.gov.cn/gongkai/1"] || !seen["https://www.mohurd.gov.cn/gongkai/backup"] {
		t.Fatal(seen)
	}
}

func TestStructureRewriteHoldsWhenTheModelMisses(t *testing.T) {
	gateway := llm.NewGateway(true, nil)
	draft := map[string]any{
		"title":    "验收先看这一步",
		"sections": []any{map[string]any{"paragraphs": []any{"综合布线验收有一条说明。", "另一条消息没有日期。"}}},
	}
	providers := map[string][]llm.Provider{"draft": {{
		Name: "结构", Protocol: "openai", BaseURL: "mock://", Model: "grok-4",
		ExtraParams: map[string]any{"_mock_structure_fail": true},
	}}}
	kept, _, trace, held := rewriteColumnStructure(t.Context(), gateway, providers, draft, map[string]any{}, "news", "")
	if !held || kept["sections"].([]any)[0].(map[string]any)["paragraphs"].([]any)[0] != "综合布线验收有一条说明。" {
		t.Fatal(held, kept, trace)
	}
	fixed, _, _, held := rewriteColumnStructure(t.Context(), gateway, map[string][]llm.Provider{"draft": {{
		Name: "结构", Protocol: "openai", BaseURL: "mock://", Model: "grok-4",
	}}}, draft, map[string]any{}, "news", "")
	if held || !quality.StructureOK("news", draftParagraphs(fixed)) {
		t.Fatal(held, draftParagraphs(fixed))
	}
	alt, ok := columns.PickNonNews([]columns.Column{
		{ID: 1, Name: "工程圈快报", Structure: "news", Status: "active"},
		{ID: 2, Name: "知识FAQ", Structure: "qa", Status: "active"},
	}, columns.Column{ID: 1, Name: "工程圈快报", Structure: "news"}, nil)
	if !ok || alt.Name != "知识FAQ" {
		t.Fatal(alt, ok)
	}
	if sourcesDated([]any{map[string]any{"title": "综合布线说明"}}) || !sourcesDated([]any{map[string]any{"title": "2024年10月的布线新闻"}}) {
		t.Fatal("dated")
	}
}

func TestTabooDoesNotFailTheTask(t *testing.T) {
	var sawRule bool
	long := strings.Repeat("现场要逐项核对编号。", 80)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		msgs, _ := req["messages"].([]any)
		blob, _ := json.Marshal(msgs)
		if strings.Contains(string(blob), "不写的说法") {
			sawRule = true
		}
		body := long
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "验收怎么核对", "angle": "今天就能做"})
			return
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
			return
		default:
			body = "现场要逐项核对。包过验收这种话不写。下一步看编号。" + long
		}
		writeChoice(w, map[string]any{"title": "验收怎么核对", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{body}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "writer",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: time.Now().UTC(), ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	user, account, plan := seedPlan(t, st, "taboo@example.com")
	plan.Direction = "弱电智能化"
	if err := st.UpdatePlan(ctx, nil, plan); err != nil {
		t.Fatal(err)
	}
	runTask(t, st, deps, user, account, plan, time.Now().UTC())
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 || strings.Contains(articles[0].BodyHTML, "包过验收") || !sawRule {
		t.Fatal(sawRule, articles)
	}
}

func TestResearchUsesWebSearchAfterModelFailure(t *testing.T) {
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"title":"弱电智能化验收说明","url":"https://www.mohurd.gov.cn/gongkai/backup"}]}`))
	}))
	defer search.Close()
	var researchCalls int
	body := strings.Repeat("合适的正文。", 120) + "弱电智能化验收说明写了这一步。[1]"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "弱电智能化验收", "angle": "今天就能做"})
		case "资料检索":
			researchCalls++
			w.WriteHeader(502)
			_, _ = w.Write([]byte("bad gateway"))
		case "来源判断":
			writeSourceJudge(w, req)
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		default:
			writeChoice(w, map[string]any{"title": "弱电智能化验收", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{body}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	deps.Gateway.BackupSearchURL = search.URL
	deps.Gateway.Backoff = time.Millisecond
	ctx := context.Background()
	user, account, plan := seedPaid(t, st, "websearch@example.com", srv.URL)
	plan.Direction = "弱电智能化"
	if err := st.UpdatePlan(ctx, nil, plan); err != nil {
		t.Fatal(err)
	}
	runTask(t, st, deps, user, account, plan, time.Now().UTC())
	if researchCalls < 2 {
		t.Fatalf("research calls %d", researchCalls)
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 5)
	if err != nil || len(articles) == 0 || !strings.Contains(articles[0].BodyHTML, "mohurd.gov.cn") {
		note := ""
		if len(articles) > 0 {
			note = articles[0].DeliveryNote + " " + articles[0].Status
		}
		t.Fatal(note, err)
	}
}

func TestConcurrentAccountsDoNotShareOpenings(t *testing.T) {
	grandma := "周末早晨，阳光从窗外洒进客厅，你推开门，看到爷爷奶奶捧着温茶转圈，那一小步就让上午亮堂起来。"
	var mu sync.Mutex
	leaked := false
	long := strings.Repeat("验收先核对回路编号。", 80)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		msgs, _ := req["messages"].([]any)
		blob, _ := json.Marshal(msgs)
		text := string(blob)
		stage := stageOf(req)
		if (stage == "选题" || stage == "大纲" || stage == "正文") && strings.Contains(text, "弱电智能化") && strings.Contains(text, "爷爷奶奶") {
			mu.Lock()
			leaked = true
			mu.Unlock()
		}
		opening := long
		if strings.Contains(text, "家庭生活") {
			opening = grandma + long
		}
		if strings.Contains(text, "弱电智能化") {
			opening = grandma + long
		}
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "今天的题目", "angle": "一个做法"})
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		default:
			writeChoice(w, map[string]any{"title": "今天的题目", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{opening}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	deps.Gateway.CacheTTL = time.Hour
	ctx := context.Background()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "writer",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: time.Now().UTC(), ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	user, familyAccount, familyPlan := seedPlan(t, st, "family@example.com")
	user.Tier = "premium"
	if err := st.UpdateUser(ctx, nil, user); err != nil {
		t.Fatal(err)
	}
	familyPlan.Direction = "家庭生活"
	if err := st.UpdatePlan(ctx, nil, familyPlan); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := st.InsertArticle(ctx, nil, &model.Article{
		TaskID: familyPlan.ID, UserID: user.ID, PlanID: familyPlan.ID, WxAccountID: familyAccount.ID,
		Title: grandma, Status: "in_draft", TemplateKey: "fresh", StyleKey: "warm", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	secret, err := security.EncryptSecret("test-master-key", "mock-secret")
	if err != nil {
		t.Fatal(err)
	}
	bound := now
	elvAccount := &model.WxAccount{UserID: user.ID, Name: "弱电陈工", AppID: "wx_mock_elv", SecretEncrypted: secret, SubjectType: "personal", ServiceType: "subscription", DeliveryMode: "draft", BindStatus: "active", BoundAt: &bound}
	if err := st.InsertAccount(ctx, nil, elvAccount); err != nil {
		t.Fatal(err)
	}
	next := now.Add(-time.Minute)
	elvPlan := &model.WritingPlan{UserID: user.ID, WxAccountID: elvAccount.ID, Direction: "弱电智能化", Mode: "managed", StyleKey: "warm", TemplateKey: "fresh", WordTarget: 800, ScheduleType: "daily", TimeWindow: "night", Status: "active", NextRunAt: &next, CreatedAt: now, Weekdays: []int{}}
	if err := st.InsertPlan(ctx, nil, elvPlan); err != nil {
		t.Fatal(err)
	}
	var familyTask, elvTask int64
	if err := st.DB.WithTx(ctx, func(tx *db.Tx) error {
		task, err := jobs.Create(ctx, st, tx, user, familyPlan, familyAccount, "manual", now, nil)
		if err != nil {
			return err
		}
		familyTask = task.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.WithTx(ctx, func(tx *db.Tx) error {
		task, err := jobs.Create(ctx, st, tx, user, elvPlan, elvAccount, "manual", now.Add(time.Second), nil)
		if err != nil {
			return err
		}
		elvTask = task.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		Execute(ctx, deps, familyTask, false, now)
	}()
	go func() {
		defer wg.Done()
		Execute(ctx, deps, elvTask, false, now.Add(time.Second))
	}()
	wg.Wait()
	if leaked {
		t.Fatal("弱电 prompt contained the other account's opening")
	}
	articles, err := st.ListArticlesByUser(ctx, user.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var elvHTML string
	for _, article := range articles {
		if article.PlanID == elvPlan.ID {
			elvHTML = article.BodyHTML
		}
	}
	if elvHTML == "" || strings.Contains(elvHTML, "爷爷奶奶") {
		t.Fatal(elvHTML)
	}
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
