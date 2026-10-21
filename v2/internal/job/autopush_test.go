package job_test

import (
	"fmt"
	"testing"
)

// 自动推送草稿箱: only plan-written articles that passed, only when the account has it on, once.
func TestAutoPush(t *testing.T) {
	e, _, user, accID, colID := setup(t)
	ctx := t.Context()
	e.fm.set("research", "", research)
	e.fm.set("write", "", article1)
	e.fm.set("review", "", `{"pass":true}`)
	e.pool.Exec(ctx, `UPDATE users SET quota_exempt=TRUE WHERE email='u@example.com'`)
	drafts := func() int { e.wx.mu.Lock(); defer e.wx.mu.Unlock(); return len(e.wx.drafts) }
	status := func(id any) string {
		var s string
		e.pool.QueryRow(ctx, `SELECT status FROM article WHERE id=$1`, id).Scan(&s)
		return s
	}
	planRun := func(topic string) any {
		e.fm.set("topic", "", `{"topic":"`+topic+`","angle":"亲自试过","search_query":"`+topic+`"}`)
		e.pool.Exec(ctx, `UPDATE write_plan SET last_run_on=NULL`)
		e.pool.Exec(ctx, `UPDATE job SET idem_key=idem_key||':'||id`) // same day: let the plan run again
		if n, err := e.jobs.Schedule(ctx); err != nil || n != 1 {
			t.Fatalf("schedule %d %v", n, err)
		}
		e.run()
		var id int64
		e.pool.QueryRow(ctx, `SELECT a.id FROM article a JOIN job j ON j.id=a.job_id WHERE j.source='plan' ORDER BY a.id DESC LIMIT 1`).Scan(&id)
		return id
	}
	if code, _, b := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/plans", accID), user, map[string]any{
		"column_id": colID, "weekdays": []int{1, 2, 3, 4, 5, 6, 7}, "time": "00:00", "enabled": true}); code != 200 {
		t.Fatal(b)
	}

	// off (default): a plan article stays ready
	a1 := planRun("孙子写作业磨蹭，台灯和计时器怎么用")
	if err := e.pub.AutoPushDue(ctx); err != nil || status(a1) != "ready" || drafts() != 0 {
		t.Fatalf("pushed while off: %s %d %v", status(a1), drafts(), err)
	}
	// on: the next plan article is pushed once; the older one (before switching on) too, since it is still ready and recent
	if code, _, b := do(t, e.srv, "PUT", fmt.Sprintf("/api/accounts/%d/controls", accID), user, map[string]any{"auto_push": true}); code != 200 {
		t.Fatal(b)
	}
	_, acc, _ := do(t, e.srv, "GET", fmt.Sprintf("/api/accounts/%d", accID), user, nil)
	if acc["auto_push"] != true {
		t.Fatalf("account: %v", acc["auto_push"])
	}
	a2 := planRun("冬天给老人选电热毯，功率和定时怎么看")
	e.pub.AutoPushDue(ctx)
	if status(a2) != "pushed" || status(a1) != "pushed" || drafts() != 2 {
		t.Fatalf("auto push: %s %s %d", status(a1), status(a2), drafts())
	}
	e.pub.AutoPushDue(ctx)
	if drafts() != 2 {
		t.Fatal("pushed twice")
	}
	// a manual "write now" article is not pushed by itself
	_, g, _ := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/generate", accID), user, map[string]any{"column_id": colID, "topic": "台灯怎么选"})
	e.run()
	man := e.jobStatus(user, g["job_id"])["article_id"]
	e.pub.AutoPushDue(ctx)
	if status(man) != "ready" || drafts() != 2 {
		t.Fatalf("manual article pushed: %s", status(man))
	}
	var n int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='article.push'`).Scan(&n)
	if n != 2 {
		t.Fatalf("audit %d", n)
	}
}
