package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
)

type PlanExtra struct {
	PlanID         int64
	Opening        string
	Ending         string
	ColumnName     string
	ColumnStyle    string
	AssetID        int64
	ProfileID      int64
	Keywords       string
	Audience       string
	BannedWords    string
	VerticalKey    string
	TopicMode      string
	ImageMode      string
	Tone           string
	PersonaColumns string
	Signatures     string
	Taboos         string
}

type StyleProfile struct {
	ID        int64
	UserID    int64
	Name      string
	Features  string
	CreatedAt time.Time
}

type PromptVariant struct {
	ID      int64
	Stage   string
	Label   string
	Body    string
	Uses    int
	Adopted int
}

type UserAsset struct {
	ID          int64
	UserID      int64
	Name        string
	Mime        string
	LicenseNote string
	Data        []byte
	CreatedAt   time.Time
}

func (s *Store) SavePlanExtra(ctx context.Context, q db.Q, row PlanExtra) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO plan_extra (plan_id, opening, ending, column_name, column_style, asset_id, profile_id, keywords, audience, banned_words, vertical_key, topic_mode, image_mode, tone, persona_columns, signatures, taboos) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(plan_id) DO UPDATE SET opening = excluded.opening, ending = excluded.ending, column_name = excluded.column_name, column_style = excluded.column_style, asset_id = excluded.asset_id, profile_id = excluded.profile_id, keywords = excluded.keywords, audience = excluded.audience, banned_words = excluded.banned_words, vertical_key = excluded.vertical_key, topic_mode = excluded.topic_mode, image_mode = excluded.image_mode, tone = excluded.tone, persona_columns = excluded.persona_columns, signatures = excluded.signatures, taboos = excluded.taboos`,
		row.PlanID, row.Opening, row.Ending, row.ColumnName, row.ColumnStyle, row.AssetID, row.ProfileID, row.Keywords, row.Audience, row.BannedWords, row.VerticalKey, row.TopicMode, row.ImageMode, row.Tone, row.PersonaColumns, row.Signatures, row.Taboos)
	return err
}

func (s *Store) GetPlanExtra(ctx context.Context, q db.Q, planID int64) (PlanExtra, error) {
	if q == nil {
		q = s.DB
	}
	var row PlanExtra
	err := q.QueryRow(ctx, `SELECT plan_id, opening, ending, column_name, column_style, asset_id, profile_id, keywords, audience, banned_words, vertical_key, topic_mode, image_mode, tone, persona_columns, signatures, taboos FROM plan_extra WHERE plan_id = ?`, planID).
		Scan(&row.PlanID, &row.Opening, &row.Ending, &row.ColumnName, &row.ColumnStyle, &row.AssetID, &row.ProfileID, &row.Keywords, &row.Audience, &row.BannedWords, &row.VerticalKey, &row.TopicMode, &row.ImageMode, &row.Tone, &row.PersonaColumns, &row.Signatures, &row.Taboos)
	if err == sql.ErrNoRows {
		return PlanExtra{PlanID: planID}, nil
	}
	return row, err
}

func (s *Store) SaveDiffProfile(ctx context.Context, userID int64, features string, now time.Time) error {
	res, err := s.DB.Exec(ctx, `UPDATE style_profile SET features = ?, created_at = ? WHERE user_id = ? AND name = ?`, features, timeReq(now), userID, "修改差异")
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err = s.InsertStyleProfile(ctx, userID, "修改差异", features, now)
	return err
}

func (s *Store) InsertStyleProfile(ctx context.Context, userID int64, name, features string, now time.Time) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `INSERT INTO style_profile (user_id, name, features, created_at) VALUES (?, ?, ?, ?) RETURNING id`, userID, name, features, timeReq(now)).Scan(&id)
	return id, err
}

func (s *Store) GetStyleProfile(ctx context.Context, id int64) (*StyleProfile, error) {
	var row StyleProfile
	var created string
	err := s.DB.QueryRow(ctx, `SELECT id, user_id, name, features, created_at FROM style_profile WHERE id = ?`, id).Scan(&row.ID, &row.UserID, &row.Name, &row.Features, &created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	row.CreatedAt = mustTime(created)
	return &row, nil
}

func (s *Store) ListPromptVariants(ctx context.Context, q db.Q, stage string) ([]PromptVariant, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT id, stage, label, body, uses, adopted FROM prompt_variant WHERE stage = ? ORDER BY id ASC`, stage)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PromptVariant
	for rows.Next() {
		var row PromptVariant
		if err := rows.Scan(&row.ID, &row.Stage, &row.Label, &row.Body, &row.Uses, &row.Adopted); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) SavePromptVariant(ctx context.Context, stage, label, body string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO prompt_variant (stage, label, body) VALUES (?, ?, ?)
		ON CONFLICT(stage, label) DO UPDATE SET body = excluded.body`, stage, label, body)
	return err
}

func (s *Store) IncrementVariantUse(ctx context.Context, q db.Q, id int64) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE prompt_variant SET uses = uses + 1 WHERE id = ?`, id)
	return err
}

