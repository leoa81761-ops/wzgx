package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/columns"
	"github.com/leoa81761-ops/wzgx/internal/content"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/jobs"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/store"
	"github.com/leoa81761-ops/wzgx/internal/topics"
)

func TestExpandRetriesOnceWithTheShortfall(t *testing.T) {
	var calls int
	var sawShortfall bool
	short := "只有一句。"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		if stageOf(req) != "扩写" {
			t.Errorf("stage %s", stageOf(req))
		}
		calls++
		msgs, _ := req["messages"].([]any)
		blob, _ := json.Marshal(msgs)
		if calls == 2 && strings.Contains(string(blob), "shortfall") {
			sawShortfall = true
		}
		writeChoice(w, map[string]any{
			"title": "短稿", "summary": "摘要",
			"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{short}, "list": []any{}}},
			"closing":  "结束", "sources": []any{},
		})
	}))
	defer srv.Close()
	gw := llm.NewGateway(false, srv.Client())
	draft := map[string]any{
		"title": "短稿", "summary": "摘要",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{short}, "list": []any{}}},
		"closing":  "结束", "sources": []any{},
	}
	out, _, _, still := fitBody(context.Background(), gw, []llm.Provider{{
		Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 10 * time.Second,
	}}, map[string]any{"word_target": 800}, draft, 800)
	if calls != 2 || !sawShortfall || !still || content.BodyRunes(out) >= 800*85/100 {
		t.Fatalf("calls %d shortfall %v still %v runes %d", calls, sawShortfall, still, content.BodyRunes(out))
	}
}

func TestShortDraftIsNotPushed(t *testing.T) {
	var expands int
	short := map[string]any{
		"title": "短稿", "summary": "摘要",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"只有一句。"}, "list": []any{}}},
		"closing":  "结束", "sources": []any{},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "短稿", "angle": "今天就能做"})
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		case "扩写":
			expands++
			writeChoice(w, short)
		default:
			writeChoice(w, short)
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
	user, account, plan := seedPlan(t, st, "too-short@example.com")
	plan.WordTarget = 800
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
	article, err := st.GetArticleByTask(ctx, nil, taskID)
	if err != nil || article == nil {
		t.Fatal(err)
	}
	if expands != 2 || article.Status != "held" || article.DraftMediaID != "" || !strings.Contains(article.DeliveryNote, "85%") {
		t.Fatalf("expands %d status %s media %s note %s", expands, article.Status, article.DraftMediaID, article.DeliveryNote)
	}
}

func TestUncitedSourcesStayOutOfTheArticle(t *testing.T) {
	body := strings.Repeat("合适的正文。", 120)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "没有标引用", "angle": "今天就能做"})
		case "资料检索":
			writeCited(w, "官方页面。", "https://www.gov.cn/zhengce/2024")
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		default:
			writeChoice(w, map[string]any{"title": "没有标引用", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{body}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := context.Background()
	user, account, plan := seedPaid(t, st, "uncited@example.com", srv.URL)
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
	article, err := st.GetArticleByTask(ctx, nil, taskID)
	if err != nil || article == nil {
		t.Fatal(err)
	}
	if article.Status != "held" || article.DraftMediaID != "" || !strings.Contains(article.BodyHTML, "延伸阅读") || !strings.Contains(article.BodyHTML, "www.gov.cn") || strings.Contains(article.BodyHTML, "参考来源") || !strings.Contains(article.DeliveryNote, "没有引用") {
		t.Fatal(article.Status, article.DraftMediaID, article.DeliveryNote, article.BodyHTML)
	}
}

func TestCleanupBelowTargetIsHeld(t *testing.T) {
	para := strings.Repeat("合适的正文。", 84)
	body := map[string]any{
		"title": "短稿", "summary": "摘要",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{para, para}, "list": []any{}}},
		"closing":  "结束", "sources": []any{},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "短稿", "angle": "今天就能做"})
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"})
		default:
			writeChoice(w, body)
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
	user, account, plan := seedPlan(t, st, "deduped-short@example.com")
	plan.WordTarget = 800
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
	before := content.BodyRunes(body)
	Execute(ctx, deps, taskID, false, now)
	article, err := st.GetArticleByTask(ctx, nil, taskID)
	if err != nil || article == nil {
		t.Fatal(err)
	}
	if before < 800*85/100 || content.BodyRunes(article.BodyJSON) >= 800*85/100 || article.Status != "held" || article.DraftMediaID != "" || !strings.Contains(article.DeliveryNote, "85%") {
		t.Fatalf("before %d after %d status %s note %s", before, content.BodyRunes(article.BodyJSON), article.Status, article.DeliveryNote)
	}
}

