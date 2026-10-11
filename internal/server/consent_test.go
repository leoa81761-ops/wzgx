package server

import (
	"net/http/httptest"
	"testing"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/config"
)

func TestLegalVersionRequiresReconsent(t *testing.T) {
	app := openApp(t, config.Settings{})
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	registered := postJSON(t, ts.URL+"/api/auth/register", "", map[string]any{
		"account": "reconsent@example.com", "password": "password12", "agree": true,
	})
	if registered.StatusCode != 200 {
		t.Fatal(registered.StatusCode, registered.Body)
	}
	token := registered.JSON["token"].(string)
	userID := int64(registered.JSON["user"].(map[string]any)["id"].(float64))
	fresh := getJSON(t, ts.URL+"/api/me/consent-status", token)
	if fresh.StatusCode != 200 || fresh.JSON["needs_reconsent"] != false {
		t.Fatal(fresh.StatusCode, fresh.JSON)
	}
	if _, err := app.DB.Exec(t.Context(), `UPDATE consent_log SET version = ? WHERE user_id = ?`, "0.0-old", userID); err != nil {
		t.Fatal(err)
	}
	stale := getJSON(t, ts.URL+"/api/me/consent-status", token)
	if stale.JSON["needs_reconsent"] != true || stale.JSON["legal_version"] != catalog.LegalVersion {
		t.Fatal(stale.JSON)
	}
	denied := postJSON(t, ts.URL+"/api/me/reconsent", token, map[string]any{"agree": false})
	if denied.StatusCode != 400 {
		t.Fatal(denied.StatusCode, denied.Body)
	}
	ok := postJSON(t, ts.URL+"/api/me/reconsent", token, map[string]any{"agree": true})
	if ok.StatusCode != 200 || ok.JSON["needs_reconsent"] != false {
		t.Fatal(ok.StatusCode, ok.JSON)
	}
	again := getJSON(t, ts.URL+"/api/me/consent-status", token)
	if again.JSON["needs_reconsent"] != false {
		t.Fatal(again.JSON)
	}
	consents := getJSON(t, ts.URL+"/api/me/consents", token)
	found := 0
	for _, item := range consents.JSONList {
		if item["version"] == catalog.LegalVersion && item["consent_item"] == "reconsent" {
			found++
		}
	}
	if found != 2 {
		t.Fatal(consents.JSONList)
	}
}
