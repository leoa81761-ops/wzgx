package pipeline

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/jobs"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/model"
)

func payloadOf(req map[string]any) map[string]any {
	payload := map[string]any{}
	msgs, _ := req["messages"].([]any)
	if len(msgs) == 0 {
		return payload
	}
	last, _ := msgs[len(msgs)-1].(map[string]any)
	_ = json.Unmarshal([]byte(llm.PayloadText(str(last["content"]))), &payload)
	return payload
}

func echoDraft(w http.ResponseWriter, payload, fallback map[string]any) {
	if draft, ok := payload["draft"].(map[string]any); ok && str(draft["title"]) != "" && len(asList(draft["sections"])) > 0 {
		writeChoice(w, draft)
		return
	}
	writeChoice(w, fallback)
}

// Article 89 spent the draft on an outline that missed the title, repeated one scene,
// and left answer prefixes in the stored body.
func TestArticle89OutlineBeforeDraftAndStoredBody(t *testing.T) {
	good := "综合布线的水晶头怎么压？"
	var outlineCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		payload := payloadOf(req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": good, "angle": "先看线序"})
		case "大纲":
			outlineCalls++
			if outlineCalls == 1 {
				writeChoice(w, map[string]any{"hook": "开头", "points": []any{
					map[string]any{"heading": "孙子端着汤", "intent": "讲故事"},
					map[string]any{"heading": "再端一次", "intent": "再讲"},
				}, "closing": "结束"})
				return
			}
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{
				map[string]any{"heading": "综合布线的水晶头怎么压", "intent": "说明线序"},
				map[string]any{"heading": "综合布线测完再收", "intent": "收尾"},
			}, "closing": "结束"})
		case "正文":
			heading := str(payload["section_heading"])
			para := "先把线序摊在桌上。孙子端着一碗汤走过来。两轮以内改完，一般就过了。先看这一项。答：再看下一项。"
			if strings.Contains(heading, "收") {
				para = "孙子端着汤又走过来。孙子端着碗走过来。孙子端着水杯走过来。收好工具。"
			}
			writeChoice(w, map[string]any{
				"title": good, "summary": "摘要",
				"sections": []any{map[string]any{
					"heading": heading, "paragraphs": []any{para}, "list": []any{"再核对绿线。"},
					"qa": []any{map[string]any{"question": strings.TrimRight(heading, "？?") + "？", "answer": "答：先看橙白。"}},
				}},
				"closing": "结束", "sources": []any{},
			})
		case "标题":
			writeChoice(w, map[string]any{"titles": []any{map[string]any{"title": good}}})
		default:
			echoDraft(w, payload, map[string]any{
				"title": good, "summary": "摘要",
				"sections": []any{map[string]any{"heading": "综合布线的水晶头怎么压", "paragraphs": []any{"先把线序摊在桌上。"}, "list": []any{}}},
				"closing":  "结束", "sources": []any{},
			})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := t.Context()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "writer",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: time.Now().UTC(), ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	user, account, plan := seedPlan(t, st, "article89@example.com")
	plan.Direction = "弱电智能化"
	if err := st.UpdatePlan(ctx, nil, plan); err != nil {
		t.Fatal(err)
	}
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
	html := article.BodyHTML
	if outlineCalls != 2 {
		t.Fatalf("outline calls %d html %s note %s", outlineCalls, html, article.DeliveryNote)
	}
	if article.Status != "in_draft" {
		t.Fatalf("status %s note %s html %s", article.Status, article.DeliveryNote, html)
	}
	intro := strings.Index(html, "先把线序摊在桌上")
	answer := strings.Index(html, "先看橙白")
	list := strings.Index(html, "再核对绿线")
	if intro < 0 || answer < intro || list < answer {
		t.Fatal(html)
	}
	if strings.Contains(html, "孙子端着汤") || strings.Count(html, "孙子端着") != 1 || !strings.Contains(html, "综合布线测完再收") {
		t.Fatal(html)
	}
	if strings.Count(html, "综合布线的水晶头怎么压") != 1 || strings.Contains(html, "答：") || strings.Contains(html, "两轮以内") {
		t.Fatal(html)
	}
}

