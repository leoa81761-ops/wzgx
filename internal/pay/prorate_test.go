package pay

import (
	"testing"
	"time"
)

func TestUpgradeDueKeepsExpiry(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	expires := now.Add(15 * 24 * time.Hour)
	cents, period := UpgradeDue(now, "standard", "month", &expires, 3000, 9900, "month", true)
	if period != "upgrade-keep" || cents != (9900-3000)*15/31 {
		t.Fatal(cents, period)
	}
	full, kind := UpgradeDue(now, "free", "", nil, 0, 9900, "month", true)
	if kind != "month" || full != 9900 {
		t.Fatal(full, kind)
	}
	year, ykind := UpgradeDue(now, "standard", "month", &expires, 3000, 29900, "year", true)
	credit := 3000 * 15 / 31
	if ykind != "upgrade-fresh-year" || year != 29900-credit {
		t.Fatal(year, ykind, credit)
	}
}
