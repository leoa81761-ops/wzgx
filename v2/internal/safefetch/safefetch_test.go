package safefetch

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestAllowed(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.20.0.5", "192.168.1.1", "169.254.169.254", "100.64.1.1", "0.0.0.0",
		"224.0.0.1", "::1", "fd00::1", "fe80::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "198.18.0.1"} {
		if Allowed(netip.MustParseAddr(s)) {
			t.Errorf("%s allowed", s)
		}
	}
	for _, s := range []string{"93.184.216.34", "1.1.1.1", "2606:4700::1111"} {
		if !Allowed(netip.MustParseAddr(s)) {
			t.Errorf("%s refused", s)
		}
	}
}

func TestCheckURL(t *testing.T) {
	for _, s := range []string{"file:///etc/passwd", "ftp://x.com/a.jpg", "gopher://x", "http://user:pw@x.com/a.jpg", "http://x.com:22/a.jpg", "//x.com/a.jpg", "http:///a.jpg"} {
		if _, err := CheckURL(s); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	if _, err := CheckURL("https://img.example.com:8443/a.jpg"); err != nil {
		t.Error(err)
	}
}

func TestGet(t *testing.T) {
	var pngB bytes.Buffer
	png.Encode(&pngB, image.NewRGBA(image.Rect(0, 0, 40, 30)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a.png":
			w.Write(pngB.Bytes())
		case "/page":
			w.Write([]byte("<html>not an image</html>"))
		case "/big":
			w.Write(bytes.Repeat([]byte{0xff}, 3<<20))
		case "/to-private":
			http.Redirect(w, r, "http://10.0.0.1/a.png", 302)
		case "/to-meta":
			http.Redirect(w, r, "http://metadata.internal/a.png", 302)
		}
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	ctx := context.Background()

	// the real resolver: the local test server itself is refused
	if _, _, err := (&Fetcher{}).Get(ctx, srv.URL+"/a.png"); err == nil {
		t.Fatal("loopback fetched")
	}
	// a "public" test host mapped onto the local server; other names resolve to private addresses
	f := &Fetcher{MaxBytes: 2 << 20,
		LookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
			switch host {
			case "img.example.com":
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			case "mixed.example.com":
				return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("192.168.0.9")}, nil
			case "metadata.internal":
				return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
			}
			if ip, err := netip.ParseAddr(host); err == nil {
				return []netip.Addr{ip}, nil
			}
			return nil, errors.New("no such host")
		},
		DialAddr: func(netip.Addr, string) string { return "127.0.0.1:" + port },
	}
	base := "http://img.example.com:8080"
	if port != "8080" {
		// the test server port is random; CheckURL only allows a few ports, so the mapping ignores it
		base = "http://img.example.com"
	}
	b, ct, err := f.Get(ctx, base+"/a.png")
	if err != nil || ct != "image/png" || len(b) != pngB.Len() {
		t.Fatalf("public image: %v %s", err, ct)
	}
	for path, want := range map[string]error{"/page": ErrType, "/big": ErrTooBig, "/to-private": ErrBlocked, "/to-meta": ErrBlocked} {
		if _, _, err := f.Get(ctx, base+path); !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", path, err, want)
		}
	}
	if _, _, err := f.Get(ctx, "http://mixed.example.com/a.png"); !errors.Is(err, ErrBlocked) {
		t.Errorf("mixed public/private answer: %v", err)
	}
	if _, _, err := f.Get(ctx, "http://169.254.169.254/latest/meta-data"); !errors.Is(err, ErrBlocked) {
		t.Errorf("metadata ip: %v", err)
	}
}
