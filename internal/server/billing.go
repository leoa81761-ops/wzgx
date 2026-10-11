package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/pay"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

func (a *App) payOptions() (sandbox, wx, ali, page bool) {
	if a.Cfg.PaySandbox {
		return true, false, false, false
	}
	if a.Cfg.WeChatPayReady() {
		if _, err := pay.NewWeChat(a.Cfg, a.HTTP); err == nil {
			wx = true
		}
	}
	if a.Cfg.AlipayReady() {
		if client, err := pay.NewAlipay(a.Cfg, a.HTTP); err == nil {
			ali = true
			page = strings.TrimSpace(client.ReturnURL) != ""
		}
	}
	return sandbox, wx, ali, page
}

func (a *App) payMeta() map[string]any {
	sandbox, wx, ali, page := a.payOptions()
	providers := []string{}
	if sandbox {
		providers = append(providers, "sandbox")
	}
	if wx {
		providers = append(providers, "wechat")
	}
	if ali {
		providers = append(providers, "alipay")
	}
	if providers == nil {
		providers = []string{}
	}
	return map[string]any{
		"enabled": len(providers) > 0, "sandbox": sandbox, "providers": providers, "alipay_page": page,
	}
}

type checkoutBody struct {
	Tier     string `json:"tier"`
	Period   string `json:"period"`
	Provider string `json:"provider"`
	Channel  string `json:"channel"`
}

func (a *App) checkout(w http.ResponseWriter, r *http.Request, user *model.User) error {
	var body checkoutBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	sandbox, wx, ali, page := a.payOptions()
	if !sandbox && !wx && !ali {
		provider := pay.Get("manual")
		result, err := provider.Create(r.Context(), pay.Order{UserID: user.ID, Tier: strings.TrimSpace(body.Tier)})
		if err != nil && !errors.Is(err, pay.ErrComingSoon) {
			return err
		}
		message := result.Message
		if message == "" {
			message = "即将开通"
		}
		writeJSON(w, http.StatusNotImplemented, map[string]any{
			"detail": message, "provider": provider.Name(), "status": "coming_soon",
		})
		return nil
	}
	tier := strings.TrimSpace(body.Tier)
	period := strings.TrimSpace(body.Period)
	if tier == "lifetime" && (period == "" || period == "lifetime") {
		period = "once"
	}
	if period == "" {
		period = "month"
	}
	cents, ok := pay.PriceCents(a.Cfg, tier, period)
	if !ok || cents <= 0 {
		return bad("请选择要开通的档位和周期")
	}
	fresh, err := a.Store.GetUser(r.Context(), nil, user.ID)
	if err != nil || fresh == nil {
		return &AppError{Status: 401, Message: "请先登录"}
	}
	effective, _ := quota.Membership(fresh, time.Now().UTC())
	if catalog.TierRank(tier) < catalog.TierRank(effective) {
		return bad("当前档位更高，降级请联系管理员")
	}
	channel := strings.TrimSpace(body.Channel)
	if channel == "" {
		channel = "qr"
	}
	if channel != "qr" && channel != "page" {
		return bad("请选择扫码或电脑网站支付")
	}
	provider := strings.TrimSpace(body.Provider)
	if sandbox {
		provider = "sandbox"
		channel = "qr"
	} else if provider == "" {
		if wx && !ali {
			provider = "wechat"
		} else if ali && !wx {
			provider = "alipay"
		} else {
			return bad("请选择微信支付或支付宝")
		}
	}
	if provider == "wechat" && (!wx || channel == "page") {
		if channel == "page" {
			return bad("微信支付当前只支持扫码")
		}
		return bad("微信支付还没配好")
	}
	if provider == "alipay" && !ali {
		return bad("支付宝还没配好")
	}
	if provider == "alipay" && channel == "page" && !page {
		return bad("电脑网站支付还需要配置 ALIPAY_RETURN_URL")
	}
	if provider != "wechat" && provider != "alipay" && provider != "sandbox" {
		return bad("不支持的支付方式")
	}
	now := time.Now().UTC()
	order := &store.PayOrder{
		UserID: fresh.ID, OutTradeNo: pay.NewOutTradeNo(fresh.ID), Provider: provider, Channel: channel,
		Tier: tier, Period: period, AmountCents: cents, Status: "pending", CreatedAt: now,
	}
	if err := a.Store.InsertPayOrder(r.Context(), nil, order); err != nil {
		return err
	}
	subject := pay.Subject(tier, period)
	var codeURL, payURL string
	switch provider {
	case "sandbox":
		codeURL = "sandbox:" + order.OutTradeNo
	case "wechat":
		client, err := pay.NewWeChat(a.Cfg, a.HTTP)
		if err != nil {
			_ = a.Store.FailPayOrder(r.Context(), nil, order.ID)
			return bad("微信支付配置无法读取")
		}
		codeURL, err = client.CreateNative(r.Context(), order.OutTradeNo, subject, cents)
		if err != nil {
			_ = a.Store.FailPayOrder(r.Context(), nil, order.ID)
			return bad(err.Error())
		}
	case "alipay":
		client, err := pay.NewAlipay(a.Cfg, a.HTTP)
		if err != nil {
			_ = a.Store.FailPayOrder(r.Context(), nil, order.ID)
			return bad("支付宝配置无法读取")
		}
		if channel == "page" {
			payURL, err = client.PagePayURL(order.OutTradeNo, subject, cents)
		} else {
			codeURL, err = client.Precreate(r.Context(), order.OutTradeNo, subject, cents)
		}
		if err != nil {
			_ = a.Store.FailPayOrder(r.Context(), nil, order.ID)
			return bad(err.Error())
		}
	}
	if err := a.Store.SavePayLinks(r.Context(), nil, order.ID, codeURL, payURL); err != nil {
		return err
	}
	message := "请完成支付。到账后会开通档位。"
	if provider == "sandbox" {
		message = "这是模拟支付，不会访问微信或支付宝。"
	} else if provider == "wechat" {
		message = "请使用微信打开下面的收款链接。支付完成后可查看结果。"
	} else if channel == "page" {
		message = "请打开支付链接，在支付宝完成付款。"
	} else {
		message = "请使用支付宝扫描下面的收款链接。"
	}
	writeJSON(w, 200, map[string]any{
		"provider": provider, "status": "pending", "out_trade_no": order.OutTradeNo,
		"code_url": codeURL, "pay_url": payURL, "amount_cents": cents, "tier": tier, "period": period,
		"message": message,
	})
	return nil
}

