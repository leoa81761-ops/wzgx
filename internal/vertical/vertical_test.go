package vertical

import (
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
