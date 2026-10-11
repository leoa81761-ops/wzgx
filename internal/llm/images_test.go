package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGenerateCoverURLAndBase64(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a}
	var sawRatio any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/images/generations":
			var body map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			sawRatio = body["aspect_ratio"]
			if body["model"] != "grok-imagine-image-2.0" {
				t.Errorf("model %#v", body["model"])
			}
			if r.Header.Get("Authorization") != "Bearer img-key" {
				t.Errorf("auth %s", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"data":[{"url":"` + "http://" + r.Host + `/cover.png"}]}`))
		case "/cover.png":
			_, _ = w.Write(png)
		default:
			var body map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			if body["aspect_ratio"] != "16:9" {
				t.Errorf("ratio %#v", body["aspect_ratio"])
			}
			encoded := base64.StdEncoding.EncodeToString(png)
			_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + encoded + `"}]}`))
		}
	}))
	defer srv.Close()
	client := srv.Client()
	got, err := GenerateCover(context.Background(), client, Provider{
		Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "img-key", Model: "grok-imagine-image-2.0", Timeout: 5 * time.Second,
	}, "封面")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(png) {
		t.Fatalf("url bytes %x", got)
	}
	if sawRatio != "16:9" {
		t.Fatal(sawRatio)
	}
	got, err = GenerateCover(context.Background(), client, Provider{
		BaseURL: srv.URL + "/b64/v1", APIKey: "img-key", Model: "grok-imagine-image-2.0", Timeout: 5 * time.Second,
	}, "封面")
	if err != nil || string(got) != string(png) {
		t.Fatalf("%v %x", err, got)
	}
}

func TestInterpretResearchMarkdown(t *testing.T) {
	notes, sources := InterpretResearch("远程办公的通知见 [人社部](https://www.example.com/notice)。")
	if notes == "" || len(sources) != 1 || sources[0]["url"] != "https://www.example.com/notice" {
		t.Fatalf("%q %#v", notes, sources)
	}
	notes, sources = InterpretResearch(`{"notes":"只有这一条","sources":[{"name":"甲","title":"乙","url":"https://a.example/x","published_on":"2026-01-02","source_type":"news"}]}`)
	if notes != "只有这一条" || len(sources) != 1 || sources[0]["source_type"] != "news" {
		t.Fatalf("%q %#v", notes, sources)
	}
}
