package job_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeImages imitates the OpenAI images API of the image relay (never the real one).
type fakeImages struct {
	mu   sync.Mutex
	reqs []map[string]any
	auth []string
	fail bool // answer 500 to every call
	jpg  []byte
}

func newFakeImages(t *testing.T) (*fakeImages, *httptest.Server) {
	img := image.NewRGBA(image.Rect(0, 0, 1600, 900))
	for y := 0; y < 900; y++ {
		for x := 0; x < 1600; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	var b bytes.Buffer
	jpeg.Encode(&b, img, &jpeg.Options{Quality: 90})
	f := &fakeImages{jpg: b.Bytes()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/files/a.jpg" {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write(f.jpg)
			return
		}
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.reqs = append(f.reqs, in)
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		n, fail := len(f.reqs), f.fail
		f.mu.Unlock()
		if r.URL.Path != "/v1/images/generations" || fail {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"error":{"message":"upstream busy"}}`)
			return
		}
		switch n % 3 { // the three reply shapes the relay may use
		case 1:
			fmt.Fprintf(w, `{"data":[{"b64_json":"data:image/jpeg;base64,%s","mime_type":"image/jpeg","revised_prompt":"x"}]}`, base64.StdEncoding.EncodeToString(f.jpg))
		case 2:
			fmt.Fprintf(w, `{"data":[{"b64_json":"%s"}]}`, base64.StdEncoding.EncodeToString(f.jpg))
		default:
			fmt.Fprintf(w, `{"data":[{"url":"http://%s/files/a.jpg"}]}`, r.Host)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeImages) calls() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.reqs) }

func TestImages(t *testing.T) {
	e, root, user, accID, colID := setup(t)
	ctx := t.Context()
	fi, isrv := newFakeImages(t)
	e.pub.DefaultCover = "https://cdn.example/cover.jpg"
	e.fm.set("research", "", "未检索到可靠来源")
	e.fm.set("write", "", article1)
	e.fm.set("review", "", `{"pass":true}`)
	e.pool.Exec(ctx, `UPDATE users SET quota_exempt=TRUE, tier='standard' WHERE email='u@example.com'`)

	// config center: an image config, only on the image route, with a connection test
	_, c, b := do(t, e.srv, "POST", "/api/admin/configs", root, map[string]any{"kind": "image", "name": "图片", "base_url": isrv.URL + "/v1",
		"api_key": "sk-img-0123456789", "model": "img-model-x", "enabled": true})
	if c["id"] == nil || c["total_timeout_s"].(float64) != 180 {
		t.Fatal(b)
	}
	imgID := c["id"]
	if code, _, _ := do(t, e.srv, "PUT", "/api/admin/routes/write", root, map[string]any{"config_ids": []any{imgID}}); code != 400 {
		t.Fatal("image config accepted on the write route")
	}
	_, cs, _ := do(t, e.srv, "GET", "/api/admin/configs?kind=text", root, nil)
	_ = cs
	var textID int64
	e.pool.QueryRow(ctx, `SELECT id FROM service_config WHERE kind='text' LIMIT 1`).Scan(&textID)
	if code, _, _ := do(t, e.srv, "PUT", "/api/admin/routes/image", root, map[string]any{"config_ids": []any{textID}}); code != 400 {
		t.Fatal("text config accepted on the image route")
	}
	if code, _, b := do(t, e.srv, "PUT", "/api/admin/routes/image", root, map[string]any{"config_ids": []any{imgID}}); code != 200 {
		t.Fatal(b)
	}
	_, tr, b := do(t, e.srv, "POST", fmt.Sprintf("/api/admin/configs/%v/test", imgID), root, nil)
	if tr["ok"] != true || !strings.HasPrefix(fmt.Sprint(tr["image"]), "data:image/jpeg;base64,") || strings.Contains(b, "sk-img-0123456789") {
		t.Fatalf("image test: %.300s", b)
	}
	if fi.reqs[0]["model"] != "img-model-x" || fi.reqs[0]["response_format"] != "b64_json" || fi.reqs[0]["aspect_ratio"] != "16:9" || fi.auth[0] != "Bearer sk-img-0123456789" {
		t.Fatalf("image request: %v", fi.reqs[0])
	}

	gen := func(topic string) map[string]any {
		_, m, b := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/generate", accID), user, map[string]any{"column_id": colID, "topic": topic})
		if m["job_id"] == nil {
			t.Fatal(b)
		}
		e.run()
		art := e.jobStatus(user, m["job_id"])["article_id"]
		_, a, _ := do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%v", art), user, nil)
		if a["status"] != "ready" {
			t.Fatalf("article: %v %v", a["status"], a["block_reason"])
		}
		return a
	}

	// the system plans a cover and one of the slots (a made-up slot is ignored), generates both
	e.fm.set("topic", "", `{"cover":"老奶奶在台灯下陪孙子写作业","images":[{"slot":1,"prompt":"书桌上一盏护眼台灯"},{"slot":7,"prompt":"不存在的位置"}]}`)
	before := fi.calls()
	a := e.gen1(gen, "孙子写作业磨蹭怎么办")
	if fi.calls()-before != 2 {
		t.Fatalf("image calls: %d", fi.calls()-before)
	}
	_, planUser := e.fm.last("topic")
	if !strings.Contains(planUser, "【候选配图位置 1】") {
		t.Fatalf("plan prompt: %s", planUser)
	}
	html := fmt.Sprint(a["html"])
	cover := fmt.Sprint(a["cover_url"])
	if !strings.HasPrefix(cover, "/api/media/") || strings.Count(html, `src="/api/media/`) != 1 || strings.Contains(html, "cdn.example") || a["ai_images"].(float64) != 2 {
		t.Fatalf("pictures: cover=%q imgs=%v html=%.400s", cover, a["ai_images"], html)
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, httptest.NewRequest("GET", cover, nil))
	im, format, err := image.Decode(bytes.NewReader(rec.Body.Bytes()))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || err != nil || format != "jpeg" || im.Bounds().Dx() != 1280 || im.Bounds().Dy() != 720 {
		t.Fatalf("media: %d %v %v", rec.Code, err, im)
	}
	rec = httptest.NewRecorder()
	e.srv.ServeHTTP(rec, httptest.NewRequest("GET", "/api/media/00000000000000000000000000000000", nil))
	if rec.Code != 404 {
		t.Fatal("unknown media served")
	}
	// a layout change keeps the generated pictures
	do(t, e.srv, "PUT", fmt.Sprintf("/api/accounts/%d/layout", accID), user, map[string]any{"layout": "warm"})
	_, a2, _ := do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%v", a["id"]), user, nil)
	if strings.Count(fmt.Sprint(a2["html"]), `src="/api/media/`) != 1 {
		t.Fatalf("rerender lost pictures: %.300s", a2["html"])
	}
	// push: the generated cover is the thumb, the inline picture goes up as a body image
	if code, _, b := do(t, e.srv, "POST", fmt.Sprintf("/api/articles/%v/push", a["id"]), user, nil); code != 200 {
		t.Fatal(b)
	}
	e.wx.mu.Lock()
	ups := strings.Join(e.wx.uploads, ",")
	draft := fmt.Sprint(e.wx.drafts[len(e.wx.drafts)-1]["articles"])
	e.wx.uploads = nil
	e.wx.mu.Unlock()
	if !strings.Contains(ups, fmt.Sprintf("add_material:cover-%v.jpg", a["id"])) || !strings.Contains(ups, "uploadimg:img-") || strings.Contains(draft, "/api/media/") {
		t.Fatalf("push uploads: %s", ups)
	}

	// the system may choose no inline picture: cover only
	e.fm.set("topic", "", `{"cover":"窗边的绿萝","images":[]}`)
	a = e.gen1(gen, "绿萝怎么养才旺")
	if strings.Contains(fmt.Sprint(a["html"]), "<img") || a["cover_url"] == "" {
		t.Fatalf("no-inline: %.300s", a["html"])
	}

	// images fail: never blocks; the slot is dropped and push uses the default cover
	fi.mu.Lock()
	fi.fail = true
	fi.mu.Unlock()
	e.fm.set("topic", "", `{"cover":"阳台","images":[{"slot":1,"prompt":"花盆"}]}`)
	a = e.gen1(gen, "阳台种菜的三个误区")
	if strings.Contains(fmt.Sprint(a["html"]), "<img") || a["cover_url"] != "" {
		t.Fatalf("failed pictures: %.300s", a["html"])
	}
	if code, _, b := do(t, e.srv, "POST", fmt.Sprintf("/api/articles/%v/push", a["id"]), user, nil); code != 200 {
		t.Fatal(b)
	}
	e.wx.mu.Lock()
	ups = strings.Join(e.wx.uploads, ",")
	e.wx.mu.Unlock()
	if !strings.Contains(ups, "add_material:cover.jpg") {
		t.Fatalf("default cover not used: %s", ups)
	}
	var errs int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM model_call WHERE stage IN ('image_cover','image_inline') AND outcome='http_error'`).Scan(&errs)
	if errs != 2 {
		t.Fatalf("image failures recorded: %d", errs)
	}
	fi.mu.Lock()
	fi.fail = false
	fi.mu.Unlock()

	// tier switch: free gets no AI pictures by default (old default-picture behaviour), admin can turn it on
	_, ts, _ := do(t, e.srv, "GET", "/api/admin/tiers", root, nil)
	_ = ts
	var freeOn, stdOn bool
	e.pool.QueryRow(ctx, `SELECT (SELECT ai_images FROM plan_tier WHERE key='free'), (SELECT ai_images FROM plan_tier WHERE key='standard')`).Scan(&freeOn, &stdOn)
	if freeOn || !stdOn {
		t.Fatal("tier defaults")
	}
	e.pool.Exec(ctx, `UPDATE users SET tier='free' WHERE email='u@example.com'`)
	before = fi.calls()
	a = e.gen1(gen, "冬天被子怎么晒")
	if fi.calls() != before || !strings.Contains(fmt.Sprint(a["html"]), "cdn.example/cover.jpg") || a["cover_url"] != "" {
		t.Fatalf("free tier: %d calls %.300s", fi.calls()-before, a["html"])
	}
	if code, _, b := do(t, e.srv, "PUT", "/api/admin/tiers/free", root, map[string]any{"name": "免费版", "max_accounts": 1, "daily_quota": 1, "visible": true, "sort": 1, "ai_images": true}); code != 200 {
		t.Fatal(b)
	}
	e.fm.set("topic", "", `{"cover":"晒被子","images":[]}`)
	a = e.gen1(gen, "冬天被子怎么晒才蓬松")
	if fi.calls() != before+1 || a["cover_url"] == "" {
		t.Fatal("tier switch ignored")
	}

	// per-image price: image spend counts toward the config's daily cap; over it, pictures are skipped
	if code, _, b := do(t, e.srv, "PUT", fmt.Sprintf("/api/admin/configs/%v/pricing", imgID), root, map[string]any{"price_per_image": 0.7, "daily_cap": 0.5}); code != 200 || !strings.Contains(b, `"price_per_image":0.7`) {
		t.Fatal(b)
	}
	e.fm.set("topic", "", `{"cover":"晾衣架","images":[]}`)
	a = e.gen1(gen, "阴天衣服怎么晾")
	var spent float64
	e.pool.QueryRow(ctx, `SELECT COALESCE(sum(cost),0)::float8 FROM model_call WHERE config_id=$1`, imgID).Scan(&spent)
	if fi.calls() != before+2 || spent < 0.69 || spent > 0.71 {
		t.Fatalf("priced image: calls %d spent %v", fi.calls()-before, spent)
	}
	a = e.gen1(gen, "阴天衣服怎么晾才不臭")
	if fi.calls() != before+2 || a["cover_url"] != "" {
		t.Fatal("image cap ignored")
	}
}

func (e *env) gen1(f func(string) map[string]any, topic string) map[string]any { return f(topic) }
