package content

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCopiedSpanAndStyleFeatures(t *testing.T) {
	source := strings.Repeat("甲乙丙丁戊己庚辛壬癸", 6)
	if !CopiedSpan("开头"+source+"结尾", []string{source}) {
		t.Fatal("expected overlap")
	}
	if CopiedSpan("完全不同的一段话", []string{source}) {
		t.Fatal("short difference should pass")
	}
	sample := strings.Repeat("这是一句完整的话。", 12)
	features, err := StyleFeatures([]string{sample, sample, sample})
	if err != nil || features["samples"] != 3 {
		t.Fatal(features, err)
	}
	if _, err := StyleFeatures([]string{sample}); err == nil {
		t.Fatal("two samples are not enough")
	}
}

func TestDiffStyleFeaturesOmitText(t *testing.T) {
	local := "今天天气很好。"
	remote := "今天天气很好吗？我觉得要带伞。"
	features := DiffStyleFeatures(local, remote)
	raw := fmt.Sprint(features)
	if strings.Contains(raw, "带伞") || strings.Contains(raw, "天气") {
		t.Fatal(features)
	}
	if features["question_delta"] != 1 || features["source"] != "edit_diff" {
		t.Fatal(features)
	}
}

func TestDropUnreachable(t *testing.T) {
	var hits int
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(200)
	}))
	defer page.Close()
	draft := map[string]any{"sources": []any{
		map[string]any{"title": "在", "url": page.URL},
		map[string]any{"title": "回环", "url": "http://127.0.0.1/secret"},
	}}
	DropUnreachable(context.Background(), page.Client(), draft)
	if hits != 0 {
		t.Fatal(hits)
	}
	left, _ := draft["sources"].([]any)
	if len(left) != 2 {
		t.Fatal(len(left))
	}
	for _, raw := range left {
		source, _ := raw.(map[string]any)
		if source["accessible"] != "未确认" {
			t.Fatal(source)
		}
	}
	if Reachable(context.Background(), page.Client(), "http://127.0.0.1/secret") != "未确认" {
		t.Fatal("loopback")
	}
	if hits != 0 {
		t.Fatal(hits)
	}
}

func TestReachLabel(t *testing.T) {
	if reachLabel(200) != "可访问" || reachLabel(404) != "打不开" || reachLabel(410) != "打不开" || reachLabel(500) != "未确认" {
		t.Fatal(reachLabel(200), reachLabel(404), reachLabel(500))
	}
}

func TestPickVariantUsesAdoption(t *testing.T) {
	rows := [][2]int{{10, 1}, {4, 3}}
	if PickVariant(rows, false, 0) != 1 {
		t.Fatal("higher rate should win")
	}
	if PickVariant(rows, true, 0) != 0 {
		t.Fatal("explore should use the given index")
	}
}
