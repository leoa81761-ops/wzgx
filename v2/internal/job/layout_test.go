package job_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

var tagRe = regexp.MustCompile(`<[^>]+>`)

func TestLayoutsPreviewCalendar(t *testing.T) {
	e, root, user, accID, colID := setup(t)
	ctx := t.Context()
	e.fm.set("research", "", "未检索到可靠来源")
	e.fm.set("write", "", article1)
	e.fm.set("review", "", `{"pass":true}`)
	e.pool.Exec(ctx, `UPDATE users SET quota_exempt=TRUE WHERE email='u@example.com'`)
	acc := fmt.Sprintf("/api/accounts/%d", accID)
	gen := func(topic string) string {
		_, m, b := do(t, e.srv, "POST", acc+"/generate", user, map[string]any{"column_id": colID, "topic": topic})
		if m["job_id"] == nil {
			t.Fatal(b)
		}
		e.run()
		j := e.jobStatus(user, m["job_id"])
		return fmt.Sprint(j["article_id"])
	}
	pushed, open := gen("先推送的这一篇：台灯怎么摆"), gen("还没推送的这一篇：作业怎么陪")
	if code, _, b := do(t, e.srv, "POST", "/api/articles/"+pushed+"/push", user, nil); code != 200 {
		t.Fatalf("push: %s", b)
	}
	html := func(id string) string {
		var h string
		e.pool.QueryRow(ctx, `SELECT html FROM article WHERE id=$1`, id).Scan(&h)
		return h
	}
	text := func(h string) string { return tagRe.ReplaceAllString(h, "") }
	before, pushedBefore := html(open), html(pushed)

	// layouts listed, default classic
	_, l, _ := do(t, e.srv, "GET", acc+"/layout", user, nil)
	if l["layout"] != "classic" || len(l["layouts"].([]any)) != 4 {
		t.Fatalf("layouts: %v", l)
	}
	if code, _, _ := do(t, e.srv, "PUT", acc+"/layout", user, map[string]any{"layout": "neon"}); code != 400 {
		t.Fatal("unknown layout accepted")
	}
	if code, _, _ := do(t, e.srv, "PUT", acc+"/layout", root, map[string]any{"layout": "fresh"}); code != 403 {
		t.Fatal("staff changed a user's layout")
	}

	// try a layout in the preview without saving
	_, _, pv := do(t, e.srv, "GET", "/api/articles/"+open+"/preview?layout=warm", user, nil)
	if !strings.Contains(pv, "#e8a65d") || html(open) != before {
		t.Fatal("layout preview")
	}
	_, _, dk := do(t, e.srv, "GET", "/api/articles/"+open+"/preview?theme=dark", user, nil)
	_, _, lt := do(t, e.srv, "GET", "/api/articles/"+open+"/preview", user, nil)
	if !strings.Contains(dk, "invert(") || strings.Contains(lt, "invert(") || !strings.Contains(dk, "先看灯光") {
		t.Fatal("dark preview")
	}

	// switching re-renders only the unpushed article: new styles, same words, no model call
	writes := len(e.fm.reqs["write"])
	_, r, b := do(t, e.srv, "PUT", acc+"/layout", user, map[string]any{"layout": "fresh"})
	if r["rerendered"] != float64(1) {
		t.Fatalf("rerender: %s", b)
	}
	after := html(open)
	if after == before || !strings.Contains(after, "#3a9d6a") || text(after) != text(before) {
		t.Fatalf("re-render changed words or nothing:\n%s\n%s", text(before), text(after))
	}
	if html(pushed) != pushedBefore || len(e.fm.reqs["write"]) != writes {
		t.Fatal("pushed article re-rendered or model called")
	}
	_, a, _ := do(t, e.srv, "GET", "/api/articles/"+open, user, nil)
	if a["layout"] != "fresh" {
		t.Fatalf("article layout: %v", a["layout"])
	}
	// new articles use the account layout
	if third := gen("第三篇换了排版之后写：书包怎么减"); !strings.Contains(html(third), "#3a9d6a") {
		t.Fatal("new article ignores layout")
	}

	// calendar: this month's articles and the plan slots still to come
	do(t, e.srv, "POST", acc+"/plans", user, map[string]any{"weekdays": []int{1, 2, 3, 4, 5, 6, 7}, "time": "23:59", "enabled": true})
	month := time.Now().In(time.FixedZone("CST", 8*3600))
	var cal map[string]any
	_, cal, b = do(t, e.srv, "GET", acc+"/calendar?month="+month.Format("2006-01"), user, nil)
	days, _ := cal["days"].([]any)
	if len(days) < 28 {
		t.Fatalf("calendar: %s", b)
	}
	arts, planned, pastPlanned := 0, 0, 0
	for _, d := range days {
		dm := d.(map[string]any)
		arts += len(dm["articles"].([]any))
		n := len(dm["planned"].([]any))
		planned += n
		if dm["date"].(string) < cal["today"].(string) {
			pastPlanned += n
		}
	}
	if arts != 3 || planned == 0 || pastPlanned != 0 {
		t.Fatalf("calendar counts: articles %d planned %d past %d", arts, planned, pastPlanned)
	}
	if code, _, _ := do(t, e.srv, "GET", acc+"/calendar", root, nil); code != 403 {
		t.Fatal("staff opened a user's calendar")
	}
}
