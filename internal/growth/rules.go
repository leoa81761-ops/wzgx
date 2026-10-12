package growth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
)

const IPBurstCount = 3

const (
	StatusPending = "pending"
	StatusValid   = "valid"
	StatusRevoked = "revoked"
	StatusFrozen  = "frozen"
)

type Rules struct {
	RewardPer   int
	RewardCap   int
	MonthMax    int
	DailyCap    int
	ObserveDays int
	DraftsNeed  int
}

func DefaultRules() Rules {
	return Rules{RewardPer: 2, RewardCap: 20, MonthMax: 30, DailyCap: 3, ObserveDays: 7, DraftsNeed: 3}
}

func Normalize(r Rules) Rules {
	base := DefaultRules()
	if r.RewardPer < 0 {
		r.RewardPer = base.RewardPer
	}
	if r.RewardCap < 0 {
		r.RewardCap = base.RewardCap
	}
	if r.MonthMax < 1 {
		r.MonthMax = base.MonthMax
	}
	if r.DailyCap < 1 {
		r.DailyCap = base.DailyCap
	}
	if r.ObserveDays < 0 {
		r.ObserveDays = base.ObserveDays
	}
	if r.DraftsNeed < 1 {
		r.DraftsNeed = base.DraftsNeed
	}
	return r
}

func RulesFromSettings(get func(string) (string, error)) (Rules, error) {
	r := DefaultRules()
	fields := []struct {
		key string
		dst *int
	}{
		{"invite_reward_per", &r.RewardPer},
		{"invite_reward_cap", &r.RewardCap},
		{"invite_month_max", &r.MonthMax},
		{"invite_daily_cap", &r.DailyCap},
		{"invite_observe_days", &r.ObserveDays},
		{"invite_drafts_need", &r.DraftsNeed},
	}
	for _, field := range fields {
		raw, err := get(field.key)
		if err != nil {
			return Rules{}, err
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			continue
		}
		*field.dst = n
	}
	return Normalize(r), nil
}

func Bonus(validCount int, baseQuota int, tier string, rules Rules) int {
	rules = Normalize(rules)
	if tier != "free" || validCount <= 0 || rules.RewardPer == 0 {
		return 0
	}
	bonus := validCount * rules.RewardPer
	if bonus > rules.RewardCap {
		bonus = rules.RewardCap
	}
	if baseQuota+bonus > rules.MonthMax {
		bonus = rules.MonthMax - baseQuota
	}
	if bonus < 0 {
		bonus = 0
	}
	return bonus
}

// Classify 在注册时给出邀请的初始状态。观察期和成稿数还没满足时保持 pending。
func Classify(virtualPhone, sameDevice, ipBurst bool) (status, reason string) {
	if virtualPhone {
		return StatusRevoked, "虚拟号"
	}
	if sameDevice {
		return StatusFrozen, "同设备"
	}
	if ipBurst {
		return StatusFrozen, "同 IP 段集中注册"
	}
	return StatusPending, ""
}

// Advance 在观察期之后更新状态。frozen 和 revoked 保持不变，除非管理员另行改。
func Advance(status string, drafts int, observeDone, unbound, inactive bool, validToday, dailyCap, draftsNeed int) (string, string) {
	if status == StatusRevoked || status == StatusFrozen || status == StatusValid {
		return status, ""
	}
	if inactive {
		return StatusRevoked, "账号已停用"
	}
	if unbound {
		return StatusRevoked, "公众号已解绑"
	}
	if drafts < draftsNeed || !observeDone {
		return StatusPending, ""
	}
	if validToday >= dailyCap {
		return StatusFrozen, "当日有效邀请已达上限"
	}
	return StatusValid, ""
}

func VirtualPhone(phone string) bool {
	phone = strings.TrimSpace(phone)
	for _, prefix := range []string{"170", "171", "165", "167", "162"} {
		if strings.HasPrefix(phone, prefix) {
			return true
		}
	}
	return false
}

func DeviceHash(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func IPPrefix(raw string) string {
	host := strings.TrimSpace(raw)
	if host == "" {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return fmt.Sprintf("%d.%d.%d", v4[0], v4[1], v4[2])
		}
		return ip.Mask(net.CIDRMask(64, 128)).String()
	}
	parts := strings.Split(host, ".")
	if len(parts) == 4 {
		return parts[0] + "." + parts[1] + "." + parts[2]
	}
	return host
}
