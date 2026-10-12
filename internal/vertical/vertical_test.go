package vertical

import (
	"strings"
	"testing"

	"github.com/leoa81761-ops/wzgx/internal/store"
)

func TestMatchLongestKeyword(t *testing.T) {
	packs := []store.VerticalPack{
		{Key: "general", Name: "通用", Enabled: true},
		{Key: "family", Name: "家庭生活", Keywords: "家庭,母婴", Enabled: true},
		{Key: "elv", Name: "弱电智能化", Keywords: "弱电,智能化", Enabled: true},
		{Key: "tech", Name: "科技", Keywords: "科技,数码", Enabled: true, Subtopics: "手机使用\n备份习惯"},
	}
	if got := Match("弱电陈工", packs); got.Key != "elv" {
		t.Fatal(got.Key)
	}
	if got := Match("奶奶不当家", packs); got.Key != "general" {
		t.Fatal(got.Key)
	}
	if got := Match("母婴辅食", packs); got.Key != "family" {
		t.Fatal(got.Key)
	}
	topics := Subtopics(packs[3])
	if len(topics) != 2 || topics[0] != "手机使用" {
		t.Fatal(topics)
	}
}

func TestResolveKeepsEachPlanPack(t *testing.T) {
	packs := []store.VerticalPack{
		{Key: "general", Name: "通用", Enabled: true, Subtopics: "通用题"},
		{Key: "elv", Name: "弱电智能化", Keywords: "弱电", Enabled: true, Subtopics: "综合布线"},
		{Key: "family", Name: "家庭生活", Keywords: "家庭", Enabled: false, Subtopics: "家常"},
	}
	engineer := Resolve("职场成长", store.PlanExtra{VerticalKey: "elv", Keywords: "验收,机房"}, packs)
	if engineer.Key != "elv" || !strings.Contains(engineer.Subtopics, "验收") || !strings.Contains(engineer.Subtopics, "综合布线") {
		t.Fatal(engineer)
	}
	family := Resolve("职场成长", store.PlanExtra{Keywords: "会议,邮件"}, packs)
	if family.Key != "general" || !strings.HasPrefix(family.Subtopics, "会议") {
		t.Fatal(family)
	}
	if strings.Contains(family.Subtopics, "验收") {
		t.Fatal(family.Subtopics)
	}
	fallback := Resolve("弱电陈工", store.PlanExtra{VerticalKey: "missing"}, packs)
	if fallback.Key != "elv" {
		t.Fatal(fallback.Key)
	}
	disabled := Resolve("职场成长", store.PlanExtra{VerticalKey: "family"}, packs)
	if disabled.Key != "general" {
		t.Fatal(disabled.Key)
	}
}
