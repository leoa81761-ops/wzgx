package risk

import "testing"

func TestGradeAndHold(t *testing.T) {
	low := map[string]any{"title": "会议纪要怎么写", "sections": []any{map[string]any{"paragraphs": []any{"先列结论"}}}}
	if Grade("职场成长", low) != "low" {
		t.Fatal(Grade("职场成长", low))
	}
	if hold, _ := ShouldHold("low", "free", false); hold {
		t.Fatal("low content should publish")
	}
	high := map[string]any{"title": "用药说明", "sections": []any{map[string]any{"paragraphs": []any{"成人剂量需遵医嘱"}}}}
	if Grade("健康", high) != "high" {
		t.Fatal(Grade("健康", high))
	}
	if hold, note := ShouldHold("high", "premium", true); !hold || note == "" {
		t.Fatal(hold, note)
	}
	baby := map[string]any{"title": "作息", "sections": []any{map[string]any{"paragraphs": []any{"睡眠节奏因人而异"}}}}
	if Grade("母婴", baby) != "medium" {
		t.Fatal(Grade("母婴", baby))
	}
	if hold, _ := ShouldHold("medium", "free", true); !hold {
		t.Fatal("free medium should stay in app")
	}
	if hold, _ := ShouldHold("medium", "standard", false); !hold {
		t.Fatal("medium without sources should stay in app")
	}
	if hold, _ := ShouldHold("medium", "standard", true); hold {
		t.Fatal("paid medium with sources can go to draft")
	}
}
