package content

import (
	"net/url"
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
		if !p.AllowsURL(asText(source["url"])) {
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
	if len(p.Allow) > 0 && !domainMatch(host, p.Allow) {
		return "不在允许名单"
	}
	return ""
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
