package store

import (
	"context"
	"database/sql"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
)

func (s *Store) GetPlanConfig(ctx context.Context, q db.Q, tier string) (*model.PlanConfig, error) {
	if q == nil {
		q = s.DB
	}
	var p model.PlanConfig
	err := q.QueryRow(ctx, `SELECT tier, name, price_label, daily_quota, period_quota, period_unit, max_accounts, max_plans, max_words, summary FROM plan_config WHERE tier = ?`, tier).
		Scan(&p.Tier, &p.Name, &p.PriceLabel, &p.DailyQuota, &p.PeriodQuota, &p.PeriodUnit, &p.MaxAccounts, &p.MaxPlans, &p.MaxWords, &p.Summary)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) ListPlanConfigs(ctx context.Context) ([]model.PlanConfig, error) {
	rows, err := s.DB.Query(ctx, `SELECT tier, name, price_label, daily_quota, period_quota, period_unit, max_accounts, max_plans, max_words, summary FROM plan_config ORDER BY daily_quota ASC, tier ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.PlanConfig
	for rows.Next() {
		var p model.PlanConfig
		if err := rows.Scan(&p.Tier, &p.Name, &p.PriceLabel, &p.DailyQuota, &p.PeriodQuota, &p.PeriodUnit, &p.MaxAccounts, &p.MaxPlans, &p.MaxWords, &p.Summary); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpsertPlanConfig(ctx context.Context, q db.Q, p model.PlanConfig) error {
	if q == nil {
		q = s.DB
	}
	existing, err := s.GetPlanConfig(ctx, q, p.Tier)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	_, err = q.Exec(ctx, `INSERT INTO plan_config (tier, name, price_label, daily_quota, period_quota, period_unit, max_accounts, max_plans, max_words, summary) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Tier, p.Name, p.PriceLabel, p.DailyQuota, p.PeriodQuota, p.PeriodUnit, p.MaxAccounts, p.MaxPlans, p.MaxWords, p.Summary)
	return err
}

const serviceCols = `id, config_type, name, protocol, base_url, api_key_encrypted, model_name, extra_headers, extra_params, priority, enabled, timeout_seconds, updated_at`

func scanService(row interface{ Scan(dest ...any) error }) (*model.ServiceConfig, error) {
	var c model.ServiceConfig
	var headers, params sql.NullString
	var enabled int
	var updated string
	err := row.Scan(&c.ID, &c.ConfigType, &c.Name, &c.Protocol, &c.BaseURL, &c.APIKeyEncrypted, &c.ModelName, &headers, &params, &c.Priority, &enabled, &c.TimeoutSeconds, &updated)
	if err != nil {
		return nil, err
	}
	c.ExtraHeaders = decodeStringMap(nstr(headers))
	c.ExtraParams = decodeMap(nstr(params))
	c.Enabled = enabled != 0
	c.UpdatedAt = mustTime(updated)
	return &c, nil
}

