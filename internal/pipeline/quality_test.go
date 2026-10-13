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
		if strings.Contains(string(blob), "shortfall") {
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
	if calls != 1 || !sawShortfall || !still || content.BodyRunes(out) >= 800*85/100 {
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
	if expands != 0 || article.Status != "held" || article.DraftMediaID != "" || !strings.Contains(article.DeliveryNote, "85%") {
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
		case "来源判断":
			writeSourceJudge(w, req)
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
	if article.Status != "in_draft" || article.DraftMediaID == "" || strings.Contains(article.BodyHTML, "经验分享") || !strings.Contains(article.BodyHTML, "参考来源") || !strings.Contains(article.BodyHTML, "[1]") || !strings.Contains(article.BodyHTML, "www.gov.cn") || strings.Contains(article.DeliveryNote, "没有引用") || strings.Contains(article.DeliveryNote, "无参考来源") {
		t.Fatal(article.Status, article.DraftMediaID, article.DeliveryNote, article.BodyHTML)
	}
}

func TestCleanupKeepsTheBodyWithTheSections(t *testing.T) {
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
		case "扩写":
			writeChoice(w, map[string]any{"title": "短稿", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{strings.Repeat("合适的正文。", 40)}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
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
	after := content.BodyRunes(article.BodyJSON)
	if before < 800*85/100 || after*10 < before*9 || article.Status != "in_draft" || article.DraftMediaID == "" || strings.Contains(article.DeliveryNote, "85%") {
		t.Fatalf("before %d after %d status %s note %s", before, after, article.Status, article.DeliveryNote)
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
	}}, "weight", nil, "弱电智能化", "验收", nil, nil, nil, nil)
	if !ok || choice.Subtopic == "亲子阅读" || strings.Contains(choice.TopicTag, "亲子阅读") || strings.Contains(choice.TopicTag, "综合布线") || choice.Column != "工程圈快报" || col.Name != "工程圈快报" {
		t.Fatal(choice, col, ok)
	}
	repeated, _, ok := ApplyColumnChoice(topics.Choice{Subtopic: "星形与环形", Angle: "损耗核算"}, at, []columns.Column{{
		ID: 2, Name: "知识FAQ", Status: "active", Positioning: "星形与环形", Structure: "qa",
	}, {
		ID: 3, Name: "方案拆解", Status: "active", Structure: "steps",
	}}, "", nil, "弱电智能化", "验收", []topics.Coverage{{Subtopic: "星形与环形", Angle: "损耗核算"}}, nil, []string{"星形和环形哪种损耗更低", "爷爷奶奶捧着温茶转圈"}, []string{"损耗核算"})
	if !ok || strings.Contains(repeated.Subtopic, "星形") || strings.Contains(repeated.Angle, "损耗") {
		t.Fatal(repeated, ok)
	}
}

func TestFinalTopUpAimsAt110(t *testing.T) {
	var calls int
	var sawGoal bool
	short := map[string]any{
		"title": "短稿", "summary": "摘要",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"只有一句。"}, "list": []any{}}},
		"closing":  "结束", "sources": []any{map[string]any{"url": "https://www.gov.cn/a", "title": "来源"}},
	}
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
		if strings.Contains(string(blob), "880") {
			sawGoal = true
		}
		writeChoice(w, short)
	}))
	defer srv.Close()
	gw := llm.NewGateway(false, srv.Client())
	out, _, _ := topUpLength(t.Context(), gw, map[string][]llm.Provider{"draft": {{
		Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 10 * time.Second,
	}}}, short, map[string]any{"word_target": 800}, 800, "")
	if calls != 1 || !sawGoal || content.BodyRunes(out) >= 800*110/100 {
		t.Fatalf("calls %d goal %v runes %d", calls, sawGoal, content.BodyRunes(out))
	}
	grown := strings.Repeat("合适的正文。", 147)
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeChoice(w, map[string]any{"title": "短稿", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{grown}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
	}))
	defer srv2.Close()
	gw2 := llm.NewGateway(false, srv2.Client())
	out, _, _ = topUpLength(t.Context(), gw2, map[string][]llm.Provider{"draft": {{
		Name: "写作", Protocol: "openai", BaseURL: srv2.URL + "/v1", Model: "writer", Timeout: 10 * time.Second,
	}}}, short, map[string]any{"word_target": 800}, 800, "")
	if content.BodyRunes(out) < 800*110/100 || str(asList(out["sources"])[0].(map[string]any)["url"]) != "https://www.gov.cn/a" {
		t.Fatal(content.BodyRunes(out), out["sources"])
	}
}

