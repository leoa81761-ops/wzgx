package hot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseMergeAndChoose(t *testing.T) {
	raw := []byte(`{"data":[{"title":"住建部通知","url":"https://www.mohurd.gov.cn/a","heat":1},{"title":"论坛转载","url":"https://example.com/x","heat":9}]}`)
	items := Parse(raw, "甲", map[string]any{"list": "data"})
	if len(items) != 2 {
		t.Fatal(len(items))
	}
	again := Parse([]byte(`[{"title":"住建部通知","url":"https://mohurd.gov.cn/a","source":"乙"}]`), "乙", nil)
	merged := Merge(items, again)
	if len(merged) != 2 || merged[0].Reports != 2 {
		t.Fatalf("%#v", merged)
	}
	picked, ok := Choose(merged, "premium", nil)
	if !ok || picked.URL == "" || picked.Reports != 2 {
		t.Fatalf("%#v", picked)
	}
	plain, ok := Choose(merged, "standard", nil)
	if !ok || plain.Title != "住建部通知" {
		t.Fatalf("%#v", plain)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"result":{"content":[{"type":"text","text":"[{\"title\":\"热点\",\"url\":\"https://news.cn/a\"}]"}]}}`))
	}))
	defer srv.Close()
	got, err := Fetch(context.Background(), srv.Client(), Source{Name: "mcp", Protocol: "mcp", BaseURL: srv.URL, APIKey: "secret"})
	if err != nil || len(got) != 1 || got[0].Title != "热点" {
		t.Fatal(err, got)
	}
}