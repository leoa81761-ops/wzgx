package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/store"
	"github.com/leoa81761-ops/wzgx/internal/topics"
)

func TestAccountVoiceReachesThePrompt(t *testing.T) {
	in := (&work{
		Audience: "物业工程", Keywords: "验收,机房", BannedWords: "1000V", TopicMode: "rotate", ImageMode: "none",
	}).input()
	fields := voiceFields(in)
	if fields["audience"] != "物业工程" || fields["keywords"] != "验收,机房" || fields["banned_words"] != "1000V" || fields["topic_mode"] != "rotate" {
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
	}, "验收", map[string]any{"subtopic": "验收", "tone": "务实，少形容词"})
	if out["title"] != pinned {
		t.Fatal(out["title"])
	}
	raw, _ := json.Marshal(out)
	text := string(raw)
	for _, piece := range []string{first, second, "[1]", "https://www.gov.cn/a", "不要删掉这条列表。", "结束。", "，", whole} {
		if !strings.Contains(text, piece) {
			t.Fatal(piece, text)
		}
	}
	alts, _ := out["alt_titles"].([]any)
	found := false
	for _, alt := range alts {
		title, _ := alt.(string)
		if title == whole {
			found = true
		}
		if strings.Contains(title, "写给正在做") || strings.Contains(title, "震惊") || strings.Contains(title, "稳赚") {
			t.Fatal(title)
		}
	}
	if !found {
		t.Fatal(alts)
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
	}, "验收", map[string]any{"subtopic": "验收"})
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
