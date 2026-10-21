package job_test

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

const tableArticle = `{"title":"三款台灯怎么选","summary":"摘要",
"blocks":[{"type":"p","text":"我是奶奶，给孙子挑台灯。"},{"type":"h2","text":"三款台灯的照度"},
{"type":"table","rows":[["台灯","照度"],["甲款","300 lx"],["乙款","500 lx"],["丙款","750 lx"]]},
{"type":"h2","text":"怎么放"},{"type":"p","text":"放在左前方。"},{"type":"p","text":"慢慢来。"}]}`

func TestCharts(t *testing.T) {
	e, _, user, accID, colID := setup(t)
	ctx := t.Context()
	e.fm.set("research", "", "未检索到可靠来源")
	e.fm.set("write", "", tableArticle)
	e.fm.set("review", "", `{"pass":true}`)
	e.pool.Exec(ctx, `UPDATE users SET quota_exempt=TRUE WHERE email='u@example.com'`)
	_, m, b := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/generate", accID), user, map[string]any{"column_id": colID, "topic": "台灯怎么选"})
	if m["job_id"] == nil {
		t.Fatal(b)
	}
	e.run()
	_, a, _ := do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%v", e.jobStatus(user, m["job_id"])["article_id"]), user, nil)
	html := fmt.Sprint(a["html"])
	ti, ci := strings.Index(html, "</table>"), strings.Index(html, `alt="数据图表"`)
	if a["status"] != "ready" || ti < 0 || ci < ti {
		t.Fatalf("chart after the table: %.600s", html)
	}
	src := html[strings.LastIndex(html[:ci], `src="`)+5:]
	src = src[:strings.Index(src, `"`)]
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, httptest.NewRequest("GET", src, nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(rec.Body.String(), "\x89PNG") {
		t.Fatalf("chart media: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	// a layout change keeps it; push uploads it as a body picture and it does not become the cover
	do(t, e.srv, "PUT", fmt.Sprintf("/api/accounts/%d/layout", accID), user, map[string]any{"layout": "fresh"})
	_, a2, _ := do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%v", a["id"]), user, nil)
	if !strings.Contains(fmt.Sprint(a2["html"]), src) {
		t.Fatal("rerender lost the chart")
	}
	if code, _, b := do(t, e.srv, "POST", fmt.Sprintf("/api/articles/%v/push", a["id"]), user, nil); code != 200 {
		t.Fatal(b)
	}
	e.wx.mu.Lock()
	ups := strings.Join(e.wx.uploads, ",")
	draft := fmt.Sprint(e.wx.drafts[len(e.wx.drafts)-1]["articles"])
	e.wx.mu.Unlock()
	if !strings.Contains(ups, "uploadimg:img-") || !strings.Contains(ups, ".png") || strings.Contains(ups, "add_material:img-") || strings.Contains(draft, "/api/media/") {
		t.Fatalf("push: %s", ups)
	}
}