func (s *Store) IncrementVariantAdopted(ctx context.Context, id int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE prompt_variant SET adopted = adopted + 1 WHERE id = ?`, id)
	return err
}

func (s *Store) SaveArticleVariant(ctx context.Context, q db.Q, articleID, variantID int64) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO article_variant (article_id, variant_id) VALUES (?, ?)
		ON CONFLICT(article_id) DO UPDATE SET variant_id = excluded.variant_id`, articleID, variantID)
	return err
}

func (s *Store) ArticleVariant(ctx context.Context, articleID int64) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `SELECT variant_id FROM article_variant WHERE article_id = ?`, articleID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

func (s *Store) GetPromptCache(ctx context.Context, key string, now time.Time) (string, bool, error) {
	var body, expires string
	err := s.DB.QueryRow(ctx, `SELECT body, expires_at FROM prompt_cache WHERE cache_key = ?`, key).Scan(&body, &expires)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if expires <= timeReq(now) {
		return "", false, nil
	}
	return body, true, nil
}

func (s *Store) PutPromptCache(ctx context.Context, key, body string, expires time.Time) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO prompt_cache (cache_key, body, expires_at) VALUES (?, ?, ?)
		ON CONFLICT(cache_key) DO UPDATE SET body = excluded.body, expires_at = excluded.expires_at`, key, body, timeReq(expires))
	return err
}

func (s *Store) InsertAsset(ctx context.Context, row *UserAsset) error {
	return s.DB.QueryRow(ctx, `INSERT INTO user_asset (user_id, name, mime, license_note, data, created_at) VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		row.UserID, row.Name, row.Mime, row.LicenseNote, row.Data, timeReq(row.CreatedAt)).Scan(&row.ID)
}

func (s *Store) GetAsset(ctx context.Context, id int64) (*UserAsset, error) {
	var row UserAsset
	var created string
	err := s.DB.QueryRow(ctx, `SELECT id, user_id, name, mime, license_note, data, created_at FROM user_asset WHERE id = ?`, id).
		Scan(&row.ID, &row.UserID, &row.Name, &row.Mime, &row.LicenseNote, &row.Data, &created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	row.CreatedAt = mustTime(created)
	return &row, nil
}

func (s *Store) SaveSample(ctx context.Context, userID int64, title, body string, now time.Time) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO sample_draft (user_id, title, body, created_at) VALUES (?, ?, ?, ?)`, userID, title, body, timeReq(now))
	return err
}

func (s *Store) GetSample(ctx context.Context, userID int64) (title, body string, ok bool, err error) {
	err = s.DB.QueryRow(ctx, `SELECT title, body FROM sample_draft WHERE user_id = ?`, userID).Scan(&title, &body)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	return title, body, err == nil, err
}

func (s *Store) CountTrials(ctx context.Context, userID int64, day string) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM trial_log WHERE user_id = ? AND day_key = ?`, userID, day).Scan(&n)
	return n, err
}

func (s *Store) InsertTrial(ctx context.Context, userID int64, day string, now time.Time) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO trial_log (user_id, day_key, created_at) VALUES (?, ?, ?)`, userID, day, timeReq(now))
	return err
}

func (s *Store) HasAnyConsent(ctx context.Context, userID int64, docType string) (bool, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM consent_log WHERE user_id = ? AND doc_type = ? AND withdrawn_at IS NULL`, userID, docType).Scan(&n)
	return n > 0, err
}

func (s *Store) QuietStreak(ctx context.Context, q db.Q, planID int64) (int, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT id FROM article WHERE plan_id = ? AND status IN ('in_draft', 'exported', 'published', 'publish_failed', 'publish_unknown') ORDER BY id DESC LIMIT 5`, planID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ids) < 5 {
		return len(ids), nil
	}
	streak := 0
	for _, id := range ids {
		var n int
		if err := q.QueryRow(ctx, `SELECT COUNT(*) FROM feedback WHERE article_id = ?`, id).Scan(&n); err != nil {
			return 0, err
		}
		if n > 0 {
			break
		}
		streak++
	}
	return streak, nil
}
