package schedule

import (
	"fmt"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	_ "time/tzdata"
)

const Stamp = "2006-01-02T15:04:05Z"

var Shanghai *time.Location

func init() {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(err)
	}
	Shanghai = loc
}

func Format(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(Stamp)
}

func Parse(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(Stamp, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

func AsUTC(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC()
}

func ShanghaiNow(now time.Time) time.Time {
	if now.IsZero() {
		now = time.Now()
	}
	return now.UTC().In(Shanghai)
}

func ShanghaiDay(now time.Time) string {
	return ShanghaiNow(now).Format("2006-01-02")
}

func TomorrowStart(now time.Time) time.Time {
	local := ShanghaiNow(now).AddDate(0, 0, 1)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, Shanghai)
	return start.UTC()
}

func WindowEnd(dayKey, windowKey string) (time.Time, error) {
	_, endH, ok := catalog.WindowHours(windowKey)
	if !ok {
		return time.Time{}, fmt.Errorf("未知时段")
	}
	day, err := time.ParseInLocation("2006-01-02", dayKey, Shanghai)
	if err != nil {
		return time.Time{}, err
	}
	local := time.Date(day.Year(), day.Month(), day.Day(), endH, 0, 0, 0, Shanghai)
	return local.UTC(), nil
}

type RNG interface {
	Intn(n int) int
}

type ZeroRNG struct{}

func (ZeroRNG) Intn(int) int { return 0 }

func ISOWeekday(t time.Time) int {
	d := t.Weekday()
	if d == time.Sunday {
		return 7
	}
	return int(d)
}

func ComputeNextRun(after time.Time, scheduleType string, weekdays []int, windowKey string, rng RNG) (time.Time, error) {
	startH, endH, ok := catalog.WindowHours(windowKey)
	if !ok {
		return time.Time{}, fmt.Errorf("未知时段")
	}
	if rng == nil {
		rng = ZeroRNG{}
	}
	afterSH := ShanghaiNow(after)
	allowed := map[int]bool{}
	for _, day := range weekdays {
		allowed[day] = true
	}
	base := time.Date(afterSH.Year(), afterSH.Month(), afterSH.Day(), 0, 0, 0, 0, Shanghai)
	for offset := 0; offset < 28; offset++ {
		day := base.AddDate(0, 0, offset)
		if scheduleType == "weekly" && !allowed[ISOWeekday(day)] {
			continue
		}
		windowStart := time.Date(day.Year(), day.Month(), day.Day(), startH, 0, 0, 0, Shanghai)
		windowEnd := time.Date(day.Year(), day.Month(), day.Day(), endH, 0, 0, 0, Shanghai)
		sameDay := day.Year() == afterSH.Year() && day.Month() == afterSH.Month() && day.Day() == afterSH.Day()
		if sameDay && !afterSH.Before(windowEnd) {
			continue
		}
		earliest := windowStart
		if sameDay && afterSH.After(windowStart) {
			earliest = afterSH.Add(time.Minute).Truncate(time.Minute)
		}
		if !earliest.Before(windowEnd) {
			continue
		}
		minutes := int(windowEnd.Sub(earliest).Seconds()) / 60
		if minutes <= 0 {
			continue
		}
		pick := earliest.Add(time.Duration(rng.Intn(minutes)) * time.Minute)
		return pick.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("未来四周没有可执行时段")
}

func ShouldRetry(triggerType string, retryCount int, now, windowEnd time.Time) bool {
	return triggerType == "schedule" && retryCount < 2 && AsUTC(now).Before(AsUTC(windowEnd))
}
