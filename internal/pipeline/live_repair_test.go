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

	"github.com/leoa81761-ops/wzgx/internal/editorial"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/render"
	"github.com/leoa81761-ops/wzgx/internal/topics"
)

func TestSearchQueryUsesTopicEntities(t *testing.T) {
	payload := map[string]any{
		"topic_title":        "综合布线：对比两种常见做法",
		"subtopic":           "综合布线",
		"column":             "知识FAQ",
		"angle":              "对比两种常见做法",
		"direction":          "弱电智能化",
		"keywords":           "弱电,线槽",
		"column_positioning": "用问答把一个具体问题讲清楚",
	}
	queries := append(backupQueries(payload), shortSearchQuery(payload), focusedQuery(payload))
	for _, query := range queries {
		if strings.Contains(query, "两种做法") || strings.Contains(query, "对比两种") {
			t.Fatal(query)
		}
	}
	if !strings.Contains(shortSearchQuery(payload), "综合布线") {
		t.Fatal(shortSearchQuery(payload))
	}
}

func TestArticle73IsHeldBeforeTheDraftBox(t *testing.T) {
	var lines []any
	for i := 1; i <= 40; i++ {
		lines = append(lines, "第"+itoa(i)+"处要先看什么？答：先打开辟谣不吓人的检修口，用螺丝把盖板固定住。")
	}
	draft := map[string]any{
		"title":    "辟谣不吓人",
		"sections": []any{map[string]any{"heading": "读者还会问", "paragraphs": lines, "list": []any{}}},
		"closing":  "结束",
	}
	if !editorial.RepeatedBody(draft) {
		t.Fatal("article 73")
	}
	held, note := heldDelivery(Built{Repeated: true})
	if !held || !strings.Contains(note, "重复") || !strings.Contains(note, "未写入公众号草稿") {
		t.Fatal(held, note)
	}
	if editorial.RepeatedBody(goldenSourcedDraft()) {
		t.Fatal("golden draft")
	}
}

func TestFirstDraftIsOneCallPerSection(t *testing.T) {
	var calls int
	var sum int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		msgs, _ := req["messages"].([]any)
		system, _ := msgs[0].(map[string]any)
		if !strings.Contains(str(system["content"]), "只写一个小节") {
			t.Errorf("prompt %s", str(system["content"]))
		}
		last, _ := msgs[len(msgs)-1].(map[string]any)
		var payload map[string]any
		_ = json.Unmarshal([]byte(llm.PayloadText(str(last["content"]))), &payload)
		if str(payload["环节"]) != "正文" {
			t.Errorf("stage %s", str(payload["环节"]))
		}
		heading := str(payload["section_heading"])
		target, _ := payload["section_target"].(float64)
		sum += int(target)
		calls++
		writeChoice(w, map[string]any{
			"title": "分节来写", "summary": "摘要",
			"sections": []any{map[string]any{"heading": heading, "paragraphs": []any{"这一节专门写" + heading + "，把做法写具体。"}, "list": []any{}}},
			"closing":  "结束", "sources": []any{},
		})
	}))
	defer srv.Close()
	gw := llm.NewGateway(false, srv.Client())
	payload := map[string]any{
		"word_target": 800,
		"topic_title": "分节来写",
		"outline": map[string]any{"points": []any{
			map[string]any{"heading": "先看清问题", "intent": "说明卡在哪里"},
			map[string]any{"heading": "照着做", "intent": "写出步骤"},
			map[string]any{"heading": "少踩一个坑", "intent": "提醒一处"},
		}, "closing": "结束"},
	}
	out, _, _, _, err := writeSectionedDraft(context.Background(), gw, []llm.Provider{{
		Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 10 * time.Second,
	}}, payload, topics.Choice{}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || sum != 800 {
		t.Fatalf("calls %d target sum %d", calls, sum)
	}
	blob := bodyPlain(out)
	for _, heading := range []string{"先看清问题", "照着做", "少踩一个坑"} {
		if !strings.Contains(blob, heading) || !strings.Contains(blob, "这一节专门写"+heading) {
			t.Fatal(blob)
		}
	}
}

