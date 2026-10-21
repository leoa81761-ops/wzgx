package job_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestStyle(t *testing.T) {
	e, root, user, accID, colID := setup(t)
	ctx := t.Context()
	e.fm.set("research", "", "未检索到可靠来源")
	e.fm.set("write", "", article1)
	e.fm.set("review", "", `{"pass":true}`)
	e.pool.Exec(ctx, `UPDATE users SET quota_exempt=TRUE WHERE email='u@example.com'`)
	acc := fmt.Sprintf("/api/accounts/%d", accID)
	sample := func(i int) string {
		return strings.Repeat(fmt.Sprintf("我家阳台那盆绿萝今年长得特别好，第%d篇。说起来也简单，就是别老浇水。", i), 12)
	}
	three := []string{sample(1), sample(2), sample(3)}

	if code, _, _ := do(t, e.srv, "POST", acc+"/style", user, map[string]any{"samples": three[:2]}); code != 400 {
		t.Fatal("two samples accepted")
	}
	if code, _, _ := do(t, e.srv, "POST", acc+"/style", user, map[string]any{"samples": []string{"太短", sample(2), sample(3)}}); code != 400 {
		t.Fatal("short sample accepted")
	}
	if code, _, _ := do(t, e.srv, "GET", acc+"/style", root, nil); code != 403 {
		t.Fatal("staff read a user's style")
	}
	// one call; a "trait" that copies the source text is dropped
	e.fm.set("topic", "", `{"tone":"像跟老邻居唠家常，亲切随和","sentence":"短句为主，一句一个意思","structure":"先讲自家小事再给办法","vocabulary":"口语化，常说说起来","other":"我家阳台那盆绿萝今年长得特别好"}`)
	_, p, b := do(t, e.srv, "POST", acc+"/style", user, map[string]any{"samples": three})
	tr, _ := p["traits"].(map[string]any)
	if tr == nil || tr["tone"] != "像跟老邻居唠家常，亲切随和" || tr["other"] != "" || p["samples"].(float64) != 3 {
		t.Fatalf("learn: %s", b)
	}
	sys, _ := e.fm.last("topic")
	if !strings.Contains(sys, "只描述风格特征") {
		t.Fatal("style prompt")
	}
	// the samples are not kept anywhere
	var leaks int
	e.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM style_profile WHERE traits::text LIKE '%绿萝%')
		+ (SELECT count(*) FROM audit_log WHERE detail::text LIKE '%绿萝%' OR target LIKE '%绿萝%')
		+ (SELECT count(*) FROM model_call WHERE error LIKE '%绿萝%')`).Scan(&leaks)
	if leaks != 0 {
		t.Fatalf("sample text stored: %d", leaks)
	}

	writeUser := func(topic string) string {
		_, m, b := do(t, e.srv, "POST", acc+"/generate", user, map[string]any{"column_id": colID, "topic": topic})
		if m["job_id"] == nil {
			t.Fatal(b)
		}
		e.run()
		_, u := e.fm.last("write")
		return u
	}
	if u := writeUser("绿萝黄叶怎么办"); !strings.Contains(u, "写作风格（从用户自己的范文里提炼）：语气：像跟老邻居唠家常") {
		t.Fatalf("writer did not get the style: %.400s", u)
	}
	// a column profile wins over the account's
	e.fm.set("topic", "", `{"tone":"干脆利落，像老师傅讲活","sentence":"中短句","structure":"先结论后步骤","vocabulary":"行话少"}`)
	_, cp, b := do(t, e.srv, "POST", acc+"/style", user, map[string]any{"column_id": colID, "samples": three})
	if cp["column_id"] == nil {
		t.Fatal(b)
	}
	if u := writeUser("吊兰怎么分盆"); !strings.Contains(u, "干脆利落") || strings.Contains(u, "老邻居") {
		t.Fatalf("column style: %.400s", u)
	}
	// switches: column off -> account style; both off -> none
	do(t, e.srv, "PUT", fmt.Sprintf("%s/style/%v", acc, cp["id"]), user, map[string]any{"enabled": false})
	if u := writeUser("多肉怎么浇水"); !strings.Contains(u, "老邻居") {
		t.Fatal("account style after column off")
	}
	do(t, e.srv, "PUT", fmt.Sprintf("%s/style/%v", acc, p["id"]), user, map[string]any{"enabled": false})
	if u := writeUser("文竹怎么养"); strings.Contains(u, "写作风格") {
		t.Fatal("style used while off")
	}
	_, list, _ := do(t, e.srv, "GET", acc+"/style", user, nil)
	_ = list
	if code, _, _ := do(t, e.srv, "DELETE", fmt.Sprintf("%s/style/%v", acc, cp["id"]), user, nil); code != 200 {
		t.Fatal("delete")
	}
}
