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
)

// Article 86 failed after about 21 seconds because the topic model returned nothing.
func TestTopicRetriesEmptyThenFallsBack(t *testing.T) {
	var calls int
	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		model, _ := req["model"].(string)
		models = append(models, model)
		calls++
		switch calls {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":""}}]}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"这不是json"}}]}`))
		default:
			writeChoice(w, map[string]any{"title": "综合布线验收", "angle": "先看图纸"})
		}
	}))
	defer srv.Close()
	gateway := llm.NewGateway(false, srv.Client())
	gateway.MaxAttempts = 1
	data, _, err := completeTopic(t.Context(), gateway, []llm.Provider{{
		Name: "选题", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "grok-4.7", Timeout: 5 * time.Second,
	}}, llm.StageMessages("选题", map[string]any{"subtopic": "综合布线"}), llm.StageSchema("选题"), validateTopic)
	if err != nil || str(data["title"]) != "综合布线验收" {
		t.Fatal(err, data)
	}
	if len(models) != 3 || models[0] != "grok-4.7" || models[1] != "grok-4.7" || models[2] != llm.FallbackModel {
		t.Fatal(models)
	}
}

// A fake model writes a real article, then the sentence pass drops the unsourced figures.
// Only the gutted sections are rewritten once. The stub is held.
func TestSentenceDropRefillsOnceThenHolds(t *testing.T) {
	var refillCalls int
	sections := []string{"先看图纸", "再测通断", "记下读数"}
	bodyFor := func(heading string) string {
		switch heading {
		case "再测通断":
			return "误码率8%。时延超过20毫秒。三分之一要复测。间隔5米抽查。偏差有4倍。再核对线序编号。"
		case "记下读数":
			return "丢包率4%。抖动超过9毫秒。将近半数要返工。两端留8米。误差有6倍。最后记下现场读数。"
		default:
			return "掉线率15%。衰减超过3 dB。一半以上要重测。留出30米。损耗有2倍。先对照这一处图纸。"
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "监控画面怎么回放", "angle": "先看图纸"})
		case "资料检索":
			writeChoice(w, map[string]any{"notes": "未检索到可靠来源", "sources": []any{}})
		case "来源判断":
			writeSourceJudge(w, req)
		case "大纲":
			writeChoice(w, map[string]any{
				"hook": "开头",
				"points": []any{
					map[string]any{"heading": sections[0], "intent": "说明"},
					map[string]any{"heading": sections[1], "intent": "说明"},
					map[string]any{"heading": sections[2], "intent": "说明"},
				},
				"closing": "结束",
			})
		case "句子改写":
			writeChoice(w, map[string]any{"sentences": []any{}})
		case "正文":
			payload := map[string]any{}
			msgs, _ := req["messages"].([]any)
			if len(msgs) > 0 {
				last, _ := msgs[len(msgs)-1].(map[string]any)
				content, _ := last["content"].(string)
				_ = json.Unmarshal([]byte(llm.PayloadText(content)), &payload)
			}
			heading := str(payload["section_heading"])
			refill := strings.TrimSpace(str(payload["refill"])) != ""
			if refill {
				refillCalls++
			}
			writeChoice(w, map[string]any{
				"title": "监控画面怎么回放", "summary": "摘要", "alt_titles": []any{},
				"sections": []any{map[string]any{"heading": heading, "paragraphs": []any{bodyFor(heading)}, "list": []any{}}},
				"closing":  "结束", "sources": []any{},
			})
		default:
			writeChoice(w, map[string]any{"title": "监控画面怎么回放", "summary": "摘要", "sections": []any{}, "closing": "", "sources": []any{}})
		}
	}))
	defer srv.Close()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := t.Context()
	user, account, plan := seedPaid(t, st, "drop-sentences@example.com", srv.URL)
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
	task, _ := st.GetTask(ctx, nil, taskID)
	reason := ""
	if task != nil {
		reason = task.FailureReason + " " + task.Status
	}
	if err != nil || article == nil || article.Status != "held" || !strings.Contains(article.DeliveryNote, "正文被删得太短") || refillCalls < 1 {
		note := ""
		status := ""
		html := ""
		if article != nil {
			note = article.DeliveryNote
			status = article.Status
			html = article.BodyHTML
		}
		t.Fatalf("refill %d status %s note %s task %s html %s err %v", refillCalls, status, note, reason, html, err)
	}
	if strings.Contains(article.BodyHTML, "掉线率") || strings.Contains(article.BodyHTML, "24小时") {
		t.Fatal(article.BodyHTML)
	}
}