func TestSourcedPieceIsNotLabeledExperience(t *testing.T) {
	draft := map[string]any{
		"title":    "综合布线验收",
		"sections": []any{map[string]any{"paragraphs": []any{"合适的正文写了好几句。"}, "list": []any{}}},
		"closing":  "结束",
		"sources":  []any{map[string]any{"title": "住宅综合布线说明", "url": "https://www.gov.cn/a"}},
	}
	out := ensureInlineCitations(draft)
	if strings.Contains(bodyPlain(out), "先核对这一处") || strings.Contains(bodyPlain(out), "住宅综合布线说明") || !strings.Contains(bodyPlain(out), "[1]") {
		t.Fatal(bodyPlain(out))
	}
	warm := editorial.EnsureConcreteTips(editorial.StripWarmFiller(map[string]any{
		"sections": []any{map[string]any{"paragraphs": []any{"今晚很温馨。", "彼此很有默契。"}}},
	}))
	if strings.Contains(editorialFiller(warm), "温馨") || strings.Contains(editorialFiller(warm), "先打开检修口再看线槽") {
		t.Fatal(editorialFiller(warm))
	}
}

func editorialFiller(draft map[string]any) string {
	return bodyPlain(draft)
}

func TestSectionsStayInOutlineOrder(t *testing.T) {
	table := map[string]any{
		"headers": []any{"项目", "做法"},
		"rows":    []any{[]any{"线槽", "盖板固定后再量高度"}},
	}
	part := map[string]any{"title": "插座高度", "sections": []any{
		map[string]any{"heading": "先看问题", "paragraphs": []any{"先把检修口打开，看清线槽再动手。"}},
		map[string]any{"heading": "对照表", "paragraphs": []any{"高度写在表里。"}, "table": table},
		map[string]any{"heading": "空的一节", "paragraphs": []any{}},
		map[string]any{"heading": "收尾", "paragraphs": []any{"量完再封板。"}, "list": []any{"1. 打开检修口。", "3. 固定盖板。", "1. 量插座高度。"}},
	}}
	headings := []string{"先看问题", "对照表", "空的一节", "收尾"}
	var assembled map[string]any
	for i, heading := range headings {
		assembled = absorbSection(assembled, part, heading, i, len(headings))
	}
	assembled = compactOutline(assembled)
	assembled = editorial.RepairStructure(assembled)
	sections := asList(assembled["sections"])
	if len(sections) != 3 {
		t.Fatal(len(sections), bodyPlain(assembled))
	}
	var got []string
	for _, raw := range sections {
		section, _ := raw.(map[string]any)
		got = append(got, str(section["heading"]))
		if str(section["heading"]) == "对照表" {
			if section["table"] == nil {
				t.Fatal("table left its section")
			}
		} else if section["table"] != nil {
			t.Fatal("table moved", section["heading"])
		}
	}
	if strings.Join(got, ",") != "先看问题,对照表,收尾" {
		t.Fatal(got)
	}
	list := asList(sections[2].(map[string]any)["list"])
	if str(list[0]) != "1. 打开检修口。" || str(list[1]) != "2. 固定盖板。" || str(list[2]) != "3. 量插座高度。" {
		t.Fatal(list)
	}
	md := render.Markdown(assembled)
	tableAt := strings.Index(md, "| 线槽 | 盖板固定后再量高度 |")
	headAt := strings.Index(md, "## 对照表")
	nextAt := strings.Index(md, "## 收尾")
	if strings.Index(md, "## 先看问题") < 0 || headAt < 0 || nextAt < headAt || tableAt < headAt || tableAt > nextAt || strings.Contains(md, "空的一节") {
		t.Fatal(md)
	}
}
