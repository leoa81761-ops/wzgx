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
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer okSrv.Close()
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer missing.Close()
	draft := map[string]any{"sources": []any{
		map[string]any{"title": "在", "url": okSrv.URL},
		map[string]any{"title": "丢", "url": missing.URL},
	}}
	DropUnreachable(context.Background(), okSrv.Client(), draft)
	left, _ := draft["sources"].([]any)
	if len(left) != 1 {
		t.Fatal(len(left))
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
