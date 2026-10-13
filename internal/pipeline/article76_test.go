package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/content"
	"github.com/leoa81761-ops/wzgx/internal/editorial"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/render"
	"github.com/leoa81761-ops/wzgx/internal/topics"
)

// Articles 76 and 77 were held short and scored 1-6 after the structure step
// flattened a table, restarted list numbers, and jammed an FAQ into one paragraph.
func TestArticle76And77StructureAndLength(t *testing.T) {
	outline := []string{"先看清问题", "照着做"}
	draft := map[string]any{
		"title": "综合布线六类线测试失败",
		"sections": []any{
			map[string]any{
				"heading": "先看清问题",
				"paragraphs": []any{
					"步骤注意事项1. 用测试仪核对六类线线序，注意不要拉扯2. 测衰减，注意留3米余量",
					"六类线测不通怎么办？答：先看水晶头。线序反了怎么处理？答：按568B重打。",
					"答：先换一根跳线。",
				},
				"list": []any{"1. 先看线序", "1. 再看长度", "2. 记下读数"},
			},
			map[string]any{
				"heading":    "照着做",
				"paragraphs": []any{"2. 先固定线槽", "2. 再测通断"},
				"list":       []any{},
			},
			map[string]any{
				"heading":    "额外的一步",
				"paragraphs": []any{"1. 大纲里没有的步骤", "2. 再加一步"},
				"list":       []any{},
			},
		},
	}
	out := editorial.MaterializeMarkup(draft, outline)
	blob := bodyPlain(out)
	if strings.Contains(blob, "步骤注意事项1") || strings.Contains(blob, "答：先换一根跳线") || strings.Contains(blob, "额外的一步") || strings.Contains(blob, "大纲里没有的步骤") {
		t.Fatal(blob)
	}
	sections := asList(out["sections"])
	if len(sections) != 2 {
		t.Fatalf("sections %d", len(sections))
	}
	first := sections[0].(map[string]any)
	table, _ := first["table"].(map[string]any)
	if table == nil {
		t.Fatal("table was left as a run-on paragraph")
	}
	headers := asList(table["headers"])
	if str(headers[0]) != "步骤" || str(headers[1]) != "注意事项" {
		t.Fatal(headers)
	}
	rows := asList(table["rows"])
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	list := asList(first["list"])
	if len(list) != 3 || str(list[0]) != "1. 先看线序" || str(list[1]) != "2. 再看长度" || str(list[2]) != "3. 记下读数" {
		t.Fatal(list)
	}
	var questions, answers int
	for _, item := range asList(first["paragraphs"]) {
		text := str(item)
		if strings.Contains(text, "？") {
			questions++
		}
		if strings.Contains(text, "答：") {
			answers++
		}
		if strings.Count(text, "？") > 1 {
			t.Fatal("faq still jammed", text)
		}
	}
	if questions != 2 || answers != 2 {
		t.Fatal(asList(first["paragraphs"]))
	}
	second := sections[1].(map[string]any)
	steps := asList(second["list"])
	if len(steps) != 2 || str(steps[0]) != "1. 先固定线槽" || str(steps[1]) != "2. 再测通断" {
		t.Fatal(steps, asList(second["paragraphs"]))
	}
	md := render.Markdown(out)
	html := render.RenderHTML(out, "simple", false, false)
	if !strings.Contains(md, "| 步骤 | 注意事项 |") || !strings.Contains(md, "1. 先看线序") || strings.Contains(md, "1. 再看长度") {
		t.Fatal(md)
	}
	if !strings.Contains(html, "<table") || !strings.Contains(html, "1. 先看线序") || strings.Contains(html, "额外的一步") {
		t.Fatal(html)
	}

	flat := map[string]any{
		"sections": []any{map[string]any{
			"heading":    "先看清问题",
			"paragraphs": []any{"步骤注意事项1. 被压成了一句话2. 第二行也不见了"},
			"list":       []any{"只有一条"},
		}},
	}
	kept := editorial.KeepMarkup(out, flat)
	keptTable, _ := asList(kept["sections"])[0].(map[string]any)["table"].(map[string]any)
	keptList := asList(asList(kept["sections"])[0].(map[string]any)["list"])
	if keptTable == nil || len(asList(keptTable["rows"])) != 2 || len(keptList) != 3 {
		t.Fatal("polish flattened the markup", bodyPlain(kept))
	}

	planned := assignOutlineBudgets(map[string]any{"points": []any{
		map[string]any{"heading": "先看清问题", "intent": "说明", "word_budget": 100},
		map[string]any{"heading": "照着做", "intent": "步骤", "word_budget": 100},
		map[string]any{"heading": "少踩一个坑", "intent": "提醒", "word_budget": 100},
	}}, 800)
	sum := 0
	for _, raw := range asList(planned["points"]) {
		sum += intOf(raw.(map[string]any)["word_budget"])
	}
	if sum != 800 {
		t.Fatal(sum)
	}
}

