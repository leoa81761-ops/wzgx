package schedule

import (
	"testing"
	"time"
)

func TestNextRunSpreadsInsideWindow(t *testing.T) {
	morning := time.Date(2026, 10, 12, 8, 0, 0, 0, Shanghai)
	slot, err := ComputeNextRun(morning, "daily", nil, "morning", ZeroRNG{})
	if err != nil {
		t.Fatal(err)
	}
	if got := slot.In(Shanghai); !got.Equal(time.Date(2026, 10, 12, 9, 0, 0, 0, Shanghai)) {
		t.Fatal(got)
	}
	inside := time.Date(2026, 10, 12, 10, 30, 0, 0, Shanghai)
	slot, err = ComputeNextRun(inside, "daily", nil, "morning", ZeroRNG{})
	if err != nil {
		t.Fatal(err)
	}
	if got := slot.In(Shanghai); !got.Equal(time.Date(2026, 10, 12, 10, 31, 0, 0, Shanghai)) {
		t.Fatal(got)
	}
	after := time.Date(2026, 10, 12, 13, 0, 0, 0, Shanghai)
	slot, err = ComputeNextRun(after, "daily", nil, "morning", ZeroRNG{})
	if err != nil {
		t.Fatal(err)
	}
	if got := slot.In(Shanghai); !got.Equal(time.Date(2026, 10, 13, 9, 0, 0, 0, Shanghai)) {
		t.Fatal(got)
	}
	sunday := time.Date(2026, 10, 11, 10, 0, 0, 0, Shanghai)
	slot, err = ComputeNextRun(sunday, "weekly", []int{1}, "morning", ZeroRNG{})
	if err != nil {
		t.Fatal(err)
	}
	if got := slot.In(Shanghai); !got.Equal(time.Date(2026, 10, 12, 9, 0, 0, 0, Shanghai)) || ISOWeekday(got) != 1 {
		t.Fatal(got)
	}
	night := time.Date(2026, 10, 12, 22, 0, 0, 0, Shanghai)
	slot, err = ComputeNextRun(night, "daily", nil, "night", ZeroRNG{})
	if err != nil {
		t.Fatal(err)
	}
	if got := slot.In(Shanghai); !got.Equal(time.Date(2026, 10, 13, 0, 0, 0, 0, Shanghai)) {
		t.Fatal(got)
	}
}

func TestRetryOnlyInsideWindow(t *testing.T) {
	end := time.Date(2026, 10, 13, 6, 0, 0, 0, time.UTC)
	if !ShouldRetry("schedule", 0, time.Date(2026, 10, 13, 5, 0, 0, 0, time.UTC), end) {
		t.Fatal("inside window should retry")
	}
	if ShouldRetry("schedule", 2, time.Date(2026, 10, 13, 5, 0, 0, 0, time.UTC), end) {
		t.Fatal("retry count 2 should stop")
	}
	if ShouldRetry("manual", 0, time.Date(2026, 10, 13, 5, 0, 0, 0, time.UTC), end) {
		t.Fatal("manual should not retry")
	}
	if ShouldRetry("schedule", 0, time.Date(2026, 10, 13, 7, 0, 0, 0, time.UTC), end) {
		t.Fatal("after window should not retry")
	}
}
