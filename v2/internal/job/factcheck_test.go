package job_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestFactCheck(t *testing.T) {
	e, _, user, accID, colID := setup(t)
	ctx := t.Context()
	e.fm.set("research", "", research)
	e.fm.set("write", "", article1)
	e.fm.set("review", "", `{"pass":true}`)
	e.pool.Exec(ctx, `UPDATE users SET quota_exempt=TRUE WHERE email='u@example.com'`)
	acc := fmt.Sprintf("/api/accounts/%d", accID)
	gen := func(topic string) map[string]any {
		_, m, b := do(t, e.srv, "POST", acc+"/generate", user, map[string]any{"column_id": colID, "topic": topic})
		if m["job_id"] == nil {
			t.Fatal(b)
		}
		e.run()
		_, a, _ := do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%v", e.jobStatus(user, m["job_id"])["article_id"]), user, nil)
		return a
	}
	// default off: nothing checked
	if a := gen("台灯怎么放"); a["fact_check"] != nil {
		t.Fatalf("checked while off: %v", a["fact_check"])
	}
	_, p, _ := do(t, e.srv, "GET", acc+"/profile", user, nil)
	p["fact_check"] = true
	if code, _, b := do(t, e.srv, "PUT", acc+"/profile", user, p); code != 200 {
		t.Fatal(b)
	}
	a := gen("台灯怎么放才不伤眼")
	if a["status"] != "ready" {
		t.Fatalf("fact check must not block: %v %v", a["status"], a["block_reason"])
	}
	raw, _ := json.Marshal(a["fact_check"])
	var fc struct {
		Claims []struct {
			Text      string `json:"text"`
			Kind      string `json:"kind"`
			Supported bool   `json:"supported"`
		} `json:"claims"`
		Unsupported int `json:"unsupported"`
	}
	json.Unmarshal(raw, &fc)
	sup := map[string]bool{}
	for _, c := range fc.Claims {
		sup[c.Text] = c.Supported
	}
	if v, ok := sup["GB 50034"]; !ok || !v {
		t.Fatalf("standard should be supported: %s", raw)
	}
	if v, ok := sup["300 lx"]; !ok || v || fc.Unsupported == 0 {
		t.Fatalf("300 lx is not in the research: %s", raw)
	}
	// the article is not rewritten, and only the preview carries the marks
	if strings.Contains(fmt.Sprint(a["html"]), "<mark") || !strings.Contains(fmt.Sprint(a["html"]), "300 lx") {
		t.Fatal("stored html changed")
	}
	code, _, prev := do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%v/preview", a["id"]), user, nil)
	if code != 200 || !strings.Contains(prev, `title="资料里没找到依据，请核对">300 lx</mark>`) || strings.Contains(prev, `请核对">GB 50034</mark>`) {
		t.Fatalf("preview marks: %.600s", prev)
	}
	_, _, plain := do(t, e.srv, "GET", fmt.Sprintf("/api/articles/%v/preview?marks=0", a["id"]), user, nil)
	if strings.Contains(plain, "<mark") {
		t.Fatal("marks=0 still marked")
	}
}
