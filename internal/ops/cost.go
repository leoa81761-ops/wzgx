package ops

import "strconv"

// 成本单价和熔断动作存在 app_setting。月上限为 0 时不熔断。
const (
	KeyChatCentsPer1K = "cost_chat_cents_per_1k"
	KeyImageCents     = "cost_image_cents"
	KeySearchCents    = "cost_search_cents"
	KeyMonthCapCents  = "cost_month_cap_cents"
	KeyBreakerAction  = "cost_breaker_action"
	KeyAILabel        = "ai_label"
	KeyLegalVersion   = "legal_version"

	ActionDegrade = "degrade"
	ActionPause   = "pause"
)

type Policy struct {
	ChatCentsPer1K int
	ImageCents     int
	SearchCents    int
	MonthCapCents  int
	Action         string
	AILabel        bool
}

func DefaultPolicy() Policy {
	return Policy{ChatCentsPer1K: 2, ImageCents: 10, SearchCents: 5, MonthCapCents: 0, Action: ActionDegrade}
}

func Normalize(p Policy) Policy {
	base := DefaultPolicy()
	if p.ChatCentsPer1K < 0 {
		p.ChatCentsPer1K = base.ChatCentsPer1K
	}
	if p.ImageCents < 0 {
		p.ImageCents = base.ImageCents
	}
	if p.SearchCents < 0 {
		p.SearchCents = base.SearchCents
	}
	if p.MonthCapCents < 0 {
		p.MonthCapCents = 0
	}
	if p.Action != ActionPause {
		p.Action = ActionDegrade
	}
	return p
}

func LoadPolicy(get func(string) (string, error)) (Policy, error) {
	p := DefaultPolicy()
	var err error
	if p.ChatCentsPer1K, err = settingInt(get, KeyChatCentsPer1K, p.ChatCentsPer1K); err != nil {
		return p, err
	}
	if p.ImageCents, err = settingInt(get, KeyImageCents, p.ImageCents); err != nil {
		return p, err
	}
	if p.SearchCents, err = settingInt(get, KeySearchCents, p.SearchCents); err != nil {
		return p, err
	}
	if p.MonthCapCents, err = settingInt(get, KeyMonthCapCents, p.MonthCapCents); err != nil {
		return p, err
	}
	action, err := get(KeyBreakerAction)
	if err != nil {
		return p, err
	}
	if action != "" {
		p.Action = action
	}
	label, err := get(KeyAILabel)
	if err != nil {
		return p, err
	}
	p.AILabel = label == "1" || label == "true"
	return Normalize(p), nil
}

func settingInt(get func(string) (string, error), key string, fallback int) (int, error) {
	raw, err := get(key)
	if err != nil || raw == "" {
		return fallback, err
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback, nil
	}
	return n, nil
}

func ChatCents(tokens, centsPer1K int) int {
	if tokens <= 0 || centsPer1K <= 0 {
		return 0
	}
	return (tokens*centsPer1K + 999) / 1000
}

// Decide 在本月已发生成本达到上限时返回 degrade 或 pause。上限为 0 表示关闭。
func Decide(monthCents int, p Policy) string {
	p = Normalize(p)
	if p.MonthCapCents <= 0 || monthCents < p.MonthCapCents {
		return ""
	}
	return p.Action
}
