package pipeline

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/jobs"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/model"
)

func TestTenTopicsPerColumnShareNoTemplate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		payload := payloadOf(req)
		switch stageOf(req) {
		case "选题":
			for _, key := range []string{"account_name", "column_positioning", "recent_titles", "season_hint", "trend_hint"} {
				if strings.TrimSpace(str(payload[key])) == "" && key != "recent_titles" {
					t.Errorf("选题缺少 %s", key)
				}
			}
			if _, ok := payload["recent_titles"]; !ok {
				t.Errorf("选题缺少 recent_titles")
			}
			name := str(payload["account_name"])
			hint := str(payload["trend_hint"])
			var seeds []string
			if strings.Contains(name, "奶奶") {
				seeds = []string{"被子晒到窗台", "灶台边上的抹布", "门口的拖鞋", "窗台的花", "晚饭前的碗"}
			} else {
				seeds = []string{"面板高度", "信息点标签", "线槽盖板", "配线架端口", "弱电间门禁"}
			}
			var cands []any
			for _, seed := range seeds {
				cands = append(cands, map[string]any{
					"title": name + "：" + seed + hint,
					"angle": seed,
					"pain":  seed,
				})
			}
			writeChoice(w, map[string]any{"title": name + "：" + seeds[0] + hint, "angle": seeds[0], "candidates": cands})
		case "选题判断":
			cands, _ := payload["candidates"].([]any)
			winner := ""
			if len(cands) > 0 {
				row, _ := cands[0].(map[string]any)
				winner = str(row["title"])
			}
			writeChoice(w, map[string]any{"winner": winner})
		default:
			t.Errorf("unexpected stage %s", stageOf(req))
			writeChoice(w, map[string]any{})
		}
	}))
	defer srv.Close()
	gateway := llm.NewGateway(false, srv.Client())
	providers := []llm.Provider{{
		Name: "选题", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 5 * time.Second,
	}}
	accounts := []map[string]any{
		{"account_name": "弱电陈工", "column": "现场笔记", "column_positioning": "把现场的一步写给做工程的人", "direction": "弱电智能化", "tone": "务实", "audience": "做工程的读者"},
		{"account_name": "奶奶不当家", "column": "奶奶的一天", "column_positioning": "把家里的一件事说清楚", "direction": "家庭日常", "tone": "奶奶的口气", "audience": "跟着奶奶过日子的读者"},
	}
	banned := []string{"测到一半对不上", "不知道先查哪一段", "拐杖放在顺手", "孙子测水表"}
	seen := map[string]map[string]bool{}
	for _, account := range accounts {
		name := str(account["account_name"])
		seen[name] = map[string]bool{}
		for i := 0; i < 10; i++ {
			brief := map[string]any{}
			for key, value := range account {
				brief[key] = value
			}
			brief["recent_titles"] = []any{"上一篇已经写过的事"}
			brief["season_hint"] = "十月"
			brief["trend_hint"] = strconv.Itoa(i)
			title, err := ChooseTopic(t.Context(), gateway, providers, brief)
			if err != nil || title == "" {
				t.Fatal(name, err, title)
			}
			if seen[name][title] {
				t.Fatal("repeated", title)
			}
			seen[name][title] = true
			if !strings.Contains(title, name) {
				t.Fatal(title)
			}
			for _, phrase := range banned {
				if strings.Contains(title, phrase) {
					t.Fatal(title)
				}
			}
		}
		if len(seen[name]) != 10 {
			t.Fatal(name, len(seen[name]))
		}
	}
	for title := range seen["弱电陈工"] {
		if seen["奶奶不当家"][title] {
			t.Fatal("shared", title)
		}
		if strings.Contains(title, "被子") || strings.Contains(title, "拖鞋") {
			t.Fatal(title)
		}
	}
	for title := range seen["奶奶不当家"] {
		if strings.Contains(title, "面板") || strings.Contains(title, "信息点") || strings.Contains(title, "水表") || strings.Contains(title, "综合布线") {
			t.Fatal(title)
		}
	}
}

