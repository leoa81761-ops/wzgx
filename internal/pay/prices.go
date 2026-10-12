package pay

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/config"
)

func PriceCents(cfg config.Settings, tier, period string) (int, bool) {
	switch tier + ":" + period {
	case "standard:month":
		return orPrice(cfg.PriceStandardMonth, 3000), true
	case "standard:year":
		return orPrice(cfg.PriceStandardYear, 29900), true
	case "premium:month":
		return orPrice(cfg.PricePremiumMonth, 9900), true
	case "premium:year":
		return orPrice(cfg.PricePremiumYear, 99000), true
	case "lifetime:once":
		return orPrice(cfg.PriceLifetime, 300000), true
	case "pack:pack":
		return orPrice(cfg.PricePack, 1900), true
	default:
		return 0, false
	}
}

func orPrice(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func Subject(tier, period string) string {
	name := catalog.TierLabels[tier]
	if name == "" {
		name = tier
	}
	switch period {
	case "year":
		return "公号智写" + name + "年付"
	case "once":
		return "公号智写" + name
	case "pack":
		return "公号智写加量包"
	default:
		if len(period) >= 4 && period[:4] == "pack" {
			return "公号智写加量包"
		}
		return "公号智写" + name + "月付"
	}
}

func NewOutTradeNo(userID int64) string {
	var buf [6]byte
	_, _ = rand.Read(buf[:])
	s := fmt.Sprintf("p%x%s", userID, hex.EncodeToString(buf[:]))
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}

func CentsToYuan(cents int) string {
	if cents < 0 {
		cents = 0
	}
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

func YuanToCents(raw string) (int, error) {
	raw = trimYuan(raw)
	if raw == "" {
		return 0, fmt.Errorf("金额为空")
	}
	whole, frac := raw, "00"
	if i := indexByte(raw, '.'); i >= 0 {
		whole = raw[:i]
		frac = raw[i+1:]
	}
	if whole == "" {
		whole = "0"
	}
	if len(frac) > 2 {
		frac = frac[:2]
	}
	for len(frac) < 2 {
		frac += "0"
	}
	yuan, err := atoi(whole)
	if err != nil {
		return 0, err
	}
	fen, err := atoi(frac)
	if err != nil {
		return 0, err
	}
	return yuan*100 + fen, nil
}

func trimYuan(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\n' && s[i] != '\r' {
			out = append(out, s[i])
		}
	}
	return string(out)
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func atoi(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("金额不正确")
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("金额不正确")
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, nil
}
