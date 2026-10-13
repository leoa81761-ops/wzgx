package pipeline

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/content"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/topics"
)

func TestAssemblyKeepsNonEmptySections(t *testing.T) {
	var assembled map[string]any
	sum := 0
	for i := 0; i < 5; i++ {
		body := strings.Repeat(string(rune('甲'+i)), 220+i*30)
		heading := "第" + itoa(i+1) + "节"
		sum += utf8.RuneCountInString(body) + utf8.RuneCountInString(heading)
		part := map[string]any{
			"title": "正在生成",
			"sections": []any{
				map[string]any{"heading": heading, "paragraphs": []any{}},
				map[string]any{"heading": "写偏了", "paragraphs": []any{body}},
			},
		}
		assembled = absorbSection(assembled, part, heading, i, 5)
	}
	assembled = compactOutline(assembled)
	got := sectionsRunes(assembled)
	if got*10 < sum*9 || !bodyKeepsSections(assembled, got) || content.BodyRunes(assembled) == 0 {
		t.Fatalf("body %d sections %d written %d", content.BodyRunes(assembled), got, sum)
	}
	wiped := map[string]any{"title": "正在生成", "sections": []any{}, "closing": ""}
	restored := keepBody(wiped, assembled)
	if content.BodyRunes(restored)*10 < got*9 {
		t.Fatal(content.BodyRunes(restored), got)
	}
}

func TestOutlineRejectsDriftAndDuplicateHeadings(t *testing.T) {
	outline := anchorHeadings(map[string]any{"points": []any{
		map[string]any{"heading": "六类线怎么测", "intent": "步骤"},
		map[string]any{"heading": "今天的心情", "intent": "跑题"},
		map[string]any{"heading": "今天的心情", "intent": "再跑一次"},
		map[string]any{"heading": "六类线怎么测", "intent": "重复"},
	}}, "综合布线六类线测试失败")
	core := coreEntity("综合布线六类线测试失败")
	var headings []string
	seen := map[string]bool{}
	for _, raw := range asList(outline["points"]) {
		heading := str(raw.(map[string]any)["heading"])
		if core == "" || !strings.Contains(heading, core) || heading == "今天的心情" {
			t.Fatal(heading, core)
		}
		if seen[heading] {
			t.Fatal("duplicate", heading)
		}
		seen[heading] = true
		headings = append(headings, heading)
	}
	if len(headings) != 2 {
		t.Fatal(headings)
	}
}

func TestBuildBothColumnsStayOnTopic(t *testing.T) {
	gateway := llm.NewGateway(true, nil)
	provider := llm.Provider{Name: "模拟", Protocol: "openai", BaseURL: "mock://", Model: "writer"}
	providers := map[string][]llm.Provider{
		"topic": {provider}, "research": {provider}, "outline": {provider}, "draft": {provider}, "review": {provider},
	}
	cases := []topics.Choice{
		{Subtopic: "综合布线六类线测试", Angle: "失败时先看线序", Column: "方案拆解"},
		{Subtopic: "厨房油烟机清洗", Angle: "每月一次的步骤", Column: "生活小窍门"},
	}
	for _, topic := range cases {
		locked := topic
		built, err := Build(context.Background(), gateway, Input{
			Direction: topic.Subtopic, StyleKey: "warm", TemplateKey: "simple", WordTarget: 800,
			Tier: "standard", Locked: &locked, AccountID: 1,
		}, nil, providers, content.DefaultSourcePolicy(), time.Now())
		if err != nil {
			t.Fatal(topic.Column, err)
		}
		body := bodyPlain(built.Draft)
		if body == "" || strings.Contains(str(built.Draft["title"]), "正在生成") {
			t.Fatal(topic.Column, str(built.Draft["title"]), body)
		}
		if content.BodyRunes(built.Draft) < 800*85/100 {
			t.Fatalf("%s runes %d", topic.Column, content.BodyRunes(built.Draft))
		}
		core := coreEntity(topic.Subtopic)
		seen := map[string]bool{}
		for _, raw := range asList(built.Draft["sections"]) {
			section, _ := raw.(map[string]any)
			heading := str(section["heading"])
			if heading == "" || seen[heading] {
				t.Fatal(topic.Column, heading)
			}
			seen[heading] = true
			if core != "" && !strings.Contains(heading, core) {
				t.Fatal(topic.Column, heading, core)
			}
		}
		if core != "" && !strings.Contains(body, core) {
			t.Fatal(topic.Column, core, body)
		}
		if built.Repeated {
			t.Fatal(topic.Column, "repeated")
		}
	}
}
