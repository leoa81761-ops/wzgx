package stats

import (
	"context"
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