func TestArticle76QueriesAreNarrowAndJunkIsDropped(t *testing.T) {
	queries := backupQueries(map[string]any{
		"topic_title": "施工坑点：更稳妥的做法",
		"subtopic":    "综合布线六类线测试失败常见原因",
		"keywords":    "六类线,测试仪",
		"direction":   "弱电智能化",
	})
	if len(queries) < 2 || len(queries) > 4 {
		t.Fatal(queries)
	}
	joined := strings.Join(queries, "\n")
	if strings.Contains(joined, "更稳妥的做法") || strings.Contains(joined, "施工坑点") || !strings.Contains(joined, "综合布线六类线") {
		t.Fatal(queries)
	}
	for _, query := range queries {
		if utf8.RuneCountInString(query) > 32 {
			t.Fatal(query)
		}
	}
	title := topics.FitTitle("施工坑点：更稳妥的做法", "避坑辟谣", "", "custom", "综合布线 六类线 测试失败")
	if strings.Contains(title, "更稳妥") || strings.Contains(title, "施工坑点") || !strings.Contains(title, "综合布线") {
		t.Fatal(title)
	}
	if content.OffTopicPage("https://www.example.com/zb", "综合布线招标公告") == "" {
		t.Fatal("tender")
	}
	if content.OffTopicPage("https://weread.qq.com/book/1", "六类线电子书") == "" {
		t.Fatal("ebook")
	}
	if !content.SearchResultURL("https://std.samr.gov.cn/gb/download/1") {
		t.Fatal("standard download")
	}
	if content.SearchResultURL("https://std.samr.gov.cn/gb") {
		t.Fatal("standard page should stay")
	}
}

func TestBingNoiseNeedsEntityOverlap(t *testing.T) {
	_, kept, dropped, _ := takeSources(t.Context(), nil, "笔记", []map[string]any{
		{"title": "六类线测试失败现场记录", "snippet": "综合布线里六类线测不通", "url": "https://www.example.com/cat6"},
		{"title": "办公室网络布线的现场记录", "snippet": "线槽先看清楚再放线", "url": "https://www.example.com/office"},
		{"title": "英语测试失败的常见原因", "snippet": "考试没通过就再练", "url": "https://www.example.com/exam"},
		{"title": "国家工程标准公告", "snippet": "住房城乡建设发布通知", "url": "https://www.example.com/notice"},
		{"title": "智能化新闻", "snippet": "今日热点", "url": "https://www.example.com/news"},
	}, content.DefaultSourcePolicy(), "综合布线六类线测试失败常见原因")
	seen := map[string]bool{}
	for _, row := range kept {
		seen[str(row["url"])] = true
	}
	for _, url := range []string{"https://www.example.com/cat6", "https://www.example.com/office"} {
		if !seen[url] {
			t.Fatal("missing", url, kept, dropped)
		}
	}
	for _, url := range []string{"https://www.example.com/exam", "https://www.example.com/notice", "https://www.example.com/news"} {
		if seen[url] {
			t.Fatal("kept off-topic", url, kept)
		}
	}
	for _, row := range dropped {
		if str(row["url"]) == "https://www.example.com/exam" && str(row["reason"]) != "和选题无关" {
			t.Fatal(row)
		}
	}
}

func TestExpandStopsAfterTwoFailedPasses(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		text := strings.Repeat("现场补了一句。", n)
		writeChoice(w, map[string]any{
			"title": "短稿", "summary": "摘要",
			"sections": []any{map[string]any{"heading": "现场案例", "paragraphs": []any{text}, "list": []any{}}},
			"closing":  "结束", "sources": []any{},
		})
	}))
	defer srv.Close()
	draft := map[string]any{
		"title": "短稿", "summary": "摘要",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"只有一句。"}, "list": []any{}}},
		"closing":  "结束", "sources": []any{},
	}
	gw := llm.NewGateway(false, srv.Client())
	out, _, _, still := fitBody(context.Background(), gw, []llm.Provider{{
		Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 10 * time.Second,
	}}, map[string]any{"word_target": 800}, draft, 800)
	if n != 2 || !still || !strings.Contains(bodyPlain(out), "只有一句。") {
		t.Fatalf("passes %d still %v runes %d", n, still, content.BodyRunes(out))
	}
}

func TestExperienceDropsWarmFillerAndStrayTips(t *testing.T) {
	draft := map[string]any{"sections": []any{map[string]any{
		"paragraphs": []any{"今晚的气氛很温馨。", "用测试仪在机房把六类线测3遍，衰减超过3分贝就换一根。"},
		"list":       []any{"记得多喝水", "用测试仪测3遍线序"},
	}}}
	out := editorial.DropStrayTips(editorial.StripWarmFiller(draft))
	text := bodyPlain(out)
	if strings.Contains(text, "温馨") || strings.Contains(text, "记得多喝水") {
		t.Fatal(text)
	}
	if !strings.Contains(text, "测试仪") || !strings.Contains(text, "3") {
		t.Fatal(text)
	}
	prompt := llm.StageMessages("正文", map[string]any{"writing_mode": "experience", "column_structure": "steps"})[0]["content"]
	if !strings.Contains(prompt, "数字") || !strings.Contains(prompt, "工具") || !strings.Contains(prompt, "场景") || strings.Contains(prompt, "至少写出 5 条") {
		t.Fatal(prompt)
	}
}
