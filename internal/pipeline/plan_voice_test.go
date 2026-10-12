package pipeline

import (
	"strings"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/content"
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

func TestPolishKeepsSpecifiedTitleInsideTheLengthBand(t *testing.T) {
	para := strings.Repeat("验收时先看线槽是否盖好。", 30)
	draft := map[string]any{
		"title":      "震惊！验收必看",
		"alt_titles": []any{"稳赚不赔的验收"},
		"sections":   []any{map[string]any{"heading": "", "paragraphs": []any{para}}},
		"closing":    "",
	}
	out := polishDraft(draft, Input{
		Direction: "弱电智能化", WordTarget: 200, SpecifiedTitle: "验收先核对这一项",
		Pack: store.VerticalPack{Key: "elv", RedLines: "包过验收"},
	}, topics.Choice{Subtopic: "验收", Column: "清单", Angle: "少做一件事"}, "验收")
	if out["title"] != "验收先核对这一项" {
		t.Fatal(out["title"])
	}
	if content.BodyRunes(out) > 200*130/100 {
		t.Fatal(content.BodyRunes(out))
	}
	alts, _ := out["alt_titles"].([]any)
	if len(alts) < 1 {
		t.Fatal(alts)
	}
	for _, alt := range alts {
		if strings.Contains(alt.(string), "震惊") || strings.Contains(alt.(string), "稳赚") {
			t.Fatal(alt)
		}
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
