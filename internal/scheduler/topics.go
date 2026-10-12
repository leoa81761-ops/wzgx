package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/hot"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/pipeline"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/store"
	"github.com/leoa81761-ops/wzgx/internal/topics"
	"github.com/leoa81761-ops/wzgx/internal/vertical"
)

const topicDecision = 24 * time.Hour

func confirmActive(user *model.User, plan *model.WritingPlan, now time.Time) bool {
	if plan == nil || !plan.ConfirmMode || user == nil {
		return false
	}
	tier, _ := quota.Membership(user, now)
	return tier == "premium" || tier == "lifetime"
}

func (r *Runner) advanceReferrals(ctx context.Context, tx *db.Tx, now time.Time) error {
	ids, err := r.Deps.Store.ListPendingInvitees(ctx, tx, 200)
	if err != nil {
		return err
	}
	for _, id := range ids {
		change, err := r.Deps.Store.SettleReferral(ctx, tx, id, now)
		if err != nil {
			return err
		}
		if change == nil {
			continue
		}
		title, body := referralNotice(change)
		if err := r.Deps.Store.InsertNotification(ctx, tx, &store.Notification{
			UserID: change.InviterID, Kind: "invite", Title: title, Body: body, CreatedAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func referralNotice(change *store.ReferralChange) (string, string) {
	switch change.Status {
	case "valid":
		return "邀请奖励已生效", "有一条邀请通过了观察期，免费额度已增加。付费期间不加篇，降回免费后会恢复。"
	case "frozen":
		reason := change.Reason
		if reason == "" {
			reason = "需要人工审核"
		}
		return "有一条邀请待审核", "这条邀请暂不计入额度：" + reason + "。管理员可以在配置中心通过或撤销。"
	default:
		reason := change.Reason
		if reason == "" {
			reason = "不再计入"
		}
		return "有一条邀请已撤销", "这条邀请不再增加额度：" + reason + "。已经写出的文章不会追回。"
	}
}

func (r *Runner) prepareTopics(ctx context.Context, tx *db.Tx, now time.Time) error {
	until := schedule.Format(now.Add(topicDecision))
	plans, err := r.Deps.Store.ListPlansAwaitingProposal(ctx, tx, schedule.Format(now), until)
	if err != nil {
		return err
	}
	for i := range plans {
		plan := &plans[i]
		user, err := r.Deps.Store.GetUser(ctx, tx, plan.UserID)
		if err != nil {
			return err
		}
		if !confirmActive(user, plan, now) || plan.NextRunAt == nil {
			continue
		}
		tier, _ := quota.Membership(user, now)
		choice, title, err := r.SuggestTopic(ctx, tx, plan, tier, now)
		if err != nil {
			return err
		}
		if choice.TopicTag == "" {
			continue
		}
		row := &store.TopicProposal{
			PlanID: plan.ID, TopicTag: choice.TopicTag, Title: title, Status: "pending",
			ProposedAt: now, ExecuteAt: plan.NextRunAt.UTC(),
		}
		if err := r.Deps.Store.InsertProposal(ctx, tx, row); err != nil {
			return err
		}
		deadline := now.Add(topicDecision).In(schedule.Shanghai).Format("1月2日 15:04")
		if err := r.Deps.Store.InsertNotification(ctx, tx, &store.Notification{
			UserID: plan.UserID, Kind: "topic", Title: "请确认下一篇选题",
			Body:      fmt.Sprintf("下一篇选题是「%s」。%s 前可以采用或换一个。超时按这个选题写；如果原定时间更早，会等到确认时间结束再写。", title, deadline),
			CreatedAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) SuggestTopic(ctx context.Context, q db.Q, plan *model.WritingPlan, tier string, now time.Time) (topics.Choice, string, error) {
	historyRows, err := r.Deps.Store.ListCoverage(ctx, q, plan.ID)
	if err != nil {
		return topics.Choice{}, "", err
	}
	veto, err := r.Deps.Store.ListVetoTags(ctx, q, plan.ID)
	if err != nil {
		return topics.Choice{}, "", err
	}
	var history []topics.Coverage
	used := map[string]bool{}
	for _, row := range historyRows {
		history = append(history, topics.Coverage{Subtopic: row.Subtopic, Column: row.ColumnType, Angle: row.Angle, CreatedAt: row.CreatedAt})
		used[row.Subtopic+"|"+row.ColumnType+"|"+row.Angle] = true
	}
	history, err = pipeline.AppendSiblingCoverage(ctx, r.Deps.Store, q, plan.UserID, plan.ID, history)
	if err != nil {
		return topics.Choice{}, "", err
	}
	for _, row := range history[len(historyRows):] {
		used[row.Subtopic+"|"+row.Column+"|"+row.Angle] = true
	}
	for _, tag := range veto {
		used[tag] = true
	}
	packs, err := r.Deps.Store.ListVerticals(ctx, q, true)
	if err != nil {
		return topics.Choice{}, "", err
	}
	pack := vertical.Match(plan.Direction, packs)
	if tier != "free" {
		rows, err := r.Deps.Store.ListHotItems(ctx, q)
		if err != nil {
			return topics.Choice{}, "", err
		}
		items := hotRows(rows)
		items = hot.ForVertical(items, pack.Key, plan.Direction)
		if item, ok := hot.Choose(items, tier, used); ok {
			if choice, yes := topics.ParseTag(hot.Tag(item)); yes && !used[choice.TopicTag] {
				return choice, item.Title, nil
			}
		}
	}
	choice := topics.PickTree(vertical.Subtopics(pack), plan.Direction, tier, history, now, veto...)
	title := choice.Subtopic + " / " + choice.Column + " / " + choice.Angle
	return choice, title, nil
}

func hotRows(rows []store.HotRow) []hot.Item {
	items := make([]hot.Item, 0, len(rows))
	for _, row := range rows {
		reports := row.Reports
		if reports < 1 {
			reports = 1
		}
		items = append(items, hot.Item{
			Title: row.Title, Summary: row.Summary, Source: row.SourceLabel, SourceName: row.SourceName,
			URL: row.URL, PublishedOn: row.PublishedOn, Heat: row.Heat, Vertical: row.Vertical, Reports: reports,
		})
	}
	return items
}
