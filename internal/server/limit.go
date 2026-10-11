package server

import (
	"sync"
	"time"
)

// ipWindow 是进程内的固定窗口计数，给未登录投诉用。单机 4C/4G 上足够，不另起存储。
type ipWindow struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
}

func newIPWindow(limit int, window time.Duration) *ipWindow {
	if limit < 1 {
		limit = 1
	}
	if window <= 0 {
		window = time.Hour
	}
	return &ipWindow{limit: limit, window: window, hits: map[string][]time.Time{}}
}

func (w *ipWindow) allow(ip string) bool {
	if w == nil {
		return true
	}
	if ip == "" {
		ip = "unknown"
	}
	now := time.Now()
	cut := now.Add(-w.window)
	w.mu.Lock()
	defer w.mu.Unlock()
	kept := w.hits[ip][:0]
	for _, at := range w.hits[ip] {
		if at.After(cut) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= w.limit {
		w.hits[ip] = kept
		return false
	}
	w.hits[ip] = append(kept, now)
	if len(w.hits) > 2000 {
		for key, times := range w.hits {
			fresh := times[:0]
			for _, at := range times {
				if at.After(cut) {
					fresh = append(fresh, at)
				}
			}
			if len(fresh) == 0 {
				delete(w.hits, key)
			} else {
				w.hits[key] = fresh
			}
		}
	}
	return true
}
