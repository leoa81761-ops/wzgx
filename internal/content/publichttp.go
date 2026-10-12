package content

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"
	"syscall"
	"time"
)

const maxPublicRedirects = 3

var (
	errBlockedAddress = errors.New("blocked address")
	errBlockedPort    = errors.New("blocked port")
	errBlockedScheme  = errors.New("blocked scheme")
	errTooManyHops    = errors.New("stopped after 3 redirects")
)

var (
	publicOnce sync.Once
	publicHTTP *http.Client
)

// PublicClient fetches public web pages. It refuses loopback, private, link-local,
// CGNAT, unspecified, and multicast addresses at connect time, so a DNS rebinding
// response cannot reach them. Redirects are limited to three http(s) hops on ports 80 and 443.
func PublicClient() *http.Client {
	publicOnce.Do(func() {
		dialer := &net.Dialer{Timeout: 4 * time.Second, Control: dialControl}
		publicHTTP = &http.Client{
			Timeout:       4 * time.Second,
			CheckRedirect: guardRedirect,
			Transport: &http.Transport{
				Proxy:                 nil,
				DialContext:           dialer.DialContext,
				TLSHandshakeTimeout:   4 * time.Second,
				ResponseHeaderTimeout: 4 * time.Second,
				IdleConnTimeout:       30 * time.Second,
				MaxIdleConns:          8,
			},
		}
	})
	return publicHTTP
}

func dialControl(network, address string, _ syscall.RawConn) error {
	switch network {
	case "tcp", "tcp4", "tcp6":
	default:
		return errBlockedAddress
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errBlockedAddress
	}
	if port != "80" && port != "443" {
		return errBlockedPort
	}
	if blockedIP(net.ParseIP(host)) {
		return errBlockedAddress
	}
	return nil
}

func guardRedirect(req *http.Request, via []*http.Request) error {
	// via holds requests already sent, so length 3 is the third redirect.
	if len(via) > maxPublicRedirects {
		return errTooManyHops
	}
	for _, hop := range via {
		if hop == nil || hop.URL == nil {
			return errBlockedAddress
		}
		if err := guardURL(hop.URL); err != nil {
			return err
		}
	}
	if req == nil || req.URL == nil {
		return errBlockedAddress
	}
	return guardURL(req.URL)
}

func guardURL(raw *url.URL) error {
	if raw == nil {
		return errBlockedAddress
	}
	if raw.Scheme != "http" && raw.Scheme != "https" {
		return errBlockedScheme
	}
	port := raw.Port()
	if port == "" {
		if raw.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if port != "80" && port != "443" {
		return errBlockedPort
	}
	host := raw.Hostname()
	if host == "" {
		return errBlockedAddress
	}
	if ip := net.ParseIP(host); ip != nil {
		if blockedIP(ip) {
			return errBlockedAddress
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		return errBlockedAddress
	}
	for _, addr := range addrs {
		if blockedIP(addr.IP) {
			return errBlockedAddress
		}
	}
	return nil
}

func blockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	// CGNAT 100.64.0.0/10 is not covered by IsPrivate.
	return v4[0] == 100 && v4[1]&0xc0 == 0x40
}
