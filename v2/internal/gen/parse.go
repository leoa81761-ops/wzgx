package gen

import (
	"encoding/json"
	"errors"
	"strings"
)

// decodeJSON reads a JSON object from a model reply, tolerating code fences or text around it.
func decodeJSON(s string, v any) error {
	s = strings.TrimSpace(s)
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return errors.New("reply has no JSON object")
	}
	return json.Unmarshal([]byte(s[i:j+1]), v)
}
