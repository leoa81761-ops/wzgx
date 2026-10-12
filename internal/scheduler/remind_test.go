package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/pipeline"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

func TestExpiryReminderOnce(t *testing.T) {
	database, err := db.Open("sqlite://"+filepath.Join(t.TempDir(), "t.db"), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	st := store.New(database)
	now := time.Date(2026, 10, 12, 2, 0, 0, 0, schedule.Shanghai).UTC()
	expires := time.Date(2026, 10, 19, 2, 0, 0, 0, schedule.Shanghai).UTC()
	user := &model.User{Email: "soon@example.com", PasswordHash: "x", Tier: "standard", TierExpiresAt: &expires, IsActive: true, CreatedAt: now}
	if err := st.InsertUser(context.Background(), nil, user); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Deps: pipeline.Deps{Store: st}}
	if err := runner.remindExpiring(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := runner.remindExpiring(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	notes, unread, err := st.ListNotifications(context.Background(), user.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if unread != 1 || len(notes) != 1 || notes[0].Kind != "expiry" || notes[0].Title != "会员还剩 7 天" {
		t.Fatal(unread, notes)
	}
}