func TestLongColumnTopUpUsesFourPasses(t *testing.T) {
	var calls int
	short := map[string]any{
		"title": "短稿", "summary": "摘要",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"只有一句。"}, "list": []any{}}},
		"closing":  "结束", "sources": []any{map[string]any{"url": "https://www.gov.cn/a", "title": "来源"}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		text := strings.Repeat("合适的正文。", calls*20)
		writeChoice(w, map[string]any{"title": "短稿", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{text}, "list": []any{}}}, "closing": "结束", "sources": []any{}})
	}))
	defer srv.Close()
	gw := llm.NewGateway(false, srv.Client())
	_, _, _ = topUpLength(t.Context(), gw, map[string][]llm.Provider{"draft": {{
		Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 10 * time.Second,
	}}}, short, map[string]any{"word_target": 1500}, 1500, "")
	if calls != 4 {
		t.Fatal(calls)
	}
}

func TestCopiedSpanIsRewrittenNotFailed(t *testing.T) {
	span := strings.Repeat("回路编号要先抄下来再逐点核对是否一致", 3)
	draft := map[string]any{
		"title":    "验收记录",
		"sections": []any{map[string]any{"paragraphs": []any{span + "[1]"}, "list": []any{}}},
		"closing":  "结束。",
		"sources":  []any{map[string]any{"summary": span, "url": "https://www.gov.cn/a"}},
	}
	if !content.CopiedSpan(content.DraftText(draft), content.SourceTexts(draft)) {
		t.Fatal("fixture should copy")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		msgs, _ := req["messages"].([]any)
		blob, _ := json.Marshal(msgs)
		if stageOf(req) != "表述改写" || !strings.Contains(string(blob), "[n]") {
			t.Errorf("stage %s", stageOf(req))
		}
		writeChoice(w, map[string]any{
			"title": "验收记录", "summary": "摘要",
			"sections": []any{map[string]any{"paragraphs": []any{"现场先把编号抄进表格，再逐点看一遍，缺的那一项单独标出来。[1]"}, "list": []any{}}},
			"closing":  "结束。", "sources": []any{},
		})
	}))
	defer srv.Close()
	gw := llm.NewGateway(false, srv.Client())
	out, _, trace := rewriteCopiedSpans(t.Context(), gw, map[string][]llm.Provider{"draft": {{
		Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 10 * time.Second,
	}}}, draft, map[string]any{}, "")
	if content.CopiedSpan(content.DraftText(out), content.SourceTexts(draft)) || !strings.Contains(bodyPlain(out), "[1]") {
		t.Fatal(bodyPlain(out), trace)
	}
	if str(asList(out["sources"])[0].(map[string]any)["url"]) != "https://www.gov.cn/a" {
		t.Fatal(out["sources"])
	}
}

