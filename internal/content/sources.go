package content

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var standardNumber = regexp.MustCompile(`(?i)GB\s*/?\s*T?\s*\d{2,}`)

// DefaultBlockedDomains 是没单独配置时屏蔽的聚合站、转载站和低质量镜像。管理员保存过名单后以保存的为准。
var DefaultBlockedDomains = []string{
	"xilu.com", "fyun.com", "huanancj.com",
	"bzko.com", "yuanlin8.com", "bbs.co188.com", "ireader.com",
	"abook.qq.com", "gcs66.com", "bzfyw.com", "baijiantest.com",
	"netkao.com", "netkao.cn", "biaozhun.org", "51cps.net",
}

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
	if DictionaryHost(host) {
		return false
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

// ForTopic 在选题里出现标准号时，放开标准正文站。其他选题仍按屏蔽名单处理。
func (p SourcePolicy) ForTopic(topic string) SourcePolicy {
	if !StandardNumberTopic(topic) {
		return p
	}
	block := make([]string, 0, len(p.Block))
	for _, domain := range p.Block {
		if StandardTextHost(domain) {
			continue
		}
		block = append(block, domain)
	}
	return SourcePolicy{Block: block, Allow: append([]string{}, p.Allow...)}
}

// StandardNumberTopic reports a topic that names a standard, such as GB 50311 or GB/T 50311.
func StandardNumberTopic(topic string) bool {
	return standardNumber.MatchString(topic)
}

// SharesStandardNumber reports that text cites the same GB number named in the topic.
func SharesStandardNumber(topic, text string) bool {
	for _, num := range standardNumber.FindAllString(topic, -1) {
		digits := make([]rune, 0, 6)
		for _, r := range num {
			if r >= '0' && r <= '9' {
				digits = append(digits, r)
			}
		}
		if len(digits) >= 4 && strings.Contains(text, string(digits)) {
			return true
		}
	}
	return false
}

// StandardTextHost is a standards-text site that stays blocked until the topic names a standard number.
func StandardTextHost(domain string) bool {
	domain = normalizeDomain(domain)
	if domain == "" {
		return false
	}
	for _, root := range []string{"bzfyw.com", "biaozhun.org", "co188.com"} {
		if domain == root || strings.HasSuffix(domain, "."+root) || strings.Contains(domain, root) {
			return true
		}
	}
	return false
}

// TechnicalTopic is an engineering or standards subject, not a household one.
func TechnicalTopic(topic string) bool {
	for _, word := range []string{"弱电", "智能化", "工程", "布线", "机房", "监控", "门禁", "标准", "GB", "验收"} {
		if strings.Contains(topic, word) {
			return true
		}
	}
	return false
}

// OfficialHost is gov.cn or a standards site. A technical topic can keep these even when the title is thin.
func OfficialHost(raw string) bool {
	host := sourceHost(raw)
	if host == "" {
		return false
	}
	if host == "gov.cn" || strings.HasSuffix(host, ".gov.cn") {
		return true
	}
	if standardsHost(host) || StandardTextHost(host) {
		return true
	}
	return false
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
	if DictionaryHost(host) {
		return "词典站点"
	}
	if junkPage(raw) {
		return "网店或登录页"
	}
	if len(p.Allow) > 0 && !domainMatch(host, p.Allow) {
		return "不在允许名单"
	}
	return ""
}

// PreferSources 先给全部来源打分，再按同一主机最多两条截到前 8 条。
// gov.cn、标准站和主流媒体靠前；中文选题再优先 .cn。弱电选题里含国家标准的条目再加分。
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
		if len(out) >= 8 {
			break
		}
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
	if standardsHost(host) {
		score += 40
	}
	if majorMedia(host) {
		score += 30
	}
	if mode == "zh" || mode == "elv" {
		if host == "cn" || strings.HasSuffix(host, ".cn") {
			score += 20
		}
	}
	if mode == "elv" && (strings.Contains(text, "GB/T") || strings.Contains(text, "GB 5") || strings.Contains(text, "GB5") || strings.Contains(text, "国家标准")) {
		score += 30
	}
	return score
}

