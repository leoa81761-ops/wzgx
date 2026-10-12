package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/hot"
	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

func (r *Runner) refreshHot(ctx context.Context, now time.Time) error {
	sources, err := r.Deps.Store.ListHotSources(ctx, nil, true)
	if err != nil || len(sources) == 0 {
		return err
	}
	window := 12 * time.Hour
	for _, src := range sources {
		hours := time.Duration(hot.CacheHours(src.ExtraParams)) * time.Hour
		if hours < window {
			window = hours
		}
	}
	if latest, ok, err := r.Deps.Store.LatestHotFetch(ctx, nil); err != nil {
		return err
	} else if ok && now.Sub(latest) < window {
		return nil
	}
	var groups [][]hot.Item
	okSources := 0
	for _, src := range sources {
		key := ""
		if src.APIKeyEncrypted != "" {
			key, err = security.DecryptSecret(r.Deps.Settings.MasterKey, src.APIKeyEncrypted)
			if err != nil {
				log.Printf("热点源 %s 密钥无法读取: %v", src.Name, err)
				continue
			}
		}
		timeout := time.Duration(src.TimeoutSeconds) * time.Second
		if timeout < 3*time.Second || timeout > 20*time.Second {
			timeout = 8 * time.Second
		}
		items, err := hot.Fetch(ctx, r.Deps.HTTP, hot.Source{
			Name: src.Name, Protocol: src.Protocol, BaseURL: src.BaseURL, APIKey: key, Params: src.ExtraParams, Timeout: timeout,
		})
		if err != nil {
			log.Printf("热点源 %s 刷新失败，沿用缓存: %v", src.Name, err)
			continue
		}
		okSources++
		groups = append(groups, items)
	}
	if okSources == 0 {
		return nil
	}
	merged := hot.Merge(groups...)
	if len(merged) == 0 && okSources < len(sources) {
		return nil
	}
	rows := make([]store.HotRow, 0, len(merged))
	for _, item := range merged {
		rows = append(rows, store.HotRow{
			SourceName: item.SourceName, Vertical: item.Vertical, Title: item.Title, Summary: item.Summary,
			SourceLabel: item.Source, URL: item.URL, PublishedOn: item.PublishedOn, Heat: item.Heat, Reports: item.Reports, FetchedAt: now,
		})
	}
	return r.Deps.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		return r.Deps.Store.ReplaceHotItems(ctx, tx, rows)
	})
}
