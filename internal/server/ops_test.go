package server

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/leoa81761-ops/wzgx/internal/config"
)

func TestOpsSurface(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{
		"account": "ops@example.com", "password": "password12", "agree": true,
	})
	if registered.StatusCode != 200 {
		t.Fatal(registered.StatusCode, registered.Body)
	}
	token := registered.JSON["token"].(string)
	bound := postJSON(t, ts.URL+"/api/accounts", token, map[string]any{
		"name": "测试号", "app_id": "wx_mock_ops", "app_secret": "mock-secret",
		"subject_type": "personal", "service_type": "subscription", "verified": false, "consent": true,
	})
	if bound.StatusCode != 200 {
		t.Fatal(bound.StatusCode, bound.Body)
	}
	accountID := int64(bound.JSON["id"].(float64))
	created := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": accountID, "direction": "职场成长", "style_key": "warm", "template_key": "simple",
		"word_target": 800, "schedule_type": "daily", "weekdays": []int{}, "time_window": "night",
	})
	if created.StatusCode != 200 {
		t.Fatal(created.StatusCode, created.Body)
	}
	planID := int64(created.JSON["id"].(float64))
	cal := getJSON(t, ts.URL+"/api/plans/"+itoa(planID)+"/calendar?count=3", token)
	items, _ := cal.JSON["items"].([]any)
	if cal.StatusCode != 200 || len(items) != 3 {
		t.Fatal(cal.StatusCode, cal.Body)
	}
	first, _ := items[0].(map[string]any)
	tag, _ := first["topic_tag"].(string)
	veto := postJSON(t, ts.URL+"/api/plans/"+itoa(planID)+"/veto", token, map[string]any{"topic_tag": tag})
	if veto.StatusCode != 200 || len(veto.JSON["vetoes"].([]any)) != 1 {
		t.Fatal(veto.StatusCode, veto.Body)
	}
	pinned := putJSON(t, ts.URL+"/api/plans/"+itoa(planID)+"/pin", token, map[string]any{"title": "下周例会怎么记"})
	if pinned.StatusCode != 200 || pinned.JSON["specified_title"] != "下周例会怎么记" {
		t.Fatal(pinned.StatusCode, pinned.Body)
	}
	generated := postJSON(t, ts.URL+"/api/plans/"+itoa(planID)+"/generate", token, map[string]any{})
	if generated.StatusCode != 200 {
		t.Fatal(generated.StatusCode, generated.Body)
	}
	articleID := int64(generated.JSON["id"].(float64))
	if generated.JSON["status"] == "generating" {
		generated.JSON = waitArticle(t, ts.URL, token, articleID)
	}
	if generated.JSON["title"] != "下周例会怎么记" {
		t.Fatalf("specified title not used: %#v", generated.JSON["title"])
	}
	plans := getJSON(t, ts.URL+"/api/plans", token)
	if plans.StatusCode != 200 || len(plans.JSONList) == 0 || plans.JSONList[0]["specified_title"] != "" {
		t.Fatalf("pin should clear after a successful run: %#v", plans.JSONList)
	}
	again := getJSON(t, ts.URL+"/api/plans/"+itoa(planID)+"/calendar?count=2", token)
	nextItems, _ := again.JSON["items"].([]any)
	if len(nextItems) == 0 {
		t.Fatal(again.Body)
	}
	next, _ := nextItems[0].(map[string]any)
	if next["topic_tag"] == tag {
		t.Fatalf("calendar still shows vetoed tag %s", tag)
	}
	reread := postJSON(t, ts.URL+"/api/articles/"+itoa(articleID)+"/reread", token, map[string]any{})
	if reread.StatusCode != 200 || (reread.JSON["draft_state"] != "unchanged" && reread.JSON["draft_state"] != "modified") {
		t.Fatal(reread.StatusCode, reread.Body)
	}
	fb := postJSON(t, ts.URL+"/api/articles/"+itoa(articleID)+"/feedback", token, map[string]any{"kind": "published", "score": 4, "note": "已在手机上发出"})
	if fb.StatusCode != 200 || fb.JSON["kind"] != "published" {
		t.Fatal(fb.StatusCode, fb.Body)
	}
	complaint := postJSON(t, ts.URL+"/api/complaints", token, map[string]any{"kind": "content", "content": "这篇稿和我的方向不符"})
	if complaint.StatusCode != 200 {
		t.Fatal(complaint.StatusCode, complaint.Body)
	}
	anon := postJSON(t, ts.URL+"/api/complaints", "", map[string]any{"kind": "privacy", "content": "想删除个人数据", "contact": "reader@example.com"})
	if anon.StatusCode != 200 {
		t.Fatal(anon.StatusCode, anon.Body)
	}
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	adminToken := admin.JSON["token"].(string)
	listed := getJSON(t, ts.URL+"/api/admin/complaints", adminToken)
	if listed.StatusCode != 200 || len(listed.JSONList) < 2 {
		t.Fatal(listed.StatusCode, listed.Body)
	}
	replyID := int64(listed.JSONList[0]["id"].(float64))
	replied := putJSON(t, ts.URL+"/api/admin/complaints/"+itoa(replyID), adminToken, map[string]any{"status": "closed", "reply": "已记录"})
	if replied.StatusCode != 200 {
		t.Fatal(replied.StatusCode, replied.Body)
	}
	tasks := getJSON(t, ts.URL+"/api/admin/tasks", adminToken)
	if tasks.StatusCode != 200 || len(tasks.JSONList) == 0 {
		t.Fatal(tasks.StatusCode, tasks.Body)
	}
	checkout := postJSON(t, ts.URL+"/api/billing/checkout", token, map[string]any{"tier": "standard"})
	if checkout.StatusCode != 501 || !strings.Contains(checkout.JSON["detail"].(string), "即将开通") {
		t.Fatal(checkout.StatusCode, checkout.Body)
	}
	exported := getJSON(t, ts.URL+"/api/me/export", token)
	if exported.StatusCode != 200 || strings.Contains(exported.Body, "mock-secret") || strings.Contains(exported.Body, "secret_encrypted") {
		t.Fatal("export leaked a secret", exported.StatusCode)
	}
	gone := sendJSON(t, "DELETE", ts.URL+"/api/me", token, map[string]any{"password": "password12"})
	if gone.StatusCode != 200 {
		t.Fatal(gone.StatusCode, gone.Body)
	}
	againLogin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "ops@example.com", "password": "password12"})
	if againLogin.StatusCode == 200 {
		t.Fatal("deleted account can still log in")
	}
}

func TestAnonymousComplaintRateLimit(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	body := map[string]any{"kind": "other", "content": "请查一下这条投诉", "contact": "limit@example.com"}
	for i := 0; i < 5; i++ {
		resp := postJSON(t, ts.URL+"/api/complaints", "", body)
		if resp.StatusCode != 200 {
			t.Fatalf("attempt %d: %d %s", i+1, resp.StatusCode, resp.Body)
		}
	}
	blocked := postJSON(t, ts.URL+"/api/complaints", "", body)
	if blocked.StatusCode != 429 {
		t.Fatal(blocked.StatusCode, blocked.Body)
	}
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{
		"account": "limit-user@example.com", "password": "password12", "agree": true,
	})
	if registered.StatusCode != 200 {
		t.Fatal(registered.StatusCode, registered.Body)
	}
	token := registered.JSON["token"].(string)
	logged := postJSON(t, ts.URL+"/api/complaints", token, map[string]any{"kind": "content", "content": "登录后仍可提交"})
	if logged.StatusCode != 200 {
		t.Fatal(logged.StatusCode, logged.Body)
	}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }
