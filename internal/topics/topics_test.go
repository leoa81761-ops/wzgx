package topics

import (
	"strings"
	"testing"
	"time"
)

func TestAccountTreeFollowsTheAccount(t *testing.T) {
	pack := []string{"家庭日常", "亲子阅读", "家常备菜", "收纳整理", "作息节奏"}
	family := "家庭,母婴,生活,美食,亲子"
	got := AccountTree(pack, family, "养老日常", "晚年", "带娃记")
	for _, sub := range got {
		if sub == "亲子阅读" || sub == "家常备菜" || sub == "收纳整理" {
			t.Fatal(got)
		}
	}
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "带娃记") || !strings.Contains(joined, "晚年") || !strings.Contains(joined, "养老日常") {
		t.Fatal(got)
	}
	kept := AccountTree(pack, family, "家庭生活", "收纳", "")
	if len(kept) != 1 || kept[0] != "收纳整理" {
		t.Fatal(kept)
	}
	if same := AccountTree(pack, family, "", "", ""); len(same) != len(pack) || same[0] != pack[0] {
		t.Fatal(same)
	}
	elv := []string{"综合布线", "视频监控"}
	onTheme := AccountTree(elv, "弱电,智能化", "弱电智能化", "", "现场笔记,验收清单")
	if len(onTheme) != 2 || onTheme[0] != "综合布线" {
		t.Fatal(onTheme)
	}
	off := AccountTree(pack, family, "养老日常", "", "带娃记")
	if strings.Contains(strings.Join(off, ","), "亲子阅读") || !strings.Contains(strings.Join(off, ","), "养老日常") {
		t.Fatal(off)
	}
}

func TestDivergeSkipsATitleAlreadyUsed(t *testing.T) {
	choice := Choice{Subtopic: "环形布线", Column: "方案拆解", Angle: "对比两种常见做法"}
	titles := []string{"环形布线：对比两种常见做法"}
	got := Diverge(choice, []string{"环形布线", "星形拓扑", "验收记录"}, AngleOptions(choice.Angle), titles)
	if got.Subtopic == "环形布线" || strings.Contains(got.Subtopic, "环形") {
		t.Fatal(got)
	}
	if got.Angle == choice.Angle {
		t.Fatal(got)
	}
}

func TestVetoSkipsTagAndCalendarAdvances(t *testing.T) {
	now := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	first := Pick("职场成长", "free", nil, now)
	second := Pick("职场成长", "free", nil, now, first.TopicTag)
	if second.TopicTag == first.TopicTag {
		t.Fatalf("veto should skip %s", first.TopicTag)
	}
	slots, err := Project("职场成长", "free", "daily", nil, "night", nil, now, 4, []string{first.TopicTag})
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 4 {
		t.Fatalf("%d slots", len(slots))
	}
	if slots[0].Choice.TopicTag == first.TopicTag {
		t.Fatal(slots[0].Choice.TopicTag)
	}
	for i := 1; i < len(slots); i++ {
		if !slots[i].At.After(slots[i-1].At) {
			t.Fatal(slots[i-1].At, slots[i].At)
		}
	}
}
