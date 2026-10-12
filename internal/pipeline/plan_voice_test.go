package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/store"
	"github.com/leoa81761-ops/wzgx/internal/topics"
)

func TestAccountVoiceReachesThePrompt(t *testing.T) {
	in := (&work{
		Audience: "物业工程", Keywords: "验收,机房", BannedWords: "1000V", TopicMode: "rotate", ImageMode: "none",
	}).input()
	fields := voiceFields(in)
	if fields["reader_profile"] != "物业工程" || fields["audience"] != nil || fields["keywords"] != "验收,机房" || fields["banned_words"] != "1000V" || fields["topic_mode"] != "rotate" {
		t.Fatal(fields)
	}
	voiced := (&work{Direction: "弱电智能化", Tone: "我的语气", Pack: store.VerticalPack{Key: "elv"}}).input()
	fields = voiceFields(fillPersona(voiced))
	if fields["tone"] != "我的语气" || fields["signatures"] == "" || fields["taboos"] == "" {
		t.Fatal(fields)
	}
	if hotAllowed(&in) {
		t.Fatal("rotate should skip hot topics")
	}
	auto := in
	auto.TopicMode = "auto"
	auto.Tier = "premium"
	if !hotAllowed(&auto) {
		t.Fatal("auto still allows hot topics")
	}
	words := mergeBanned([]string{"诊断"}, "收益，内部价,诊断")
	if len(words) != 3 || words[1] != "收益" || words[2] != "内部价" {
		t.Fatal(words)
	}
}

func TestImagePlanModes(t *testing.T) {
	use, cover, inline := ImagePlan("auto", false, true, 2)
	if !use || !cover || inline != 2 {
		t.Fatal(use, cover, inline)
	}
	use, cover, inline = ImagePlan("none", true, true, 2)
	if use || cover || inline != 0 {
		t.Fatal(use, cover, inline)
	}
	use, cover, inline = ImagePlan("cover", false, true, 2)
	if !use || !cover || inline != 0 {
		t.Fatal(use, cover, inline)
	}
	use, _, inline = ImagePlan("asset", true, true, 2)
	if use || inline != 0 {
		t.Fatal(use, inline)
	}
	use, cover, inline = ImagePlan("asset", false, true, 2)
	if !use || !cover || inline != 2 {
		t.Fatal(use, cover, inline)
	}
}

func TestMockRewriteKeepsParagraphsAndLeavesTitlesWhole(t *testing.T) {
	first := "验收时核对线槽，是否盖好。"
	second := "拍完留在当天的记录里[1]。"
	pinned := "验收先核对这一项，这句话写得比较长，不能被截成二十八个字"
	whole := "验收这件事不必一次做完，也不必为了凑标题去截断句子本身，请原样保留"
	draft := map[string]any{
		"title":      "模型原来的标题",
		"alt_titles": []any{"关于验收的另一句"},
		"sections": []any{
			map[string]any{"heading": "", "paragraphs": []any{first}, "list": []any{"不要删掉这条列表。"}},
			map[string]any{"heading": "", "paragraphs": []any{second}},
		},
		"closing": "结束。",
		"sources": []any{map[string]any{"url": "https://www.gov.cn/a", "title": "原文"}},
	}
	gateway := llm.NewGateway(true, nil)
	providers := map[string][]llm.Provider{
		"review": {{Name: "mock", Protocol: "openai", Model: "mock"}},
	}
	out, _, _ := finishPiece(context.Background(), gateway, providers, draft, Input{
		Direction: "弱电智能化", WordTarget: 800, SpecifiedTitle: pinned, Tone: "务实，少形容词",
		Pack: store.VerticalPack{Key: "elv", RedLines: "包过验收"},
	}, "验收", map[string]any{"subtopic": "验收", "tone": "务实，少形容词"}, nil)
	if out["title"] != pinned {
		t.Fatal(out["title"])
	}
	raw, _ := json.Marshal(out)
	text := string(raw)
	for _, piece := range []string{first, second, "[1]", "https://www.gov.cn/a", "不要删掉这条列表。", "结束。", "，"} {
		if !strings.Contains(text, piece) {
			t.Fatal(piece, text)
		}
	}
	if strings.Contains(text, whole) {
		t.Fatal("titles over 30 runes are dropped, not clipped", text)
	}
	alts, _ := out["alt_titles"].([]any)
	for _, alt := range alts {
		title := ""
		switch row := alt.(type) {
		case string:
			title = row
		case map[string]any:
			title, _ = row["title"].(string)
			if row["hook"] == nil || row["consistent"] == nil {
				t.Fatal("scores missing", row)
			}
		}
		if title == whole || utf8.RuneCountInString(title) > 30 {
			t.Fatal(title)
		}
		if strings.Contains(title, "写给正在做") || strings.Contains(title, "震惊") || strings.Contains(title, "稳赚") {
			t.Fatal(title)
		}
	}
}

func TestHookPolishKeepsThePairwiseChoice(t *testing.T) {
	first := "验收时核对线槽，是否盖好。"
	draft := map[string]any{
		"title":    "模型原来的标题",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{first}, "list": []any{}}},
		"closing":  "可以先记住这一步。欢迎留言说说你的做法。",
	}
	gateway := llm.NewGateway(true, nil)
	providers := map[string][]llm.Provider{
		"review": {{Name: "mock", Protocol: "openai", Model: "mock", ExtraParams: map[string]any{"_mock_hook_choice": "rewrite"}}},
	}
	out, _, trace := finishPiece(context.Background(), gateway, providers, draft, Input{
		Direction: "弱电智能化", WordTarget: 800,
	}, "验收", map[string]any{"subtopic": "验收"}, nil)
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), first) || !strings.Contains(string(raw), "为什么先把这一步说清楚？") {
		t.Fatal(string(raw))
	}
	saw := false
	for _, row := range trace {
		if row["stage"] == "hook" {
			saw = true
		}
	}
	if !saw {
		t.Fatal(trace)
	}
}

