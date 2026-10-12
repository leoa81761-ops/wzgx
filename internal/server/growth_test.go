package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
)

func TestInviteRewardAndAntiAbuse(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	inviter := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{
		"account": "13800000001", "password": "password12", "agree": true,
	})
	if inviter.StatusCode != 200 {
		t.Fatal(inviter.StatusCode, inviter.Body)
	}
	token := inviter.JSON["token"].(string)
	inviterID := int64(inviter.JSON["user"].(map[string]any)["id"].(float64))
	ref := getJSON(t, ts.URL+"/api/me/referral", token)
	code := ref.JSON["code"].(string)
	if code == "" || ref.JSON["reward_per"].(float64) != 2 {
		t.Fatal(ref.JSON)
	}
	bogus := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{
		"account": "13800000009", "password": "password12", "agree": true, "invite_code": "no-such-code",
	})
	if bogus.StatusCode != 200 {
		t.Fatal(bogus.StatusCode, bogus.Body)
	}
	good := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{
		"account": "13800000002", "password": "password12", "agree": true, "invite_code": code,
		"device_id": "dev-good", "device_consent": true,
	})
	if good.StatusCode != 200 {
		t.Fatal(good.StatusCode, good.Body)
	}
	goodID := int64(good.JSON["user"].(map[string]any)["id"].(float64))
	goodToken := good.JSON["token"].(string)
	virtual := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{
		"account": "17000000001", "password": "password12", "agree": true, "invite_code": code,
	})
	if virtual.StatusCode != 200 {
		t.Fatal(virtual.StatusCode, virtual.Body)
	}
	same := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{
		"account": "13800000003", "password": "password12", "agree": true, "invite_code": code,
		"device_id": "dev-good", "device_consent": true,
	})
	if same.StatusCode != 200 {
		t.Fatal(same.StatusCode, same.Body)
	}
	burst := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{
		"account": "13800000004", "password": "password12", "agree": true, "invite_code": code,
		"device_id": "dev-other", "device_consent": true,
	})
	if burst.StatusCode != 200 {
		t.Fatal(burst.StatusCode, burst.Body)
	}
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	adminToken := admin.JSON["token"].(string)
	rows := getJSON(t, ts.URL+"/api/admin/referrals", adminToken)
	statusOf := map[string]string{}
	for _, row := range rows.JSONList {
		statusOf[row["invitee"].(string)] = row["status"].(string)
	}
	if statusOf["13800000002"] != "pending" || statusOf["17000000001"] != "revoked" || statusOf["13800000003"] != "frozen" || statusOf["13800000004"] != "frozen" {
		t.Fatal(statusOf)
	}
	bound := postJSON(t, ts.URL+"/api/accounts", goodToken, map[string]any{
		"name": "邀请号", "app_id": "wx_mock_invitee", "app_secret": "mock-secret",
		"subject_type": "personal", "service_type": "subscription", "verified": false, "consent": true,
	})
	if bound.StatusCode != 200 {
		t.Fatal(bound.StatusCode, bound.Body)
	}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := app.Store.InsertArticle(ctx, nil, &model.Article{UserID: goodID, Title: "稿", Status: "in_draft", CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.DB.SQL.ExecContext(ctx, `UPDATE referral SET observe_until = ? WHERE invitee_id = ?`, schedule.Format(time.Now().Add(-time.Hour)), goodID); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Runner.ScanOnce(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	quota := getJSON(t, ts.URL+"/api/me/quota", token)
	if quota.JSON["period_limit"].(float64) != 12 || quota.JSON["invite_bonus"].(float64) != 2 {
		t.Fatal(quota.JSON)
	}
	upgraded := putJSON(t, ts.URL+"/api/admin/users/"+strconv.FormatInt(inviterID, 10)+"/tier", adminToken, map[string]any{"tier": "standard", "note": "测试"})
	if upgraded.StatusCode != 200 {
		t.Fatal(upgraded.StatusCode, upgraded.Body)
	}
	paid := getJSON(t, ts.URL+"/api/me/quota", token)
	if paid.JSON["period_limit"].(float64) != 30 || paid.JSON["invite_bonus"].(float64) != 0 {
		t.Fatal(paid.JSON)
	}
}

func TestTopicConfirmWaitsForDecision(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	user := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{
		"account": "writer-confirm@example.com", "password": "password12", "agree": true,
	})
	token := user.JSON["token"].(string)
	userID := int64(user.JSON["user"].(map[string]any)["id"].(float64))
	bound := postJSON(t, ts.URL+"/api/accounts", token, map[string]any{
		"name": "确认号", "app_id": "wx_mock_confirm", "app_secret": "mock-secret",
		"subject_type": "personal", "service_type": "subscription", "verified": false, "consent": true,
	})
	if bound.StatusCode != 200 {
		t.Fatal(bound.StatusCode, bound.Body)
	}
	accountID := int64(bound.JSON["id"].(float64))
	created := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": accountID, "direction": "职场成长", "style_key": "warm", "template_key": "simple",
		"word_target": 800, "schedule_type": "daily", "time_window": "morning", "daily_count": 1,
	})
	if created.StatusCode != 200 {
		t.Fatal(created.StatusCode, created.Body)
	}
	planID := int64(created.JSON["id"].(float64))
	denied := putJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(planID, 10)+"/confirm", token, map[string]any{"enabled": true})
	if denied.StatusCode != 400 {
		t.Fatal(denied.StatusCode, denied.Body)
	}
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	up := putJSON(t, ts.URL+"/api/admin/users/"+strconv.FormatInt(userID, 10)+"/tier", admin.JSON["token"].(string), map[string]any{"tier": "premium", "note": "测试"})
	if up.StatusCode != 200 {
		t.Fatal(up.StatusCode, up.Body)
	}
	second := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": accountID, "direction": "城市徒步", "style_key": "warm", "template_key": "simple",
		"word_target": 800, "schedule_type": "daily", "time_window": "morning", "daily_count": 1,
	})
	if second.StatusCode != 200 {
		t.Fatal(second.StatusCode, second.Body)
	}
	planTwo := int64(second.JSON["id"].(float64))
	now := time.Now().UTC().Truncate(time.Second)
	next := now.Add(2 * time.Hour)
	ctx := context.Background()
	for _, id := range []int64{planID, planTwo} {
		enabled := putJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(id, 10)+"/confirm", token, map[string]any{"enabled": true})
		if enabled.StatusCode != 200 || enabled.JSON["confirm_mode"] != true {
			t.Fatal(enabled.StatusCode, enabled.Body)
		}
		if _, err := app.DB.SQL.ExecContext(ctx, `UPDATE writing_plan SET next_run_at = ? WHERE id = ?`, schedule.Format(next), id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Runner.ScanOnce(ctx, now); err != nil {
		t.Fatal(err)
	}
	if tasks := countTasks(t, app, planID) + countTasks(t, app, planTwo); tasks != 0 {
		t.Fatal(tasks)
	}
	plans := getJSON(t, ts.URL+"/api/plans", token)
	withProposal := 0
	for _, plan := range plans.JSONList {
		if plan["proposal"] != nil {
			withProposal++
		}
	}
	if withProposal != 2 {
		t.Fatal(plans.Body)
	}
	accepted := postJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(planID, 10)+"/proposal/accept", token, map[string]any{})
	if accepted.StatusCode != 200 || accepted.JSON["proposal"].(map[string]any)["status"] != "approved" {
		t.Fatal(accepted.StatusCode, accepted.Body)
	}
	if _, err := app.Runner.ScanOnce(ctx, next); err != nil {
		t.Fatal(err)
	}
	if countTasks(t, app, planID) != 1 || countTasks(t, app, planTwo) != 0 {
		t.Fatalf("accepted %d waiting %d", countTasks(t, app, planID), countTasks(t, app, planTwo))
	}
	if _, err := app.Runner.ScanOnce(ctx, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if countTasks(t, app, planTwo) != 1 {
		t.Fatal(countTasks(t, app, planTwo))
	}
	var tag string
	if err := app.DB.SQL.QueryRowContext(ctx, `SELECT topic_tag FROM topic_lock WHERE task_id = (SELECT id FROM article_task WHERE plan_id = ? ORDER BY id DESC LIMIT 1)`, planTwo).Scan(&tag); err != nil || tag == "" {
		t.Fatal(tag, err)
	}
}

