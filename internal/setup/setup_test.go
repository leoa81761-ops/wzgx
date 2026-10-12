package setup

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/store"
)

func testPacks() []store.VerticalPack {
	return []store.VerticalPack{
		{Key: "general", Name: "通用", Enabled: true},
		{Key: "elv", Name: "弱电智能化", Keywords: "弱电,智能化", Subtopics: "综合布线,机房", Enabled: true},
		{Key: "family", Name: "家庭生活", Keywords: "家庭,母婴,美食", Enabled: true},
		{Key: "tech", Name: "科技", Keywords: "数码,科技", Enabled: true},
	}
}

func TestSuggestDiffersByAccount(t *testing.T) {
	packs := testPacks()
	engineer := Suggest(Input{AccountName: "弱电陈工", SubjectType: "personal", ServiceType: "subscription", Tier: "premium", MaxWords: 4000, Packs: packs})
	family := Suggest(Input{AccountName: "奶奶不当家", SubjectType: "personal", ServiceType: "subscription", Tier: "premium", MaxWords: 4000, Packs: packs})
	company := Suggest(Input{AccountName: "公司号", SubjectType: "enterprise", ServiceType: "subscription", Verified: true, Tier: "premium", MaxWords: 4000, Packs: packs})
	if engineer.Direction != "弱电智能化" || engineer.VerticalKey != "elv" || engineer.StyleKey != "plain" || engineer.Audience != "做工程和物业的读者" {
		t.Fatal(engineer)
	}
	if family.Direction != "个人成长" || family.VerticalKey != "" || family.StyleKey != "warm" {
		t.Fatal(family)
	}
	if company.Direction != "职场成长" || company.StyleKey != "formal" || company.TemplateKey != "business" || company.TimeWindow != "morning" {
		t.Fatal(company)
	}
	if engineer.Direction == family.Direction && engineer.StyleKey == family.StyleKey {
		t.Fatal("two accounts collapsed to the same defaults")
	}
	free := Suggest(Input{AccountName: "弱电陈工", SubjectType: "personal", ServiceType: "subscription", Tier: "free", MaxWords: 1000, Packs: packs})
	if free.TimeWindow != "night" || free.StyleKey != "formal" || free.WordTarget != 1000 {
		t.Fatal(free)
	}
	if !strings.Contains(free.Reason, "免费版") || !strings.Contains(engineer.Reason, "弱电") {
		t.Fatal(free.Reason, engineer.Reason)
	}
}

func TestSampleIsTwoHundredRunes(t *testing.T) {
	text := Sample("弱电智能化", "科普通俗")
	if utf8.RuneCountInString(text) != 200 {
		t.Fatal(utf8.RuneCountInString(text))
	}
	if !strings.Contains(text, "弱电智能化") || !strings.Contains(text, "科普通俗") {
		t.Fatal(text)
	}
	if utf8.RuneCountInString(Sample("", "")) != 200 {
		t.Fatal("empty sample length")
	}
}
