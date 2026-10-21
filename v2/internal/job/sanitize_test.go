package job

import (
	"strings"
	"testing"
)

func TestSanitizeArticleHTML(t *testing.T) {
	in := `<section style="color:red"><p onclick="x()">好<script>alert(1)</script></p><img src="https://a/b.jpg" onerror="x()"><a href="javascript:x()">l</a><iframe src="https://e"></iframe></section>`
	out := sanitizeArticleHTML(in)
	for _, bad := range []string{"<script", "onclick", "onerror", "javascript:", "<iframe"} {
		if strings.Contains(out, bad) {
			t.Fatalf("%s kept: %s", bad, out)
		}
	}
	for _, good := range []string{`style="color:red"`, `<img src="https://a/b.jpg"`, "<section"} {
		if !strings.Contains(out, good) {
			t.Fatalf("%s lost: %s", good, out)
		}
	}
}
