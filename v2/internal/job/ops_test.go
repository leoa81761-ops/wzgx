package job_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestOpsCostsBudgetOrders(t *testing.T) {
	e, root, user, accID, colID := setup(t)
	ctx := t.Context()
	e.fm.set("research", "", "未检索到可靠来源")
	e.fm.set("write", "", article1)
	e.fm.set("review", "", `{"pass":true}`)
	e.pool.Exec(ctx, `UPDATE users SET quota_exempt=TRUE WHERE email='u@example.com'`)
	var writeID, uid int64
	e.pool.QueryRow(ctx, `SELECT id FROM service_config WHERE name='write'`).Scan(&writeID)
	e.pool.QueryRow(ctx, `SELECT id FROM users WHERE email='u@example.com'`).Scan(&uid)
	n := 0
	gen := func() float64 {
		n++
		_, m, b := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/generate", accID), user, map[string]any{"column_id": colID, "topic": fmt.Sprintf("第%d个题目：冬天怎么给老人选拖鞋", n)})
		if m["job_id"] == nil {
			t.Fatal(b)
		}
		return m["job_id"].(float64)
	}

	// prices: super admin only, recorded cost = tokens × price
	pp := fmt.Sprintf("/api/admin/configs/%d/pricing", writeID)
	if code, _, b := do(t, e.srv, "PUT", pp, root, map[string]any{"price_in_per_m": -1}); code != 400 {
		t.Fatalf("negative price: %d %s", code, b)
	}
	if code, c, b := do(t, e.srv, "PUT", pp, root, map[string]any{"price_in_per_m": 2, "price_out_per_m": 8}); code != 200 || c["price_out_per_m"] != float64(8) {
		t.Fatalf("pricing: %d %s", code, b)
	}
	if code, _, _ := do(t, e.srv, "PUT", pp, user, map[string]any{"price_in_per_m": 1}); code != 403 {
		t.Fatal("user set a price")
	}
	j1 := gen()
	e.run()
	var cost float64
	e.pool.QueryRow(ctx, `SELECT sum(cost)::float8 FROM model_call WHERE config_id=$1`, writeID).Scan(&cost)
	if cost < 0.0179 || cost > 0.0181 { // 1000 in × 2 + 2000 out × 8 per million
		t.Fatalf("cost %v", cost)
	}
	_, c, _ := do(t, e.srv, "GET", "/api/admin/costs?days=7", root, nil)
	if bc := c["by_config"].([]any); len(bc) == 0 || bc[0].(map[string]any)["cost"].(float64) < 0.0179 {
		t.Fatalf("by config: %v", c["by_config"])
	}
	if bu := c["by_user"].([]any); len(bu) != 1 || bu[0].(map[string]any)["label"] != "u@example.com" {
		t.Fatalf("by user: %v", c["by_user"])
	}
	if bd := c["by_day"].([]any); len(bd) != 1 {
		t.Fatalf("by day: %v", c["by_day"])
	}

	// task monitor: detail with model attempts
	_, d, _ := do(t, e.srv, "GET", fmt.Sprintf("/api/admin/jobs/%v", j1), root, nil)
	if calls := d["calls"].([]any); len(calls) < 2 || d["job"].(map[string]any)["status"] != "done" {
		t.Fatalf("detail: %v", d)
	}
	if code, _, _ := do(t, e.srv, "GET", "/api/admin/jobs", user, nil); code != 403 {
		t.Fatal("user saw the task monitor")
	}

	// per-config cap: the capped config is skipped, the next one in the route is used
	_, c2, _ := do(t, e.srv, "POST", "/api/admin/configs", root, map[string]any{"kind": "text", "name": "write-b", "base_url": e.fm.url + "/write/v1",
		"api_key": "sk-fake-0123456789", "model": "m-write-b", "enabled": true})
	do(t, e.srv, "PUT", fmt.Sprintf("/api/admin/configs/%v/pricing", c2["id"]), root, map[string]any{"price_in_per_m": 2, "price_out_per_m": 8})
	do(t, e.srv, "PUT", "/api/admin/routes/write", root, map[string]any{"config_ids": []any{writeID, c2["id"]}})
	do(t, e.srv, "PUT", pp, root, map[string]any{"price_in_per_m": 2, "price_out_per_m": 8, "daily_cap": 0.01})
	j2 := gen()
	e.run()
	var used string
	e.pool.QueryRow(ctx, `SELECT c.name FROM model_call m JOIN service_config c ON c.id=m.config_id WHERE m.job_id=$1 AND m.stage='write'`, int64(j2)).Scan(&used)
	if used != "write-b" {
		t.Fatalf("capped config not skipped: %q", used)
	}
	// every write config capped -> the job waits, admin sees the banner
	do(t, e.srv, "PUT", fmt.Sprintf("/api/admin/configs/%v/pricing", c2["id"]), root, map[string]any{"price_in_per_m": 2, "price_out_per_m": 8, "daily_cap": 0.01})
	j3 := gen()
	e.run()
	if j := e.jobStatus(user, j3); j["status"] != "queued" {
		t.Fatalf("not waiting: %v", j)
	}
	var att int
	e.pool.QueryRow(ctx, `SELECT attempt FROM job WHERE id=$1`, int64(j3)).Scan(&att)
	if att != 0 {
		t.Fatalf("budget wait counted as attempt: %d", att)
	}
	_, bg, _ := do(t, e.srv, "GET", "/api/admin/budget", root, nil)
	if bg["any_hit"] != true || bg["waiting_jobs"] != float64(1) {
		t.Fatalf("budget: %v", bg)
	}
	// lift the caps; platform cap below today's spend stops claiming at all
	do(t, e.srv, "PUT", pp, root, map[string]any{"price_in_per_m": 2, "price_out_per_m": 8})
	if code, _, b := do(t, e.srv, "PUT", "/api/admin/settings", root, map[string]any{"auto_pause_after": 0, "daily_cap": 0.01}); code != 200 {
		t.Fatal(b)
	}
	e.pool.Exec(ctx, `UPDATE job SET run_at=now() WHERE id=$1`, int64(j3))
	if ran, _ := e.jobs.RunOnce(ctx); ran {
		t.Fatal("ran over the platform cap")
	}
	_, bg, _ = do(t, e.srv, "GET", "/api/admin/budget", root, nil)
	if bg["platform_hit"] != true {
		t.Fatalf("platform banner: %v", bg)
	}
	do(t, e.srv, "PUT", "/api/admin/settings", root, map[string]any{"auto_pause_after": 0, "daily_cap": 0})

	// cancel a queued job, retry it, only finished ones can be retried
	jp := fmt.Sprintf("/api/admin/jobs/%v", j3)
	if code, _, b := do(t, e.srv, "POST", jp+"/retry", root, nil); code != 400 {
		t.Fatalf("retried a queued job: %s", b)
	}
	if code, _, b := do(t, e.srv, "POST", jp+"/cancel", root, nil); code != 200 {
		t.Fatal(b)
	}
	_, l, _ := do(t, e.srv, "GET", "/api/admin/jobs?status=cancelled", root, nil)
	if js := l["jobs"].([]any); len(js) != 1 {
		t.Fatalf("cancelled list: %v", l)
	}
	if code, _, _ := do(t, e.srv, "POST", jp+"/cancel", root, nil); code != 400 {
		t.Fatal("cancelled twice")
	}
	if code, _, b := do(t, e.srv, "POST", jp+"/retry", root, nil); code != 200 {
		t.Fatal(b)
	}
	e.run()
	if j := e.jobStatus(user, j3); j["status"] != "done" {
		t.Fatalf("retry: %v", j)
	}

	// consents
	var cons []map[string]any
	getList(t, e, fmt.Sprintf("/api/admin/users/%d/consents", uid), root, &cons)
	if len(cons) != 2 {
		t.Fatalf("consents: %v", cons)
	}

	// orders: manual activation applies the plan and is audited
	if code, _, _ := do(t, e.srv, "POST", "/api/admin/orders", root, map[string]any{"user_id": uid, "tier": "gold"}); code != 400 {
		t.Fatal("bad tier accepted")
	}
	if code, _, _ := do(t, e.srv, "POST", "/api/admin/orders", user, map[string]any{"user_id": uid, "tier": "premium"}); code != 403 {
		t.Fatal("user activated a plan")
	}
	if code, _, b := do(t, e.srv, "POST", "/api/admin/orders", root, map[string]any{"user_id": uid, "tier": "premium", "max_accounts": 5, "daily_quota": 3, "months": 12, "amount": 999, "note": "线下转账"}); code != 201 {
		t.Fatal(b)
	}
	var tier string
	var maxAcc, quota int
	var hasExp bool
	e.pool.QueryRow(ctx, `SELECT tier, max_accounts, daily_quota, tier_expires_at > now() + interval '360 days' FROM users WHERE id=$1`, uid).Scan(&tier, &maxAcc, &quota, &hasExp)
	if tier != "premium" || maxAcc != 5 || quota != 3 || !hasExp {
		t.Fatalf("plan not applied: %s %d %d %v", tier, maxAcc, quota, hasExp)
	}
	var orders []map[string]any
	getList(t, e, "/api/admin/orders", root, &orders)
	if len(orders) != 1 || orders[0]["note"] != "线下转账" || orders[0]["created_by"] != "root@example.com" {
		t.Fatalf("orders: %v", orders)
	}
	var aud string
	e.pool.QueryRow(ctx, `SELECT string_agg(action, ',') FROM audit_log`).Scan(&aud)
	for _, a := range []string{"order.activate", "config.pricing", "job.cancel", "job.retry"} {
		if !strings.Contains(aud, a) {
			t.Fatalf("audit missing %s: %s", a, aud)
		}
	}
}
