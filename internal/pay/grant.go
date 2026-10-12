package pay

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

var (
	ErrOrderNotFound = errors.New("找不到订单")
	ErrAmount        = errors.New("支付金额和订单不一致")
	ErrOrderState    = errors.New("订单状态不能入账")
)

// MarkPaid 在同一事务里把订单从 pending 改成 paid，并开通档位。已经 paid 的再次调用不会延长到期时间。
func MarkPaid(ctx context.Context, st *store.Store, outTradeNo, provider, tradeNo string, amountCents int, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	return st.DB.WithTx(ctx, func(tx *db.Tx) error {
		order, err := st.GetPayOrder(ctx, tx, outTradeNo)
		if err != nil {
			return err
		}
		if order == nil {
			return ErrOrderNotFound
		}
		if order.Status == "paid" {
			return nil
		}
		if order.Status != "pending" || order.Provider != provider {
			return ErrOrderState
		}
		if order.AmountCents != amountCents {
			return ErrAmount
		}
		changed, err := st.FinishPayOrder(ctx, tx, outTradeNo, tradeNo, now)
		if err != nil {
			return err
		}
		if !changed {
			again, err := st.GetPayOrder(ctx, tx, outTradeNo)
			if err != nil {
				return err
			}
			if again != nil && again.Status == "paid" {
				return nil
			}
			return ErrOrderState
		}
		return grantTier(ctx, st, tx, order, now)
	})
}

func grantTier(ctx context.Context, st *store.Store, tx *db.Tx, order *store.PayOrder, now time.Time) error {
	user, err := st.GetUser(ctx, tx, order.UserID)
	if err != nil {
		return err
	}
	if user == nil {
		return errors.New("找不到用户")
	}
	note := "支付订单 " + order.OutTradeNo
	detail := "支付入账 " + order.OutTradeNo
	if strings.HasPrefix(order.Period, "pack") || order.Tier == "pack" {
		articles := 10
		if i := strings.Index(order.Period, ":"); i >= 0 {
			if n, err := strconv.Atoi(order.Period[i+1:]); err == nil && n > 0 {
				articles = n
			}
		}
		if err := st.InsertPack(ctx, tx, user.ID, order.OutTradeNo, articles, now.Add(90*24*time.Hour), now); err != nil {
			return err
		}
		actor := user.ID
		return st.InsertAudit(ctx, tx, model.AuditLog{
			ActorID: &actor, Action: "payment_paid", Target: "order:" + order.OutTradeNo,
			Detail: detail + "，加量包 " + strconv.Itoa(articles) + " 篇，90 天内有效", CreatedAt: now,
		})
	}
	if order.Period == "upgrade-keep" || strings.HasPrefix(order.Period, "upgrade-fresh") {
		return grantUpgrade(ctx, st, tx, user, order, note, detail, now)
	}
	if catalog.TierRank(order.Tier) < catalog.TierRank(user.Tier) {
		detail += "。当前档位更高，没有自动降级，请在会员页另行处理"
		actor := user.ID
		return st.InsertAudit(ctx, tx, model.AuditLog{
			ActorID: &actor, Action: "payment_paid", Target: "order:" + order.OutTradeNo, Detail: detail, CreatedAt: now,
		})
	}
	var expires *time.Time
	if order.Tier == "lifetime" {
		user.Tier = "lifetime"
		user.TierExpiresAt = nil
		if user.TierStartedAt == nil {
			started := now
			user.TierStartedAt = &started
		}
		detail += "，档位为买断版"
	} else {
		base := now
		if user.Tier == order.Tier && user.TierExpiresAt != nil {
			current := schedule.AsUTC(*user.TierExpiresAt)
			if current.After(base) {
				base = current
			}
		}
		next := base.Add(31 * 24 * time.Hour)
		if order.Period == "year" {
			next = base.Add(365 * 24 * time.Hour)
		}
		expires = &next
		user.Tier = order.Tier
		user.TierExpiresAt = expires
		detail += "，档位为" + catalog.TierLabels[order.Tier]
	}
	if err := st.UpdateUser(ctx, tx, user); err != nil {
		return err
	}
	if err := st.ClearPendingTier(ctx, tx, user.ID); err != nil {
		return err
	}
	exp := ""
	if expires != nil {
		exp = schedule.Format(expires.UTC())
	}
	if err := st.InsertPaymentEntitlement(ctx, tx, user.ID, order.Tier, note, exp, schedule.Format(now)); err != nil {
		return err
	}
	actor := user.ID
	return st.InsertAudit(ctx, tx, model.AuditLog{
		ActorID: &actor, Action: "payment_paid", Target: "order:" + order.OutTradeNo, Detail: detail, CreatedAt: now,
	})
}

func grantUpgrade(ctx context.Context, st *store.Store, tx *db.Tx, user *model.User, order *store.PayOrder, note, detail string, now time.Time) error {
	var expires *time.Time
	if order.Period == "upgrade-keep" && order.Tier != "lifetime" {
		expires = user.TierExpiresAt
		user.Tier = order.Tier
		detail += "，按剩余天数补差，到期时间不变"
	} else if order.Tier == "lifetime" || strings.HasSuffix(order.Period, "once") {
		user.Tier = "lifetime"
		user.TierExpiresAt = nil
		if user.TierStartedAt == nil {
			started := now
			user.TierStartedAt = &started
		}
		detail += "，档位为买断版"
	} else {
		next := now.Add(31 * 24 * time.Hour)
		if strings.HasSuffix(order.Period, "year") {
			next = now.Add(365 * 24 * time.Hour)
		}
		expires = &next
		user.Tier = order.Tier
		user.TierExpiresAt = expires
		detail += "，已扣未使用天数后开通" + catalog.TierLabels[order.Tier]
	}
	if order.Period == "upgrade-keep" {
		user.TierExpiresAt = expires
	}
	if err := st.UpdateUser(ctx, tx, user); err != nil {
		return err
	}
	if err := st.ClearPendingTier(ctx, tx, user.ID); err != nil {
		return err
	}
	exp := ""
	if user.TierExpiresAt != nil {
		exp = schedule.Format(user.TierExpiresAt.UTC())
	}
	if err := st.InsertPaymentEntitlement(ctx, tx, user.ID, order.Tier, note, exp, schedule.Format(now)); err != nil {
		return err
	}
	actor := user.ID
	return st.InsertAudit(ctx, tx, model.AuditLog{
		ActorID: &actor, Action: "payment_paid", Target: "order:" + order.OutTradeNo, Detail: detail, CreatedAt: now,
	})
}
