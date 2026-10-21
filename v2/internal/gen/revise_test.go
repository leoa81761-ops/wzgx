package gen

import "testing"

func TestParseRevise(t *testing.T) {
	cases := map[string]int{
		`{"gaps":["a"],"article":{"title":"T","blocks":[{"type":"p","text":"x"}]}}`:                                     1,
		"```json\n{\"gaps\":[\"a\"],\"article\":{\"title\":\"T\",\"blocks\":[{\"type\":\"p\",\"text\":\"x\"},]},}\n```": 1,
		`{"gaps":"a","article":"{\"title\":\"T\",\"blocks\":[{\"type\":\"p\",\"text\":\"x\"}]}"}`:                       1,
		`{"title":"T","blocks":[{"type":"p","text":"x"},{"type":"p","text":"y"}]}`:                                      2,
		`{"gaps":[],"article":null}`: 0,
		`说明 {"gaps":["a"],"article":{"title":"T","blocks":[{"type":"p","text":"x"}]}} 备注 {见上}`: 1,
	}
	for in, n := range cases {
		var rv reviseReply
		if err := parseRevise(in, &rv); err != nil || len(rv.Article.Blocks) != n {
			t.Errorf("%q: err=%v blocks=%d", in, err, len(rv.Article.Blocks))
		}
	}
}
