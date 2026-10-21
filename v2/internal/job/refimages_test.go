package job_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"gonghao/internal/auth"
	"gonghao/internal/safefetch"
)

func TestRefImages(t *testing.T) {
	e, _, user, accID, colID := setup(t)
	ctx := t.Context()
	var photo bytes.Buffer
	jpeg.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 400, 300)), nil)
	hits := 0
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/photo.jpg":
			w.Write(photo.Bytes())
		case "/page":
			w.Write([]byte("<html>hello</html>"))
		case "/hop":
			http.Redirect(w, r, "http://10.1.2.3/photo.jpg", 302)
		}
	}))
	defer src.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(src.URL, "http://"))
	// photos.example.com stands for a public host (mapped onto the test server); everything else
	// resolves as written, so loopback / private / metadata addresses meet the real checks
	e.jobs.Fetch = &safefetch.Fetcher{
		LookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
			if host == "photos.example.com" {
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			}
			if ip, err := netip.ParseAddr(host); err == nil {
				return []netip.Addr{ip}, nil
			}
			if host == "localhost" {
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			}
			return nil, errors.New("no such host")
		},
		DialAddr: func(ip netip.Addr, p string) string {
			if ip.String() == "93.184.216.34" {
				return "127.0.0.1:" + port
			}
			return net.JoinHostPort(ip.String(), p)
		},
	}
	lib := fmt.Sprintf("/api/accounts/%d/ref-images", accID)
	good := "http://photos.example.com/photo.jpg"

	if code, _, b := do(t, e.srv, "POST", lib, user, map[string]any{"url": good}); code != 400 || !strings.Contains(b, "授权") {
		t.Fatalf("no statement: %d %s", code, b)
	}
	for _, u := range []string{src.URL + "/photo.jpg", "http://localhost/photo.jpg", "http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.8/a.jpg", "file:///etc/passwd", "http://photos.example.com/page", "http://photos.example.com/hop"} {
		if code, _, b := do(t, e.srv, "POST", lib, user, map[string]any{"url": u, "authorized": true}); code != 400 {
			t.Errorf("%s accepted: %d %s", u, code, b)
		}
	}
	if hits != 2 { // only /page and /hop reached the server (the loopback URL was refused before dialing)
		t.Errorf("source hits %d", hits)
	}
	code, m, b := do(t, e.srv, "POST", lib, user, map[string]any{"url": good, "caption": "我家的台灯", "authorized": true})
	if code != 200 || m["id"] == nil {
		t.Fatal(b)
	}
	_, l, _ := do(t, e.srv, "GET", lib, user, nil)
	imgs, _ := l["images"].([]any)
	if len(imgs) != 1 || !strings.Contains(fmt.Sprint(l["statement"]), "使用权") {
		t.Fatalf("library: %v", l)
	}
	media := fmt.Sprint(imgs[0].(map[string]any)["url"])
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, httptest.NewRequest("GET", media, nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("library media %d", rec.Code)
	}
	var st string
	e.pool.QueryRow(ctx, `SELECT statement FROM ref_image`).Scan(&st)
	if st == "" {
		t.Fatal("statement not stored")
	}
	e.auth.Register(ctx, "o@example.com", "password1", "", auth.Consent{TermsVersion: "1", PrivacyVersion: "1"}, "", "")
	_, otok, _ := e.auth.Login(ctx, "o@example.com", "password1", "", "")
	if code, _, _ := do(t, e.srv, "GET", lib, otok, nil); code != 404 && code != 403 {
		t.Fatalf("other user sees the library: %d", code)
	}

	// generation without generated pictures: the cover comes from the library
	e.fm.set("research", "", research)
	e.fm.set("write", "", article1)
	e.fm.set("review", "", `{"pass":true}`)
	e.pool.Exec(ctx, `UPDATE users SET quota_exempt=TRUE WHERE email='u@example.com'`)
	_, g, b := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/generate", accID), user, map[string]any{"column_id": colID, "topic": "台灯怎么选"})
	if g["job_id"] == nil {
		t.Fatal(b)
	}
	e.run()
	artID := e.jobStatus(user, g["job_id"])["article_id"]
	var refCover int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM article_image WHERE article_id=$1 AND slot=0 AND ref_id IS NOT NULL`, artID).Scan(&refCover)
	if refCover != 1 {
		t.Fatal("no library cover")
	}

	// put a new authorized picture into the article's (only) image slot, replacing the default picture
	put := fmt.Sprintf("/api/articles/%v/ref-image", artID)
	if code, _, _ := do(t, e.srv, "POST", put, user, map[string]any{"slot": 9, "ref_id": m["id"]}); code != 400 {
		t.Fatal("slot 9 accepted")
	}
	if code, _, _ := do(t, e.srv, "POST", put, otok, map[string]any{"slot": 1, "ref_id": m["id"]}); code != 404 {
		t.Fatal("other user changed the article")
	}
	if code, _, b := do(t, e.srv, "POST", put, user, map[string]any{"slot": 1, "url": good, "caption": "台灯摆放"}); code != 400 || !strings.Contains(b, "授权") {
		t.Fatalf("article without statement: %s", b)
	}
	if code, _, b := do(t, e.srv, "POST", put, user, map[string]any{"slot": 1, "url": good, "caption": "台灯摆放", "authorized": true}); code != 200 {
		t.Fatal(b)
	}
	_, a, _ := do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%v", artID), user, nil)
	html := fmt.Sprint(a["html"])
	if !strings.Contains(html, `/api/media/`) || strings.Contains(html, "cdn.example/cover.jpg") {
		t.Fatalf("slot html: %s", html[len(html)/2:])
	}
	// a layout change keeps it
	do(t, e.srv, "PUT", fmt.Sprintf("/api/accounts/%d/layout", accID), user, map[string]any{"layout": "fresh"})
	_, a, _ = do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%v", artID), user, nil)
	if !strings.Contains(fmt.Sprint(a["html"]), `/api/media/`) {
		t.Fatal("rerender lost the picture")
	}
	if code, _, b := do(t, e.srv, "POST", fmt.Sprintf("/api/articles/%v/push", artID), user, nil); code != 200 {
		t.Fatal(b)
	}
	e.wx.mu.Lock()
	ups := strings.Join(e.wx.uploads, ",")
	e.wx.mu.Unlock()
	if !strings.Contains(ups, "add_material:cover-") || !strings.Contains(ups, "uploadimg:img-") {
		t.Fatalf("push uploads: %s", ups)
	}
	if code, _, _ := do(t, e.srv, "POST", put, user, map[string]any{"slot": 0, "ref_id": m["id"]}); code != 400 {
		t.Fatal("changed a pushed article")
	}
	var n int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action IN ('ref_image.add','article.ref_image')`).Scan(&n)
	if n != 2 {
		t.Fatalf("audit %d", n)
	}
	if code, _, _ := do(t, e.srv, "DELETE", fmt.Sprintf("%s/%v", lib, m["id"]), user, nil); code != 200 {
		t.Fatal("delete")
	}
	e.pool.QueryRow(ctx, `SELECT count(*) FROM article_image WHERE article_id=$1 AND slot=0`, artID).Scan(&n)
	if n != 1 {
		t.Fatal("deleting from the library removed the article's cover")
	}
}
