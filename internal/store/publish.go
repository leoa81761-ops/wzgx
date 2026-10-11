package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
)

type PlanPublish struct {
	PlanID       int64
	Enabled      bool
	DelayMinutes int
	ConsentedAt  *time.Time
	UpdatedAt    time.Time
}

type ArticlePublish struct {
	ArticleID   int64
	Status      string
	PublishID   string
	ArticleURL  string
	ScheduledAt time.Time
	SubmittedAt *time.Time
	Note        string
	Polls       int
}

func (s *Store) GetPlanPublish(ctx context.Context, q db.Q, planID int64) (PlanPublish, error) {
	if q == nil {
		q = s.DB
	}
	var row PlanPublish
	var enabled int
	var consented, updated sql.NullString
	err := q.QueryRow(ctx, `SELECT plan_id, enabled, delay_minutes, consented_at, updated_at FROM plan_publish WHERE plan_id = ?`, planID).
		Scan(&row.PlanID, &enabled, &row.DelayMinutes, &consented, &updated)
	if err == sql.ErrNoRows {
		return PlanPublish{PlanID: planID, DelayMinutes: 30}, nil
	}
	if err != nil {
		return PlanPublish{}, err
	}
	row.Enabled = enabled != 0
	row.ConsentedAt = parsePtr(consented)
	row.UpdatedAt = mustTime(nstr(updated))
	return row, nil
}

func (s *Store) SavePlanPublish(ctx context.Context, q db.Q, row PlanPublish) error {
	if q == nil {
		q = s.DB
	}
	if row.DelayMinutes < 0 {
		row.DelayMinutes = 0
	}
	if row.DelayMinutes > 180 {
		row.DelayMinutes = 180
	}
	_, err := q.Exec(ctx, `INSERT INTO plan_publish (plan_id, enabled, delay_minutes, consented_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(plan_id) DO UPDATE SET enabled = excluded.enabled, delay_minutes = excluded.delay_minutes, consented_at = excluded.consented_at, updated_at = excluded.updated_at`,
		row.PlanID, bint(row.Enabled), row.DelayMinutes, timeArg(row.ConsentedAt), timeReq(row.UpdatedAt))
	return err
}

func (s *Store) GetArticlePublish(ctx context.Context, q db.Q, articleID int64) (ArticlePublish, error) {
	if q == nil {
		q = s.DB
	}
	var row ArticlePublish
	var scheduled, submitted, note sql.NullString
	err := q.QueryRow(ctx, `SELECT article_id, status, publish_id, article_url, scheduled_at, submitted_at, note, polls FROM article_publish WHERE article_id = ?`, articleID).
		Scan(&row.ArticleID, &row.Status, &row.PublishID, &row.ArticleURL, &scheduled, &submitted, &note, &row.Polls)
	if err == sql.ErrNoRows {
		return ArticlePublish{}, nil
	}
	if err != nil {
		return ArticlePublish{}, err
	}
	row.ScheduledAt = mustTime(nstr(scheduled))
	row.SubmittedAt = parsePtr(submitted)
	row.Note = nstr(note)
	return row, nil
}

func (s *Store) SaveArticlePublish(ctx context.Context, q db.Q, row ArticlePublish) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO article_publish (article_id, status, publish_id, article_url, scheduled_at, submitted_at, note, polls)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(article_id) DO UPDATE SET status = excluded.status, publish_id = excluded.publish_id, article_url = excluded.article_url, scheduled_at = excluded.scheduled_at, submitted_at = excluded.submitted_at, note = excluded.note, polls = excluded.polls`,
		row.ArticleID, row.Status, row.PublishID, row.ArticleURL, timeReq(row.ScheduledAt), timeArg(row.SubmittedAt), row.Note, row.Polls)
	return err
}

func (s *Store) ListDuePublishes(ctx context.Context, q db.Q, now time.Time, limit int) ([]int64, error) {
	if q == nil {
		q = s.DB
	}
	if limit < 1 {
		limit = 20
	}
	rows, err := q.Query(ctx, `SELECT article_id FROM article_publish WHERE status IN ('publish_wait', 'publishing') AND scheduled_at <= ? ORDER BY scheduled_at ASC LIMIT ?`, schedule.Format(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ConsumePublishSlot 在未达每日上限时加 1。返回 false 表示已经到顶，不增加计数。
func (s *Store) ConsumePublishSlot(ctx context.Context, q db.Q, accountID int64, day string, cap int) (bool, error) {
	if q == nil {
		q = s.DB
	}
	if cap < 1 {
		cap = 1
	}
	var calls int
	err := q.QueryRow(ctx, `SELECT calls FROM publish_usage WHERE wx_account_id = ? AND day_key = ?`, accountID, day).Scan(&calls)
	if err == sql.ErrNoRows {
		_, err = q.Exec(ctx, `INSERT INTO publish_usage (wx_account_id, day_key, calls) VALUES (?, ?, 1)`, accountID, day)
		return err == nil, err
	}
	if err != nil {
		return false, err
	}
	if calls >= cap {
		return false, nil
	}
	_, err = q.Exec(ctx, `UPDATE publish_usage SET calls = calls + 1 WHERE wx_account_id = ? AND day_key = ?`, accountID, day)
	return err == nil, err
}
