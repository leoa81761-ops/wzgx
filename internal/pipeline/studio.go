package pipeline

import (
	"context"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
)

func attachStudio(ctx context.Context, deps Deps, w *work) {
	extra, err := deps.Store.GetPlanExtra(ctx, nil, w.PlanID)
	if err == nil {
		w.Opening = strings.TrimSpace(extra.Opening)
		w.Ending = strings.TrimSpace(extra.Ending)
		w.ColumnName = strings.TrimSpace(extra.ColumnName)
		w.ColumnStyle = strings.TrimSpace(extra.ColumnStyle)
		if extra.ProfileID > 0 && (w.Tier == "premium" || w.Tier == "lifetime") {
			if profile, err := deps.Store.GetStyleProfile(ctx, extra.ProfileID); err == nil && profile != nil && profile.UserID == w.UserID {
				if w.StyleHint != "" {
					w.StyleHint += "。"
				}
				w.StyleHint += "风格画像 " + profile.Features
			}
		}
		if extra.AssetID > 0 {
			if asset, err := deps.Store.GetAsset(ctx, extra.AssetID); err == nil && asset != nil && asset.UserID == w.UserID && len(asset.Data) > 0 {
				w.AssetCover = asset.Data
				w.AssetNote = strings.TrimSpace(asset.Name + " " + asset.LicenseNote)
			}
		}
	}
	agreed, err := deps.Store.HasAnyConsent(ctx, w.UserID, "draft_titles")
	if err != nil || !agreed || w.AppSecret == "" {
		return
	}
	mode := "live"
	if strings.HasPrefix(w.AppID, "wx_mock") {
		mode = "mock"
	}
	client := wechat.Build(w.AppID, w.AppSecret, mode, "https://api.weixin.qq.com", deps.HTTP, deps.Tokens)
	titles, err := client.ListDraftTitles(ctx, 20)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, title := range w.RecentTitles {
		seen[title] = true
	}
	for _, title := range titles {
		if title == "" || seen[title] {
			continue
		}
		w.RecentTitles = append(w.RecentTitles, title)
		seen[title] = true
	}
}

func pauseIfQuiet(ctx context.Context, deps Deps, tx *db.Tx, plan *model.WritingPlan, now time.Time) (bool, error) {
	var q db.Q
	if tx != nil {
		q = tx
	}
	pub, err := deps.Store.GetPlanPublish(ctx, q, plan.ID)
	if err != nil {
		return false, err
	}
	if pub.Enabled {
		return false, nil
	}
	streak, err := deps.Store.QuietStreak(ctx, q, plan.ID)
	if err != nil {
		return false, err
	}
	return streak >= 5, nil
}

// PauseIfQuiet is used by tests. It pauses an active plan that already has five untouched drafts.
func PauseIfQuiet(ctx context.Context, deps Deps, planID int64, now time.Time) (bool, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_ = schedule.ShanghaiDay(now)
	plan, err := deps.Store.GetPlan(ctx, nil, planID)
	if err != nil || plan == nil || plan.Status != "active" {
		return false, err
	}
	paused, err := pauseIfQuiet(ctx, deps, nil, plan, now)
	if err != nil || !paused {
		return paused, err
	}
	plan.Status = "paused"
	plan.PauseReason = "连续 5 篇没有反馈"
	return true, deps.Store.UpdatePlan(ctx, nil, plan)
}