func TestCondenseRejectsBelowTheTarget(t *testing.T) {
	long := strings.Repeat("原文说明引用这里[1]。", 80)
	original := map[string]any{
		"title":    "验收记录",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{long}, "list": []any{}}},
		"closing":  "结束。",
		"sources":  []any{map[string]any{"name": "官网", "url": "https://www.gov.cn/a"}},
	}
	short := map[string]any{
		"title":    "验收记录",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"压短后的说明仍见[1]。"}, "list": []any{}}},
		"closing":  "结束。",
		"sources":  []any{},
	}
	if content.BodyRunes(short) >= 800 {
		t.Fatal(content.BodyRunes(short))
	}
	kept, ok := keepCondensed(original, short, 800)
	if ok || content.BodyRunes(kept) != content.BodyRunes(original) {
		t.Fatal(ok, content.BodyRunes(kept))
	}
	kept, ok = keepCondensed(original, short, 0)
	if !ok || !strings.Contains(bodyPlain(kept), "压短后的说明仍见[1]。") {
		t.Fatal(ok, kept)
	}
}

func TestFamilyFooterIsNotTheMedicalLine(t *testing.T) {
	home := footnoteDisclaimer(store.VerticalPack{Key: "family", Disclaimer: "本文仅供科普，不能替代医生诊疗意见。"})
	if strings.Contains(home, "医生") || !strings.Contains(home, "家庭生活") {
		t.Fatal(home)
	}
	if text := footnoteDisclaimer(store.VerticalPack{Key: "general", Disclaimer: "本文仅供科普，不能替代医生诊疗意见。"}); strings.Contains(text, "医生") {
		t.Fatal(text)
	}
	if text := footnoteDisclaimer(store.VerticalPack{Key: "elv", Disclaimer: ""}); !strings.Contains(text, "工程") {
		t.Fatal(text)
	}
	topic := topics.AssignColumn(topics.Choice{Subtopic: "验收", Column: "避坑辟谣", Angle: "一个说法"}, "现场笔记,验收清单", nil)
	if topic.Column != "现场笔记" || strings.Contains(topic.TopicTag, "避坑辟谣") {
		t.Fatal(topic)
	}
	next := topics.AssignColumn(topic, "现场笔记,验收清单", []topics.Coverage{{Column: "现场笔记"}})
	if next.Column != "验收清单" {
		t.Fatal(next)
	}
}

func TestActiveColumnReplacesPackSubtopics(t *testing.T) {
	at := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	choice, col, ok := ApplyColumnChoice(topics.Choice{Subtopic: "亲子阅读", Column: "避坑辟谣", Angle: "一个说法"}, at, []columns.Column{{
		ID: 1, Name: "工程圈快报", Status: "active", Positioning: "行业消息",
	}}, "weight", nil, "弱电智能化", "验收", nil)
	if !ok || choice.Subtopic == "亲子阅读" || strings.Contains(choice.TopicTag, "亲子阅读") || strings.Contains(choice.TopicTag, "综合布线") || choice.Column != "工程圈快报" || col.Name != "工程圈快报" {
		t.Fatal(choice, col, ok)
	}
}

func TestClicheListCatchesTheNewMotifs(t *testing.T) {
	cleaned, _ := content.StripCliches("结论是：更稳妥的做法是把一件小事，一份真心留到最后。安心不是一下子就有的。").(string)
	for _, phrase := range []string{"结论是：", "更稳妥的做法是", "一件小事，一份真心", "安心不是一下子"} {
		if strings.Contains(cleaned, phrase) {
			t.Fatal(cleaned)
		}
	}
}
