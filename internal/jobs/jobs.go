package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

func Create(ctx context.Context, st *store.Store, q db.Q, user *model.User, plan *model.WritingPlan, account *model.WxAccount, trigger string, now time.Time, regen *model.Article) (*model.ArticleTask, error) {
	if err := st.LockUserQuota(ctx, q, user.ID); err != nil {
		return nil, err
	}
	if account.BindStatus != "active" {
		return nil, &quota.Error{Message: "公众号当前不可用，请重新检测或重新绑定"}
	}
	if plan.Status != "active" && trigger != "manual" && trigger != "regenerate" {
		reason := plan.PauseReason
		if reason == "" {
			reason = "写作计划已暂停"
		}
		return nil, &quota.Error{Message: reason}
	}
	open, err := st.PlanHasOpen(ctx, q, plan.ID)
	if err != nil {
		return nil, err
	}
	if open {
		return nil, &quota.Error{Message: "这个写作方向正在生成，请稍后再试"}
	}
	if err := quota.ApplyPending(ctx, st, q, user, now); err != nil {
		return nil, err
	}
	day := schedule.ShanghaiDay(now)
	free := false
	var idem string
	var regenID *int64
	if trigger == "regenerate" {
		if regen == nil {
			return nil, &quota.Error{Message: "找不到要重写的文章"}
		}
		free = regen.RegenCount <= 0
		idem = "regen:" + itoa(regen.ID) + ":" + randHex()
		id := regen.ID
		regenID = &id
	} else if trigger == "schedule" {
		slot := now
		if plan.NextRunAt != nil && !plan.NextRunAt.IsZero() {
			slot = plan.NextRunAt.UTC()
		}
		idem = "schedule:" + itoa(plan.ID) + ":" + schedule.Format(slot)
	} else {
		idem = "manual:" + itoa(plan.ID) + ":" + randHex()
	}
	tier, _ := quota.Membership(user, now)
	status := "pending"
	var started *time.Time
	if trigger != "schedule" {
		status = "running"
		t := now
		started = &t
	}
	task := &model.ArticleTask{
		PlanID: plan.ID, UserID: user.ID, WxAccountID: account.ID, RegenOfArticleID: regenID,
		IdempotencyKey: idem, TriggerType: trigger, ScheduleDate: day, RunAt: now, Status: status,
		Priority: quota.Priority(tier, trigger), StartedAt: started, CreatedAt: now,
	}
	if err := st.InsertTask(ctx, q, task); err != nil {
		if planOpenConflict(err) {
			return nil, &quota.Error{Message: "这个写作方向正在生成，请稍后再试"}
		}
		return nil, err
	}
	note := "立即生成"
	if trigger == "schedule" {
		note = "定时生成"
	} else if trigger == "regenerate" {
		note = "重新生成"
	}
	if err := quota.Reserve(ctx, st, q, user, task.ID, now, free, note); err != nil {
		return nil, err
	}
	return task, nil
}

func planOpenConflict(err error) bool {
	if err == nil {
		return false
	}
	if db.ConstraintName(err) == "uq_article_task_plan_open" {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "uq_article_task_plan_open") || strings.Contains(msg, "article_task.plan_id")
}

func randHex() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
