package llm

import "testing"

func TestParseSearchBodyReadsJSONAndHTML(t *testing.T) {
	jsonRows := ParseSearchBody([]byte(`{"results":[{"title":"弱电智能化验收","url":"https://www.mohurd.gov.cn/a"},{"title":"跳过","url":"https://duckduckgo.com/skip"}]}`))
	if len(jsonRows) != 1 || jsonRows[0]["url"] != "https://www.mohurd.gov.cn/a" {
		t.Fatal(jsonRows)
	}
	htmlRows := ParseSearchBody([]byte(`<a href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fwww.gov.cn%2Fzhengce%2F1&amp;rut=1">`))
	if len(htmlRows) != 1 || htmlRows[0]["url"] != "https://www.gov.cn/zhengce/1" {
		t.Fatal(htmlRows)
	}
}
