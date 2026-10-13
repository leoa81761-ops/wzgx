package llm

import (
	"net/url"
	"strings"
)

// ChainOptions is everything that can appear in one search, for one user.
// A user Tavily key is never placed on a target that another user will call.
type ChainOptions struct {
	UserTavilyKey     string
	UserTavilyURL     string
	SearxngURL        string
	PlatformTavilyKey string
	PlatformTavilyURL string
	Provider          string
	APIURL            string
	APIKey            string
	Bing              string
	Sogou             string
	Baidu             string
}

// PlanTargets orders the engines for one generation.
// A user key goes first, then SearXNG, then the free HTML engines.
// Platform keys are used only when that user did not bring a key.
func PlanTargets(opts ChainOptions) []SearchTarget {
	var out []SearchTarget
	userKey := strings.TrimSpace(opts.UserTavilyKey)
	if userKey != "" {
		endpoint := strings.TrimSpace(opts.UserTavilyURL)
		if endpoint == "" {
			endpoint = DefaultSearchEndpoint("tavily")
		}
		out = append(out, SearchTarget{URL: endpoint, APIKey: userKey, Provider: "tavily", Owner: "user"})
	}
	if raw := strings.TrimSpace(opts.SearxngURL); raw != "" {
		out = append(out, SearchTarget{URL: raw, Provider: "searxng"})
	}
	if userKey == "" {
		if key := strings.TrimSpace(opts.PlatformTavilyKey); key != "" {
			endpoint := strings.TrimSpace(opts.PlatformTavilyURL)
			if endpoint == "" {
				endpoint = DefaultSearchEndpoint("tavily")
			}
			out = append(out, SearchTarget{URL: endpoint, APIKey: key, Provider: "tavily", Owner: "platform"})
		}
		if target, ok := ConfiguredSearch(opts.Provider, opts.APIURL, opts.APIKey); ok && !duplicateTarget(out, target) {
			target.Owner = "platform"
			out = append(out, target)
		}
	}
	for _, raw := range []string{opts.Bing, opts.Sogou, opts.Baidu} {
		if target, ok := htmlTarget(raw); ok {
			out = append(out, target)
		}
	}
	return out
}

func duplicateTarget(have []SearchTarget, next SearchTarget) bool {
	for _, item := range have {
		if strings.EqualFold(item.Provider, next.Provider) && item.APIKey == next.APIKey && strings.TrimRight(item.URL, "/") == strings.TrimRight(next.URL, "/") {
			return true
		}
	}
	return false
}

func htmlTarget(raw string) (SearchTarget, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return SearchTarget{}, false
	}
	parsed, err := url.Parse(raw)
	host := ""
	if err == nil {
		host = strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	}
	provider := "web"
	switch {
	case strings.Contains(host, "bing"):
		provider = "bing"
	case strings.Contains(host, "sogou"):
		provider = "sogou"
	case strings.Contains(host, "baidu"):
		provider = "baidu"
	}
	return SearchTarget{URL: raw, Provider: provider}, true
}
