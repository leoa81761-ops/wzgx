package job_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestAutoPublish(t *testing.T) {
	e, root, user, accID, colID := setup(t)
	ctx := t.Context()
	e.fm.set("research", "", "未检索到可靠来源")
	e.fm.set("write", "", article1)
	e.fm.set("review", "", `{"pass":true}`)
	e.pool.Exec(ctx, `UPDATE users SET quota_exempt=TRUE WHERE email='u@example.com'`)
	e.pub.AutoPublish = true
	acc := fmt.Sprintf("/api/accounts/%d", accID)
	n := 0
	pushOne := func() int64 {
		n++
		_, m, b := do(t, e.srv, "POST", acc+"/generate", user, map[string]any{"column_id": colID, "topic": fmt.Sprintf("第%d篇自动发表测试：冬天怎么选拖鞋", n)})
		if m["job_id"] == nil {
			t.Fatal(b)
		}
		e.run()
		art := fmt.Sprint(e.jobStatus(user, m["job_id"])["article_id"])
		if code, _, b := do(t, e.srv, "POST", "/api/articles/"+art+"/push", user, nil); code != 200 {
			t.Fatalf("push: %s", b)
		}
		var id int64
		fmt.Sscan(art, &id)
		return id
	}
	pub := func(id int64) (status, note, media string) {
		e.pool.QueryRow(ctx, `SELECT publish_status, publish_note, draft_media_id FROM article WHERE id=$1`, id).Scan(&status, &note, &media)
		return
	}
	due := func(ids ...int64) {
		for _, id := range ids {
			e.pool.Exec(ctx, `UPDATE article SET publish_at=now() - interval '1 minute' WHERE id=$1`, id)
		}
	}
	tick := func() {
		if err := e.pub.PublishDue(ctx); err != nil {
			t.Fatal(err)
		}
	}
	nsub := func() int { e.wx.mu.Lock(); defer e.wx.mu.Unlock(); return len(e.wx.submits) }

	// only verified accounts; default off
	if code, _, _ := do(t, e.srv, "PUT", acc+"/controls", user, map[string]any{"auto_publish": true}); code != 400 {
		t.Fatal("unverified account turned on auto-publish")
	}
	off := pushOne()
	if s, _, _ := pub(off); s != "" {
		t.Fatalf("scheduled while off: %s", s)
	}
	do(t, e.srv, "PATCH", "/api/admin/accounts/"+fmt.Sprint(accID), root, map[string]any{"verified": true})
	if code, _, b := do(t, e.srv, "PUT", acc+"/controls", user, map[string]any{"auto_publish": true, "review_minutes": 60}); code != 200 {
		t.Fatal(b)
	}
	if code, _, _ := do(t, e.srv, "PUT", acc+"/controls", user, map[string]any{"review_minutes": 5}); code != 400 {
		t.Fatal("5-minute window accepted")
	}

	// review window: scheduled, not published before it ends, cancel works inside it
	a, b, c := pushOne(), pushOne(), pushOne()
	var mins float64
	e.pool.QueryRow(ctx, `SELECT extract(epoch FROM publish_at - pushed_at)/60 FROM article WHERE id=$1`, a).Scan(&mins)
	if s, _, _ := pub(a); s != "scheduled" || mins < 59 || mins > 61 {
		t.Fatalf("schedule: %s %.1f", s, mins)
	}
	tick()
	if nsub() != 0 {
		t.Fatal("published inside the review window")
	}
	if code, _, _ := do(t, e.srv, "POST", fmt.Sprintf("/api/articles/%d/publish/cancel", c), user, nil); code != 200 {
		t.Fatal("cancel")
	}
	if code, _, _ := do(t, e.srv, "POST", fmt.Sprintf("/api/articles/%d/publish/cancel", c), user, nil); code == 200 {
		t.Fatal("cancelled twice")
	}
	if code, _, _ := do(t, e.srv, "POST", fmt.Sprintf("/api/articles/%d/publish/cancel", a), root, nil); code != 403 {
		t.Fatal("staff cancelled a user's publish")
	}

	// window over: read back, publish once; the daily cap (1) holds the second until tomorrow
	due(a, b, c)
	tick()
	if s, _, _ := pub(a); s != "publishing" || nsub() != 1 {
		t.Fatalf("submit: %s, %d submits", s, nsub())
	}
	if s, note, _ := pub(b); s != "scheduled" || !strings.Contains(note, "上限") {
		t.Fatalf("cap: %s %s", s, note)
	}
	if s, _, _ := pub(c); s != "cancelled" {
		t.Fatal("cancelled one published")
	}
	tick() // poll
	var url string
	e.pool.QueryRow(ctx, `SELECT publish_url FROM article WHERE id=$1`, a).Scan(&url)
	if s, _, _ := pub(a); s != "published" || !strings.HasPrefix(url, "https://mp.weixin.qq.com/s/fake") {
		t.Fatalf("poll: %s %s", s, url)
	}
	// never twice, even if the row is pushed back to scheduled
	e.pool.Exec(ctx, `UPDATE article SET publish_status='scheduled', publish_at=now() - interval '1 minute' WHERE id=$1`, a)
	tick()
	tick()
	if nsub() != 1 {
		t.Fatalf("published twice: %d", nsub())
	}
	e.pool.Exec(ctx, `UPDATE article SET publish_status='published' WHERE id=$1`, a)

	// the draft was deleted by hand: not published
	do(t, e.srv, "PUT", acc+"/controls", user, map[string]any{"publish_cap": 5})
	_, _, media := pub(b)
	e.wx.mu.Lock()
	e.wx.gone[media] = true
	e.wx.mu.Unlock()
	due(b)
	tick()
	if s, note, _ := pub(b); s != "failed" || !strings.Contains(note, "草稿箱里已经没有") || nsub() != 1 {
		t.Fatalf("gone draft: %s %s", s, note)
	}

	// user pause-all holds it; platform pause holds it; resume publishes
	d := pushOne()
	due(d)
	do(t, e.srv, "PUT", "/api/me/publish-pause", user, map[string]any{"paused": true})
	tick()
	if s, _, _ := pub(d); s != "scheduled" {
		t.Fatal("user pause ignored")
	}
	do(t, e.srv, "PUT", "/api/me/publish-pause", user, map[string]any{"paused": false})
	if code, _, _ := do(t, e.srv, "PUT", "/api/admin/publish-pause", user, map[string]any{"paused": true}); code != 403 {
		t.Fatal("user used the platform switch")
	}
	do(t, e.srv, "PUT", "/api/admin/publish-pause", root, map[string]any{"paused": true})
	tick()
	if s, _, _ := pub(d); s != "scheduled" {
		t.Fatal("platform pause ignored")
	}
	do(t, e.srv, "PUT", "/api/admin/publish-pause", root, map[string]any{"paused": false})
	tick()
	var pid string
	e.pool.QueryRow(ctx, `SELECT publish_id FROM article WHERE id=$1`, d).Scan(&pid)
	if s, _, _ := pub(d); s != "publishing" || pid == "" {
		t.Fatalf("after resume: %s", s)
	}
	// WeChat review fails: recorded
	e.wx.mu.Lock()
	e.wx.pubStat[pid] = 4
	e.wx.mu.Unlock()
	tick()
	if s, note, _ := pub(d); s != "failed" || !strings.Contains(note, "平台审核不通过") {
		t.Fatalf("review failure: %s %s", s, note)
	}

	// WeChat refuses the submit: failed with its reason
	g := pushOne()
	due(g)
	e.wx.mu.Lock()
	e.wx.subErr = 48001
	e.wx.mu.Unlock()
	tick()
	if s, note, _ := pub(g); s != "failed" || !strings.Contains(note, "微信拒绝发表") {
		t.Fatalf("refused: %s %s", s, note)
	}

	// worker switch off: nothing happens; turning the account off cancels what is waiting
	h := pushOne()
	due(h)
	e.pub.AutoPublish = false
	tick()
	if s, _, _ := pub(h); s != "scheduled" {
		t.Fatal("published with V2_AUTOPUBLISH off")
	}
	do(t, e.srv, "PUT", acc+"/controls", user, map[string]any{"auto_publish": false})
	if s, _, _ := pub(h); s != "cancelled" {
		t.Fatal("turning off kept it scheduled")
	}
	_, art, _ := do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%d", d), user, nil)
	if art["publish_status"] != "failed" {
		t.Fatalf("article api: %v", art["publish_status"])
	}
}
