package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
)

type StatRow struct {
	AccountID int64
	Date      string
	Key       string
	Reads     int
	Shares    int
	Favors    int
	Follows   int
	Source    string
	Title     string
	CreatedAt time.Time
}

type OpsReport struct {
	ID         int64
	AccountID  int64
	Period     string
	Summary    string
	Advice     string
	Adjustment string
	Adopted    string
	CreatedAt  time.Time
}

type LayoutCheck struct {
	ID        int64
	LayoutKey string
	Device    string
	Theme     string
	Result    string
	Note      string
	CreatedAt time.Time
}

func (s *Store) ListVerifiedAccounts(ctx context.Context) ([]model.WxAccount, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+accountCols+` FROM wx_account WHERE bind_status = 'active' AND verified = 1 AND secret_encrypted != '' ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAccounts(rows)
}

func (s *Store) HasWeChatStat(ctx context.Context, accountID int64, day string) (bool, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM mp_stats WHERE account_id = ? AND stat_date = ? AND source = 'wechat'`, accountID, day).Scan(&n)
	return n > 0, err
}

func (s *Store) SaveStat(ctx context.Context, q db.Q, row StatRow) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO mp_stats (account_id, stat_date, article_key, reads, shares, favors, follows, source, article_title, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id, stat_date, article_key, source) DO UPDATE SET
		reads = excluded.reads, shares = excluded.shares, favors = excluded.favors, follows = excluded.follows,
		article_title = CASE WHEN excluded.article_title != '' THEN excluded.article_title ELSE mp_stats.article_title END`,
		row.AccountID, row.Date, row.Key, row.Reads, row.Shares, row.Favors, row.Follows, row.Source, row.Title, timeReq(row.CreatedAt))
	return err
}

func (s *Store) ListStats(ctx context.Context, accountID int64) ([]StatRow, error) {
	rows, err := s.DB.Query(ctx, `SELECT stat_date, article_key, reads, shares, favors, follows, source, article_title, created_at FROM mp_stats WHERE account_id = ? ORDER BY stat_date ASC, article_key ASC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatRow
	for rows.Next() {
		var row StatRow
		var created string
		row.AccountID = accountID
		if err := rows.Scan(&row.Date, &row.Key, &row.Reads, &row.Shares, &row.Favors, &row.Follows, &row.Source, &row.Title, &created); err != nil {
			return nil, err
		}
		row.CreatedAt, _ = schedule.Parse(created)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) InsertReport(ctx context.Context, row *OpsReport) error {
	return s.DB.QueryRow(ctx, `INSERT INTO ops_report (account_id, period, summary, advice, adjustment, adopted, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		row.AccountID, row.Period, row.Summary, row.Advice, row.Adjustment, row.Adopted, timeReq(row.CreatedAt)).Scan(&row.ID)
}

func (s *Store) ListReports(ctx context.Context, accountID int64) ([]OpsReport, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, account_id, period, summary, advice, adjustment, adopted, created_at FROM ops_report WHERE account_id = ? ORDER BY id DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OpsReport
	for rows.Next() {
		var row OpsReport
		var created string
		if err := rows.Scan(&row.ID, &row.AccountID, &row.Period, &row.Summary, &row.Advice, &row.Adjustment, &row.Adopted, &created); err != nil {
			return nil, err
		}
		row.CreatedAt, _ = schedule.Parse(created)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) FeedbackTopics(ctx context.Context, accountID int64) (map[string][2]int, error) {
	rows, err := s.DB.Query(ctx, `SELECT a.subtopic, f.kind FROM feedback f JOIN article a ON a.id = f.article_id WHERE a.wx_account_id = ?`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][2]int{}
	for rows.Next() {
		var sub, kind string
		if err := rows.Scan(&sub, &kind); err != nil {
			return nil, err
		}
		pair := out[sub]
		switch kind {
		case "published", "edited_published":
			pair[0]++
		case "discarded":
			pair[1]++
		}
		out[sub] = pair
	}
	return out, rows.Err()
}

func (s *Store) SaveTopicWeight(ctx context.Context, planID int64, subtopic string, weight int, now time.Time) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO topic_weight (plan_id, subtopic, weight, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(plan_id, subtopic) DO UPDATE SET weight = excluded.weight, updated_at = excluded.updated_at`,
		planID, subtopic, weight, timeReq(now))
	return err
}

func (s *Store) ListTopicWeights(ctx context.Context, q db.Q, planID int64) (map[string]int, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT subtopic, weight FROM topic_weight WHERE plan_id = ?`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var sub string
		var weight int
		if err := rows.Scan(&sub, &weight); err != nil {
			return nil, err
		}
		out[sub] = weight
	}
	return out, rows.Err()
}

func (s *Store) SaveTitleBias(ctx context.Context, accountID int64, shape string, now time.Time) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO title_bias (account_id, shape, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(account_id) DO UPDATE SET shape = excluded.shape, updated_at = excluded.updated_at`,
		accountID, shape, timeReq(now))
	return err
}

func (s *Store) GetTitleBias(ctx context.Context, q db.Q, accountID int64) (string, error) {
	if q == nil {
		q = s.DB
	}
	var shape string
	err := q.QueryRow(ctx, `SELECT shape FROM title_bias WHERE account_id = ?`, accountID).Scan(&shape)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return shape, err
}

func (s *Store) ClearTopicWeights(ctx context.Context, planID int64) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM topic_weight WHERE plan_id = ?`, planID)
	return err
}

func (s *Store) InsertLayoutCheck(ctx context.Context, row *LayoutCheck) error {
	return s.DB.QueryRow(ctx, `INSERT INTO layout_check (layout_key, device, theme, result, note, created_at) VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		row.LayoutKey, row.Device, row.Theme, row.Result, row.Note, timeReq(row.CreatedAt)).Scan(&row.ID)
}

func (s *Store) ListLayoutChecks(ctx context.Context, layoutKey string) ([]LayoutCheck, error) {
	query := `SELECT id, layout_key, device, theme, result, note, created_at FROM layout_check`
	var rows *sql.Rows
	var err error
	if layoutKey != "" {
		rows, err = s.DB.Query(ctx, query+` WHERE layout_key = ? ORDER BY id DESC`, layoutKey)
	} else {
		rows, err = s.DB.Query(ctx, query+` ORDER BY id DESC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LayoutCheck
	for rows.Next() {
		var row LayoutCheck
		var created string
		if err := rows.Scan(&row.ID, &row.LayoutKey, &row.Device, &row.Theme, &row.Result, &row.Note, &created); err != nil {
			return nil, err
		}
		row.CreatedAt, _ = schedule.Parse(created)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) LayoutMeasured(ctx context.Context, layoutKey string) (bool, error) {
	var light, dark int
	err := s.DB.QueryRow(ctx, `SELECT
		COALESCE(SUM(CASE WHEN theme = 'light' AND result = 'pass' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN theme = 'dark' AND result = 'pass' THEN 1 ELSE 0 END), 0)
		FROM layout_check WHERE layout_key = ?`, layoutKey).Scan(&light, &dark)
	return light > 0 && dark > 0, err
}

func (s *Store) SaveTemplateOpenID(ctx context.Context, userID int64, openid string, now time.Time) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO tpl_bind (user_id, openid, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET openid = excluded.openid, updated_at = excluded.updated_at`,
		userID, openid, timeReq(now))
	return err
}

func (s *Store) TemplateOpenID(ctx context.Context, userID int64) (string, error) {
	var openid string
	err := s.DB.QueryRow(ctx, `SELECT openid FROM tpl_bind WHERE user_id = ?`, userID).Scan(&openid)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return openid, err
}
