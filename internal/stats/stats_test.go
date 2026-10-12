package stats

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/wechat"
)

func TestCollectReadsSharesAndFollows(t *testing.T) {
	wechat.ResetMock()
	const appID = "wx_mock_stats"
	wechat.SetMockDatacube(appID, "/datacube/getarticleread", map[string]any{"list": []any{
		map[string]any{"ref_date": "2026-10-10", "msgid": "11_1", "detail": map[string]any{"read_user": float64(8)}},
	}})
	wechat.SetMockDatacube(appID, "/datacube/getarticleshare", map[string]any{"list": []any{
		map[string]any{"ref_date": "2026-10-10", "msgid": "11_1", "detail": map[string]any{"share_user": float64(2)}},
	}})
	wechat.SetMockDatacube(appID, "/datacube/getusersummary", map[string]any{"list": []any{
		map[string]any{"ref_date": "2026-10-10", "user_source": float64(0), "new_user": float64(1)},
		map[string]any{"ref_date": "2026-10-10", "user_source": float64(1), "new_user": float64(4)},
	}})
	client := wechat.Build(appID, "mock-secret", "mock", "", nil, nil)
	rows, err := Collect(context.Background(), client, "2026-10-10")
	if err != nil {
		t.Fatal(err)
	}
	reads, shares, follows := 0, 0, 0
	for _, row := range rows {
		reads += row.Reads
		shares += row.Shares
		follows += row.Follows
	}
	if reads != 8 || shares != 2 || follows != 5 {
		t.Fatalf("reads=%d shares=%d follows=%d", reads, shares, follows)
	}
}

func TestDescribeDoesNotInvent(t *testing.T) {
	summary, advice, days := Describe([]Row{{Date: "2026-10-10", Reads: 8, Shares: 2, Follows: 5}})
	if summary != "阅读 8，分享 2，收藏 0，新增关注 5。" {
		t.Fatal(summary)
	}
	if days != 1 || advice == "" {
		t.Fatal(advice, days)
	}
	weights, note := SuggestWeights([]TopicCount{{Subtopic: "入门", Published: 1, Discarded: 1}})
	if len(weights) != 0 || note == "" {
		t.Fatal(weights, note)
	}
	weights, note = SuggestWeights([]TopicCount{{Subtopic: "入门", Published: 0, Discarded: 3}})
	if weights["入门"] != 0 {
		t.Fatal(weights, note)
	}
}

func TestReadsTuneTopicAndTitleWithoutThinSamples(t *testing.T) {
	thin := []Engagement{
		{Subtopic: "验收", Title: "为什么验收总卡在同一步", Reads: 20, Shares: 2},
		{Subtopic: "验收", Title: "验收可以先做的那一步吗", Reads: 18, Shares: 1},
	}
	weights, note, shape := MergeTune(nil, "样本不足或没有明显差别，不调整选题权重。", thin)
	if len(weights) != 0 || shape != "" || !strings.Contains(note, "不调整选题权重") {
		t.Fatal(weights, note, shape)
	}
	matched := []Engagement{
		{Subtopic: "验收", Title: "为什么验收总卡在同一步", Reads: 20, Shares: 2},
		{Subtopic: "验收", Title: "验收可以先做的那一步吗", Reads: 18, Shares: 1},
		{Subtopic: "验收", Title: "怎么把验收拆开", Reads: 16, Shares: 1},
		{Subtopic: "收纳", Title: "先把柜子清空", Reads: 1, Shares: 0},
		{Subtopic: "收纳", Title: "晚饭后收一轮", Reads: 1, Shares: 0},
		{Subtopic: "收纳", Title: "玩具放回原处", Reads: 0, Shares: 0},
	}
	weights, note, shape = MergeTune(map[string]int{"验收": 0}, "按反馈调整：验收降低。", matched)
	if weights["验收"] != 0 {
		t.Fatal("feedback weight should win", weights)
	}
	if shape != "question" || !strings.Contains(note, "问句") {
		t.Fatal(note, shape)
	}
	weights, note, shape = MergeTune(nil, "样本不足或没有明显差别，不调整选题权重。", matched)
	if weights["验收"] != 2 || weights["收纳"] != 0 || shape != "question" {
		t.Fatal(weights, note, shape)
	}
	hits := []ArticleHit{{ID: 7, Title: "为什么验收总卡在同一步", Subtopic: "验收"}}
	got := MatchReads(hits, []Row{
		{Key: "manual", Reads: 100},
		{Key: "11_1", Title: "为什么验收总卡在同一步", Reads: 9, Shares: 1},
		{Key: "empty", Reads: 0},
	})
	if len(got) != 1 || got[0].Reads != 9 || got[0].Subtopic != "验收" {
		t.Fatal(got)
	}
	if len(MatchReads(hits, []Row{{Key: "11_1", Reads: 9}})) != 0 {
		t.Fatal("msgid without a title must not invent a topic")
	}
}

func TestPullDueAfterEight(t *testing.T) {
	// 2026-10-12 00:03 UTC is 08:03 in Shanghai.
	now := time.Date(2026, 10, 12, 0, 3, 0, 0, time.UTC)
	if PullDue(now, 5) {
		t.Fatal("account 5 waits until minute 5")
	}
	if !PullDue(now, 1) {
		t.Fatal("account 1 is due at 08:03")
	}
	early := time.Date(2026, 10, 11, 23, 0, 0, 0, time.UTC)
	if PullDue(early, 1) {
		t.Fatal("before 08:00 should wait")
	}
	if Yesterday(now) != "2026-10-11" {
		t.Fatal(Yesterday(now))
	}
}
