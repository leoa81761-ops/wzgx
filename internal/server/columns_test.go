package server

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/leoa81761-ops/wzgx/internal/config"
)

func TestBindSuggestsColumnsAndAdminCanExemptQuota(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{
		"account": "cols@example.com", "password": "password12", "agree": true,
	})
	if registered.StatusCode != 200 {
		t.Fatal(registered.StatusCode, registered.Body)
	}
	token := registered.JSON["token"].(string)
	bound := postJSON(t, ts.URL+"/api/accounts", token, map[string]any{
		"name": "弱电陈工", "app_id": "wx_mock_cols", "app_secret": "mock-secret",
		"subject_type": "personal", "service_type": "subscription", "verified": false, "consent": true,
	})
	if bound.StatusCode != 200 {
		t.Fatal(bound.StatusCode, bound.Body)
	}
	accountID := int64(bound.JSON["id"].(float64))
	listed := getJSON(t, ts.URL+"/api/accounts/"+strconv.FormatInt(accountID, 10)+"/columns", token)
	if listed.StatusCode != 200 || len(listed.JSONList) < 3 {
		t.Fatal(listed.StatusCode, listed.Body)
	}
	var newsID int64
	sawNews := false
	for _, row := range listed.JSONList {
		if row["name"] == "工程圈快报" && row["structure"] == "news" && row["status"] == "suggested" {
			sawNews = true
			newsID = int64(row["id"].(float64))
		}
	}
	if !sawNews {
		t.Fatal(listed.Body)
	}
	confirmed := postJSON(t, ts.URL+"/api/accounts/"+strconv.FormatInt(accountID, 10)+"/columns/confirm", token, map[string]any{"ids": []int64{newsID}})
	if confirmed.StatusCode != 200 {
		t.Fatal(confirmed.StatusCode, confirmed.Body)
	}
	active := 0
	for _, row := range confirmed.JSONList {
		if row["status"] == "active" && row["name"] == "工程圈快报" {
			active++
		}
	}
	if active != 1 {
		t.Fatal(confirmed.Body)
	}
	created := postJSON(t, ts.URL+"/api/plans", token, map[string]any{
		"wx_account_id": accountID, "direction": "弱电智能化", "style_key": "warm", "template_key": "simple",
		"word_target": 800, "schedule_type": "daily", "weekdays": []int{}, "time_window": "night",
	})
	if created.StatusCode != 200 {
		t.Fatal(created.StatusCode, created.Body)
	}
	planID := int64(created.JSON["id"].(float64))
	saved := putJSON(t, ts.URL+"/api/plans/"+strconv.FormatInt(planID, 10), token, map[string]any{
		"column_rotation": "weekday",
		"column_weekdays": map[string]int64{"1": newsID},
	})
	if saved.StatusCode != 200 || saved.JSON["column_rotation"] != "weekday" {
		t.Fatal(saved.StatusCode, saved.Body)
	}
	admin := postJSON(t, ts.URL+"/api/auth/login", "", map[string]any{"account": "admin@example.com", "password": "admin123456"})
	adminToken := admin.JSON["token"].(string)
	users := getJSON(t, ts.URL+"/api/admin/users", adminToken)
	var userID int64
	for _, row := range users.JSONList {
		if row["account"] == "cols@example.com" {
			userID = int64(row["id"].(float64))
		}
	}
	if userID == 0 {
		t.Fatal(users.Body)
	}
	exempt := putJSON(t, ts.URL+"/api/admin/users/"+strconv.FormatInt(userID, 10)+"/quota-exempt", adminToken, map[string]any{"quota_exempt": true})
	if exempt.StatusCode != 200 || exempt.JSON["quota_exempt"] != true {
		t.Fatal(exempt.StatusCode, exempt.Body)
	}
	logs, err := app.Store.ListAudit(t.Context(), 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range logs {
		if row.Action == "set_quota_exempt" && strings.Contains(row.Detail, "不限额") {
			found = true
		}
	}
	if !found {
		t.Fatal(logs)
	}
}