func TestOutlineMissBlocksBeforeDraft(t *testing.T) {
	var draftCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		switch stageOf(req) {
		case "选题":
			writeChoice(w, map[string]any{"title": "面板高度和信息点对不上", "angle": "先核对面板"})
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{
				map[string]any{"heading": "今天的天气", "intent": "闲聊"},
				map[string]any{"heading": "另一件完全无关的事", "intent": "跑题"},
			}, "closing": "结束"})
		case "正文":
			draftCalls++
			writeChoice(w, map[string]any{"title": "不该写到这里", "summary": "摘要", "sections": []any{map[string]any{"heading": "正文", "paragraphs": []any{"不该出现。"}, "list": []any{}}}, "closing": "结束"})
		default:
			writeChoice(w, map[string]any{"title": "面板高度和信息点对不上", "summary": "摘要", "sections": []any{map[string]any{"heading": "面板高度", "paragraphs": []any{"不该出现。"}, "list": []any{}}}, "closing": "结束"})
		}
	}))
	defer srv.Close()
	article := runAccountArticle(t, srv, "outline-block@example.com", "弱电陈工", "弱电智能化")
	if draftCalls != 0 {
		t.Fatalf("draft calls %d", draftCalls)
	}
	if article.Status != "held" || !strings.Contains(article.DeliveryNote, "大纲") {
		t.Fatalf("%s %s", article.Status, article.DeliveryNote)
	}
}

func TestBothAccountsKeepTheirOwnTopic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		payload := payloadOf(req)
		name := str(payload["account_name"])
		title := str(payload["topic_title"])
		if title == "" {
			if strings.Contains(name, "奶奶") {
				title = "奶奶不当家：被子晒到窗台"
			} else {
				title = "弱电陈工：面板高度对不上"
			}
		}
		switch stageOf(req) {
		case "选题":
			var seeds []string
			if strings.Contains(name, "奶奶") {
				seeds = []string{"被子晒到窗台", "门口的拖鞋", "窗台的花", "晚饭前的碗", "灶台边的抹布"}
			} else {
				seeds = []string{"面板高度对不上", "信息点标签", "线槽盖板", "配线架端口", "弱电间记录"}
			}
			var cands []any
			for _, seed := range seeds {
				cands = append(cands, map[string]any{"title": name + "：" + seed, "angle": seed, "pain": seed})
			}
			writeChoice(w, map[string]any{"title": name + "：" + seeds[0], "angle": seeds[0], "candidates": cands})
		case "选题判断":
			cands, _ := payload["candidates"].([]any)
			winner := ""
			if len(cands) > 0 {
				row, _ := cands[0].(map[string]any)
				winner = str(row["title"])
			}
			writeChoice(w, map[string]any{"winner": winner})
		case "大纲":
			writeChoice(w, map[string]any{"hook": "开头", "points": []any{
				map[string]any{"heading": title + "先看哪里", "intent": title},
				map[string]any{"heading": title + "做完再收", "intent": title},
			}, "closing": "结束"})
		case "正文":
			heading := str(payload["section_heading"])
			if heading == "" {
				heading = title
			}
			index := 0
			switch n := payload["section_index"].(type) {
			case float64:
				index = int(n)
			}
			if index == 1 {
				intro := "进场或进门时先把要看的东西摊开。"
				answer := "先看左上那一处。"
				if strings.Contains(name, "奶奶") {
					intro = "起床后先把要收的一件摊开。"
					answer = "先把被子的一角拉平。"
				}
				writeChoice(w, map[string]any{
					"title": title, "summary": "摘要",
					"sections": []any{map[string]any{
						"heading":    heading,
						"paragraphs": []any{intro, answer},
						"qa": []any{
							map[string]any{"question": title + "先看哪里？", "answer": "答：" + answer + "超过一半先停下。"},
							map[string]any{"question": title + "先看哪里？", "answer": "这是重复的问题。"},
							map[string]any{"question": title + "第二步看什么？", "answer": "再看旁边那一处。两轮以内不要收工。"},
							map[string]any{"question": title + "对不上怎么办？", "answer": "先停下，把眼前这一处标出来。"},
							map[string]any{"question": title + "收工前留什么？", "answer": "留一张写了结果的纸条。"},
							map[string]any{"question": title + "第二天还看什么？", "answer": "再看一遍昨天标出的那一处。"},
						},
						"list": []any{},
					}},
					"closing": "结束", "sources": []any{},
				})
				return
			}
			writeChoice(w, map[string]any{
				"title": title, "summary": "摘要",
				"sections": []any{map[string]any{
					"heading":    heading,
					"paragraphs": []any{"做完这一处再收。"},
					"list":       []any{},
					"table": map[string]any{
						"headers": []any{"项目", "说明"},
						"rows":    []any{[]any{"结果", "按现场记录。超过一半要重测。两轮以内不要收工。"}},
					},
				}},
				"closing": "结束", "sources": []any{},
			})
		case "标题":
			writeChoice(w, map[string]any{"titles": []any{map[string]any{"title": title}}})
		default:
			echoDraft(w, payload, map[string]any{
				"title": title, "summary": "摘要",
				"sections": []any{map[string]any{"heading": title, "paragraphs": []any{"做完这一处再收。"}, "list": []any{}}},
				"closing":  "结束", "sources": []any{},
			})
		}
	}))
	defer srv.Close()
	weak := runAccountArticle(t, srv, "weak-account@example.com", "弱电陈工", "弱电智能化")
	home := runAccountArticle(t, srv, "grandma-account@example.com", "奶奶不当家", "家庭日常")
	if weak.Status != "in_draft" || !strings.Contains(weak.Title, "弱电陈工") || !strings.Contains(weak.Title, "面板") {
		t.Fatalf("weak %s %s %s", weak.Status, weak.Title, weak.DeliveryNote)
	}
	if home.Status != "in_draft" || !strings.Contains(home.Title, "奶奶不当家") || !strings.Contains(home.Title, "被子") {
		t.Fatalf("home %s %s %s", home.Status, home.Title, home.DeliveryNote)
	}
	if weak.Title == home.Title {
		t.Fatal(weak.Title)
	}
	for _, article := range []*model.Article{weak, home} {
		html := article.BodyHTML
		if !strings.Contains(html, "<h2") || strings.Contains(html, "答：") || strings.Contains(html, "两轮以内") || strings.Contains(html, "超过一半") || strings.Contains(html, "这是重复的问题") {
			t.Fatal(article.Title, html)
		}
		if strings.Contains(html, "测到一半") || strings.Contains(html, "不知道先查哪一段") {
			t.Fatal(html)
		}
		if !strings.Contains(html, "按现场记录") {
			t.Fatal(html)
		}
	}
	if strings.Contains(home.BodyHTML, "面板") || strings.Contains(home.BodyHTML, "水表") || strings.Contains(home.BodyHTML, "综合布线") || strings.Contains(home.Title, "水表") {
		t.Fatal(home.Title, home.BodyHTML)
	}
	if strings.Contains(weak.BodyHTML, "被子") || strings.Contains(weak.Title, "被子") {
		t.Fatal(weak.Title, weak.BodyHTML)
	}
	if strings.Count(home.BodyHTML, "先把被子的一角拉平") != 1 {
		t.Fatal(home.BodyHTML)
	}
	if strings.Count(weak.BodyHTML, "先看左上那一处") != 1 {
		t.Fatal(weak.BodyHTML)
	}
}

func TestJudgeShapesDoNotHoldAnEmptyTitle(t *testing.T) {
	accounts := []struct {
		name, direction string
		seeds           []string
	}{
		{"弱电陈工", "弱电智能化", []string{"面板高度对不上", "信息点标签", "线槽盖板", "配线架端口", "弱电间记录"}},
		{"奶奶不当家", "家庭日常", []string{"被子晒到窗台", "门口的拖鞋", "窗台的花", "晚饭前的碗", "灶台边的抹布"}},
	}
	type shape struct {
		name  string
		index int
		body  func(titles []string) string
	}
	shapes := []shape{
		{"index", 1, func(titles []string) string { return `{"winner":2}` }},
		{"title", 2, func(titles []string) string { return `{"winner":"` + titles[2] + `"}` }},
		{"json", 3, func(titles []string) string {
			return `{"winner":{"title":"` + titles[3] + `"}}`
		}},
		{"twenty", 0, func(titles []string) string { return strings.Repeat("甲", 20) }},
		{"thirty-six", 0, func(titles []string) string { return strings.Repeat("乙", 36) }},
	}
	n := 0
	for _, account := range accounts {
		for _, item := range shapes {
			n++
			titles := make([]string, len(account.seeds))
			for i, seed := range account.seeds {
				titles[i] = account.name + "：" + seed
			}
			body := item.body(titles)
			if item.name == "twenty" && utf8.RuneCountInString(body) != 20 {
				t.Fatal(utf8.RuneCountInString(body))
			}
			if item.name == "thirty-six" && utf8.RuneCountInString(body) != 36 {
				t.Fatal(utf8.RuneCountInString(body))
			}
			var topicCalls int
			var outlineTitle string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				var req map[string]any
				_ = json.Unmarshal(raw, &req)
				payload := payloadOf(req)
				switch stageOf(req) {
				case "选题":
					topicCalls++
					if _, ok := payload["recent_titles"]; !ok {
						t.Errorf("选题缺少 recent_titles")
					}
					if _, ok := payload["avoid_topics"]; !ok {
						t.Errorf("选题缺少 avoid_topics")
					}
					var cands []any
					for _, title := range titles {
						cands = append(cands, map[string]any{"title": title, "angle": title, "pain": title})
					}
					writeChoice(w, map[string]any{"title": titles[0], "angle": account.seeds[0], "candidates": cands})
				case "选题判断":
					writeRawContent(w, body)
				case "大纲":
					outlineTitle = str(payload["topic_title"])
					writeChoice(w, map[string]any{"hook": "开头", "points": []any{
						map[string]any{"heading": outlineTitle + "先看哪里", "intent": outlineTitle},
						map[string]any{"heading": outlineTitle + "做完再收", "intent": outlineTitle},
					}, "closing": "结束"})
				case "正文":
					heading := str(payload["section_heading"])
					if heading == "" {
						heading = outlineTitle
					}
					index := 0
					switch num := payload["section_index"].(type) {
					case float64:
						index = int(num)
					}
					paragraph := "做完这一处再收。"
					if index == 1 {
						paragraph = "进场时先把要看的那一处摊开。"
						if strings.Contains(account.name, "奶奶") {
							paragraph = "起床后先把要收的一件摊开。"
						}
					}
					writeChoice(w, map[string]any{
						"title": outlineTitle, "summary": "摘要",
						"sections": []any{map[string]any{
							"heading": heading, "paragraphs": []any{paragraph}, "list": []any{},
						}},
						"closing": "结束", "sources": []any{},
					})
				case "标题":
					writeChoice(w, map[string]any{"titles": []any{map[string]any{"title": outlineTitle}}})
				default:
					echoDraft(w, payload, map[string]any{
						"title": outlineTitle, "summary": "摘要",
						"sections": []any{map[string]any{"heading": outlineTitle, "paragraphs": []any{"做完这一处再收。"}, "list": []any{}}},
						"closing":  "结束", "sources": []any{},
					})
				}
			}))
			email := "judge-" + strconv.Itoa(n) + "@example.com"
			article := runAccountArticle(t, srv, email, account.name, account.direction)
			srv.Close()
			want := titles[item.index]
			if topicCalls != 1 {
				t.Fatalf("%s %s topic calls %d", account.name, item.name, topicCalls)
			}
			if outlineTitle == "" || outlineTitle != want {
				t.Fatalf("%s %s outline %q want %q", account.name, item.name, outlineTitle, want)
			}
			if article.Title == "" || strings.Contains(article.DeliveryNote, "大纲没有覆盖标题") {
				t.Fatalf("%s %s title %q note %q status %s", account.name, item.name, article.Title, article.DeliveryNote, article.Status)
			}
			if !strings.Contains(article.Title, account.seeds[item.index]) {
				t.Fatalf("%s %s title %q", account.name, item.name, article.Title)
			}
		}
	}
}

func writeRawContent(w http.ResponseWriter, content string) {
	raw, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
	})
	_, _ = w.Write(raw)
}

func runAccountArticle(t *testing.T, srv *httptest.Server, email, name, direction string) *model.Article {
	t.Helper()
	st, deps := openDeps(t, "live", srv.Client())
	ctx := t.Context()
	if err := st.InsertService(ctx, nil, &model.ServiceConfig{
		ConfigType: "text_model", Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", ModelName: "writer",
		Enabled: true, TimeoutSeconds: 10, UpdatedAt: time.Now().UTC(), ExtraHeaders: map[string]string{}, ExtraParams: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	user, account, plan := seedPlan(t, st, email)
	account.Name = name
	if err := st.UpdateAccount(ctx, nil, account); err != nil {
		t.Fatal(err)
	}
	plan.Direction = direction
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
	return article
}
