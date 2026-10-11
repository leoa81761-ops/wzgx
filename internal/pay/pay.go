package pay

import (
	"context"
	"errors"
)

// ErrComingSoon 表示还没配齐商户参数。配齐后由服务层直接调用微信支付或支付宝，不再走这个 manual 渠道。
var ErrComingSoon = errors.New("即将开通")

type Order struct {
	UserID      int64
	Tier        string
	AmountLabel string
}

type Result struct {
	Provider string
	Status   string
	Message  string
}

// Provider 是支付渠道。商户密钥只从环境变量读取，不要写进仓库。
type Provider interface {
	Name() string
	Create(ctx context.Context, order Order) (Result, error)
}

type Manual struct{}

func (Manual) Name() string { return "manual" }

func (Manual) Create(context.Context, Order) (Result, error) {
	return Result{Provider: "manual", Status: "coming_soon", Message: "在线支付即将开通。确认收款后由管理员手工开通档位。"}, ErrComingSoon
}

var registry = map[string]Provider{"manual": Manual{}}

func Register(p Provider) {
	if p == nil || p.Name() == "" {
		return
	}
	registry[p.Name()] = p
}

func Get(name string) Provider {
	if name == "" {
		name = "manual"
	}
	return registry[name]
}
