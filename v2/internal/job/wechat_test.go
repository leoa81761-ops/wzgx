package job_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeWX imitates the WeChat endpoints we use. Nothing in the tests talks to the real API.
type fakeWX struct {
	srv     *httptest.Server
	mu      sync.Mutex
	secret  string // the AppSecret it accepts
	expire  bool   // next draft/add answers 40001 once
	tokens  int
	drafts  []map[string]any
	uploads []string
	edited  map[string]string // media_id -> content the "user" changed it to
	gone    map[string]bool   // media_id -> published or deleted
}

func newFakeWX(t *testing.T) *fakeWX {
	f := &fakeWX{secret: "0123456789abcdef0123456789abcdef", edited: map[string]string{}, gone: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/cgi-bin/stable_token":
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			if in["secret"] != f.secret {
				fmt.Fprint(w, `{"errcode":40125,"errmsg":"invalid appsecret"}`)
				return
			}
			f.tokens++
			fmt.Fprintf(w, `{"access_token":"TOKEN%d","expires_in":7200}`, f.tokens)
		case "/cgi-bin/media/uploadimg", "/cgi-bin/material/add_material":
			if !strings.HasPrefix(r.URL.Query().Get("access_token"), "TOKEN") {
				fmt.Fprint(w, `{"errcode":40001,"errmsg":"invalid credential"}`)
				return
			}
			file, hdr, err := r.FormFile("media")
			if err != nil {
				fmt.Fprint(w, `{"errcode":41005,"errmsg":"media data missing"}`)
				return
			}
			b, _ := io.ReadAll(file)
			if !bytes.HasPrefix(b, []byte("\xff\xd8")) {
				fmt.Fprint(w, `{"errcode":40113,"errmsg":"unsupported file type"}`)
				return
			}
			f.uploads = append(f.uploads, r.URL.Path+":"+hdr.Filename)
			if r.URL.Path == "/cgi-bin/media/uploadimg" {
				fmt.Fprint(w, `{"url":"http://mmbiz.qpic.cn/mmbiz_jpg/fake/0"}`)
			} else {
				fmt.Fprint(w, `{"media_id":"THUMB1","url":"http://mmbiz.qpic.cn/thumb"}`)
			}
		case "/cgi-bin/draft/add":
			if f.expire {
				f.expire = false
				fmt.Fprint(w, `{"errcode":40001,"errmsg":"access_token expired"}`)
				return
			}
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			f.drafts = append(f.drafts, in)
			fmt.Fprintf(w, `{"media_id":"DRAFT%d"}`, len(f.drafts))
		case "/cgi-bin/draft/get":
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			id := in["media_id"]
			var n int
			fmt.Sscanf(id, "DRAFT%d", &n)
			if f.gone[id] || n < 1 || n > len(f.drafts) {
				fmt.Fprint(w, `{"errcode":40007,"errmsg":"invalid media_id"}`)
				return
			}
			a := f.drafts[n-1]["articles"].([]any)[0].(map[string]any)
			content := a["content"].(string)
			if c, ok := f.edited[id]; ok {
				content = c
			}
			b, _ := json.Marshal(map[string]any{"news_item": []any{map[string]any{"title": a["title"], "content": content}}})
			w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// imageTransport answers every image download with a tiny JPEG.
type imageTransport struct{}

func (imageTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/jpeg"}},
		Body: io.NopCloser(bytes.NewReader([]byte("\xff\xd8\xff\xe0fakejpeg"))), Request: r}, nil
}

func TestBindCheckAndDraftPush(t *testing.T) {
	e, root, user, accID, colID := setup(t)

	// bind check with the stored (encrypted) AppSecret
	code, rep, b := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/check", accID), user, nil)
	if code != 200 || rep["ok"] != true {
		t.Fatalf("check: %d %s", code, b)
	}
	e.wx.mu.Lock()
	e.wx.secret = "something-else"
	e.wx.mu.Unlock()
	_, rep, _ = do(t, e.srv, "POST", fmt.Sprintf("/api/admin/accounts/%d/check", accID), root, nil)
	if rep["ok"] != false || rep["errcode"] != float64(40125) || !strings.Contains(rep["hint"].(string), "AppSecret") {
		t.Fatalf("bad secret report: %v", rep)
	}
	var stored string
	e.pool.QueryRow(t.Context(), `SELECT check_report::text FROM wx_account WHERE id=$1`, accID).Scan(&stored)
	if !strings.Contains(stored, "40125") || strings.Contains(stored, "0123456789abcdef0123456789abcdef") {
		t.Fatalf("stored report: %s", stored)
	}
	e.wx.mu.Lock()
	e.wx.secret = "0123456789abcdef0123456789abcdef"
	e.wx.mu.Unlock()

	// generate an article (never pushed by the worker)
	e.fm.set("topic", "", `{"topic":"孙子写作业磨蹭，台灯和计时器怎么用","search_query":"台灯"}`)
	e.fm.set("research", "", "未检索到可靠来源")
	e.fm.set("write", "", article1)
	e.fm.set("review", "", `{"pass":true}`)
	_, m, _ := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/generate", accID), user, map[string]any{"column_id": colID})
	e.run()
	_, j, _ := do(t, e.srv, "GET", fmt.Sprintf("/api/jobs/%v", m["job_id"]), user, nil)
	art := fmt.Sprint(j["article_id"])
	e.wx.mu.Lock()
	if len(e.wx.drafts) != 0 {
		t.Fatal("worker must not push")
	}
	e.wx.expire = true // the cached token is rejected once; the push refreshes it
	e.wx.mu.Unlock()

	if code, _, _ := do(t, e.srv, "POST", "/api/articles/"+art+"/push", root, nil); code != 403 {
		t.Fatalf("staff push: %d", code)
	}
	code, out, b := do(t, e.srv, "POST", "/api/articles/"+art+"/push", user, nil)
	if code != 200 || out["draft_media_id"] != "DRAFT1" {
		t.Fatalf("push: %d %s", code, b)
	}
	e.wx.mu.Lock()
	d := e.wx.drafts[0]["articles"].([]any)[0].(map[string]any)
	ups := strings.Join(e.wx.uploads, ",")
	e.wx.mu.Unlock()
	if d["thumb_media_id"] != "THUMB1" || d["title"] != "孙子写作业磨蹭，我用三个小办法" || !strings.Contains(d["content"].(string), "mmbiz.qpic.cn/mmbiz_jpg/fake/0") ||
		strings.Contains(d["content"].(string), "cdn.example") {
		t.Fatalf("draft body: %v", d)
	}
	if !strings.Contains(ups, "uploadimg:cover.jpg") || !strings.Contains(ups, "add_material:cover.jpg") {
		t.Fatalf("uploads: %s", ups)
	}
	_, a, _ := do(t, e.srv, "GET", "/api/articles/"+art, user, nil)
	if a["status"] != "pushed" {
		t.Fatalf("status %v", a["status"])
	}
	if code, _, _ := do(t, e.srv, "POST", "/api/articles/"+art+"/push", user, nil); code != 409 {
		t.Fatal("second push must be refused")
	}
	// a blocked article can't be pushed
	e.pool.Exec(t.Context(), `UPDATE article SET status='blocked' WHERE id=$1`, art)
	if code, _, _ := do(t, e.srv, "POST", "/api/articles/"+art+"/push", user, nil); code != 409 {
		t.Fatal("blocked push")
	}
}
