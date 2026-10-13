package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// SearxGuard is the process-wide guard for the self-hosted SearXNG.
// One token bucket (1 request/s, burst 3), a 24h cache of identical queries,
// and a circuit that opens after repeated failures. Result pages are not fetched here.
// The instance itself exempts this backend from its limiter; calls use http://searxng:8080 on the Docker network.
type SearxGuard struct {
	mu        sync.Mutex
	tokens    float64
	updated   time.Time
	rate      float64
	burst     float64
	maxWait   time.Duration
	fails     int
	openUntil time.Time
	breaker   int
	cooldown  time.Duration
	cache     map[string]searxEntry
	ttl       time.Duration
	dailyCap  int
	daily     map[string]int
	// Quota, when set, is the durable per-user daily counter. A nil Quota uses the in-memory map.
	Quota func(ctx context.Context, userID int64, cap int) (bool, error)
	now   func() time.Time
}

type searxEntry struct {
	until time.Time
	rows  []map[string]any
}

// NewSearxGuard starts with a full burst and a five-failure circuit.
func NewSearxGuard() *SearxGuard {
	return &SearxGuard{
		tokens:   3,
		updated:  time.Now(),
		rate:     1,
		burst:    3,
		maxWait:  2 * time.Second,
		breaker:  5,
		cooldown: 5 * time.Minute,
		cache:    map[string]searxEntry{},
		ttl:      24 * time.Hour,
		daily:    map[string]int{},
		now:      time.Now,
	}
}

// SetLimits updates the daily cap and the failure count that opens the circuit.
// A daily cap of 0 means unlimited. A non-positive breaker leaves the current value.
func (g *SearxGuard) SetLimits(dailyCap, breaker int) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if dailyCap >= 0 {
		g.dailyCap = dailyCap
	}
	if breaker > 0 {
		g.breaker = breaker
	}
}

// Search calls the SearXNG JSON API. The client may reach 127.0.0.1 because the
// instance is local. Result URLs are returned as data and are not requested.
func (g *SearxGuard) Search(ctx context.Context, client *http.Client, endpoint, query string, userID int64) ([]map[string]any, map[string]any, error) {
	endpoint = strings.TrimSpace(endpoint)
	query = strings.Join(strings.Fields(query), " ")
	meta := map[string]any{"provider": "searxng"}
	if g == nil || endpoint == "" || query == "" {
		return nil, meta, nil
	}
	if rows, ok := g.cached(query); ok {
		meta["cached"] = true
		return cloneRows(rows), meta, nil
	}
	if err := g.before(ctx, userID); err != nil {
		return nil, meta, err
	}
	rows, err := g.fetch(ctx, client, endpoint, query)
	g.after(query, rows, err)
	return rows, meta, err
}

func (g *SearxGuard) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

func (g *SearxGuard) cached(query string) ([]map[string]any, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	hit, ok := g.cache[query]
	if !ok || !g.clock().Before(hit.until) {
		return nil, false
	}
	return hit.rows, true
}

func (g *SearxGuard) before(ctx context.Context, userID int64) error {
	now := g.clock()
	g.mu.Lock()
	if !g.openUntil.IsZero() && now.Before(g.openUntil) {
		g.mu.Unlock()
		return &Error{Message: "searxng 熔断中"}
	}
	wait, ok := g.takeLocked(now)
	g.mu.Unlock()
	if !ok {
		if wait > 0 && wait <= g.maxWait {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			g.mu.Lock()
			_, ok = g.takeLocked(g.clock())
			g.mu.Unlock()
		}
		if !ok {
			return &Error{Message: "searxng 请求过于频繁"}
		}
	}
	allowed, err := g.allowDaily(ctx, userID)
	if err != nil {
		return err
	}
	if !allowed {
		return &Error{Message: "searxng 今日次数已用完"}
	}
	return nil
}

func (g *SearxGuard) takeLocked(now time.Time) (time.Duration, bool) {
	if g.burst <= 0 {
		g.burst = 3
	}
	if g.rate <= 0 {
		g.rate = 1
	}
	if g.updated.IsZero() {
		g.updated = now
		g.tokens = g.burst
	}
	elapsed := now.Sub(g.updated).Seconds()
	if elapsed > 0 {
		g.tokens += elapsed * g.rate
		if g.tokens > g.burst {
			g.tokens = g.burst
		}
		g.updated = now
	}
	if g.tokens >= 1 {
		g.tokens--
		return 0, true
	}
	need := (1 - g.tokens) / g.rate
	return time.Duration(need * float64(time.Second)), false
}