func TestCitationsAreRestoredAfterStructureRewrite(t *testing.T) {
	body := strings.Repeat("合适的正文。", 40) + "[1]"
	numbered := "1. " + strings.Repeat("合适的正文。", 40)
	cited := "1. " + strings.Repeat("合适的正文。", 40) + "[1]"
	var sawMarker bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		msgs, _ := req["messages"].([]any)
		blob, _ := json.Marshal(msgs)
		if strings.Contains(string(blob), "[n]") {
			sawMarker = true
		}
		switch stageOf(req) {
		case "栏目结构":
			writeChoice(w, map[string]any{"title": "拆解", "sections": []any{map[string]any{"paragraphs": []any{numbered, "2. 再核对编号。"}}}, "closing": "结束", "sources": []any{}})
		case "补引用":
			writeChoice(w, map[string]any{"title": "拆解", "sections": []any{map[string]any{"paragraphs": []any{cited, "2. 再核对编号。[1]"}}}, "closing": "结束", "sources": []any{}})
		default:
			t.Errorf("stage %s", stageOf(req))
		}
	}))
	defer srv.Close()
	gw := llm.NewGateway(false, srv.Client())
	providers := map[string][]llm.Provider{"draft": {{
		Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 10 * time.Second,
	}}}
	draft := map[string]any{
		"title":    "拆解",
		"sections": []any{map[string]any{"paragraphs": []any{body}}},
		"closing":  "结束",
		"sources":  []any{map[string]any{"title": "综合布线验收说明", "snippet": "合适的正文", "url": "https://www.gov.cn/a"}},
	}
	rewritten, _, _, held := rewriteColumnStructure(t.Context(), gw, providers, draft, map[string]any{}, "steps", "")
	if held || citationMarks.MatchString(bodyPlain(rewritten)) {
		t.Fatal(held, bodyPlain(rewritten))
	}
	added, _, _ := addCitations(t.Context(), gw, providers, rewritten, map[string]any{})
	if !citationMarks.MatchString(bodyPlain(added)) || !sawMarker {
		t.Fatal(bodyPlain(added), sawMarker)
	}
}

func TestClicheRewriteDropsThePhrase(t *testing.T) {
	draft := map[string]any{
		"sections": []any{map[string]any{"paragraphs": []any{"综上所述，现场先核对回路。[1]"}}},
		"sources":  []any{map[string]any{"url": "https://www.gov.cn/a"}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeChoice(w, map[string]any{"title": "核对", "sections": []any{map[string]any{"paragraphs": []any{"现场先核对回路。[1]"}}}, "sources": []any{}})
	}))
	defer srv.Close()
	gw := llm.NewGateway(false, srv.Client())
	out, _, _ := rewriteCliches(t.Context(), gw, map[string][]llm.Provider{"draft": {{
		Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 10 * time.Second,
	}}}, draft, map[string]any{}, "")
	if strings.Contains(bodyPlain(out), "综上所述") || !strings.Contains(bodyPlain(out), "[1]") {
		t.Fatal(bodyPlain(out))
	}
}

func TestBodyMotifUsesTheTitleDedupe(t *testing.T) {
	motifs := topics.RepeatedMotifs([]string{"爷爷奶奶捧着温茶转圈"})
	draft := map[string]any{
		"title":    "下午的记录",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"爷爷奶奶捧着温茶转圈，下午继续核对。"}}},
		"closing":  "结束",
		"sources":  []any{map[string]any{"url": "https://www.gov.cn/a"}},
	}
	if !bodyHasAny(draft, motifs) {
		t.Fatal(motifs)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		msgs, _ := req["messages"].([]any)
		blob, _ := json.Marshal(msgs)
		if stageOf(req) != "意象改写" || !strings.Contains(string(blob), "温茶转圈") {
			t.Errorf("stage %s", stageOf(req))
		}
		writeChoice(w, map[string]any{
			"title": "下午的记录", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"下午继续核对现场记录。"}}},
			"closing": "结束", "sources": []any{},
		})
	}))
	defer srv.Close()
	gw := llm.NewGateway(false, srv.Client())
	base := map[string]any{"avoid_motifs": motifs}
	out, _, _ := rewriteMotif(t.Context(), gw, map[string][]llm.Provider{"draft": {{
		Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 10 * time.Second,
	}}}, draft, base)
	if strings.Contains(bodyPlain(out), "温茶转圈") || str(asList(out["sources"])[0].(map[string]any)["url"]) != "https://www.gov.cn/a" {
		t.Fatal(bodyPlain(out), out["sources"])
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
