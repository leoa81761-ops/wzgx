package render

import (
	"encoding/json"
	"fmt"
	"strings"
)

// typeAlias maps the block type names models actually emit to ours. A block whose type is not
// known keeps its name, so the gate can block it instead of the renderer silently dropping it.
var typeAlias = map[string]string{
	"h2": "h2", "h1": "h2", "h3": "h2", "heading": "h2", "header": "h2", "subtitle": "h2", "section": "h2", "title": "h2",
	"p": "p", "paragraph": "p", "text": "p", "para": "p",
	"list": "list", "ul": "list", "ol": "list", "bullets": "list", "bullet_list": "list", "ordered_list": "list", "unordered_list": "list",
	"table": "table",
	"quote": "quote", "blockquote": "quote",
	"image": "image", "img": "image", "picture": "image",
}

// Known reports whether a block type is one the renderer handles.
func Known(t string) bool {
	switch t {
	case "h2", "p", "list", "table", "quote", "image":
		return true
	}
	return false
}

// UnmarshalJSON accepts common model variants: type aliases (ul, paragraph, h3…), "content"/"value"
// for text, and list items given as objects or a single string.
func (b *Block) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type    string          `json:"type"`
		Text    json.RawMessage `json:"text"`
		Content json.RawMessage `json:"content"`
		Value   json.RawMessage `json:"value"`
		Items   json.RawMessage `json:"items"`
		Rows    [][]any         `json:"rows"`
		Caption string          `json:"caption"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	t := strings.ToLower(strings.TrimSpace(raw.Type))
	if a, ok := typeAlias[t]; ok {
		t = a
	}
	*b = Block{Type: t, Caption: raw.Caption}
	for _, f := range []json.RawMessage{raw.Text, raw.Content, raw.Value} {
		if b.Text != "" || len(f) == 0 {
			continue
		}
		var s string
		if json.Unmarshal(f, &s) == nil {
			b.Text = s
		} else if b.Items == nil {
			b.Items = strList(f) // "content": ["…","…"]
		}
	}
	if len(raw.Items) > 0 {
		b.Items = strList(raw.Items)
	}
	for _, r := range raw.Rows {
		var row []string
		for _, c := range r {
			if c != nil {
				row = append(row, strings.TrimSpace(fmt.Sprint(c)))
			} else {
				row = append(row, "")
			}
		}
		if len(row) > 0 {
			b.Rows = append(b.Rows, row)
		}
	}
	return nil
}

func strList(m json.RawMessage) []string {
	var s string
	if json.Unmarshal(m, &s) == nil {
		if s == "" {
			return nil
		}
		return []string{s}
	}
	var xs []any
	if json.Unmarshal(m, &xs) != nil {
		return nil
	}
	var out []string
	for _, x := range xs {
		switch v := x.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				out = append(out, v)
			}
		case map[string]any:
			for _, k := range []string{"text", "content", "item", "value"} {
				if t, ok := v[k].(string); ok && t != "" {
					out = append(out, t)
					break
				}
			}
		}
	}
	return out
}
