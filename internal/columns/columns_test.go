package columns

import (
	"testing"
	"time"
)

func TestSuggestUsesTheAccountVertical(t *testing.T) {
	got := Suggest("弱电陈工", "elv", "现场笔记", []string{"验收旧闻"})
	names := map[string]Column{}
	for _, col := range got {
		names[col.Name] = col
	}
	news, ok := names["工程圈快报"]
	steps, okSteps := names["方案拆解"]
	faq, okFAQ := names["知识FAQ"]
	if !ok || !okSteps || !okFAQ || news.Structure != "news" || steps.Structure != "steps" || faq.Structure != "qa" {
		t.Fatal(got)
	}
	if names["现场笔记"].Structure != "custom" || names["验收旧闻"].Name != "验收旧闻" {
		t.Fatal(got)
	}
}

func TestTopicsStayOnTheColumnAndTheme(t *testing.T) {
	got := Topics("工程圈快报", "用几条行业消息讲清和读者的关系", "弱电智能化", "验收")
	seen := map[string]bool{}
	for _, item := range got {
		seen[item] = true
	}
	if !seen["工程圈快报"] || !seen["弱电智能化"] || !seen["验收"] {
		t.Fatal(got)
	}
	if seen["亲子阅读"] || seen["综合布线"] {
		t.Fatal(got)
	}
}

func TestWeekdayRotationUsesTheMappedColumn(t *testing.T) {
	active := []Column{
		{ID: 1, Name: "工程圈快报", Status: "active", Weight: 1},
		{ID: 2, Name: "方案拆解", Status: "active", Weight: 1},
	}
	monday := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	got, ok := Pick(active, "weekday", WeekdayOf(monday), map[int]int64{1: 2}, nil)
	if !ok || got.Name != "方案拆解" {
		t.Fatal(got, ok, WeekdayOf(monday))
	}
	weighted, ok := Pick(active, "weight", 1, nil, []string{"工程圈快报"})
	if !ok || weighted.Name != "方案拆解" {
		t.Fatal(weighted, ok)
	}
}

func TestNormalizeStructure(t *testing.T) {
	if NormalizeStructure("news-brief") != "news" || NormalizeStructure("step-by-step") != "steps" || NormalizeStructure("Q&A") != "qa" || NormalizeStructure("别的") != "custom" {
		t.Fatal("structure labels")
	}
}
