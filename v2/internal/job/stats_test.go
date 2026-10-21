package job_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"gonghao/internal/auth"
)

func statItem(msgid, title, url string, days ...[2]any) map[string]any {
	var dl []any
	for _, d := range days {
		dl = append(dl, map[string]any{"stat_date": d[0], "read_user": d[1], "share_user": d[1].(int) / 10, "collection_user": d[1].(int) / 20,
			"like_user": 3, "zaikan_user": 1, "read_finish_rate": 0.62})
	}
	return map[string]any{"msgid": msgid, "title": title, "content_url": url, "detail_list": dl}
}

func TestStats(t *testing.T) {
	e, _, user, accID, colID := setup(t)
	ctx := t.Context()
	cst := time.FixedZone("CST", 8*3600)
	y, m, d := time.Now().In(cst).Date()
	now := time.Date(y, m, d, 10, 0, 0, 0, cst)
	e.pub.Now = func() time.Time { return now }
	day := func(n int) string { return now.AddDate(0, 0, -n).Format("2006-01-02") }

	_, c2, _ := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/columns", accID), user, map[string]any{"name": "闲聊", "article_type_id": 1, "enabled": true})
	col2 := int64(c2["id"].(float64))
	var uid int64
	e.pool.QueryRow(ctx, `SELECT id FROM users WHERE email='u@example.com'`).Scan(&uid)
	ins := func(title string, col int64, pushedDaysAgo int, url string) int64 {
		var id int64
		e.pool.QueryRow(ctx, `INSERT INTO article (user_id, account_id, column_id, title, status, pushed_at, publish_url)
			VALUES ($1,$2,$3,$4,'pushed',$5,$6) RETURNING id`, uid, accID, col, title, now.AddDate(0, 0, -pushedDaysAgo), url).Scan(&id)
		return id
	}
	a1 := ins("台灯怎么选", colID, 4, "https://mp.weixin.qq.com/s/aaa")
	ins("护眼小办法", colID, 4, "")
	ins("周末闲聊一", col2, 3, "")
	ins("周末闲聊二", col2, 3, "")
	e.wx.stats = map[string][]any{
		day(4): {statItem("100_1", "改过的标题", "https://mp.weixin.qq.com/s/aaa", [2]any{day(4), 300}, [2]any{day(3), 500}, [2]any{day(1), 600}),
			statItem("100_2", "护眼小办法", "https://mp.weixin.qq.com/s/bbb", [2]any{day(4), 200}, [2]any{day(1), 400}),
			statItem("999_1", "不是我们写的", "https://mp.weixin.qq.com/s/zzz", [2]any{day(4), 9999})},
		day(3): {statItem("101_1", "周末闲聊一", "", [2]any{day(3), 40}, [2]any{day(1), 60}),
			statItem("101_2", "周末闲聊二", "", [2]any{day(2), 80})},
	}

	// not verified: nothing is asked
	if err := e.pub.StatsDue(ctx); err != nil || len(e.wx.statDay) != 0 {
		t.Fatalf("unverified account pulled: %v %v", err, e.wx.statDay)
	}
	e.pool.Exec(ctx, `UPDATE wx_account SET verified=TRUE WHERE id=$1`, accID)
	if err := e.pub.StatsDue(ctx); err != nil {
		t.Fatal(err)
	}
	e.wx.mu.Lock()
	asked := strings.Join(e.wx.statDay, ",")
	e.wx.mu.Unlock()
	if !strings.Contains(asked, day(4)) || !strings.Contains(asked, day(1)) || strings.Contains(asked, day(0)) {
		t.Fatalf("asked %s", asked)
	}
	var n int
	e.pool.QueryRow(ctx, `SELECT count(DISTINCT article_id) FROM article_stat_day`).Scan(&n)
	if n != 4 {
		t.Fatalf("matched %d articles", n)
	}
	e.pool.QueryRow(ctx, `SELECT count(*) FROM article_stat_day WHERE read_user=9999`).Scan(&n)
	if n != 0 {
		t.Fatal("stored somebody else's article")
	}
	// once a day: a second tick asks nothing, and only known publish dates are asked next time
	before := len(e.wx.statDay)
	e.pub.StatsDue(ctx)
	if len(e.wx.statDay) != before {
		t.Fatal("pulled twice in a day")
	}
	if code, _, _ := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/stats/refresh", accID), user, nil); code == 200 {
		t.Fatal("refresh right after a pull")
	}

	_, st, b := do(t, e.srv, "GET", fmt.Sprintf("/api/accounts/%d/stats", accID), user, nil)
	arts, _ := st["articles"].([]any)
	if st["verified"] != true || len(arts) != 4 || !strings.Contains(fmt.Sprint(st["note"]), "4 篇") {
		t.Fatalf("stats: %s", b)
	}
	var top map[string]any
	for _, x := range arts {
		if int64(x.(map[string]any)["id"].(float64)) == a1 {
			top = x.(map[string]any)
		}
	}
	if top["reads"].(float64) != 600 || top["shares"].(float64) != 60 || top["finish_rate"].(float64) != 0.62 {
		t.Fatalf("article numbers: %v", top)
	}
	days, _ := st["days"].([]any)
	got := map[string]float64{}
	for _, x := range days {
		got[x.(map[string]any)["date"].(string)] = x.(map[string]any)["reads"].(float64)
	}
	// day 4: 300+200; day 3: +200 (a1) +40; day 2: +80; day 1: +100 (a1) +200 (a2) +20
	if got[day(4)] != 500 || got[day(3)] != 240 || got[day(2)] != 80 || got[day(1)] != 320 {
		t.Fatalf("daily reads: %v", got)
	}
	_, wk, _ := do(t, e.srv, "GET", "/api/me/weekly", user, nil)
	if !strings.Contains(fmt.Sprint(wk["text"]), "新增阅读 1140 人") || !strings.Contains(fmt.Sprint(wk["text"]), "《台灯怎么选》") {
		t.Fatalf("weekly: %v", wk)
	}
	// columns: 妙招 avg (600+400)/2=500, 闲聊 (60+80)/2=70, account avg 285 → 1.5 and 0.7 (clamped)
	cols, _ := st["columns"].([]any)
	fs := map[string]float64{}
	for _, x := range cols {
		fs[x.(map[string]any)["name"].(string)] = x.(map[string]any)["factor"].(float64)
	}
	if fs["妙招"] != 1.5 || fs["闲聊"] != 0.7 {
		t.Fatalf("factors %v", fs)
	}

	// rotation: off by default (nothing recorded); on → the pick says what it did
	e.fm.set("research", "", research)
	e.fm.set("write", "", article1)
	e.fm.set("review", "", `{"pass":true}`)
	e.pool.Exec(ctx, `UPDATE users SET quota_exempt=TRUE WHERE id=$1`, uid)
	gen := func() {
		_, g, b := do(t, e.srv, "POST", fmt.Sprintf("/api/accounts/%d/generate", accID), user, map[string]any{"topic": "台灯怎么选"})
		if g["job_id"] == nil {
			t.Fatal(b)
		}
		e.run()
	}
	gen()
	var note string
	e.pool.QueryRow(ctx, `SELECT rotation_note FROM wx_account WHERE id=$1`, accID).Scan(&note)
	if note != "" {
		t.Fatalf("weighting ran while off: %s", note)
	}
	if code, _, _ := do(t, e.srv, "PUT", fmt.Sprintf("/api/accounts/%d/stats/weighting", accID), user, map[string]any{"on": true}); code != 200 {
		t.Fatal("toggle")
	}
	gen()
	e.pool.QueryRow(ctx, `SELECT rotation_note FROM wx_account WHERE id=$1`, accID).Scan(&note)
	if !strings.Contains(note, "「妙招」1×1.5") || !strings.Contains(note, "「闲聊」1×0.7") || !strings.Contains(note, "这次选了") {
		t.Fatalf("rotation note: %s", note)
	}
	_, st, _ = do(t, e.srv, "GET", fmt.Sprintf("/api/accounts/%d/stats", accID), user, nil)
	if st["rotation_note"] != note || st["weighting"] != true {
		t.Fatal("page does not show the rotation")
	}

	// another user sees nothing
	e.auth.Register(ctx, "o@example.com", "password1", "", auth.Consent{TermsVersion: "1", PrivacyVersion: "1"}, "", "")
	_, otok, _ := e.auth.Login(ctx, "o@example.com", "password1", "", "")
	if code, _, _ := do(t, e.srv, "GET", fmt.Sprintf("/api/accounts/%d/stats", accID), otok, nil); code != 404 {
		t.Fatal("other user sees the stats")
	}
	if _, wk, _ := do(t, e.srv, "GET", "/api/me/weekly", otok, nil); wk["text"] != "" {
		t.Fatal("other user's weekly")
	}
}
