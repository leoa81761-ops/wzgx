package job_test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestWritePlans(t *testing.T) {
	e, root, user, accID, colID := setup(t)
	base := fmt.Sprintf("/api/accounts/%d/plans", accID)
	// validation
	if code, _, _ := do(t, e.srv, "POST", base, user, map[string]any{"weekdays": []int{8}, "time": "08:00", "enabled": true}); code != 400 {
		t.Fatal("weekday 8")
	}
	if code, _, _ := do(t, e.srv, "POST", base, user, map[string]any{"weekdays": []int{1}, "time": "8am", "enabled": true}); code != 400 {
		t.Fatal("bad time")
	}
	if code, _, _ := do(t, e.srv, "POST", base, root, map[string]any{"weekdays": []int{1}, "time": "08:00"}); code != 403 {
		t.Fatal("staff can't own plans")
	}
	// Saturday 2026-10-17 and Monday, 09:30 Beijing time, fixed column
	code, p, b := do(t, e.srv, "POST", base, user, map[string]any{"weekdays": []int{6, 1, 6}, "time": "09:30", "column_id": colID, "enabled": true})
	if code != 200 || fmt.Sprint(p["weekdays"]) != "[1 6]" || p["time"] != "09:30" {
		t.Fatalf("save: %d %s", code, b)
	}
	cst := time.FixedZone("CST", 8*3600)
	at := func(h, m int) { e.jobs.Now = func() time.Time { return time.Date(2026, 10, 17, h, m, 0, 0, cst) } }
	n := func() int {
		var c int
		e.pool.QueryRow(t.Context(), `SELECT count(*) FROM job`).Scan(&c)
		return c
	}
	at(9, 29)
	if q, err := e.jobs.Schedule(t.Context()); q != 0 || err != nil || n() != 0 {
		t.Fatalf("too early: %d %v", q, err)
	}
	at(9, 31)
	if q, _ := e.jobs.Schedule(t.Context()); q != 1 || n() != 1 {
		t.Fatalf("due: %d jobs=%d", q, n())
	}
	if q, _ := e.jobs.Schedule(t.Context()); q != 0 || n() != 1 {
		t.Fatal("once a day")
	}
	var dry bool
	var col int64
	e.pool.QueryRow(t.Context(), `SELECT dry_run, column_id FROM job`).Scan(&dry, &col)
	if !dry || col != colID {
		t.Fatal("plan jobs are dry runs on the plan's column")
	}
	// Sunday: not a plan day
	e.jobs.Now = func() time.Time { return time.Date(2026, 10, 18, 10, 0, 0, 0, cst) }
	if q, _ := e.jobs.Schedule(t.Context()); q != 0 {
		t.Fatal("sunday")
	}
	// Monday: due again, but the daily quota (1) of that day is free -> queued; a second plan that day hits quota
	do(t, e.srv, "POST", base, user, map[string]any{"weekdays": []int{1}, "time": "11:00", "enabled": true})
	e.jobs.Now = func() time.Time { return time.Date(2026, 10, 19, 12, 0, 0, 0, cst) }
	if q, _ := e.jobs.Schedule(t.Context()); q != 1 {
		t.Fatalf("monday queued %d", q)
	}
	_, _, ps := doList(t, e, base, user)
	if !strings.Contains(ps, "今日额度已用完") || !strings.Contains(ps, "已排队") {
		t.Fatalf("results: %s", ps)
	}
	// disabled plans don't run; delete works
	do(t, e.srv, "PUT", fmt.Sprintf("%s/%v", base, p["id"]), user, map[string]any{"weekdays": []int{2}, "time": "09:30", "enabled": false})
	e.jobs.Now = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, cst) }
	if q, _ := e.jobs.Schedule(t.Context()); q != 0 {
		t.Fatal("disabled")
	}
	if code, _, _ := do(t, e.srv, "DELETE", fmt.Sprintf("%s/%v", base, p["id"]), user, nil); code != 200 {
		t.Fatal("delete")
	}
}

func doList(t *testing.T, e *env, path, tok string) (int, any, string) {
	code, _, b := do(t, e.srv, "GET", path, tok, nil)
	return code, nil, b
}
