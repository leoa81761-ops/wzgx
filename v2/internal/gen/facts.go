package gen

import (
	"regexp"
	"strings"
)

var (
	bulletRe = regexp.MustCompile(`^\s*(?:[-*•·]|\d{1,2}[.、)）])\s*`)
	// "fact（来源：a｜b｜c）" — full- or half-width brackets, 来源/出处, ｜ or | or ， separators
	srcRe   = regexp.MustCompile(`[（(]\s*(?:来源|出处|据)\s*[:：]\s*([^）)]*)[）)]\s*[。.]?\s*$`)
	dateRe  = regexp.MustCompile(`(20\d{2})\s*[-年./]\s*(\d{1,2})(?:\s*[-月./]\s*(\d{1,2})日?)?`)
	urlRe   = regexp.MustCompile(`https?://\S+`)
	labelRe = regexp.MustCompile(`^(?:来源|出处|标准号|文号|发布日期|日期|标题)\s*[:：]\s*`)
	mdRe    = regexp.MustCompile(`\*\*|__|^#+\s*`)
	stdNoRe = regexp.MustCompile(`(?i)GB(?:/T|/Z)?\s?\d{3,6}(?:[.-]\d{1,4})*(?:-\d{4})?`)
)

// ParseFacts reads the search model's plain-text answer, tolerantly: bullet lines with an optional
// "（来源：标题｜文号｜日期）" tail. Lines without a source are kept as facts with no title.
// Returns nothing when the model says it found nothing.
func ParseFacts(text string) []Evidence {
	text = strings.TrimSpace(text)
	if text == "" || strings.Contains(text, "未检索到可靠来源") && len([]rune(text)) < 40 {
		return nil
	}
	if strings.HasPrefix(strings.Trim(text, "*` \n"), "{") {
		// a JSON answer: take facts/notes if present, else treat as nothing found
		var j struct {
			Notes string `json:"notes"`
			Facts []struct {
				Claim, Title, Date string
				DocNumber          string `json:"doc_number"`
			} `json:"facts"`
		}
		if decodeJSON(strings.Trim(text, "*` \n"), &j) == nil {
			var out []Evidence
			for _, f := range j.Facts {
				if f.Claim != "" {
					out = append(out, Evidence{Title: or(f.Title, f.Claim), Snippet: f.Claim, Date: normDate(f.Date), DocNumber: f.DocNumber, From: "search"})
				}
			}
			if len(out) == 0 && j.Notes != "" {
				return ParseFacts(j.Notes)
			}
			return out
		}
	}
	var out []Evidence
	lines := strings.Split(text, "\n")
	bulleted := 0
	for _, l := range lines {
		if bulletRe.MatchString(l) {
			bulleted++
		}
	}
	for _, l := range lines {
		l = strings.TrimSpace(mdRe.ReplaceAllString(l, ""))
		if l == "" || (bulleted > 0 && !bulletRe.MatchString(l)) {
			continue
		}
		l = strings.TrimSpace(bulletRe.ReplaceAllString(l, ""))
		e := Evidence{From: "search"}
		var parts []string
		first := 0 // index of the part that names the source
		if m := srcRe.FindStringSubmatchIndex(l); m != nil {
			parts = splitBars(l[m[2]:m[3]])
			l = strings.TrimSpace(l[:m[0]])
		} else if bars := splitBars(l); len(bars) > 1 {
			// "标题｜文号｜日期" without the 来源 bracket: the first part is both the fact and the title
			l, parts, first = strings.TrimSpace(bars[0]), bars, -1
			e.Title = l
		}
		{
			for i, part := range parts {
				part = strings.TrimSpace(labelRe.ReplaceAllString(strings.TrimSpace(part), ""))
				switch {
				case i == first:
					e.Title = part
				case i == 0:
				case dateRe.MatchString(part) && e.Date == "":
					e.Date = normDate(part)
				case e.DocNumber == "" && (stdNoRe.MatchString(part) || docNoRe.MatchString(part) || strings.Contains(part, "号")):
					e.DocNumber = part
				}
			}
		}
		if e.DocNumber == "" {
			if m := stdNoRe.FindString(l + " " + e.Title); m != "" {
				e.DocNumber = m
			}
		}
		if u := urlRe.FindString(l); u != "" {
			e.URL = strings.TrimRight(u, "）)。，,")
			l = strings.TrimSpace(strings.Replace(l, u, "", 1))
		}
		e.Snippet = l
		if len([]rune(e.Snippet)) < 8 {
			continue
		}
		if e.Title == "" {
			r := []rune(e.Snippet)
			e.Title = string(r[:min(len(r), 30)])
		}
		out = append(out, e)
		if len(out) >= 10 {
			break
		}
	}
	if len(out) == 0 && !strings.Contains(text, "未检索到") {
		r := []rune(text)
		out = append(out, Evidence{Title: "检索摘要", Snippet: string(r[:min(len(r), 1500)]), From: "search"})
	}
	return out
}

func normDate(s string) string {
	m := dateRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	mo, d := m[2], m[3]
	if len(mo) == 1 {
		mo = "0" + mo
	}
	if d == "" {
		return m[1] + "-" + mo
	}
	if len(d) == 1 {
		d = "0" + d
	}
	return m[1] + "-" + mo + "-" + d
}

func or(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	r := []rune(b)
	return string(r[:min(len(r), 30)])
}

func splitBars(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '｜' || r == '|' || r == '；' || r == ';' })
}
