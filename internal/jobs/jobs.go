package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

func Create(ctx context.Context, st *store.Store, q db.Q, user *model.User, plan *model.WritingPlan, account *model.WxAccount, trigger string, now time.Time, regen *model.Article) (*model.ArticleTask, error) {
	if account.BindStatus != "active" {
		return nil, &quota.Error{Message: "公众号当前不可用，请重新检测或重新绑定"}
	}
	if plan.Status != "active" {
		reason := plan.PauseReason
		if reason == "" {
			reason = "写作计划已暂停"
		}
		return nil, &quota.Error{Message: reason}
	}
	running, err := st.UserHasRunning(ctx, q, user.ID)
	if err != nil {
		return nil, err
	}
	if running {
		return nil, &quota.Error{Message: "已有文章正在生成，请稍后再试"}
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
	} else {
		taken, err := st.BatchTaken(ctx, q, account.ID, day)
		if err != nil {
			return nil, err
		}
		if taken {
			return nil, &quota.Error{Message: "该公众号今日已生成一批（每日最多 1 批）"}
		}
		if trigger == "schedule" {
			idem = "schedule:" + itoa(plan.ID) + ":" + day
		} else {
			idem = "manual:" + itoa(plan.ID) + ":" + randHex()
		}
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