func (s *Store) GetService(ctx context.Context, q db.Q, id int64) (*model.ServiceConfig, error) {
	if q == nil {
		q = s.DB
	}
	c, err := scanService(q.QueryRow(ctx, `SELECT `+serviceCols+` FROM service_config WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

func (s *Store) FindService(ctx context.Context, q db.Q, configType, name string) (*model.ServiceConfig, error) {
	if q == nil {
		q = s.DB
	}
	c, err := scanService(q.QueryRow(ctx, `SELECT `+serviceCols+` FROM service_config WHERE config_type = ? AND name = ? ORDER BY id ASC LIMIT 1`, configType, name))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

func (s *Store) ListAdminModels(ctx context.Context, q db.Q) ([]model.ServiceConfig, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT `+serviceCols+` FROM service_config WHERE config_type IN ('text_model', 'image_model') ORDER BY priority ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ServiceConfig
	for rows.Next() {
		c, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) ListImageModels(ctx context.Context, q db.Q, enabledOnly bool) ([]model.ServiceConfig, error) {
	return s.listModels(ctx, q, "image_model", enabledOnly)
}

func (s *Store) ListTextModels(ctx context.Context, q db.Q, enabledOnly bool) ([]model.ServiceConfig, error) {
	return s.listModels(ctx, q, "text_model", enabledOnly)
}

func (s *Store) listModels(ctx context.Context, q db.Q, configType string, enabledOnly bool) ([]model.ServiceConfig, error) {
	if q == nil {
		q = s.DB
	}
	query := `SELECT ` + serviceCols + ` FROM service_config WHERE config_type = ?`
	args := []any{configType}
	if enabledOnly {
		query += ` AND enabled = 1`
	}
	query += ` ORDER BY priority ASC, id ASC`
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ServiceConfig
	for rows.Next() {
		c, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) InsertService(ctx context.Context, q db.Q, c *model.ServiceConfig) error {
	if q == nil {
		q = s.DB
	}
	return q.QueryRow(ctx, `INSERT INTO service_config (config_type, name, protocol, base_url, api_key_encrypted, model_name, extra_headers, extra_params, priority, enabled, timeout_seconds, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		c.ConfigType, c.Name, c.Protocol, c.BaseURL, c.APIKeyEncrypted, c.ModelName, jsonStr(c.ExtraHeaders), jsonStr(c.ExtraParams), c.Priority, bint(c.Enabled), c.TimeoutSeconds, timeReq(c.UpdatedAt),
	).Scan(&c.ID)
}

func (s *Store) UpdateService(ctx context.Context, q db.Q, c *model.ServiceConfig) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE service_config SET config_type=?, name=?, protocol=?, base_url=?, api_key_encrypted=?, model_name=?, extra_headers=?, extra_params=?, priority=?, enabled=?, timeout_seconds=?, updated_at=? WHERE id=?`,
		c.ConfigType, c.Name, c.Protocol, c.BaseURL, c.APIKeyEncrypted, c.ModelName, jsonStr(c.ExtraHeaders), jsonStr(c.ExtraParams), c.Priority, bint(c.Enabled), c.TimeoutSeconds, timeReq(c.UpdatedAt), c.ID)
	return err
}

func (s *Store) DeleteService(ctx context.Context, q db.Q, id int64) error {
	if q == nil {
		q = s.DB
	}
	if _, err := q.Exec(ctx, `DELETE FROM stage_route WHERE service_config_id = ?`, id); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM service_config WHERE id = ?`, id)
	return err
}

