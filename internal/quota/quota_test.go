package quota

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

func TestDailyMonthlyReleaseAndGrace(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	user := &model.User{Email: "quota@example.com", PasswordHash: "x", Tier: "free", IsActive: true, CreatedAt: now}
	if err := st.InsertUser(ctx, nil, user); err != nil {
		t.Fatal(err)
	}
	if err := Reserve(ctx, st, nil, user, 1, now, false, ""); err != nil {
		t.Fatal(err)
	}
	reason, err := BlockReason(ctx, st, nil, user, now)
	if err != nil || reason != "今日额度已用完" {
		t.Fatal(reason, err)
	}
	if err := Reserve(ctx, st, nil, user, 2, now, false, ""); err == nil {
		t.Fatal("second reserve should fail")
	}
	if err := Release(ctx, st, nil, 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := Reserve(ctx, st, nil, user, 3, now, false, ""); err != nil {
		t.Fatal(err)
	}

	freeUser := &model.User{Email: "free-regen@example.com", PasswordHash: "x", Tier: "free", IsActive: true, CreatedAt: now}
	if err := st.InsertUser(ctx, nil, freeUser); err != nil {
		t.Fatal(err)
	}
	if err := Reserve(ctx, st, nil, freeUser, 9, now, true, "首次重写"); err != nil {
		t.Fatal(err)
	}
	reason, err = BlockReason(ctx, st, nil, freeUser, now)
	if err != nil || reason != "" {
		t.Fatalf("free regen should not consume quota, got %q %v", reason, err)
	}

	start := time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)
	monthUser := &model.User{Email: "month@example.com", PasswordHash: "x", Tier: "free", IsActive: true, CreatedAt: start}
	if err := st.InsertUser(ctx, nil, monthUser); err != nil {
		t.Fatal(err)
	}
	for offset := 0; offset < 10; offset++ {
		if err := Reserve(ctx, st, nil, monthUser, int64(100+offset), start.AddDate(0, 0, offset), false, ""); err != nil {
			t.Fatal(offset, err)
		}
	}
	blocked, err := BlockReason(ctx, st, nil, monthUser, start.AddDate(0, 0, 20))
	if err != nil || blocked != "本月额度已用完" {
		t.Fatal(blocked, err)
	}

	graceNow := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	expires := graceNow.Add(-3 * 24 * time.Hour)
	member := &model.User{Tier: "standard", TierExpiresAt: &expires}
	tier, state := Membership(member, graceNow)
	if tier != "standard" || state != "grace" {
		t.Fatal(tier, state)
	}
	tier, state = Membership(member, expires.Add(8*24*time.Hour))
	if tier != "free" || state != "expired" {
		t.Fatal(tier, state)
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	database, err := db.Open("sqlite://"+filepath.Join(t.TempDir(), "t.db"), 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return store.New(database)
}