// Article 90 stored a template topic and a flattened table, and kept almost no sources.
func TestArticle90TopicTableAndSourceReasons(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		payload := payloadOf(req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "综合布线的线序", "angle": "先核对线序"})
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{
				map[string]any{"heading": "综合布线的线序", "intent": "说明线序"},
				map[string]any{"heading": "综合布线的测试", "intent": "说明测试"},
			}, "closing": "结束"})
		case "正文":
			heading := str(payload["section_heading"])
			if heading == "" {
				heading = "综合布线的线序"
			}
			writeChoice(w, map[string]any{
				"title": "综合布线的线序", "summary": "摘要",
				"sections": []any{map[string]any{
					"heading": heading,
					"paragraphs": []any{
						"先把线序摊在桌上。两轮以内改完，一般就过了。",
						"| 项目 | 说明 | | --- | --- | | 线序 | 按568B。超过一半要重测。 |",
					},
					"list": []any{},
					"qa":   []any{map[string]any{"question": "还要看什么？", "answer": "答：先看橙白。超过一半先停下。"}},
				}},
				"closing": "结束", "sources": []any{},
			})
		case "标题":
			writeChoice(w, map[string]any{"titles": []any{map[string]any{"title": "综合布线的线序"}}})
		default:
			echoDraft(w, payload, map[string]any{
				"title": "综合布线的线序", "summary": "摘要",
				"sections": []any{map[string]any{"heading": "综合布线的线序", "paragraphs": []any{"先把线序摊在桌上。"}, "list": []any{}}},
				"closing":  "结束", "sources": []any{},
			})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := t.Context()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "writer",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: time.Now().UTC(), ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	user, account, plan := seedPlan(t, st, "article90@example.com")
	plan.Direction = "弱电智能化"
	if err := st.UpdatePlan(ctx, nil, plan); err != nil {
		t.Fatal(err)
	}
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
	if article.Status != "in_draft" || article.Title != "综合布线的线序" || strings.Contains(article.Title, "测到一半") {
		t.Fatalf("status %s title %s note %s", article.Status, article.Title, article.DeliveryNote)
	}
	html := article.BodyHTML
	if !strings.Contains(html, "<table") || !strings.Contains(html, "<td") || !strings.Contains(html, "按568B") || strings.Contains(html, "| 项目 |") {
		t.Fatal(html)
	}
	if strings.Contains(html, "两轮以内") || strings.Contains(html, "答：") || strings.Contains(html, "超过一半") {
		t.Fatal(html)
	}

	title := "六类线水晶头怎么压"
	page := "综合布线系统工程验收规范写明水晶头按线序压接，并先核对信息点。"
	sources := []map[string]any{{
		"title": "综合布线系统工程验收规范", "snippet": "水晶头按线序压接并核对信息点",
		"url": "https://www.mohurd.gov.cn/gongkai/zc/cabling.html", "fetched_text": page,
	}, {
		"title": "小说下载", "snippet": "章节全文免费阅读不收费",
		"url": "https://www.example.com/novel", "fetched_text": "章节全文免费阅读不收费",
	}}
	kept, dropped := applyJudgeScores(sources, []any{
		map[string]any{"index": 1, "score": 3, "page_type": "standard", "on_topic": true, "exact_topic": false, "snippet": "水晶头应按线序压接，先核对信息点"},
		map[string]any{"index": 2, "score": 3, "page_type": "novel", "on_topic": true, "snippet": "章节全文免费阅读不收费"},
	}, title)
	if len(kept) != 1 || str(kept[0]["title"]) != "综合布线系统工程验收规范" {
		t.Fatal(kept, dropped)
	}
	if len(dropped) != 1 || str(dropped[0]["reason"]) == "" || str(dropped[0]["url"]) == "" || str(dropped[0]["title"]) == "" {
		t.Fatal(dropped)
	}
	var completion llm.Completion
	stampSourceTrace(&completion, kept, dropped, false, 2, nil)
	last := completion.Attempts[len(completion.Attempts)-1]
	notes, _ := last["rejected"].([]string)
	if len(notes) != 1 || !strings.Contains(notes[0], "小说下载") || !strings.Contains(notes[0], str(dropped[0]["reason"])) || !strings.Contains(notes[0], "https://www.example.com/novel") {
		t.Fatal(notes, last)
	}
	flat := normalizeTable(map[string]any{
		"headers": []any{"项目", "说明"},
		"rows":    []any{"线序|按568B"},
	})
	if flat == nil || str(asList(asList(flat["rows"])[0])[1]) != "按568B" {
		t.Fatal(flat)
	}
}
