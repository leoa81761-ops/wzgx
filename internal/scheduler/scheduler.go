package scheduler

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/jobs"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/pipeline"
	"github.com/leoa81761-ops/wzgx/internal/publish"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
)

type Pool struct {
	sem chan struct{}
}

func NewPool(n int) *Pool {
	if n < 1 {
		n = 1
	}
	return &Pool{sem: make(chan struct{}, n)}
}

func (p *Pool) TryAcquire() bool {
	select {
	case p.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (p *Pool) Acquire(ctx context.Context) error {
	select {
	case p.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Pool) Release() { <-p.sem }

type Runner struct {
	Deps pipeline.Deps
	Pool *Pool
}

func (r *Runner) ScanOnce(ctx context.Context, now time.Time) ([]int64, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := r.refreshHot(ctx, now); err != nil {
		log.Printf("刷新热点缓存失败: %v", err)
	}
	stamp := schedule.Format(now)
	var claimed []int64
	err := r.Deps.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		cutoff := schedule.Format(now.Add(-time.Duration(r.Deps.Settings.StaleTaskSeconds) * time.Second))
		if err := r.Deps.Store.RequeueStale(ctx, tx, cutoff, stamp); err != nil {
			return err
		}
		if err := r.advanceReferrals(ctx, tx, now); err != nil {
			return err
		}
		if err := r.resumeQuota(ctx, tx, now); err != nil {
			return err
		}
		ids, err := r.Deps.Store.ListActiveUserIDs(ctx, tx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			user, err := r.Deps.Store.GetUser(ctx, tx, id)
			if err != nil {
				return err
			}
			if user == nil {
				continue
			}
			if err := quota.EnforceFreeLimits(ctx, r.Deps.Store, tx, user, now); err != nil {
				return err
			}
		}
		if err := r.prepareTopics(ctx, tx, now); err != nil {
			return err
		}
		plans, err := r.Deps.Store.ListDuePlans(ctx, tx, stamp)
		if err != nil {
			return err
		}
		for i := range plans {
			if err := r.handlePlan(ctx, tx, &plans[i], now); err != nil {
				return err
			}
		}
		for {
			if r.Pool != nil && !r.Pool.TryAcquire() {
				break
			}
			id, ok, err := r.Deps.Store.ClaimOne(ctx, tx, stamp)
			if err != nil {
				if r.Pool != nil {
					r.Pool.Release()
				}
				return err
			}
			if !ok {
				if r.Pool != nil {
					r.Pool.Release()
				}
				break
			}
			claimed = append(claimed, id)
			if r.Pool == nil {
				break
			}
		}
		return nil
	})
	if err != nil {
		for range claimed {
			if r.Pool != nil {
				r.Pool.Release()
			}
		}
		return nil, err
	}
	if err := r.remindExpiring(ctx, now); err != nil {
		log.Printf("到期提醒失败: %v", err)
	}
	var wg sync.WaitGroup
	for _, id := range claimed {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			if r.Pool != nil {
				defer r.Pool.Release()
			}
			pipeline.Execute(ctx, r.Deps, id, false, now)
		}(id)
	}
	wg.Wait()
	publish.Drain(ctx, publish.Deps{
		Store: r.Deps.Store, Settings: r.Deps.Settings, HTTP: r.Deps.HTTP, Tokens: r.Deps.Tokens, Notifier: r.Deps.Notifier,
	}, now)
	cutoff := schedule.Format(now.Add(-30 * 24 * time.Hour))
	if _, err := r.Deps.Store.PurgeArticleBodies(ctx, cutoff); err != nil {
		log.Printf("清理 30 天前的正文失败: %v", err)
	}
	if err := r.pullStats(ctx, now); err != nil {
		log.Printf("拉取认证号数据失败: %v", err)
	}
	return claimed, nil
}