func standardsHost(host string) bool {
	for _, domain := range []string{"mohurd.gov.cn", "samr.gov.cn", "std.samr.gov.cn", "openstd.samr.gov.cn"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func majorMedia(host string) bool {
	for _, domain := range []string{"people.com.cn", "xinhuanet.com", "news.cn", "thepaper.cn", "caixin.com", "gmw.cn", "ce.cn", "stdaily.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

// DictionaryHost is a lexicon or dictionary site, such as json.cn. raw may be a host or a URL.
func DictionaryHost(raw string) bool {
	raw = strings.TrimSpace(raw)
	host := raw
	if strings.Contains(raw, "://") {
		if parsed, err := url.Parse(raw); err == nil {
			host = parsed.Hostname()
		}
	}
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	if host == "" {
		return false
	}
	for _, domain := range []string{
		"json.cn", "dict.cn", "zdic.net", "zdic.com", "iciba.com", "youdao.com",
		"hanyu.baidu.com", "cidianwang.com", "911cha.com", "juzimi.com", "jukuu.com",
		"baike.baidu.com", "baike.sogou.com", "baike.so.com", "baike.com",
		"wikipedia.org", "zh.wikipedia.org",
	} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

// SearchResultURL is a search page, a results list, or a standards download tool. It is not a source.
func SearchResultURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	path := strings.ToLower(parsed.Path)
	query := parsed.Query()
	if strings.Contains(path, "dcpsptools") || strings.Contains(path, "/download") && query.Get("path") != "" {
		return true
	}
	if strings.Contains(path, "/search") || strings.Contains(path, "search/org") {
		return true
	}
	switch host {
	case "baidu.com", "sogou.com", "so.com", "bing.com", "google.com", "duckduckgo.com":
		if path == "/s" || strings.HasPrefix(path, "/s") || strings.Contains(path, "search") {
			return true
		}
	}
	if query.Get("q") != "" || query.Get("wd") != "" || query.Get("query") != "" {
		if strings.Contains(path, "search") || strings.Contains(host, "baidu") || strings.Contains(host, "sogou") || host == "so.com" || strings.Contains(host, "bing.com") {
			return true
		}
	}
	return false
}

// ReferenceEntry is a dictionary or encyclopedia headword, not an article about the topic.
func ReferenceEntry(title string) bool {
	for _, word := range []string{"汉语词语", "百度百科", "维基百科", "百科词条"} {
		if strings.Contains(title, word) {
			return true
		}
	}
	return false
}

// LowValuePage is a hard drop for novels, job boards, book pages, loans, sentence dictionaries, and device forums.
// A gov.cn host is left for the judge. title may be empty when only the URL is known.
func LowValuePage(raw, title string) string {
	host := sourceHost(raw)
	if host == "gov.cn" || strings.HasSuffix(host, ".gov.cn") {
		return ""
	}
	blob := strings.ToLower(raw + " " + title)
	path := ""
	if parsed, err := url.Parse(strings.TrimSpace(raw)); err == nil {
		path = strings.ToLower(parsed.Path)
	}
	if novelPage(host, path, blob) {
		return "小说或TXT"
	}
	if jobPage(host, path, blob) {
		return "招聘页"
	}
	if doubanBook(host, path) {
		return "豆瓣图书"
	}
	if loanPage(host, blob) {
		return "贷款页"
	}
	if sentenceDict(host, blob) {
		return "例句或词典"
	}
	if deviceForum(host, path, blob) {
		return "数码论坛"
	}
	return ""
}

func novelPage(host, path, blob string) bool {
	if strings.Contains(host, "biquge") || strings.Contains(host, "69shu") || strings.Contains(host, "shuquge") {
		return true
	}
	for _, domain := range []string{"qidian.com", "zongheng.com", "17k.com", "jjwxc.net"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	if strings.HasSuffix(path, ".txt") || strings.Contains(path, "/txt/") || strings.Contains(path, "/novel/") {
		return true
	}
	for _, word := range []string{"txt小说", "小说下载", "txt下载", "全文阅读", "章节目录", "章节全文"} {
		if strings.Contains(blob, word) {
			return true
		}
	}
	return false
}

func jobPage(host, path, blob string) bool {
	for _, domain := range []string{"zhaopin.com", "51job.com", "liepin.com", "zhipin.com", "lagou.com", "jobui.com", "yingjiesheng.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	if strings.Contains(path, "/jobs") || strings.Contains(path, "/job/") || strings.HasSuffix(path, "/job") || strings.Contains(path, "/zhaopin") {
		return true
	}
	for _, word := range []string{"招聘", "职位", "月薪", "简历"} {
		if strings.Contains(blob, word) {
			return true
		}
	}
	return false
}

func doubanBook(host, path string) bool {
	if host == "book.douban.com" || strings.HasSuffix(host, ".book.douban.com") {
		return true
	}
	if host == "douban.com" || strings.HasSuffix(host, ".douban.com") {
		return strings.Contains(path, "/subject/")
	}
	return false
}

func loanPage(host, blob string) bool {
	for _, domain := range []string{"rong360.com", "51credit.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	for _, word := range []string{"贷款", "借钱", "网贷", "信用卡申请", "借款"} {
		if strings.Contains(blob, word) {
			return true
		}
	}
	return false
}

func sentenceDict(host, blob string) bool {
	if DictionaryHost(host) {
		return false
	}
	for _, word := range []string{"例句", "造句"} {
		if strings.Contains(blob, word) {
			return true
		}
	}
	return false
}

func deviceForum(host, path, blob string) bool {
	if strings.Contains(blob, "手机论坛") {
		return true
	}
	forum := false
	for _, domain := range []string{"zol.com.cn", "pconline.com.cn", "chiphell.com", "bbs.xiaomi.com", "club.huawei.com", "v2ex.com", "tieba.baidu.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			forum = true
			break
		}
	}
	if !forum {
		return false
	}
	return strings.Contains(path, "/thread") || strings.Contains(path, "/forum")
}

// NormalizeSourceURL folds a URL so tracking parameters and a www prefix do not keep the same page twice.
func NormalizeSourceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return strings.ToLower(raw)
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	if port := parsed.Port(); port != "" && port != "80" && port != "443" {
		host += ":" + port
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	query := parsed.Query()
	keys := make([]string, 0, len(query))
	for key := range query {
		low := strings.ToLower(key)
		if strings.HasPrefix(low, "utm_") || low == "spm" || low == "from" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var parts []string
	for _, key := range keys {
		values := append([]string{}, query[key]...)
		sort.Strings(values)
		for _, value := range values {
			parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(value))
		}
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "" {
		scheme = "https"
	}
	out := scheme + "://" + host + path
	if len(parts) > 0 {
		out += "?" + strings.Join(parts, "&")
	}
	return out
}

// NormalizeSourceTitle folds a title so spacing does not keep the same page twice.
func NormalizeSourceTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	return strings.Join(strings.Fields(title), " ")
}

// OffTopicPage is a hard drop for software-download, video or drama, and app-store pages.
// Term overlap does not keep these. title may be empty when only the URL is known.
func OffTopicPage(raw, title string) string {
	blob := strings.ToLower(raw + " " + title)
	host := sourceHost(raw)
	if softwareDownload(host, blob) {
		return "软件下载"
	}
	if videoOrDrama(host, blob) {
		return "视频或剧集"
	}
	if appStore(host, blob) {
		return "应用商店"
	}
	return ""
}

func softwareDownload(host, blob string) bool {
	for _, domain := range []string{
		"xitongzhijia.net", "xitongzhijia.com", "it168.com", "onlinedown.net", "onlinedown.com",
		"pc6.com", "pc6.net", "crsky.com", "skycn.com", "newhua.com", "xiazaiba.com",
		"downxia.com", "2265.com", "duote.com", "xz7.com",
	} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	for _, word := range []string{"系统之家", "系统下载", "软件下载", "windows iso", ".iso", "ghost系统", "原版系统", "win10下载", "win11下载"} {
		if strings.Contains(blob, word) {
			return true
		}
	}
	return false
}

func videoOrDrama(host, blob string) bool {
	for _, domain := range []string{"iqiyi.com", "youku.com", "v.qq.com", "mgtv.com", "le.com", "pptv.com", "tudou.com", "bilibili.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	for _, word := range []string{"电视剧", "在线观看", "韩剧", "美剧", "日剧", "追剧", "全集在线", "影视"} {
		if strings.Contains(blob, word) {
			return true
		}
	}
	return false
}

func appStore(host, blob string) bool {
	for _, domain := range []string{"apps.apple.com", "itunes.apple.com", "play.google.com", "sj.qq.com", "wandoujia.com", "appgallery.huawei.com", "android.myapp.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	for _, word := range []string{"应用宝", "应用商店", "豌豆荚"} {
		if strings.Contains(blob, word) {
			return true
		}
	}
	return false
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
