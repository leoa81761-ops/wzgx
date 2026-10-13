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
	engines   string
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

// SetPace replaces the token bucket. Tests use it so a short run is not throttled.
func (g *SearxGuard) SetPace(rate, burst float64) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if rate > 0 {
		g.rate = rate
	}
	if burst > 0 {
		g.burst = burst
		g.tokens = burst
	}
	g.maxWait = time.Millisecond
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

// SetEngines stores the SearXNG engines list. A blank or invalid value uses DefaultSearxEngines.
func (g *SearxGuard) SetEngines(engines string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.engines = NormalizeSearxEngines(engines)
}

// Search calls the SearXNG JSON API. The client may reach 127.0.0.1 because the
// instance is local. Result URLs are returned as data and are not requested.
func (g *SearxGuard) Search(ctx context.Context, client *http.Client, endpoint, query string, userID int64) ([]map[string]any, map[string]any, error) {
	endpoint = strings.TrimSpace(endpoint)
	query = strings.Join(strings.Fields(query), " ")
	engines := DefaultSearxEngines
	if g != nil {
		engines = g.engineList()
	}
	meta := map[string]any{"provider": "searxng", "engines": engines}
	if g == nil || endpoint == "" || query == "" {
		return nil, meta, nil
	}
	if rows, ok := g.cached(engines, query); ok {
		meta["cached"] = true
		return cloneRows(rows), meta, nil
	}
	if err := g.before(ctx, userID); err != nil {
		return nil, meta, err
	}
	rows, err := g.fetch(ctx, client, endpoint, query, engines)
	g.after(engines, query, rows, err)
	return rows, meta, err
}

func (g *SearxGuard) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

func (g *SearxGuard) engineList() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if strings.TrimSpace(g.engines) == "" {
		return DefaultSearxEngines
	}
	return g.engines
}

func (g *SearxGuard) cached(engines, query string) ([]map[string]any, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	hit, ok := g.cache[engines+"\n"+query]
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

func (g *SearxGuard) after(engines, query string, rows []map[string]any, err error) {
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
	g.cache[engines+"\n"+query] = searxEntry{until: g.clock().Add(g.ttl), rows: cloneRows(rows)}
}

func (g *SearxGuard) fetch(ctx context.Context, client *http.Client, endpoint, query, engines string) ([]map[string]any, error) {
	target, err := searxEndpoint(endpoint, query, engines)
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

// DefaultSearxEngines is the engine list this SearXNG answers.
// sogou (patched) and bing (cn.bing.com) each return results. 360search is still
// requested. "sogou wechat" only runs when it is named in the same list.
const DefaultSearxEngines = "sogou,bing,360search,sogou wechat"

// NormalizeSearxEngines keeps a comma-separated engine list. A blank or unusable
// value falls back to DefaultSearxEngines. Spaces inside a name stay, so
// "sogou wechat" is one engine.
func NormalizeSearxEngines(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultSearxEngines
	}
	var parts []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.Join(strings.Fields(part), " ")
		if part == "" || seen[strings.ToLower(part)] || !validSearxEngine(part) {
			continue
		}
		seen[strings.ToLower(part)] = true
		parts = append(parts, part)
		if len(parts) == 8 {
			break
		}
	}
	if len(parts) == 0 {
		return DefaultSearxEngines
	}
	return strings.Join(parts, ",")
}

// ValidSearxEngines reports a list an operator can save. Each name is letters,
// digits, spaces, hyphens, or underscores, and there is at least one name.
func ValidSearxEngines(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 200 {
		return false
	}
	n := 0
	for _, part := range strings.Split(raw, ",") {
		part = strings.Join(strings.Fields(part), " ")
		if part == "" {
			continue
		}
		if !validSearxEngine(part) {
			return false
		}
		n++
	}
	return n > 0 && n <= 8
}

func validSearxEngine(name string) bool {
	if name == "" || len(name) > 40 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ' ', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

func searxEndpoint(endpoint, query, engines string) (*url.URL, error) {
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
	target.RawQuery = encoded + "engines=" + strings.ReplaceAll(NormalizeSearxEngines(engines), " ", "%20")
	return target, nil
}

// searxHTTP talks only to the configured SearXNG host and port.
// That host is an internal trusted URL, including a private Docker address on
// port 8080, so this client does not reuse a public-web transport that would
// refuse it. A redirect anywhere else is refused.
func searxHTTP(base *http.Client, allowed string) *http.Client {
	dialer := &net.Dialer{Timeout: 6 * time.Second}
	timeout := 6 * time.Second
	if base != nil && base.Timeout > 0 && base.Timeout < timeout {
		timeout = base.Timeout
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				if !dialAllowed(address, allowed) {
					return nil, &Error{Message: "searxng 地址不在允许名单"}
				}
				return dialer.DialContext(ctx, network, address)
			},
			TLSHandshakeTimeout:   4 * time.Second,
			ResponseHeaderTimeout: timeout,
		},
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

func dialAllowed(address, allowed string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	if port == "" {
		port = "80"
	}
	return host+":"+port == allowed
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