func (r *Runner) handlePlan(ctx context.Context, tx *db.Tx, plan *model.WritingPlan, now time.Time) error {
	user, err := r.Deps.Store.GetUser(ctx, tx, plan.UserID)
	if err != nil {
		return err
	}
	account, err := r.Deps.Store.GetAccount(ctx, tx, plan.WxAccountID)
	if err != nil {
		return err
	}
	if user == nil || account == nil {
		plan.Status = "paused"
		plan.PauseReason = "授权失效"
		return r.Deps.Store.UpdatePlan(ctx, tx, plan)
	}
	if account.BindStatus != "active" {
		plan.Status = "paused"
		if account.BindStatus == "auth_invalid" {
			plan.PauseReason = "授权失效"
		} else {
			plan.PauseReason = "已解绑"
		}
		return r.Deps.Store.UpdatePlan(ctx, tx, plan)
	}
	day := schedule.ShanghaiDay(now)
	open, err := r.Deps.Store.PlanHasOpen(ctx, tx, plan.ID)
	if err != nil {
		return err
	}
	if open {
		return nil
	}
	produced, err := r.Deps.Store.CountPlanTasks(ctx, tx, plan.ID, day, []string{"schedule"})
	if err != nil {
		return err
	}
	if produced >= plan.ArticlesPerDay() {
		return r.advancePlan(ctx, tx, plan, now, produced)
	}
	lockTag := ""
	if confirmActive(user, plan, now) {
		prop, err := r.Deps.Store.OpenProposal(ctx, tx, plan.ID)
		if err != nil {
			return err
		}
		if prop != nil && prop.Status == "pending" && now.Before(prop.ProposedAt.Add(topicDecision)) {
			return nil
		}
		if prop != nil {
			lockTag = prop.TopicTag
		}
	}
	err = tx.Savepoint(ctx, func() error {
		task, err := jobs.Create(ctx, r.Deps.Store, tx, user, plan, account, "schedule", now, nil)
		if err != nil {
			return err
		}
		if lockTag != "" {
			if err := r.Deps.Store.SetTopicLock(ctx, tx, task.ID, lockTag); err != nil {
				return err
			}
		}
		return r.Deps.Store.UseProposal(ctx, tx, plan.ID)
	})
	if err == nil {
		return nil
	}
	if db.IsUnique(err) {
		log.Printf("plan %d already has a task at %s", plan.ID, schedule.Format(now))
		return r.advancePlan(ctx, tx, plan, now, produced+1)
	}
	if qe, ok := err.(*quota.Error); ok {
		if strings.Contains(qe.Message, "额度") {
			plan.Status = "paused"
			plan.PauseReason = "额度已用完"
			return r.Deps.Store.UpdatePlan(ctx, tx, plan)
		}
		log.Printf("skip plan %d: %s", plan.ID, qe.Message)
		return nil
	}
	return err
}

func (r *Runner) resumeQuota(ctx context.Context, tx *db.Tx, now time.Time) error {
	plans, err := r.Deps.Store.ListPausedQuota(ctx, tx)
	if err != nil {
		return err
	}
	for i := range plans {
		user, err := r.Deps.Store.GetUser(ctx, tx, plans[i].UserID)
		if err != nil {
			return err
		}
		if user == nil {
			continue
		}
		reason, err := quota.BlockReason(ctx, r.Deps.Store, tx, user, now)
		if err != nil {
			return err
		}
		if reason != "" {
			continue
		}
		plans[i].Status = "active"
		plans[i].PauseReason = ""
		if plans[i].NextRunAt == nil || plans[i].NextRunAt.After(now) {
			plans[i].NextRunAt = &now
		}
		if err := r.Deps.Store.UpdatePlan(ctx, tx, &plans[i]); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) advancePlan(ctx context.Context, tx *db.Tx, plan *model.WritingPlan, now time.Time, produced int) error {
	next, err := schedule.NextSlot(now, plan.ScheduleType, plan.Weekdays, plan.TimeWindow, sysRNG{}, produced, plan.ArticlesPerDay())
	if err != nil {
		return err
	}
	plan.NextRunAt = &next
	return r.Deps.Store.UpdatePlan(ctx, tx, plan)
}

type sysRNG struct{}

func (sysRNG) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(time.Now().UnixNano() % int64(n))
}
