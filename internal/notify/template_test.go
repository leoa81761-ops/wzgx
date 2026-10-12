package notify

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendTemplateUsesConfiguredFields(t *testing.T) {
	var sawSend bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		switch {
		case strings.Contains(r.URL.Path, "stable_token"):
			if strings.Contains(string(raw), "secret-value") {
				w.Write([]byte(`{"access_token":"tok","expires_in":7200}`))
				return
			}
		case strings.Contains(r.URL.Path, "template/send"):
			sawSend = true
			if !strings.Contains(string(raw), `"keyword1"`) || !strings.Contains(string(raw), "草稿已写入") {
				t.Fatalf("body %s", raw)
			}
			if strings.Contains(string(raw), "secret-value") {
				t.Fatal("secret leaked into the template body")
			}
			w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	err := SendTemplate(t.Context(), srv.Client(), srv.URL, "wx_tpl", "secret-value", "tpl-id", "keyword1,keyword2", "openid_1", "草稿已写入", "订阅号每天可群发 1 次，服务号每月可群发 4 次")
	if err != nil || !sawSend {
		t.Fatal(err, sawSend)
	}
}