func TestHotSourceUsesCache(t *testing.T) {
	var hits int32
	title := "甲热点"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"title": title, "url": "https://www.gov.cn/a", "heat": 9}}})
	}))
	defer upstream.Close()
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	token := admin.JSON["token"].(string)
	saved := postJSON(t, ts.URL+"/api/admin/hot-sources", token, map[string]any{
		"name": "热榜", "protocol": "http", "base_url": upstream.URL, "api_key": "secret", "enabled": true,
		"extra_params": map[string]any{"list": "items", "cache_hours": 12},
	})
	if saved.StatusCode != 200 {
		t.Fatal(saved.StatusCode, saved.Body)
	}
	id := int64(saved.JSON["id"].(float64))
	probed := postJSON(t, ts.URL+"/api/admin/hot-sources/"+strconv.FormatInt(id, 10)+"/test", token, map[string]any{})
	if probed.StatusCode != 200 || probed.JSON["count"].(float64) != 1 {
		t.Fatal(probed.StatusCode, probed.Body)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := app.Runner.ScanOnce(ctx, now); err != nil {
		t.Fatal(err)
	}
	title = "乙热点"
	if _, err := app.Runner.ScanOnce(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	rows, err := app.Store.ListHotItems(ctx, nil)
	if err != nil || len(rows) != 1 || rows[0].Title != "甲热点" || rows[0].Reports < 1 {
		t.Fatalf("%#v %v", rows, err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("热点在缓存期内又请求了上游: %d", got)
	}
}

func countTasks(t *testing.T, app *App, planID int64) int {
	t.Helper()
	var n int
	if err := app.DB.SQL.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM article_task WHERE plan_id = ?`, planID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
