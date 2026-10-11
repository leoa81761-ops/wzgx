package pay

import (
	"context"
	"errors"
)

// ErrComingSoon 表示渠道还没接商户号。接口形状固定，微信支付和支付宝以后实现 Provider 再 Register。
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

// Provider 是支付渠道。不要在实现里写死商户密钥，密钥以后走环境变量或后台配置。
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
