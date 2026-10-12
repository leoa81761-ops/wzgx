package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
)

const articleCols = `id, task_id, user_id, plan_id, wx_account_id, title, alt_titles, summary, body_json, body_html, sources, thumb_media_id, draft_media_id, template_key, template_version, style_key, prompt_version, risk_level, status, failure_reason, delivery_note, regen_count, subtopic, topic_tag, column_type, created_at`

func scanArticle(row interface{ Scan(dest ...any) error }) (*model.Article, error) {
	var a model.Article
	var alt, body, sources sql.NullString
	var created string
	err := row.Scan(&a.ID, &a.TaskID, &a.UserID, &a.PlanID, &a.WxAccountID, &a.Title, &alt, &a.Summary, &body, &a.BodyHTML, &sources, &a.ThumbMediaID, &a.DraftMediaID, &a.TemplateKey, &a.TemplateVersion, &a.StyleKey, &a.PromptVersion, &a.RiskLevel, &a.Status, &a.FailureReason, &a.DeliveryNote, &a.RegenCount, &a.Subtopic, &a.TopicTag, &a.ColumnType, &created)
	if err != nil {
		return nil, err
	}
	a.AltTitles = decodeList(nstr(alt))
	a.BodyJSON = decodeMap(nstr(body))
	a.Sources = decodeList(nstr(sources))
	a.CreatedAt = mustTime(created)
	return &a, nil
}

func (s *Store) GetArticle(ctx context.Context, q db.Q, id int64) (*model.Article, error) {
	if q == nil {
		q = s.DB
	}
	a, err := scanArticle(q.QueryRow(ctx, `SELECT `+articleCols+` FROM article WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}

func (s *Store) GetArticleByTask(ctx context.Context, q db.Q, taskID int64) (*model.Article, error) {
	if q == nil {
		q = s.DB
	}
	a, err := scanArticle(q.QueryRow(ctx, `SELECT `+articleCols+` FROM article WHERE task_id = ? ORDER BY id DESC LIMIT 1`, taskID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}

func (s *Store) ListArticlesByUser(ctx context.Context, userID int64, limit int) ([]model.Article, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+articleCols+` FROM article WHERE user_id = ? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Article
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (s *Store) AccountTitles(ctx context.Context, q db.Q, accountID int64, limit int) ([]string, error) {
	if q == nil {
		q = s.DB
	}
	if limit < 1 {
		limit = 20
	}
	rows, err := q.Query(ctx, `SELECT title FROM article WHERE wx_account_id = ? AND title != '' AND title != '正在生成' ORDER BY id DESC LIMIT ?`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var titles []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return nil, err
		}
		if title != "" {
			titles = append(titles, title)
		}
	}
	return titles, rows.Err()
}

// AccountTopicLines returns recent subtopics and angles for one account, across its plans.
func (s *Store) AccountTopicLines(ctx context.Context, q db.Q, accountID int64, limit int) ([]string, error) {
	if q == nil {
		q = s.DB
	}
	if limit < 1 {
		limit = 20
	}
	rows, err := q.Query(ctx, `SELECT subtopic, topic_tag FROM article WHERE wx_account_id = ? AND (subtopic != '' OR topic_tag != '') ORDER BY id DESC LIMIT ?`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var subtopic, tag string
		if err := rows.Scan(&subtopic, &tag); err != nil {
			return nil, err
		}
		if text := strings.TrimSpace(subtopic); text != "" {
			out = append(out, text)
		}
		parts := strings.Split(tag, "|")
		if len(parts) == 3 {
			if text := strings.TrimSpace(parts[2]); text != "" {
				out = append(out, text)
			}
		}
	}
	return out, rows.Err()
}

func (s *Store) RecentColumnUses(ctx context.Context, q db.Q, accountID int64) ([]string, error) {
	if q == nil {
		q = s.DB
	}
	var since sql.NullString
	if err := q.QueryRow(ctx, `SELECT MIN(created_at) FROM account_column WHERE wx_account_id = ?`, accountID).Scan(&since); err != nil {
		return nil, err
	}
	if !since.Valid || strings.TrimSpace(since.String) == "" {
		return []string{}, nil
	}
	rows, err := q.Query(ctx, `SELECT column_type FROM article WHERE wx_account_id = ? AND column_type != '' AND created_at >= ? AND status IN ('failed', 'held', 'in_draft', 'exported') ORDER BY id ASC`, accountID, strings.TrimSpace(since.String))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if strings.TrimSpace(name) != "" {
			names = append(names, name)
		}
	}
	return names, rows.Err()
}

func (s *Store) RecentTitles(ctx context.Context, q db.Q, planID int64, limit int) ([]string, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT title FROM article WHERE plan_id = ? AND status IN ('in_draft', 'exported') ORDER BY id DESC LIMIT ?`, planID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var titles []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return nil, err
		}
		if title != "" {
			titles = append(titles, title)
		}
	}
	return titles, rows.Err()
}

func (s *Store) InsertArticle(ctx context.Context, q db.Q, a *model.Article) error {
	if q == nil {
		q = s.DB
	}
	return q.QueryRow(ctx, `INSERT INTO article (task_id, user_id, plan_id, wx_account_id, title, alt_titles, summary, body_json, body_html, sources, thumb_media_id, draft_media_id, template_key, template_version, style_key, prompt_version, risk_level, status, failure_reason, delivery_note, regen_count, subtopic, topic_tag, column_type, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		a.TaskID, a.UserID, a.PlanID, a.WxAccountID, a.Title, jsonStr(a.AltTitles), a.Summary, jsonStr(a.BodyJSON), a.BodyHTML, jsonStr(a.Sources), a.ThumbMediaID, a.DraftMediaID, a.TemplateKey, a.TemplateVersion, a.StyleKey, a.PromptVersion, a.RiskLevel, a.Status, a.FailureReason, a.DeliveryNote, a.RegenCount, a.Subtopic, a.TopicTag, a.ColumnType, timeReq(a.CreatedAt),
	).Scan(&a.ID)
}

func (s *Store) UpdateArticle(ctx context.Context, q db.Q, a *model.Article) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE article SET title=?, alt_titles=?, summary=?, body_json=?, body_html=?, sources=?, thumb_media_id=?, draft_media_id=?, template_key=?, template_version=?, style_key=?, prompt_version=?, risk_level=?, status=?, failure_reason=?, delivery_note=?, regen_count=?, subtopic=?, topic_tag=?, column_type=? WHERE id=?`,
		a.Title, jsonStr(a.AltTitles), a.Summary, jsonStr(a.BodyJSON), a.BodyHTML, jsonStr(a.Sources), a.ThumbMediaID, a.DraftMediaID, a.TemplateKey, a.TemplateVersion, a.StyleKey, a.PromptVersion, a.RiskLevel, a.Status, a.FailureReason, a.DeliveryNote, a.RegenCount, a.Subtopic, a.TopicTag, a.ColumnType, a.ID)
	return err
}

func (s *Store) ListArticleBriefs(ctx context.Context, accountID int64) ([]model.Article, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, title, subtopic, draft_media_id FROM article WHERE wx_account_id = ? AND title != '' ORDER BY id DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Article
	for rows.Next() {
		var row model.Article
		if err := rows.Scan(&row.ID, &row.Title, &row.Subtopic, &row.DraftMediaID); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListSiblingCoverage(ctx context.Context, q db.Q, userID, exceptPlanID int64) ([]model.TopicCoverage, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT c.id, c.plan_id, c.subtopic, c.column_type, c.angle, c.topic_tag, c.created_at
		FROM topic_coverage c JOIN writing_plan p ON p.id = c.plan_id
		WHERE p.user_id = ? AND c.plan_id != ? ORDER BY c.id DESC LIMIT 40`, userID, exceptPlanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.TopicCoverage
	for rows.Next() {
		var c model.TopicCoverage
		var created string
		if err := rows.Scan(&c.ID, &c.PlanID, &c.Subtopic, &c.ColumnType, &c.Angle, &c.TopicTag, &created); err != nil {
			return nil, err
		}
		c.CreatedAt = mustTime(created)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) SiblingTitles(ctx context.Context, q db.Q, userID, exceptPlanID int64, limit int) ([]string, error) {
	if q == nil {
		q = s.DB
	}
	if limit < 1 {
		limit = 20
	}
	rows, err := q.Query(ctx, `SELECT a.title FROM article a JOIN writing_plan p ON p.id = a.plan_id
		WHERE p.user_id = ? AND a.plan_id != ? AND a.title != '' AND a.title != '正在生成'
		ORDER BY a.id DESC LIMIT ?`, userID, exceptPlanID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var titles []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return nil, err
		}
		titles = append(titles, title)
	}
	return titles, rows.Err()
}

