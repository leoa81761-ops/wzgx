// Package safefetch downloads a user-supplied image URL without letting it reach the server's own
// network (SSRF): http/https only, no credentials in the URL, every resolved address checked
// (loopback, private, link-local, CGNAT, multicast, metadata and other special ranges refused),
// the checked address is the one dialled (no DNS rebinding), no proxy, at most 3 redirects (each
// checked again), a size limit, a time limit, and only real JPEG/PNG/GIF images.
package safefetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var (
	ErrURL     = errors.New("图片地址不正确：只支持 http:// 或 https:// 开头的网址")
	ErrBlocked = errors.New("这个地址指向内网或保留地址，不能下载")
	ErrTooBig  = errors.New("图片太大")
	ErrType    = errors.New("不是 JPG、PNG 或 GIF 图片")
)

type Fetcher struct {
	MaxBytes int64         // default 5 MB
	Timeout  time.Duration // default 15 s
	// LookupIP and DialAddr are for tests (map a public test hostname onto a local test server);
	// the address checks still run on what LookupIP returns.
	LookupIP func(ctx context.Context, host string) ([]netip.Addr, error)
	DialAddr func(ip netip.Addr, port string) string
}

var blocked = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32",
		"::/128", "::1/128", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16",
		"fc00::/7", "fe80::/10", "ff00::/8",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// Allowed reports whether an address is a normal public unicast address.
func Allowed(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

func (f *Fetcher) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	if f.LookupIP != nil {
		return f.LookupIP(ctx, host)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip}, nil
	}
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// CheckURL validates the URL shape (scheme, no credentials, allowed port).
func CheckURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || len(raw) > 2048 {
		return nil, ErrURL
	}
	if u.User != nil {
		return nil, errors.New("图片地址里不能带用户名或密码")
	}
	switch u.Port() {
	case "", "80", "443", "8080", "8443":
	default:
		return nil, errors.New("图片地址只能用 80、443、8080、8443 端口")
	}
	return u, nil
}

// Get downloads an image. It returns the bytes and the sniffed content type.
func (f *Fetcher) Get(ctx context.Context, raw string) ([]byte, string, error) {
	u, err := CheckURL(raw)
	if err != nil {
		return nil, "", err
	}
	max, to := f.MaxBytes, f.Timeout
	if max <= 0 {
		max = 5 << 20
	}
	if to <= 0 {
		to = 15 * time.Second
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := f.lookup(ctx, host)
			if err != nil || len(ips) == 0 {
				return nil, fmt.Errorf("找不到这个网站：%s", host)
			}
			for _, ip := range ips {
				if !Allowed(ip) {
					return nil, ErrBlocked
				}
			}
			target := net.JoinHostPort(ips[0].Unmap().String(), port)
			if f.DialAddr != nil {
				target = f.DialAddr(ips[0], port)
			}
			return dialer.DialContext(ctx, network, target)
		},
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		DisableKeepAlives:      true,
	}
	client := &http.Client{Transport: tr, Timeout: to, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("跳转太多次")
		}
		if _, err := CheckURL(req.URL.String()); err != nil {
			return err
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, "", ErrURL
	}
	req.Header.Set("User-Agent", "gonghao-image-fetch/1")
	req.Header.Set("Accept", "image/jpeg,image/png,image/gif")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, ErrBlocked) {
			return nil, "", ErrBlocked
		}
		var ue *url.Error
		if errors.As(err, &ue) && ue.Timeout() {
			return nil, "", errors.New("下载图片超时")
		}
		return nil, "", fmt.Errorf("下载图片失败：%v", unwrapMsg(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("下载图片失败：网站返回 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > max {
		return nil, "", fmt.Errorf("%w（超过 %d MB）", ErrTooBig, max>>20)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, "", fmt.Errorf("下载图片失败：%v", err)
	}
	if int64(len(b)) > max {
		return nil, "", fmt.Errorf("%w（超过 %d MB）", ErrTooBig, max>>20)
	}
	ct := http.DetectContentType(b)
	switch ct {
	case "image/jpeg", "image/png", "image/gif":
		return b, ct, nil
	}
	return nil, "", ErrType
}

func unwrapMsg(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}
