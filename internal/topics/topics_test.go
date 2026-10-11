package topics

import (
	"testing"
	"time"
)

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