func TestMotifRewriteRunsWhenReviewFlagsIt(t *testing.T) {
	body := "爷爷奶奶眼睛里闪闪发光，孩子也跟着笑。[1]"
	draft := map[string]any{
		"title": "两口人把玩具收进盒子",
		"_quality": map[string]any{
			"hook": 5, "headings": true, "closing": true, "motif": true,
		},
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{body}}},
		"closing":  "可以先记住这一步。欢迎留言说说你的做法。",
		"sources":  []any{map[string]any{"name": "示例", "url": "https://www.gov.cn/a"}},
	}
	gateway := llm.NewGateway(true, nil)
	providers := map[string][]llm.Provider{
		"review": {{Name: "mock", Protocol: "openai", Model: "mock"}},
	}
	out, _, trace := finishPiece(context.Background(), gateway, providers, draft, Input{
		Direction: "家庭生活", WordTarget: 800,
	}, "收纳", map[string]any{"subtopic": "收纳"}, nil)
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), body) || !strings.Contains(string(raw), "https://www.gov.cn/a") {
		t.Fatal(string(raw))
	}
	saw := false
	for _, row := range trace {
		if row["stage"] == "motif" && row["ok"] == true {
			saw = true
		}
	}
	if !saw {
		t.Fatal(trace)
	}
	quiet := map[string]any{
		"title":    "两口人把玩具收进盒子",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{body}}},
		"closing":  "可以先记住这一步。欢迎留言说说你的做法。",
	}
	_, _, quietTrace := finishPiece(context.Background(), gateway, providers, quiet, Input{
		Direction: "家庭生活", WordTarget: 800,
	}, "收纳", map[string]any{}, nil)
	for _, row := range quietTrace {
		if row["stage"] == "motif" {
			t.Fatal(quietTrace)
		}
	}
}

func TestTitleCandidatesDropSensitiveWords(t *testing.T) {
	draft := map[string]any{
		"title":    "模型原来的标题",
		"sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"验收时核对线槽，是否盖好。"}}},
		"closing":  "可以先记住这一步。欢迎留言说说你的做法。",
	}
	gateway := llm.NewGateway(true, nil)
	providers := map[string][]llm.Provider{
		"review": {{Name: "mock", Protocol: "openai", Model: "mock"}},
	}
	out, _, trace := finishPiece(context.Background(), gateway, providers, draft, Input{
		Direction: "弱电智能化", WordTarget: 800,
	}, "验收", map[string]any{"subtopic": "内部消息"}, []string{"内部消息"})
	if out["title"] != "模型原来的标题" {
		t.Fatal(out["title"])
	}
	raw, _ := json.Marshal(out["alt_titles"])
	if strings.Contains(string(raw), "内部消息") || strings.Contains(str(out["title"]), "内部消息") {
		t.Fatal(out["title"], out["alt_titles"])
	}
	sawTitles := false
	for _, row := range trace {
		if row["stage"] == "titles" {
			sawTitles = true
		}
	}
	if !sawTitles {
		t.Fatal(trace)
	}
}

func TestFailedRewriteKeepsTheDraft(t *testing.T) {
	first := "验收时核对线槽，是否盖好。"
	draft := map[string]any{
		"title":    "模型原来的标题",
		"sections": []any{map[string]any{"heading": "", "paragraphs": []any{first}}},
		"closing":  "结束。",
		"sources":  []any{map[string]any{"url": "https://www.gov.cn/a"}},
	}
	gateway := llm.NewGateway(true, nil)
	providers := map[string][]llm.Provider{
		"draft": {{Name: "mock", Protocol: "openai", Model: "mock", ExtraParams: map[string]any{"_mock_fail": true}}},
	}
	out, _, _ := finishPiece(context.Background(), gateway, providers, draft, Input{
		Direction: "弱电智能化", WordTarget: 800,
	}, "验收", map[string]any{"subtopic": "验收"}, nil)
	raw, _ := json.Marshal(out)
	text := string(raw)
	if !strings.Contains(text, first) || !strings.Contains(text, "结束。") || !strings.Contains(text, "https://www.gov.cn/a") {
		t.Fatal(text)
	}
	if strings.Contains(text, "为什么先把这一步说清楚") {
		t.Fatal(text)
	}
	if out["title"] != "模型原来的标题" {
		t.Fatal(out["title"])
	}
}

func TestSiblingCoverageChangesTheNextAngle(t *testing.T) {
	now := time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC)
	first := topics.PickTree([]string{"验收"}, "弱电智能化", "premium", nil, now)
	second := topics.PickTree([]string{"验收"}, "弱电智能化", "premium", []topics.Coverage{{
		Subtopic: first.Subtopic, Column: first.Column, Angle: first.Angle, CreatedAt: now,
	}}, now)
	if first.TopicTag == "" || first.TopicTag == second.TopicTag {
		t.Fatal(first.TopicTag, second.TopicTag)
	}
}
