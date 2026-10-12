package content

import (
	"net/url"
	"sort"
	"strings"
)

// DefaultBlockedDomains 是没单独配置时屏蔽的聚合站和转载站。
var DefaultBlockedDomains = []string{"xilu.com", "fyun.com", "huanancj.com"}

type SourcePolicy struct {
	Block []string
	Allow []string
}

func DefaultSourcePolicy() SourcePolicy {
	return SourcePolicy{Block: append([]string{}, DefaultBlockedDomains...)}
}

// PolicyFromParams 在 configured 为 false 时用默认屏蔽名单。已经保存过配置时，空名单表示不屏蔽、不限制。
func PolicyFromParams(params map[string]any, configured bool) SourcePolicy {
	if !configured {
		return DefaultSourcePolicy()
	}
	if params == nil {
		return SourcePolicy{}
	}
	return SourcePolicy{
		Block: NormalizeDomains(asStrings(params["block"])),
		Allow: NormalizeDomains(asStrings(params["allow"])),
	}
}

func NormalizeDomains(items []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, item := range items {
		domain := normalizeDomain(item)
		if domain == "" || seen[domain] {
			continue
		}
		seen[domain] = true
		out = append(out, domain)
	}
	return out
}

func asStrings(v any) []string {
	switch items := v.(type) {
	case []string:
		return append([]string{}, items...)
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func (p SourcePolicy) AllowsURL(raw string) bool {
	host := sourceHost(raw)
	if host == "" {
		return true
	}
	if len(p.Allow) > 0 && !domainMatch(host, p.Allow) {
		return false
	}
	return !domainMatch(host, p.Block)
}

func (p SourcePolicy) Filter(sources []map[string]any) []map[string]any {
	if len(sources) == 0 {
		return sources
	}
	out := make([]map[string]any, 0, len(sources))
	for _, source := range sources {
		if source == nil {
			continue
		}
		raw := asText(source["url"])
		if !p.AllowsURL(raw) || junkPage(raw) {
			continue
		}
		out = append(out, source)
	}
	return out
}

// Relaxed 去掉允许名单，屏蔽名单仍然生效。允许名单把可用网址全部丢掉时用这一档再滤一次。
func (p SourcePolicy) Relaxed() SourcePolicy {
	return SourcePolicy{Block: append([]string{}, p.Block...)}
}

// Split 分开保留和丢弃的来源。丢弃项带 url 和 reason，供 model_trace 记录。
func (p SourcePolicy) Split(sources []map[string]any) (kept []map[string]any, dropped []map[string]any) {
	for _, source := range sources {
		if source == nil {
			continue
		}
		raw := asText(source["url"])
		reason := p.dropReason(raw)
		if reason == "" {
			kept = append(kept, source)
			continue
		}
		row := map[string]any{"url": raw, "reason": reason}
		if title := asText(source["title"]); title != "" {
			row["title"] = title
		}
		dropped = append(dropped, row)
	}
	return kept, dropped
}

func (p SourcePolicy) dropReason(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "没有网址"
	}
	host := sourceHost(raw)
	if host == "" {
		return "无法解析网址"
	}
	if domainMatch(host, p.Block) {
		return "屏蔽域名"
	}
	if junkPage(raw) {
		return "网店或登录页"
	}
	if len(p.Allow) > 0 && !domainMatch(host, p.Allow) {
		return "不在允许名单"
	}
	return ""
}

// PreferSources 把中文和权威来源排到前面，同一主机最多保留两条。mode 为 elv 时再提高住建部、市监局和国家标准的优先级。
func PreferSources(sources []map[string]any, mode string) []map[string]any {
	if len(sources) == 0 {
		return sources
	}
	type scored struct {
		source map[string]any
		score  int
		index  int
	}
	rows := make([]scored, 0, len(sources))
	for i, source := range sources {
		if source == nil {
			continue
		}
		rows = append(rows, scored{source: source, score: sourceScore(source, mode), index: i})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].score == rows[j].score {
			return rows[i].index < rows[j].index
		}
		return rows[i].score > rows[j].score
	})
	out := make([]map[string]any, 0, len(rows))
	perHost := map[string]int{}
	for _, row := range rows {
		host := sourceHost(asText(row.source["url"]))
		if host != "" {
			if perHost[host] >= 2 {
				continue
			}
			perHost[host]++
		}
		out = append(out, row.source)
	}
	return out
}

func sourceScore(source map[string]any, mode string) int {
	raw := asText(source["url"])
	host := sourceHost(raw)
	text := strings.ToUpper(asText(source["title"]) + " " + asText(source["name"]) + " " + raw)
	score := 0
	if host == "gov.cn" || strings.HasSuffix(host, ".gov.cn") {
		score += 100
	}
	if host == "cn" || strings.HasSuffix(host, ".cn") {
		score += 20
	}
	if mode == "elv" {
		if host == "mohurd.gov.cn" || strings.HasSuffix(host, ".mohurd.gov.cn") {
			score += 40
		}
		if host == "samr.gov.cn" || strings.HasSuffix(host, ".samr.gov.cn") {
			score += 40
		}
		if strings.Contains(text, "GB/T") || strings.Contains(text, "GB 5") || strings.Contains(text, "GB5") || strings.Contains(text, "国家标准") {
			score += 30
		}
	}
	return score
}

func junkPage(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	host = strings.TrimPrefix(host, "www.")
	if strings.Contains(host, "webstore") || strings.Contains(host, "helpdesk") || strings.HasPrefix(host, "shop.") {
		return true
	}
	for _, seg := range strings.Split(strings.ToLower(parsed.Path), "/") {
		switch seg {
		case "store", "shop", "cart", "login", "signin", "checkout":
			return true
		}
	}
	return false
}

func sourceHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	return strings.TrimPrefix(host, "www.")
}

func domainMatch(host string, domains []string) bool {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	for _, domain := range domains {
		domain = normalizeDomain(domain)
		if domain == "" {
			continue
		}
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func normalizeDomain(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	if i := strings.IndexAny(domain, "/?#"); i >= 0 {
		domain = domain[:i]
	}
	domain = strings.TrimPrefix(domain, "www.")
	return strings.Trim(domain, ".")
}
