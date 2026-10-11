package scheduler

import (
	"context"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/jobs"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/pipeline"
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
	stamp := schedule.Format(now)
	var claimed []int64
	err := r.Deps.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		cutoff := schedule.Format(now.Add(-time.Duration(r.Deps.Settings.StaleTaskSeconds) * time.Second))
		if err := r.Deps.Store.RequeueStale(ctx, tx, cutoff, stamp); err != nil {
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
		plans, err := r.Deps.Store.ListDuePlans(ctx, tx, stamp)
		if err != nil {
			return err
		}
		grouped := map[int64][]model.WritingPlan{}
		for _, plan := range plans {
			grouped[plan.WxAccountID] = append(grouped[plan.WxAccountID], plan)
		}
		for _, group := range grouped {
			day := schedule.ShanghaiDay(now)
			taken, err := r.Deps.Store.BatchTaken(ctx, tx, group[0].WxAccountID, day)
			if err != nil {
				return err
			}
			if taken {
				for i := range group {
					if err := r.bumpTomorrow(ctx, tx, &group[i], now); err != nil {
						return err
					}
				}
				continue
			}
			chosen := group[hashDay(day)%len(group)]
			if err := r.handlePlan(ctx, tx, &chosen, now); err != nil {
				return err
			}
			for i := range group {
				if group[i].ID == chosen.ID {
					continue
				}
				if err := r.bumpTomorrow(ctx, tx, &group[i], now); err != nil {
					return err
				}
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
	existing, err := r.Deps.Store.FindScheduleTask(ctx, tx, plan.ID, day)
	if err != nil {
		return err
	}
	if existing != nil {
		return r.advanceExisting(ctx, tx, plan, existing, now)
	}
	taken, err := r.Deps.Store.BatchTaken(ctx, tx, account.ID, day)
	if err != nil {
		return err
	}
	if taken {
		return r.bumpTomorrow(ctx, tx, plan, now)
	}
	err = tx.Savepoint(ctx, func() error {
		_, err := jobs.Create(ctx, r.Deps.Store, tx, user, plan, account, "schedule", now, nil)
		return err
	})
	if err == nil {
		return nil
	}
	if db.IsUnique(err) {
		log.Printf("plan %d already has a task for %s", plan.ID, day)
		return nil
	}
	if qe, ok := err.(*quota.Error); ok {
		if strings.Contains(qe.Message, "额度") {
			plan.Status = "paused"
			plan.PauseReason = "额度已用完"
			return r.Deps.Store.UpdatePlan(ctx, tx, plan)
		}
		if strings.Contains(qe.Message, "已生成一批") {
			return r.bumpTomorrow(ctx, tx, plan, now)
		}
		log.Printf("skip plan %d: %s", plan.ID, qe.Message)
		return nil
	}
	return err
}

func (r *Runner) advanceExisting(ctx context.Context, tx *db.Tx, plan *model.WritingPlan, task *model.ArticleTask, now time.Time) error {
	switch task.Status {
	case "success":
		return r.bumpTomorrow(ctx, tx, plan, now)
	case "pending", "running":
		return nil
	case "failed":
		end, err := schedule.WindowEnd(task.ScheduleDate, plan.TimeWindow)
		if err == nil && schedule.ShouldRetry(task.TriggerType, task.RetryCount, now, end) {
			task.Status = "pending"
			task.RetryCount++
			task.RunAt = now
			return r.Deps.Store.UpdateTask(ctx, tx, task)
		}
		task.Status = "abandoned"
		if err := r.Deps.Store.UpdateTask(ctx, tx, task); err != nil {
			return err
		}
		if err := quota.Release(ctx, r.Deps.Store, tx, task.ID, "超出时段放弃"); err != nil {
			return err
		}
		return r.bumpTomorrow(ctx, tx, plan, now)
	case "abandoned":
		return r.bumpTomorrow(ctx, tx, plan, now)
	default:
		return nil
	}
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

func (r *Runner) bumpTomorrow(ctx context.Context, tx *db.Tx, plan *model.WritingPlan, now time.Time) error {
	next, err := schedule.ComputeNextRun(schedule.TomorrowStart(now), plan.ScheduleType, plan.Weekdays, plan.TimeWindow, sysRNG{})
	if err != nil {
		return err
	}
	plan.NextRunAt = &next
	return r.Deps.Store.UpdatePlan(ctx, tx, plan)
}

func hashDay(day string) int {
	n, _ := strconv.Atoi(strings.ReplaceAll(day, "-", ""))
	if n < 0 {
		return -n
	}
	return n
}

type sysRNG struct{}

func (sysRNG) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(time.Now().UnixNano() % int64(n))
}
