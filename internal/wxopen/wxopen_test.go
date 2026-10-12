package wxopen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMessageRoundTripAndScan(t *testing.T) {
	key := strings.TrimRight(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), "=")
	encrypted, signature, err := EncryptMessage(key, "token", "wxapp", "1", "2", `<xml><InfoType>component_verify_ticket</InfoType><ComponentVerifyTicket>ticket-1</ComponentVerifyTicket></xml>`)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := DecryptMessage(key, "token", "1", "2", signature, encrypted)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := ParseTicket(plain)
	if err != nil || ticket.Value != "ticket-1" {
		t.Fatal(ticket, err, string(plain))
	}
	if _, err := DecryptMessage(key, "token", "1", "2", "bad", encrypted); err == nil {
		t.Fatal("signature")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"openid": "openid-1", "unionid": "union-1"})
	}))
	defer upstream.Close()
	profile, err := ExchangeScan(context.Background(), upstream.Client(), upstream.URL, "app", "secret", "code")
	if err != nil || profile.OpenID != "openid-1" || profile.UnionID != "union-1" {
		t.Fatal(profile, err)
	}
	link := ScanURL("app", "https://example.com/cb", "state1")
	if !strings.Contains(link, "appid=app") || !strings.Contains(link, "snsapi_login") {
		t.Fatal(link)
	}
}
