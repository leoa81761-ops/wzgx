package pipeline

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leoa81761-ops/wzgx/internal/llm"
)

func TestChargeSearchSkipsAUsersOwnKey(t *testing.T) {
	if chargeSearch(true, true, 5) != 0 || chargeSearch(true, false, 5) != 5 || chargeSearch(false, false, 5) != 0 {
		t.Fatal(chargeSearch(true, true, 5), chargeSearch(true, false, 5))
	}
}

func TestBackupTraceShowsTheUserProvider(t *testing.T) {
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tavily"):
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			key, _ := body["api_key"].(string)
			keys = append(keys, key)
			if key != "user-key-aaaa" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"error":"invalid api key"}`)
				return
			}
			_, _ = io.WriteString(w, `{"results":[{"title":"综合布线验收说明","url":"https://www.gov.cn/user"}]}`)
		case strings.HasSuffix(r.URL.Path, "/search"):
			_, _ = io.WriteString(w, `{"results":[{"title":"综合布线验收说明","url":"https://www.gov.cn/searx","content":"公开说明"}]}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	gateway := llm.NewGateway(false, srv.Client())
	gateway.SearxngURL = srv.URL
	gateway.PlatformTavilyKey = "platform-key"
	gateway.SearchAPIProvider = "bocha"
	gateway.SearchAPIKey = "bocha-key"
	gateway.SearchAPIURL = srv.URL + "/bocha"
	gateway.BackupSearchURL = srv.URL + "/bing"
	rows, engines, err := backupHTMLSearch(t.Context(), gateway, "综合布线验收", searchActor{UserID: 3, TavilyKey: "user-key-aaaa", TavilyURL: srv.URL + "/tavily"})
	if err != nil || len(rows) != 1 || rows[0]["url"] != "https://www.gov.cn/user" {
		t.Fatal(rows, err)
	}
	if len(engines) != 1 || engines[0]["provider"] != "tavily" || engines[0]["owner"] != "user" {
		t.Fatal(engines)
	}
	if strings.Join(keys, ",") != "user-key-aaaa" {
		t.Fatal(keys)
	}
	failed, failedEngines, err := backupHTMLSearch(t.Context(), gateway, "线槽检修", searchActor{UserID: 8, TavilyKey: "user-key-bbbb", TavilyURL: srv.URL + "/tavily"})
	if err != nil || len(failed) != 1 || failed[0]["url"] != "https://www.gov.cn/searx" {
		t.Fatal(failed, err)
	}
	if len(failedEngines) < 2 || failedEngines[0]["provider"] != "tavily" || failedEngines[0]["key_error"] != "invalid" || failedEngines[1]["provider"] != "searxng" {
		t.Fatal(failedEngines)
	}
	notice, ok := tavilyKeyState(llm.Completion{Attempts: []map[string]any{{"engines": failedEngines}}})
	if ok || !strings.Contains(notice, "密钥无效") {
		t.Fatal(notice, ok)
	}
	for _, key := range keys {
		if key == "platform-key" || key == "bocha-key" {
			t.Fatal(keys)
		}
	}
}