func (s *Store) ListCoverage(ctx context.Context, q db.Q, planID int64) ([]model.TopicCoverage, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT id, plan_id, subtopic, column_type, angle, topic_tag, created_at FROM topic_coverage WHERE plan_id = ? ORDER BY id ASC`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.TopicCoverage
	for rows.Next() {
		var c model.TopicCoverage
		var created string
		if err := rows.Scan(&c.ID, &c.PlanID, &c.Subtopic, &c.ColumnType, &c.Angle, &c.TopicTag, &created); err != nil {
			return nil, err
		}
		c.CreatedAt = mustTime(created)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) InsertCoverage(ctx context.Context, q db.Q, c *model.TopicCoverage) error {
	if q == nil {
		q = s.DB
	}
	return q.QueryRow(ctx, `INSERT INTO topic_coverage (plan_id, subtopic, column_type, angle, topic_tag, created_at) VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		c.PlanID, c.Subtopic, c.ColumnType, c.Angle, c.TopicTag, timeReq(c.CreatedAt)).Scan(&c.ID)
}

func (s *Store) ReplaceSources(ctx context.Context, q db.Q, articleID int64, sources []model.ArticleSource) error {
	if q == nil {
		q = s.DB
	}
	if _, err := q.Exec(ctx, `DELETE FROM article_source WHERE article_id = ?`, articleID); err != nil {
		return err
	}
	for _, src := range sources {
		if _, err := q.Exec(ctx, `INSERT INTO article_source (article_id, seq, source_type, name, title, published_on, raw_url, display_url, accessible) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			articleID, src.Seq, src.SourceType, src.Name, src.Title, src.PublishedOn, src.RawURL, src.DisplayURL, src.Accessible); err != nil {
			return err
		}
	}
	return nil
}
