package gen

import (
	"encoding/json"
	"strings"

	"gonghao/internal/render"
)

type reviseReply struct {
	Gaps    []string
	Article render.Article
}

// parseRevise reads the revise step's reply. It accepts {"gaps":[…],"article":{…}|null}, an article
// given as a JSON string, gaps given as one string, and a bare article object without the wrapper.
func parseRevise(text string, out *reviseReply) error {
	var raw struct {
		Gaps    json.RawMessage `json:"gaps"`
		Article json.RawMessage `json:"article"`
		Blocks  json.RawMessage `json:"blocks"`
	}
	if err := decodeJSON(text, &raw); err != nil {
		return err
	}
	*out = reviseReply{}
	var gaps []string
	if json.Unmarshal(raw.Gaps, &gaps) == nil {
		out.Gaps = gaps
	} else {
		var g string
		if json.Unmarshal(raw.Gaps, &g) == nil && strings.TrimSpace(g) != "" {
			out.Gaps = []string{g}
		}
	}
	art := raw.Article
	var inner string
	if json.Unmarshal(art, &inner) == nil { // article sent as a string holding JSON
		art = nil
		if strings.Contains(inner, "{") {
			var a render.Article
			if err := decodeJSON(inner, &a); err != nil {
				return err
			}
			out.Article = a
			return nil
		}
	}
	if len(art) > 0 && string(art) != "null" {
		return json.Unmarshal(art, &out.Article)
	}
	if len(raw.Blocks) > 0 && len(raw.Article) == 0 { // bare article, no wrapper
		return decodeJSON(text, &out.Article)
	}
	return nil
}
