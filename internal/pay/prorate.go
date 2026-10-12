package pay

import (
	"time"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
)

// UpgradeDue is the price for moving to a higher tier.
// Same billing period keeps the current expiry and charges the price gap for the days left.
// A longer period starts a new term and subtracts the unused value of the current term.
func UpgradeDue(now time.Time, currentTier, currentPeriod string, expires *time.Time, currentPrice, targetPrice int, targetPeriod string, targetRankHigher bool) (cents int, orderPeriod string) {
	if !targetRankHigher || currentTier == "" || currentTier == "free" || expires == nil || !expires.After(now) || currentPrice <= 0 {
		return targetPrice, targetPeriod
	}
	if currentTier == "lifetime" {
		return targetPrice, targetPeriod
	}
	days, periodDays := remainingDays(now, *expires, currentPeriod)
	if targetPeriod == currentPeriod {
		gap := targetPrice - currentPrice
		if gap < 0 {
			gap = 0
		}
		return gap * days / periodDays, "upgrade-keep"
	}
	credit := currentPrice * days / periodDays
	due := targetPrice - credit
	if due < 0 {
		due = 0
	}
	return due, "upgrade-fresh-" + targetPeriod
}

func remainingDays(now, expires time.Time, period string) (days, periodDays int) {
	periodDays = 31
	if period == "year" {
		periodDays = 365
	}
	days = int(expires.Sub(now).Hours() / 24)
	if days < 1 {
		days = 1
	}
	if days > periodDays {
		days = periodDays
	}
	return days, periodDays
}

func RankHigher(current, target string) bool {
	return catalog.TierRank(target) > catalog.TierRank(current)
}
