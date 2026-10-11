package server

import (
	"net/http"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/publish"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

func (a *App) setPlanPublish(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "plan_id", "找不到写作计划")
	if err != nil {
		return err
	}
	var body struct {
		Enabled      *bool `json:"enabled"`
		DelayMinutes *int  `json:"delay_minutes"`
		Consent      bool  `json:"consent"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if body.Enabled == nil {
		return bad("请选择开启或关闭自动发布")
	}
	delay := 30
	if body.DelayMinutes != nil {
		delay = *body.DelayMinutes
	}
	if delay < 0 || delay > 180 {
		return bad("审阅窗口请填 0 到 180 分钟")
	}
	now := time.Now().UTC()
	var plan *model.WritingPlan
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		var err error
		plan, err = a.ownPlan(r.Context(), tx, user.ID, id)
		if err != nil {
			return err
		}
		fresh, err := a.Store.GetUser(r.Context(), tx, user.ID)
		if err != nil {
			return err
		}
		if fresh != nil {
			if err := quota.ApplyPending(r.Context(), a.Store, tx, fresh, now); err != nil {
				return err
			}
		}
		account, err := a.Store.GetAccount(r.Context(), tx, plan.WxAccountID)
		if err != nil {
			return err
		}
		current, err := a.Store.GetPlanPublish(r.Context(), tx, plan.ID)
		if err != nil {
			return err
		}
		if !*body.Enabled {
			current.Enabled = false
			current.UpdatedAt = now
			if body.DelayMinutes != nil {
				current.DelayMinutes = delay
			}
			return a.Store.SavePlanPublish(r.Context(), tx, current)
		}
		if !body.Consent {
			return bad("开启前请勾选内容责任确认")
		}
		ready, reason := publish.Ready(fresh, account, now)
		if !ready {
			if reason == "" {
				reason = "当前不能开启自动发布"
			}
			return bad(reason)
		}
		if err := a.Store.InsertConsent(r.Context(), tx, &model.Consent{
			UserID: user.ID, DocType: "auto_publish", Version: catalog.LegalVersion, ConsentItem: "enable",
			IP: clientIP(r), UserAgent: userAgent(r), CreatedAt: now,
		}); err != nil {
			return err
		}
		consented := now
		return a.Store.SavePlanPublish(r.Context(), tx, store.PlanPublish{
			PlanID: plan.ID, Enabled: true, DelayMinutes: delay, ConsentedAt: &consented, UpdatedAt: now,
		})
	})
	if err != nil {
		return err
	}
	view, err := a.planView(r.Context(), plan)
	if err != nil {
		return err
	}
	view["auto_publish_note"] = "已保存。只有低风险稿会在审阅窗口结束后提交。失败或草稿不在时，稿件留在草稿箱。"
	writeJSON(w, 200, view)
	return nil
}
