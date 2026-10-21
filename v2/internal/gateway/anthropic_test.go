package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAnthropicStream(t *testing.T) {
	var got map[string]any
	var hdr http.Header
	var path string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, hdr = r.URL.Path, r.Header
		json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range []string{
			`event: message_start` + "\n" + `data: {"type":"message_start","message":{"model":"up-model","usage":{"input_tokens":7}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"嗯"}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"你好"}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"世界"}}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}`,
			`data: {"type":"message_stop"}`,
		} {
			fmt.Fprint(w, l+"\n\n")
		}
	}))
	defer s.Close()
	g, _, _ := testGW()
	c := Config{ID: 3, Protocol: "anthropic", BaseURL: s.URL, APIKey: "k", Model: "m", Params: map[string]any{"max_tokens": 16000}, FirstByte: time.Second, Idle: time.Second, Total: 5 * time.Second}
	res, err := g.Chat(context.Background(), []Config{c}, Request{Messages: []Message{{Role: "system", Content: "S"}, {Role: "user", Content: "U"}}}, time.Minute)
	if err != nil || res.Text != "你好世界" || res.FinishReason != "stop" || res.Model != "up-model" || res.TokensOut != 9 {
		t.Fatalf("%v %+v", err, res)
	}
	if path != "/v1/messages" || hdr.Get("x-api-key") != "k" || hdr.Get("Authorization") != "" || got["system"] != "S" || got["max_tokens"] != float64(16000) {
		t.Fatalf("request: %s %v %v", path, hdr, got)
	}
	if len(got["messages"].([]any)) != 1 {
		t.Fatal("system must not be in messages")
	}
}
