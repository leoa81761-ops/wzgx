package gen

import (
	"regexp"
	"sort"
	"strings"

	"gonghao/internal/render"
)

// Fact check (optional, per account): after writing, the concrete claims in the article (standard
// numbers, document numbers, dates, numbers with units) are looked up in the research facts the
// writer was given. Claims with no match are flagged for the user in the preview. It never blocks
// and never rewrites; it is a plain text comparison, no model call.

type Claim struct {
	Text      string `json:"text"`
	Kind      string `json:"kind"` // standard | doc_number | date | number
	Supported bool   `json:"supported"`
	Source    int    `json:"source,omitempty"` // evidence number that supports it
	Context   string `json:"context"`          // the sentence it appears in (shortened)
}

type FactCheck struct {
	Claims      []Claim `json:"claims"`
	Unsupported int     `json:"unsupported"`
	NoResearch  bool    `json:"no_research"` // nothing was researched: nothing to check against
}

var (
	fcStdRe  = regexp.MustCompile(`(?i)\b(GB/T|GB/Z|GB|JGJ/T|JGJ|CJJ/T|CJJ|YD/T|GA/T|JB/T|DL/T|DB\d{2}/T|DB\d{2})\s?(\d{2,6}(?:\.\d+)?)(?:\s?[-—–]\s?(\d{4}))?`)
	fcDocRe  = regexp.MustCompile(`[\p{Han}]{1,8}[〔\[［(（]\d{4}[〕\]］)）]\s*\d+\s*号`)
	fcDateRe = regexp.MustCompile(`(\d{4})\s*年\s*(\d{1,2})\s*月(?:\s*(\d{1,2})\s*日)?|(\d{4})-(\d{1,2})-(\d{1,2})`)
	fcNumRe  = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(%|％|万元|亿元|万|亿|元|千瓦|瓦|伏|毫米|厘米|公里|千米|米|平方米|㎡|吨|千克|公斤|克|lx|勒克斯|K|W|V|Mbps|Gbps|MB|GB|TB|小时|分钟|秒|天|周|个月|岁|人|倍|摄氏度|℃|度|户|家|座|台)`)
	fcMarkRe = regexp.MustCompile(`\[\d+\]`)
)

// CheckFacts compares the article's concrete claims with the research evidence.
func CheckFacts(a render.Article, ev []Evidence) FactCheck {
	var sentences []string
	add := func(s string) {
		s = fcMarkRe.ReplaceAllString(s, "")
		for _, x := range regexp.MustCompile(`[。！？!?\n]`).Split(s, -1) {
			if strings.TrimSpace(x) != "" {
				sentences = append(sentences, strings.TrimSpace(x))
			}
		}
	}
	for _, b := range a.Blocks {
		add(b.Text)
		for _, it := range b.Items {
			add(it)
		}
		for _, r := range b.Rows {
			add(strings.Join(r, "，"))
		}
	}
	type src struct {
		n    int
		text string
	}
	var corpus []src
	for _, e := range ev {
		corpus = append(corpus, src{e.N, fcNorm(strings.Join([]string{e.Title, e.Snippet, e.DocNumber, e.Date, e.Issuer}, " "))})
	}
	find := func(keys ...string) int {
		for _, c := range corpus {
			for _, k := range keys {
				if k != "" && strings.Contains(c.text, fcNorm(k)) {
					return c.n
				}
			}
		}
		return 0
	}
	out := FactCheck{Claims: []Claim{}, NoResearch: len(ev) == 0}
	seen := map[string]bool{}
	for _, s := range sentences {
		taken := make([]bool, len(s))
		claim := func(loc []int, kind string, keys ...string) {
			for i := loc[0]; i < loc[1]; i++ {
				if taken[i] {
					return
				}
			}
			for i := loc[0]; i < loc[1]; i++ {
				taken[i] = true
			}
			t := strings.TrimSpace(s[loc[0]:loc[1]])
			if seen[t] {
				return
			}
			seen[t] = true
			c := Claim{Text: t, Kind: kind, Context: clipRunes(s, 80)}
			c.Source = find(keys...)
			c.Supported = c.Source > 0
			if !c.Supported {
				out.Unsupported++
			}
			out.Claims = append(out.Claims, c)
		}
		for _, m := range fcStdRe.FindAllStringSubmatchIndex(s, -1) {
			code := s[m[2]:m[3]] + s[m[4]:m[5]] // e.g. GB50034: year differences are not checked
			claim(m[:2], "standard", code)
		}
		for _, m := range fcDocRe.FindAllStringIndex(s, -1) {
			for _, pre := range []string{"依据", "根据", "按照", "参照", "对照", "按", "据", "见", "和", "与", "及"} {
				if strings.HasPrefix(s[m[0]:m[1]], pre) {
					m[0] += len(pre)
					break
				}
			}
			t := s[m[0]:m[1]]
			claim(m, "doc_number", t, regexp.MustCompile(`[〔\[［(（]\d{4}[〕\]］)）]\s*\d+\s*号`).FindString(t))
		}
		for _, m := range fcDateRe.FindAllStringSubmatchIndex(s, -1) {
			var y, mo, d string
			if m[2] >= 0 {
				y, mo = s[m[2]:m[3]], s[m[4]:m[5]]
				if m[6] >= 0 {
					d = s[m[6]:m[7]]
				}
			} else {
				y, mo, d = s[m[8]:m[9]], s[m[10]:m[11]], s[m[12]:m[13]]
			}
			claim(m[:2], "date", dateKeys(y, mo, d)...)
		}
		for _, m := range fcNumRe.FindAllStringSubmatchIndex(s, -1) {
			num, unit := s[m[2]:m[3]], s[m[4]:m[5]]
			if m[2] > 0 && (s[m[2]-1] == '-' || s[m[2]-1] == '/') { // part of a code or date
				continue
			}
			claim(m[:2], "number", num+unit, num+" "+unit)
		}
	}
	sort.SliceStable(out.Claims, func(i, j int) bool { return !out.Claims[i].Supported && out.Claims[j].Supported })
	return out
}

func dateKeys(y, m, d string) []string {
	pad := func(s string) string {
		if len(s) == 1 {
			return "0" + s
		}
		return s
	}
	if d == "" {
		return []string{y + "年" + m + "月", y + "年" + pad(m) + "月", y + "-" + pad(m), y + "." + pad(m), y + "/" + pad(m)}
	}
	return []string{y + "年" + m + "月" + d + "日", y + "年" + pad(m) + "月" + pad(d) + "日", y + "-" + pad(m) + "-" + pad(d),
		y + "." + pad(m) + "." + pad(d), y + "/" + pad(m) + "/" + pad(d), y + "-" + m + "-" + d}
}

// fcNorm: lower case, half-width, no spaces, so "GB 50034" matches "GB50034" and "３００ lx" matches "300lx".
func fcNorm(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '０' && r <= '９':
			r = r - '０' + '0'
		case r >= 'Ａ' && r <= 'Ｚ':
			r = r - 'Ａ' + 'a'
		case r >= 'ａ' && r <= 'ｚ':
			r = r - 'ａ' + 'a'
		case r == '％':
			r = '%'
		case r == ' ' || r == '\t' || r == '\u3000' || r == '\n':
			continue
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}