func (a *App) sandboxComplete(w http.ResponseWriter, r *http.Request, user *model.User) error {
	if !a.Cfg.PaySandbox {
		return &AppError{Status: 404, Message: "模拟支付没有打开"}
	}
	var body struct {
		OutTradeNo string `json:"out_trade_no"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	order, err := a.Store.GetPayOrder(r.Context(), nil, strings.TrimSpace(body.OutTradeNo))
	if err != nil {
		return err
	}
	if order == nil || order.UserID != user.ID {
		return &AppError{Status: 404, Message: "找不到订单"}
	}
	if err := pay.MarkPaid(r.Context(), a.Store, order.OutTradeNo, "sandbox", "sandbox", order.AmountCents, time.Now().UTC()); err != nil {
		return bad(err.Error())
	}
	fresh, _ := a.Store.GetUser(r.Context(), nil, user.ID)
	tier := ""
	if fresh != nil {
		tier = fresh.Tier
	}
	writeJSON(w, 200, map[string]any{"ok": true, "status": "paid", "tier": tier})
	return nil
}

func (a *App) billingOrder(w http.ResponseWriter, r *http.Request, user *model.User) error {
	order, err := a.Store.GetPayOrder(r.Context(), nil, r.PathValue("out_trade_no"))
	if err != nil {
		return err
	}
	if order == nil || order.UserID != user.ID {
		return &AppError{Status: 404, Message: "找不到订单"}
	}
	writeJSON(w, 200, orderView(order))
	return nil
}

func (a *App) wechatNotify(w http.ResponseWriter, r *http.Request) error {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	client, err := pay.NewWeChat(a.Cfg, a.HTTP)
	if err != nil {
		writeJSON(w, 404, map[string]string{"code": "FAIL", "message": "微信支付未配置"})
		return nil
	}
	notice, err := client.ParseNotify(time.Now(), r.Header, body)
	if err != nil {
		writeJSON(w, 401, map[string]string{"code": "FAIL", "message": err.Error()})
		return nil
	}
	if err := a.settlePaid(r.Context(), notice, "wechat"); err != nil {
		writeJSON(w, 400, map[string]string{"code": "FAIL", "message": err.Error()})
		return nil
	}
	writeJSON(w, 200, map[string]string{"code": "SUCCESS", "message": "成功"})
	return nil
}

func (a *App) alipayNotify(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "fail", http.StatusBadRequest)
		return nil
	}
	client, err := pay.NewAlipay(a.Cfg, a.HTTP)
	if err != nil {
		http.Error(w, "fail", http.StatusNotFound)
		return nil
	}
	values := r.PostForm
	if len(values) == 0 {
		values = r.Form
	}
	notice, err := client.ParseNotify(values)
	if err != nil {
		_, _ = w.Write([]byte("fail"))
		return nil
	}
	if err := a.settlePaid(r.Context(), notice, "alipay"); err != nil {
		_, _ = w.Write([]byte("fail"))
		return nil
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("success"))
	return nil
}

func (a *App) settlePaid(ctx context.Context, notice pay.Notice, provider string) error {
	if !notice.Paid {
		return nil
	}
	if notice.OutTradeNo == "" {
		return pay.ErrOrderNotFound
	}
	return pay.MarkPaid(ctx, a.Store, notice.OutTradeNo, provider, notice.TradeNo, notice.AmountCents, time.Now().UTC())
}

func (a *App) adminOrders(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	rows, err := a.Store.ListPayOrders(r.Context(), 100)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		out = append(out, orderView(&rows[i]))
	}
	writeJSON(w, 200, out)
	return nil
}

func (a *App) adminRefundOrder(w http.ResponseWriter, r *http.Request, admin *model.User) error {
	id, err := pathID(r, "id", "找不到订单")
	if err != nil {
		return err
	}
	order, err := a.Store.GetPayOrderByID(r.Context(), nil, id)
	if err != nil {
		return err
	}
	if order == nil {
		return &AppError{Status: 404, Message: "找不到订单"}
	}
	if order.Status == "refunded" {
		writeJSON(w, 200, map[string]any{"ok": true, "status": "refunded", "message": refundHint})
		return nil
	}
	if order.Status != "paid" {
		return bad("只能标记已支付的订单。退款请在微信商户平台或支付宝后台手工操作。")
	}
	now := time.Now().UTC()
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		changed, err := a.Store.RefundPayOrder(r.Context(), tx, id)
		if err != nil {
			return err
		}
		if !changed {
			return bad("只能标记已支付的订单")
		}
		actor := admin.ID
		return a.Store.InsertAudit(r.Context(), tx, model.AuditLog{
			ActorID: &actor, Action: "payment_refund", Target: "order:" + order.OutTradeNo,
			Detail: refundHint, CreatedAt: now,
		})
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true, "status": "refunded", "message": refundHint})
	return nil
}

const refundHint = "已标记退款。请在会员页另行调整档位。退款需在微信商户平台或支付宝后台手工操作。"

func orderView(order *store.PayOrder) map[string]any {
	return map[string]any{
		"id": order.ID, "user_id": order.UserID, "account": order.Account, "out_trade_no": order.OutTradeNo,
		"provider": order.Provider, "channel": order.Channel, "tier": order.Tier, "period": order.Period,
		"amount_cents": order.AmountCents, "status": order.Status, "code_url": order.CodeURL, "pay_url": order.PayURL,
		"provider_trade_no": order.ProviderTradeNo, "created_at": jtimev(order.CreatedAt), "paid_at": jtime(order.PaidAt),
	}
}