func (s *Store) ListRoutes(ctx context.Context, q db.Q) ([]model.StageRoute, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT id, stage, tier, service_config_id, sort_order FROM stage_route ORDER BY stage ASC, sort_order ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.StageRoute
	for rows.Next() {
		var r model.StageRoute
		if err := rows.Scan(&r.ID, &r.Stage, &r.Tier, &r.ServiceConfigID, &r.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) RoutesFor(ctx context.Context, q db.Q, stage, tier string) ([]model.StageRoute, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT id, stage, tier, service_config_id, sort_order FROM stage_route WHERE stage = ? AND tier = ? ORDER BY sort_order ASC, id ASC`, stage, tier)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.StageRoute
	for rows.Next() {
		var r model.StageRoute
		if err := rows.Scan(&r.ID, &r.Stage, &r.Tier, &r.ServiceConfigID, &r.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ReplaceRoutes(ctx context.Context, q db.Q, stage, tier string, providerIDs []int64) error {
	if q == nil {
		q = s.DB
	}
	if _, err := q.Exec(ctx, `DELETE FROM stage_route WHERE stage = ? AND tier = ?`, stage, tier); err != nil {
		return err
	}
	for i, id := range providerIDs {
		if _, err := q.Exec(ctx, `INSERT INTO stage_route (stage, tier, service_config_id, sort_order) VALUES (?, ?, ?, ?)`, stage, tier, id, i); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListLegal(ctx context.Context) ([]model.LegalDoc, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, doc_type, version, title, content, status, effective_date FROM legal_doc WHERE status = 'active' ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectLegal(rows)
}

func (s *Store) GetLegal(ctx context.Context, docType string) (*model.LegalDoc, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, doc_type, version, title, content, status, effective_date FROM legal_doc WHERE doc_type = ? AND status = 'active' ORDER BY id DESC LIMIT 1`, docType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list, err := collectLegal(rows)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

func collectLegal(rows *sql.Rows) ([]model.LegalDoc, error) {
	var out []model.LegalDoc
	for rows.Next() {
		var d model.LegalDoc
		if err := rows.Scan(&d.ID, &d.DocType, &d.Version, &d.Title, &d.Content, &d.Status, &d.EffectiveDate); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) HasLegal(ctx context.Context, q db.Q, docType, version string) (bool, error) {
	if q == nil {
		q = s.DB
	}
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM legal_doc WHERE doc_type = ? AND version = ?`, docType, version).Scan(&n)
	return n > 0, err
}

func (s *Store) InsertLegal(ctx context.Context, q db.Q, d model.LegalDoc) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO legal_doc (doc_type, version, title, content, status, effective_date) VALUES (?, ?, ?, ?, ?, ?)`,
		d.DocType, d.Version, d.Title, d.Content, d.Status, d.EffectiveDate)
	return err
}

func (s *Store) InsertConsent(ctx context.Context, q db.Q, c *model.Consent) error {
	if q == nil {
		q = s.DB
	}
	return q.QueryRow(ctx, `INSERT INTO consent_log (user_id, doc_type, version, consent_item, ip, user_agent, created_at) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		c.UserID, c.DocType, c.Version, c.ConsentItem, c.IP, c.UserAgent, timeReq(c.CreatedAt)).Scan(&c.ID)
}

func (s *Store) ListConsents(ctx context.Context, userID int64) ([]model.Consent, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, user_id, doc_type, version, consent_item, ip, user_agent, created_at FROM consent_log WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Consent
	for rows.Next() {
		var c model.Consent
		var created string
		if err := rows.Scan(&c.ID, &c.UserID, &c.DocType, &c.Version, &c.ConsentItem, &c.IP, &c.UserAgent, &created); err != nil {
			return nil, err
		}
		c.CreatedAt = mustTime(created)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) InsertAudit(ctx context.Context, q db.Q, a model.AuditLog) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO audit_log (actor_id, action, target, detail, created_at) VALUES (?, ?, ?, ?, ?)`,
		nullInt(a.ActorID), a.Action, a.Target, a.Detail, timeReq(a.CreatedAt))
	return err
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]model.AuditLog, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, actor_id, action, target, detail, created_at FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AuditLog
	for rows.Next() {
		var a model.AuditLog
		var actor sql.NullInt64
		var created string
		if err := rows.Scan(&a.ID, &actor, &a.Action, &a.Target, &a.Detail, &created); err != nil {
			return nil, err
		}
		a.ActorID = scanNullInt(actor)
		a.CreatedAt = mustTime(created)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) InsertEntitlement(ctx context.Context, q db.Q, userID, adminID int64, tier, note, expires, created string) error {
	if q == nil {
		q = s.DB
	}
	var exp any
	if expires != "" {
		exp = expires
	}
	_, err := q.Exec(ctx, `INSERT INTO entitlement (user_id, source, tier, expires_at, admin_id, note, created_at) VALUES (?, 'admin', ?, ?, ?, ?, ?)`,
		userID, tier, exp, adminID, note, created)
	return err
}

func (s *Store) HasStyle(ctx context.Context, q db.Q, key string) (bool, error) {
	if q == nil {
		q = s.DB
	}
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM style_preset WHERE key = ?`, key).Scan(&n)
	return n > 0, err
}

func (s *Store) InsertStyle(ctx context.Context, q db.Q, key, name, description string) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO style_preset (key, name, description, enabled) VALUES (?, ?, ?, 1)`, key, name, description)
	return err
}

func (s *Store) HasTemplate(ctx context.Context, q db.Q, key string) (bool, error) {
	if q == nil {
		q = s.DB
	}
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM layout_template WHERE key = ?`, key).Scan(&n)
	return n > 0, err
}

func (s *Store) InsertTemplate(ctx context.Context, q db.Q, key, name, description string) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO layout_template (key, name, description, version, enabled) VALUES (?, ?, ?, '1', 1)`, key, name, description)
	return err
}
