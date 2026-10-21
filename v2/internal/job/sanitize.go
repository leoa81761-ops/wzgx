package job

import (
	"regexp"

	"github.com/microcosm-cc/bluemonday"
)

// articlePolicy keeps the tags and inline styles our renderer emits and WeChat accepts, and drops
// scripts, event handlers, iframes, forms and javascript: URLs from user-edited article HTML.
var articlePolicy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowElements("section", "span", "div", "p", "br", "strong", "b", "em", "i", "u", "h1", "h2", "h3", "h4", "blockquote",
		"ul", "ol", "li", "table", "thead", "tbody", "tr", "th", "td", "img", "figure", "figcaption", "hr", "sup", "sub")
	p.AllowAttrs("style").Globally()
	p.AllowStyles().Matching(regexp.MustCompile(`^[^<>"]*$`)).Globally()
	p.AllowAttrs("src", "alt").OnElements("img")
	p.AllowDataURIImages()
	p.AllowURLSchemes("http", "https")
	p.RequireNoFollowOnLinks(false)
	return p
}()

func sanitizeArticleHTML(h string) string { return articlePolicy.Sanitize(h) }
