package probe

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/llm"
)

func TestProbeAgainstFakeServers(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	var draftArticles any
	var stream bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/cgi-bin/stable_token":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["grant_type"] != "client_credential" || body["appid"] != "wx-real" || body["secret"] != "sec" {
				t.Errorf("token %#v", body)
			}
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":7200}`))
		case "/cgi-bin/material/add_material":
			if r.URL.Query().Get("type") != "image" || r.URL.Query().Get("access_token") != "tok" {
				t.Errorf("material %s", r.URL.RawQuery)
			}
			name, ctype := filePart(t, r)
			if name != "cover.png" || ctype != "image/png" {
				t.Errorf("cover %s %s", name, ctype)
			}
			_, _ = w.Write([]byte(`{"media_id":"thumb-real"}`))
		case "/cgi-bin/media/uploadimg":
			if r.URL.Query().Get("type") != "" {
				t.Errorf("uploadimg should not set type: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"url":"https://mmbiz.qpic.cn/probe"}`))
		case "/cgi-bin/draft/add":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			draftArticles = body["articles"]
			_, _ = w.Write([]byte(`{"media_id":"draft-real"}`))
		case "/cgi-bin/draft/get":
			_, _ = w.Write([]byte(`{"news_item":[{"title":"连通测试标题"}]}`))
		case "/v1/chat/completions":
			raw, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(raw, &req)
			if req["stream"] != true || req["model"] != "demo-writer" {
				t.Errorf("llm %#v", req)
			}
			stream = true
			if r.Header.Get("Authorization") != "Bearer sk-test" {
				t.Errorf("auth %s", r.Header.Get("Authorization"))
			}
			_, _ = w.Write(llmBody(stageOf(req)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	steps, err := Run(context.Background(), Config{
		AppID: "wx-real", Secret: "sec", WeChatBase: srv.URL, Protocol: "openai",
		LLMBase: srv.URL + "/v1", LLMKey: "sk-test", LLMModel: "demo-writer", Timeout: 10 * time.Second,
		HTTP: srv.Client(), Direction: "职场成长",
	})
	if err != nil {
		t.Fatal(err, steps)
	}
	for _, step := range steps {
		if !step.OK {
			t.Fatalf("%s %s", step.Name, step.Detail)
		}
	}
	if !stream {
		t.Fatal("llm was not called with stream")
	}
	articles, ok := draftArticles.([]any)
	if !ok || len(articles) != 1 {
		t.Fatalf("draft articles %#v", draftArticles)
	}
	first := articles[0].(map[string]any)
	title, _ := first["title"].(string)
	if first["thumb_media_id"] != "thumb-real" || title == "" || strings.Contains(title, "震惊") {
		t.Fatal(first)
	}
	content, _ := first["content"].(string)
	if !strings.Contains(content, "https://mmbiz.qpic.cn/probe") {
		t.Fatal(content)
	}
	for _, path := range []string{"/cgi-bin/stable_token", "/cgi-bin/material/add_material", "/cgi-bin/media/uploadimg", "/cgi-bin/draft/add", "/cgi-bin/draft/get", "/v1/chat/completions"} {
		if hits[path] == 0 {
			t.Fatalf("missing %s in %#v", path, hits)
		}
	}
	if hits["/v1/chat/completions"] < 4 {
		t.Fatalf("expected four writing stages, got %d", hits["/v1/chat/completions"])
	}
}

func TestLiveCredentialsSkippedWithoutEnv(t *testing.T) {
	cfg := FromEnv()
	t.Setenv("WX_APP_ID", "")
	t.Setenv("WX_APP_SECRET", "")
	t.Setenv("LLM_BASE_URL", "")
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_MODEL", "")
	cfg = FromEnv()
	if cfg.Missing() == "" {
		t.Fatal("empty env should be incomplete")
	}
	if testing.Short() {
		t.Skip("short")
	}
	_ = cfg
}

func llmBody(stage string) []byte {
	var data map[string]any
	switch stage {
	case "选题":
		data = map[string]any{"title": "连通测试选题"}
	case "大纲":
		data = map[string]any{"points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"}
	default:
		data = map[string]any{"title": "连通测试标题", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"这是正文。"}, "list": []any{}}}, "closing": "结束", "sources": []any{}}
	}
	raw, _ := json.Marshal(data)
	out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(raw)}}}})
	return out
}

func stageOf(req map[string]any) string {
	msgs, _ := req["messages"].([]any)
	if len(msgs) == 0 {
		return ""
	}
	last, _ := msgs[len(msgs)-1].(map[string]any)
	content, _ := last["content"].(string)
	var payload map[string]any
	_ = json.Unmarshal([]byte(llm.PayloadText(content)), &payload)
	stage, _ := payload["环节"].(string)
	return stage
}

func filePart(t *testing.T, r *http.Request) (string, string) {
	t.Helper()
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	part, err := multipart.NewReader(r.Body, params["boundary"]).NextPart()
	if err != nil {
		t.Fatal(err)
	}
	defer part.Close()
	_, _ = io.ReadAll(part)
	return part.FileName(), part.Header.Get("Content-Type")
}
