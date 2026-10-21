package job_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func (e *env) jobStatus(user string, id any) map[string]any {
	_, j, _ := do(e.t, e.srv, "GET", fmt.Sprintf("/api/jobs/%v", id), user, nil)
	return j
}

func (e *env) topicCalls() int {
	e.fm.mu.Lock()
	defer e.fm.mu.Unlock()
	return len(e.fm.reqs["topic"])
}

func TestManualControls(t *testing.T) {
	e, root, user, accID, colID := setup(t)
	e.fm.set("research", "", "未检索到可靠来源")
	e.fm.set("write", "", article1)
	e.fm.set("review", "", `{"pass":true}`)
	acc := fmt.Sprintf("/api/accounts/%d", accID)
	e.pool.Exec(t.Context(), `UPDATE users SET quota_exempt=TRUE WHERE email='u@example.com'`)

	// 1. write now with the user's own topic: no topic stage, the topic is kept as given
	if code, _, _ := do(t, e.srv, "POST", acc+"/generate", user, map[string]any{"topic": "x"}); code != 400 {
		t.Fatalf("one-character topic accepted: %d", code)
	}
	_, m, _ := do(t, e.srv, "POST", acc+"/generate", user, map[string]any{"column_id": colID, "topic": "  奶奶教你  用台灯 "})
	e.run()
	j := e.jobStatus(user, m["job_id"])
	_, a, _ := do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%v", j["article_id"]), user, nil)
	if a["topic"] != "奶奶教你 用台灯" || e.topicCalls() != 0 {
		t.Fatalf("fixed topic: %v, topic calls %d", a["topic"], e.topicCalls())
	}

	// 2. topic confirmation: the job waits after the topic stage
	if code, _, _ := do(t, e.srv, "PUT", acc+"/controls", user, map[string]any{"confirm_topic": true}); code != 200 {
		t.Fatal("confirm on")
	}
	e.fm.set("topic", "", `{"topic":"孙子的书包太重怎么减","search_query":"书包 重量"}`, `{"topic":"老花镜多久换一次","search_query":"老花镜"}`)
	_, m, _ = do(t, e.srv, "POST", acc+"/generate", user, map[string]any{"column_id": colID})
	e.run()
	if j := e.jobStatus(user, m["job_id"]); j["status"] != "awaiting_topic" {
		t.Fatalf("not waiting: %v", j)
	}
	var pending []map[string]any
	getList(t, e, "/api/me/topics", user, &pending)
	if len(pending) != 1 || pending[0]["proposal"].(map[string]any)["topic"] != "孙子的书包太重怎么减" || pending[0]["rejects_left"] != float64(3) {
		t.Fatalf("pending: %v", pending)
	}
	// still counts against today's quota while waiting
	var used int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM job WHERE status='awaiting_topic'`).Scan(&used)
	if used != 1 {
		t.Fatal("awaiting job missing")
	}
	jp := fmt.Sprintf("/api/jobs/%v/topic", m["job_id"])
	if code, _, _ := do(t, e.srv, "POST", jp, root, map[string]any{"action": "approve"}); code != 403 {
		t.Fatal("staff decided a user's topic")
	}
	// reject -> a new proposal that was told about the rejected one
	if _, r, _ := do(t, e.srv, "POST", jp, user, map[string]any{"action": "reject"}); r["status"] != "queued" {
		t.Fatalf("reject: %v", r)
	}
	e.run()
	if _, u := e.fm.last("topic"); !strings.Contains(u, "孙子的书包太重怎么减（用户没有采用）") {
		t.Fatalf("rejected topic not passed on: %s", u)
	}
	getList(t, e, "/api/me/topics", user, &pending)
	if pending[0]["proposal"].(map[string]any)["topic"] != "老花镜多久换一次" || pending[0]["rejects_left"] != float64(2) {
		t.Fatalf("second proposal: %v", pending)
	}
	// change -> the user's topic is written
	do(t, e.srv, "POST", jp, user, map[string]any{"action": "change", "topic": "我自己定的题目：冬天怎么给老人选拖鞋"})
	calls := e.topicCalls()
	e.run()
	j = e.jobStatus(user, m["job_id"])
	_, a, _ = do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%v", j["article_id"]), user, nil)
	if j["status"] != "done" || a["topic"] != "我自己定的题目：冬天怎么给老人选拖鞋" || e.topicCalls() != calls {
		t.Fatalf("changed topic: %v %v", j["status"], a["topic"])
	}
	// approve -> written with the proposed topic
	e.fm.set("topic", "", `{"topic":"雨天晾衣服的三个办法","search_query":"晾衣"}`)
	_, m, _ = do(t, e.srv, "POST", acc+"/generate", user, nil)
	e.run()
	do(t, e.srv, "POST", fmt.Sprintf("/api/jobs/%v/topic", m["job_id"]), user, map[string]any{"action": "approve"})
	e.run()
	j = e.jobStatus(user, m["job_id"])
	_, a, _ = do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%v", j["article_id"]), user, nil)
	if a["topic"] != "雨天晾衣服的三个办法" {
		t.Fatalf("approved topic: %v", a["topic"])
	}
	// three rejections cancel the job and release the quota
	e.fm.set("topic", "", `{"topic":"手机字体调大的方法","search_query":"b"}`, `{"topic":"电热毯安全用法","search_query":"c"}`, `{"topic":"冰箱除味小妙招","search_query":"d"}`)
	_, m, _ = do(t, e.srv, "POST", acc+"/generate", user, nil)
	jp = fmt.Sprintf("/api/jobs/%v/topic", m["job_id"])
	var last map[string]any
	for i := 0; i < 3; i++ {
		e.run()
		_, last, _ = do(t, e.srv, "POST", jp, user, map[string]any{"action": "reject"})
	}
	if j := e.jobStatus(user, m["job_id"]); last["status"] != "cancelled" || j["status"] != "cancelled" || !strings.Contains(j["failure"].(string), "否决 3 次") {
		t.Fatalf("three rejects: %v %v", last, j)
	}
	do(t, e.srv, "PUT", acc+"/controls", user, map[string]any{"confirm_topic": false})

	// 3. pause / resume: a paused account runs no plans
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.FixedZone("CST", 8*3600)) // a Monday
	e.jobs.Now = func() time.Time { return now }
	do(t, e.srv, "POST", acc+"/plans", user, map[string]any{"weekdays": []int{1, 2, 3, 4, 5, 6, 7}, "time": "08:00", "enabled": true})
	do(t, e.srv, "PUT", acc+"/controls", user, map[string]any{"paused": true})
	var accs []map[string]any
	getList(t, e, "/api/accounts", user, &accs)
	if accs[0]["paused"] != true || accs[0]["paused_reason"] != "user" {
		t.Fatalf("paused flag: %v", accs[0])
	}
	if n, _ := e.jobs.Schedule(t.Context()); n != 0 {
		t.Fatal("plan ran on a paused account")
	}
	do(t, e.srv, "PUT", acc+"/controls", user, map[string]any{"paused": false})
	if n, _ := e.jobs.Schedule(t.Context()); n != 1 {
		t.Fatal("plan did not run after resume")
	}
	e.jobs.Now = nil
	e.pool.Exec(t.Context(), `UPDATE wx_account SET resumed_at=NULL`) // the resume above used the test clock (a future Monday)
	e.pool.Exec(t.Context(), `UPDATE job SET status='cancelled' WHERE status='queued'`)

	// 4. feedback, shown in admin
	art := fmt.Sprint(j["article_id"])
	if code, _, _ := do(t, e.srv, "PUT", "/api/articles/"+art+"/feedback", user, map[string]any{"rating": 2}); code != 400 {
		t.Fatal("bad rating accepted")
	}
	if code, _, _ := do(t, e.srv, "PUT", "/api/articles/"+art+"/feedback", root, map[string]any{"rating": 1}); code != 403 {
		t.Fatal("staff feedback")
	}
	do(t, e.srv, "PUT", "/api/articles/"+art+"/feedback", user, map[string]any{"rating": -1, "note": "开头太长"})
	do(t, e.srv, "PUT", "/api/articles/"+art+"/feedback", user, map[string]any{"rating": 1, "note": "改了一下，这次好多了"})
	var fb []map[string]any
	getList(t, e, "/api/admin/feedback", root, &fb)
	if len(fb) != 1 || fb[0]["rating"] != float64(1) || fb[0]["note"] != "改了一下，这次好多了" || fb[0]["owner_email"] != "u@example.com" {
		t.Fatalf("admin feedback: %v", fb)
	}
	_, a, _ = do(t, e.srv, "GET", "/api/articles/"+art, user, nil)
	if a["rating"] != float64(1) {
		t.Fatalf("article rating: %v", a["rating"])
	}

	// 5. draft read-back: untouched, then edited, then gone
	if code, _, b := do(t, e.srv, "POST", "/api/articles/"+art+"/push", user, nil); code != 200 {
		t.Fatalf("push: %s", b)
	}
	if err := e.pub.ReadBackDue(t.Context()); err != nil { // too early: nothing read
		t.Fatal(err)
	}
	_, a, _ = do(t, e.srv, "GET", "/api/articles/"+art, user, nil)
	if a["readback_status"] != "" {
		t.Fatalf("read back too early: %v", a["readback_status"])
	}
	e.pub.Now = func() time.Time { return time.Now().Add(3 * time.Hour) }
	if err := e.pub.ReadBackDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, a, _ = do(t, e.srv, "GET", "/api/articles/"+art, user, nil)
	if a["readback_status"] != "unedited" || a["change_ratio"] != float64(0) {
		t.Fatalf("untouched: %v %v", a["readback_status"], a["change_ratio"])
	}
	e.wx.mu.Lock()
	var mediaID string
	e.pool.QueryRow(t.Context(), `SELECT draft_media_id FROM article WHERE id=$1`, art).Scan(&mediaID)
	e.wx.edited[mediaID] = `<p>我是奶奶。学习桌照明要符合 GB 50034 的要求。</p><p>这段是用户自己加的：我家孙子用的是落地灯。</p>`
	e.wx.mu.Unlock()
	_, rb, _ := do(t, e.srv, "POST", "/api/articles/"+art+"/readback", user, nil)
	if rb["readback_status"] != "edited" || rb["change_ratio"].(float64) <= 0.1 {
		t.Fatalf("edited: %v", rb)
	}
	e.wx.mu.Lock()
	e.wx.gone[mediaID] = true
	e.wx.mu.Unlock()
	e.pool.Exec(t.Context(), `UPDATE article SET readback_status='unedited' WHERE id=$1`, art)
	_, rb, _ = do(t, e.srv, "POST", "/api/articles/"+art+"/readback", user, nil)
	if rb["readback_status"] != "gone" {
		t.Fatalf("gone: %v", rb)
	}

	// 6. auto-pause after N pushed-but-untouched articles without feedback (off by default)
	e.pool.Exec(t.Context(), `UPDATE article SET readback_status='unedited', pushed_at=now() - interval '4 days' WHERE id=$1`, art)
	if n, _ := e.jobs.AutoPause(t.Context()); n != 0 {
		t.Fatal("auto-pause ran while off")
	}
	if code, _, _ := do(t, e.srv, "PUT", "/api/admin/settings", root, map[string]any{"auto_pause_after": 1}); code != 200 {
		t.Fatal("settings")
	}
	if n, _ := e.jobs.AutoPause(t.Context()); n != 0 {
		t.Fatal("paused although the article has feedback")
	}
	e.pool.Exec(t.Context(), `DELETE FROM article_feedback`)
	if n, _ := e.jobs.AutoPause(t.Context()); n != 1 {
		t.Fatal("no auto-pause")
	}
	getList(t, e, "/api/accounts", user, &accs)
	if accs[0]["paused"] != true || accs[0]["paused_reason"] != "auto" {
		t.Fatalf("auto-paused flag: %v", accs[0])
	}
	// after a resume, only articles pushed later count
	do(t, e.srv, "PUT", acc+"/controls", user, map[string]any{"paused": false})
	if n, _ := e.jobs.AutoPause(t.Context()); n != 0 {
		t.Fatal("paused again right after resume")
	}
}

func getList(t *testing.T, e *env, path, tok string, out any) {
	t.Helper()
	code, _, body := do(t, e.srv, "GET", path, tok, nil)
	if code != 200 {
		t.Fatalf("GET %s: %d %s", path, code, body)
	}
	if err := jsonUnmarshal(body, out); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
