package gen

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

var trailingCommaRe = regexp.MustCompile(`,\s*([}\]])`)

var thinkRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// decodeJSON reads a JSON object from a model reply, tolerating code fences or text around it.
func decodeJSON(s string, v any) error {
	s = thinkRe.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return errors.New("reply has no JSON object")
	}
	first := json.Unmarshal([]byte(s[i:j+1]), v)
	if first == nil {
		return nil
	}
	// tolerant passes: trailing commas, then the first complete object from each '{' (text after it ignored)
	if fixed := trailingCommaRe.ReplaceAllString(s[i:j+1], "$1"); json.Unmarshal([]byte(fixed), v) == nil {
		return nil
	}
	for k := i; k >= 0 && k < len(s); {
		dec := json.NewDecoder(strings.NewReader(trailingCommaRe.ReplaceAllString(s[k:], "$1")))
		if dec.Decode(v) == nil {
			return nil
		}
		n := strings.Index(s[k+1:], "{")
		if n < 0 {
			break
		}
		k += n + 1
	}
	return first
}
