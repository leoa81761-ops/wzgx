package content

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestDialControlRejectsPrivateAndOddPorts(t *testing.T) {
	blocked := []string{
		"127.0.0.1:80",
		"127.0.0.1:443",
		"[::1]:80",
		"[::ffff:127.0.0.1]:443",
		"10.1.2.3:80",
		"172.16.0.1:443",
		"172.31.255.1:80",
		"192.168.0.1:80",
		"169.254.1.1:80",
		"[fe80::1]:443",
		"[fc00::1]:80",
		"[fd12::1]:80",
		"100.64.0.1:80",
		"100.127.255.255:443",
		"[::ffff:100.64.0.1]:80",
		"0.0.0.0:80",
		"[::]:443",
		"224.0.0.1:80",
		"[ff02::1]:80",
		"8.8.8.8:8080",
		"1.1.1.1:8443",
	}
	for _, address := range blocked {
		if err := dialControl("tcp", address, nil); err == nil {
			t.Fatalf("allowed %s", address)
		}
	}
	if err := dialControl("udp", "8.8.8.8:80", nil); err == nil {
		t.Fatal("udp")
	}
	for _, address := range []string{"8.8.8.8:80", "1.1.1.1:443", "100.128.0.1:80", "100.63.0.1:443"} {
		if err := dialControl("tcp", address, nil); err != nil {
			t.Fatalf("%s: %v", address, err)
		}
	}
}

func TestPublicClientDialRefusesLoopback(t *testing.T) {
	transport, ok := PublicClient().Transport.(*http.Transport)
	if !ok || transport.DialContext == nil || transport.Proxy != nil {
		t.Fatal("transport")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	conn, err := transport.DialContext(ctx, "tcp", "127.0.0.1:80")
	if conn != nil {
		conn.Close()
	}
	if err == nil || !errors.Is(err, errBlockedAddress) {
		t.Fatal(err)
	}
	conn, err = transport.DialContext(ctx, "tcp", "[::1]:443")
	if conn != nil {
		conn.Close()
	}
	if err == nil || !errors.Is(err, errBlockedAddress) {
		t.Fatal(err)
	}
}

func TestPublicClientRejectsLoopbackRedirect(t *testing.T) {
	client := PublicClient()
	start, err := http.NewRequest(http.MethodGet, "https://1.1.1.1/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	next, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(next, []*http.Request{start}); !errors.Is(err, errBlockedAddress) {
		t.Fatal(err)
	}
	viaLoop, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/already", nil)
	if err != nil {
		t.Fatal(err)
	}
	publicNext, err := http.NewRequest(http.MethodGet, "https://1.1.1.1/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(publicNext, []*http.Request{viaLoop}); !errors.Is(err, errBlockedAddress) {
		t.Fatal(err)
	}
	ftp, err := http.NewRequest(http.MethodGet, "ftp://1.1.1.1/file", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(ftp, []*http.Request{start}); !errors.Is(err, errBlockedScheme) {
		t.Fatal(err)
	}
	oddPort, err := http.NewRequest(http.MethodGet, "https://1.1.1.1:8443/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(oddPort, []*http.Request{start}); !errors.Is(err, errBlockedPort) {
		t.Fatal(err)
	}
}

func TestPublicClientAllowsThreeRedirects(t *testing.T) {
	client := PublicClient()
	next, err := http.NewRequest(http.MethodGet, "https://1.1.1.1/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	via := make([]*http.Request, 3)
	for i := range via {
		hop, err := http.NewRequest(http.MethodGet, "https://1.1.1.1/hop", nil)
		if err != nil {
			t.Fatal(err)
		}
		via[i] = hop
	}
	if err := client.CheckRedirect(next, via); err != nil {
		t.Fatal(err)
	}
	fourth, err := http.NewRequest(http.MethodGet, "https://1.1.1.1/fourth", nil)
	if err != nil {
		t.Fatal(err)
	}
	via = append(via, fourth)
	if err := client.CheckRedirect(next, via); !errors.Is(err, errTooManyHops) {
		t.Fatal(err)
	}
}

func TestGuardURLRejectsLocalNames(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1/secret", "http://localhost/secret", "https://[::1]/secret"} {
		req, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := guardURL(req.URL); !errors.Is(err, errBlockedAddress) {
			t.Fatalf("%s: %v", raw, err)
		}
	}
}