func (g *SearxGuard) allowDaily(ctx context.Context, userID int64) (bool, error) {
	if userID <= 0 {
		return true, nil
	}
	g.mu.Lock()
	capN := g.dailyCap
	quota := g.Quota
	g.mu.Unlock()
	if capN <= 0 {
		return true, nil
	}
	if quota != nil {
		return quota(ctx, userID, capN)
	}
	day := g.clock().UTC().Format("2006-01-02")
	key := fmt.Sprintf("%d|%s", userID, day)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.daily[key] >= capN {
		return false, nil
	}
	g.daily[key]++
	return true, nil
}

func (g *SearxGuard) after(query string, rows []map[string]any, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		g.fails++
		n := g.breaker
		if n < 1 {
			n = 5
		}
		if g.fails >= n {
			cool := g.cooldown
			if cool <= 0 {
				cool = 5 * time.Minute
			}
			g.openUntil = g.clock().Add(cool)
			g.fails = 0
		}
		return
	}
	g.fails = 0
	g.openUntil = time.Time{}
	if len(rows) == 0 || g.ttl <= 0 {
		return
	}
	g.cache[query] = searxEntry{until: g.clock().Add(g.ttl), rows: cloneRows(rows)}
}

func (g *SearxGuard) fetch(ctx context.Context, client *http.Client, endpoint, query string) ([]map[string]any, error) {
	target, err := searxEndpoint(endpoint, query)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gonghao-research/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := searxHTTP(client, hostKey(target)).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("searxng 返回 %d", resp.StatusCode)
	}
	return dropPrivateResults(ParseSearchBody(body)), nil
}

// searxEngines are the engines this instance actually answers. Bing and Baidu do not.
// "sogou wechat" only runs when it is requested in the same engines list.
const searxEngines = "engines=360search,sogou,sogou%20wechat"

func searxEndpoint(endpoint, query string) (*url.URL, error) {
	raw := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if !strings.HasSuffix(strings.ToLower(raw), "/search") {
		raw += "/search"
	}
	target, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if target.Scheme != "http" && target.Scheme != "https" || target.Host == "" {
		return nil, &Error{Message: "searxng 地址不正确"}
	}
	values := target.Query()
	values.Set("q", query)
	values.Set("format", "json")
	values.Del("engines")
	encoded := strings.ReplaceAll(values.Encode(), "+", "%20")
	if encoded != "" {
		encoded += "&"
	}
	// The space in "sogou wechat" stays %20, and the commas stay literal.
	target.RawQuery = encoded + searxEngines
	return target, nil
}

// searxHTTP talks only to the configured SearXNG host and port. A redirect anywhere else is refused,
// so the local instance cannot be used as a proxy to a private URL.
func searxHTTP(base *http.Client, allowed string) *http.Client {
	var transport http.RoundTripper = http.DefaultTransport
	if base != nil && base.Transport != nil {
		transport = base.Transport
	}
	return &http.Client{
		Transport: transport,
		Timeout:   6 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 2 {
				return &Error{Message: "searxng 跳转过多"}
			}
			if req.URL == nil || hostKey(req.URL) != allowed {
				return &Error{Message: "searxng 跳转到了别的地址"}
			}
			return nil
		},
	}
}

func hostKey(raw *url.URL) string {
	if raw == nil {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(raw.Hostname()), "www.")
	port := raw.Port()
	if port == "" {
		if raw.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return host + ":" + port
}

func dropPrivateResults(rows []map[string]any) []map[string]any {
	var out []map[string]any
	for _, row := range rows {
		raw, _ := row["url"].(string)
		if privateResult(raw) {
			continue
		}
		out = append(out, row)
	}
	return out
}

func privateResult(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return true
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func cloneRows(rows []map[string]any) []map[string]any {
	out := make([]map[string]any, len(rows))
	for i, row := range rows {
		next := map[string]any{}
		for key, value := range row {
			next[key] = value
		}
		out[i] = next
	}
	return out
}
