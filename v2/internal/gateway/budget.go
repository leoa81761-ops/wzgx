package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Spend caps (circuit breaker), both off unless set: a daily cap per model config, and a
// platform-wide daily cap (platform_setting 'daily_cap'). A config over its cap is skipped and the
// next one in the route is used; when nothing is left, ErrBudget, and the worker keeps jobs waiting.
// Days are China days; spend is the estimated cost recorded per model call.

var ErrBudget = errors.New("今日模型花费已到上限，任务排队等待")

var china = time.FixedZone("CST", 8*3600)

// DayStart is midnight China time for t.
func DayStart(t time.Time) time.Time {
	n := t.In(china)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, china)
}

// PlatformCap returns the platform-wide daily cap (0 = off) and today's spend.
func PlatformCap(ctx context.Context, db *pgxpool.Pool, now time.Time) (cap, spent float64, err error) {
	var raw []byte
	err = db.QueryRow(ctx, `SELECT value FROM platform_setting WHERE key='daily_cap'`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	} else if err == nil {
		json.Unmarshal(raw, &cap)
	}
	if err != nil {
		return
	}
	err = db.QueryRow(ctx, `SELECT COALESCE(sum(cost),0)::float8 FROM model_call WHERE created_at >= $1`, DayStart(now)).Scan(&spent)
	return
}

// PlatformOverCap: the platform-wide cap is set and reached.
func PlatformOverCap(ctx context.Context, db *pgxpool.Pool, now time.Time) (bool, error) {
	cap, spent, err := PlatformCap(ctx, db, now)
	return cap > 0 && spent >= cap, err
}

// configOverCap: the config has a daily cap and today's spend reached it.
func configOverCap(ctx context.Context, db *pgxpool.Pool, id int64, now time.Time) (bool, error) {
	var hit bool
	err := db.QueryRow(ctx, `SELECT c.daily_cap IS NOT NULL AND COALESCE((SELECT sum(cost) FROM model_call m WHERE m.config_id=c.id AND m.created_at >= $2),0) >= c.daily_cap
		FROM service_config c WHERE c.id=$1`, id, DayStart(now)).Scan(&hit)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return hit, err
}
