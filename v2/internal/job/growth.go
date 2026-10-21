package job

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"gonghao/internal/auth"
	"gonghao/internal/server"
)

// ---------- pricing tiers (public page + admin settings) ----------

type Tier struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	PriceText   string `json:"price_text"`
	MaxAccounts int    `json:"max_accounts"`
	DailyQuota  int    `json:"daily_quota"`
	Description string `json:"description"`
	Visible     bool   `json:"visible"`
	Sort        int    `json:"sort"`
	AIImages    bool   `json:"ai_images"` // AI cover and inline pictures for this tier
}

func (s *Service) tiers(ctx context.Context, visibleOnly bool) ([]Tier, error) {
	q := `SELECT key, name, price_text, max_accounts, daily_quota, description, visible, sort, ai_images FROM plan_tier`
	if visibleOnly {
		q += ` WHERE visible`
	}
	rows, err := s.DB.Query(ctx, q+` ORDER BY sort, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tier{}
	for rows.Next() {
		var t Tier
		if err := rows.Scan(&t.Key, &t.Name, &t.PriceText, &t.MaxAccounts, &t.DailyQuota, &t.Description, &t.Visible, &t.Sort, &t.AIImages); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// hPublicPlans: the pricing page (no login).
func (s *Service) hPublicPlans(w http.ResponseWriter, r *http.Request) {
	ts, err := s.tiers(r.Context(), true)
	if err != nil {
		fail(w, err)
		return
	}
	st, _ := s.GetSettings(r.Context())
	server.WriteJSON(w, 200, map[string]any{"tiers": ts, "contact": *st.Contact})
}

func (s *Service) hAdminTiers(w http.ResponseWriter, r *http.Request) {
	ts, err := s.tiers(r.Context(), false)
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, ts)
}

func (s *Service) SaveTier(ctx context.Context, actor auth.User, t Tier) error {
	if actor.Role != auth.RoleSuperAdmin {
		return ErrForbidden
	}
	t.Name, t.PriceText, t.Description = strings.TrimSpace(t.Name), strings.TrimSpace(t.PriceText), strings.TrimSpace(t.Description)
	switch {
	case t.Name == "" || utf8.RuneCountInString(t.Name) > 20:
		return ErrInvalid{"请填写套餐名称（20 字以内）"}
	case utf8.RuneCountInString(t.PriceText) > 40:
		return ErrInvalid{"价格说明不超过 40 字"}
	case utf8.RuneCountInString(t.Description) > 300:
		return ErrInvalid{"介绍不超过 300 字"}
	case t.MaxAccounts < 0 || t.MaxAccounts > 100 || t.DailyQuota < 0 || t.DailyQuota > 1000:
		return ErrInvalid{"公众号数量 0–100，每天篇数 0–1000"}
	}
	tag, err := s.DB.Exec(ctx, `UPDATE plan_tier SET name=$2, price_text=$3, max_accounts=$4, daily_quota=$5, description=$6, visible=$7, sort=$8, ai_images=$9, updated_at=now() WHERE key=$1`,
		t.Key, t.Name, t.PriceText, t.MaxAccounts, t.DailyQuota, t.Description, t.Visible, t.Sort, t.AIImages)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	s.Audit.Log(ctx, actor.ID, "tier.save", t.Key, map[string]any{"name": t.Name, "price_text": t.PriceText, "max_accounts": t.MaxAccounts, "daily_quota": t.DailyQuota, "visible": t.Visible, "ai_images": t.AIImages})
	return nil
}

func (s *Service) hSaveTier(w http.ResponseWriter, r *http.Request) {
	var t Tier
	if json.NewDecoder(r.Body).Decode(&t) != nil {
		server.WriteError(w, 400, "请求格式不正确")
		return
	}
	t.Key = r.PathValue("key")
	if err := s.SaveTier(r.Context(), actor(r), t); err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, t)
}

// ---------- complaints (public form, admin list) ----------

var complaintKinds = map[string]bool{"infringe": true, "false": true, "illegal": true, "privacy": true, "other": true}

// complaint limits per client address, instead of a captcha
const (
	complaintPerHour = 3
	complaintPerDay  = 10
	complaintAllHour = 200 // everyone together, so a flood can't fill the table
)

var ErrComplaintLimit = ErrInvalid{"提交太频繁了，请稍后再试；着急的话请直接联系管理员"}

func (s *Service) Complain(ctx context.Context, ip, kind, content, link, contact string) error {
	content, link, contact = strings.TrimSpace(content), strings.TrimSpace(link), strings.TrimSpace(contact)
	switch {
	case !complaintKinds[kind]:
		return ErrInvalid{"请选择投诉类型"}
	case utf8.RuneCountInString(content) < 10 || utf8.RuneCountInString(content) > 2000:
		return ErrInvalid{"请写清楚情况（10 到 2000 字）"}
	case utf8.RuneCountInString(link) > 500:
		return ErrInvalid{"链接太长"}
	case utf8.RuneCountInString(contact) > 100:
		return ErrInvalid{"联系方式不超过 100 字"}
	}
	var hour, day, all int
	now := s.now()
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FILTER (WHERE ip=$1 AND created_at > $2),
		count(*) FILTER (WHERE ip=$1), count(*) FILTER (WHERE created_at > $2)
		FROM complaint WHERE created_at > $3`, ip, now.Add(-time.Hour), now.Add(-24*time.Hour)).Scan(&hour, &day, &all); err != nil {
		return err
	}
	if hour >= complaintPerHour || day >= complaintPerDay || all >= complaintAllHour {
		return ErrComplaintLimit
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO complaint (kind, content, link, contact, ip, created_at) VALUES ($1,$2,$3,$4,$5,$6)`, kind, content, link, contact, ip, s.now())
	return err
}

func (s *Service) hComplain(w http.ResponseWriter, r *http.Request) {
	var in struct{ Kind, Content, Link, Contact string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in) != nil {
		server.WriteError(w, 400, "请求格式不正确")
		return
	}
	if err := s.Complain(r.Context(), auth.ClientIP(r), in.Kind, in.Content, in.Link, in.Contact); err != nil {
		if errors.Is(err, ErrComplaintLimit) {
			server.WriteError(w, 429, err.Error())
			return
		}
		fail(w, err)
		return
	}
	server.WriteJSON(w, 201, map[string]bool{"ok": true})
}

func (s *Service) hAdminComplaints(w http.ResponseWriter, r *http.Request) {
	st := r.URL.Query().Get("status")
	rows, err := s.DB.Query(r.Context(), `SELECT c.id, c.kind, c.content, c.link, c.contact, c.ip, c.status, c.note, COALESCE(u.email,''), c.handled_at, c.created_at
		FROM complaint c LEFT JOIN users u ON u.id=c.handled_by WHERE ($1='' OR c.status=$1) ORDER BY c.status='handled', c.id DESC LIMIT 200`, st)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID        int64      `json:"id"`
		Kind      string     `json:"kind"`
		Content   string     `json:"content"`
		Link      string     `json:"link"`
		Contact   string     `json:"contact"`
		IP        string     `json:"ip"`
		Status    string     `json:"status"`
		Note      string     `json:"note"`
		By        string     `json:"handled_by"`
		HandledAt *time.Time `json:"handled_at"`
		CreatedAt time.Time  `json:"created_at"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.Kind, &x.Content, &x.Link, &x.Contact, &x.IP, &x.Status, &x.Note, &x.By, &x.HandledAt, &x.CreatedAt); err != nil {
			fail(w, err)
			return
		}
		out = append(out, x)
	}
	server.WriteJSON(w, 200, out)
}

func (s *Service) hHandleComplaint(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || (in.Status != "new" && in.Status != "handled") || utf8.RuneCountInString(in.Note) > 500 {
		server.WriteError(w, 400, "请选择状态，备注不超过 500 字")
		return
	}
	a := actor(r)
	id := pid(r, "id")
	tag, err := s.DB.Exec(r.Context(), `UPDATE complaint SET status=$2, note=$3,
		handled_by=CASE WHEN $2='handled' THEN $4::bigint END, handled_at=CASE WHEN $2='handled' THEN now() END WHERE id=$1`, id, in.Status, strings.TrimSpace(in.Note), a.ID)
	if err != nil {
		fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, ErrNotFound)
		return
	}
	s.Audit.Log(r.Context(), a.ID, "complaint.handle", "", map[string]any{"complaint_id": id, "status": in.Status})
	server.WriteJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- data export ----------

func (s *Service) collect(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	rows, err := s.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	fields := rows.FieldDescriptions()
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		m := map[string]any{}
		for i, f := range fields {
			v := vals[i]
			if b, ok := v.([]byte); ok { // jsonb
				v = json.RawMessage(b)
			}
			m[string(f.Name)] = v
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Export gathers everything the user put in or got out: profile, accounts (no secrets), writing
// settings, columns, plans, articles with their sources and feedback, consents, invites.
func (s *Service) Export(ctx context.Context, userID int64) (map[string]any, error) {
	out := map[string]any{"exported_at": s.now().In(cst).Format(time.RFC3339), "format": "公号智写数据导出 v1"}
	parts := []struct{ key, sql string }{
		{"user", `SELECT email, display_name, tier, max_accounts, daily_quota, bonus_articles, tier_expires_at, created_at FROM users WHERE id=$1`},
		{"accounts", `SELECT id, name, app_id, bind_status, paused, confirm_topic, layout, created_at FROM wx_account WHERE user_id=$1 ORDER BY id`},
		{"writing_settings", `SELECT p.account_id, p.positioning, p.persona, p.audience, p.tone, p.taboos, p.signature, p.opening, p.ending, p.ai_label, p.self_review, p.agent_mode
			FROM account_profile p JOIN wx_account a ON a.id=p.account_id WHERE a.user_id=$1 ORDER BY p.account_id`},
		{"columns", `SELECT c.id, c.account_id, c.name, t.name AS article_type, c.note, c.length_hint, c.weight, c.enabled
			FROM account_column c JOIN wx_account a ON a.id=c.account_id JOIN article_type t ON t.id=c.article_type_id WHERE a.user_id=$1 ORDER BY c.id`},
		{"plans", `SELECT id, account_id, column_id, weekdays, at_minute, enabled, last_run_on FROM write_plan WHERE user_id=$1 ORDER BY id`},
		{"articles", `SELECT id, account_id, column_id, title, alt_titles, summary, topic, status, layout, body_json, html, draft_media_id, created_at, pushed_at
			FROM article WHERE user_id=$1 ORDER BY id`},
		{"article_sources", `SELECT s.article_id, s.n, s.kind, s.title, s.issuer, s.doc_number, s.published_on, s.url
			FROM article_source s JOIN article a ON a.id=s.article_id WHERE a.user_id=$1 ORDER BY s.article_id, s.n`},
		{"style_profiles", `SELECT p.account_id, p.column_id, p.traits, p.samples, p.excerpts, p.enabled, p.updated_at
			FROM style_profile p JOIN wx_account a ON a.id=p.account_id WHERE a.user_id=$1 ORDER BY p.id`},
		{"ref_images", `SELECT account_id, source_url, caption, width, height, statement, authorized_at, used, created_at
			FROM ref_image WHERE user_id=$1 ORDER BY id`},
		{"article_stats", `SELECT d.article_id, d.stat_date, d.read_user, d.share_user, d.collection_user, d.like_user, d.zaikan_user, d.finish_rate
			FROM article_stat_day d JOIN article a ON a.id=d.article_id WHERE a.user_id=$1 ORDER BY d.article_id, d.stat_date`},
		{"feedback", `SELECT article_id, rating, note, updated_at FROM article_feedback WHERE user_id=$1 ORDER BY article_id`},
		{"consents", `SELECT doc_type, version, agreed_at, revoked_at FROM consent_log WHERE user_id=$1 ORDER BY agreed_at`},
		{"orders", `SELECT tier, max_accounts, daily_quota, months, expires_at, amount, note, created_at FROM plan_order WHERE user_id=$1 ORDER BY id`},
	}
	for _, p := range parts {
		rows, err := s.collect(ctx, p.sql, userID)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.key, err)
		}
		if p.key == "user" {
			if len(rows) == 1 {
				out["user"] = rows[0]
			}
			continue
		}
		out[p.key] = rows
	}
	return out, nil
}

func (s *Service) hExport(w http.ResponseWriter, r *http.Request) {
	u := actor(r)
	data, err := s.Export(r.Context(), u.ID)
	if err != nil {
		fail(w, err)
		return
	}
	s.Audit.Log(r.Context(), u.ID, "user.export", "", nil)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="gonghao-export-%s.json"`, s.now().In(cst).Format("20060102")))
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(data)
}

// ---------- account deletion: request, grace period, cancel on login, purge ----------

const deletionGrace = 7 * 24 * time.Hour

func (s *Service) RequestDeletion(ctx context.Context, actor auth.User, password string) (time.Time, error) {
	if !actor.Role.CanBindAccounts() {
		return time.Time{}, ErrInvalid{"管理人员账号不能在这里注销"}
	}
	if s.Auth == nil {
		return time.Time{}, errors.New("auth not configured")
	}
	if err := s.Auth.ConfirmPassword(ctx, actor.ID, password); err != nil {
		return time.Time{}, ErrInvalid{"密码不正确"}
	}
	now := s.now()
	if _, err := s.DB.Exec(ctx, `UPDATE users SET delete_requested_at=$2, updated_at=now() WHERE id=$1`, actor.ID, now); err != nil {
		return time.Time{}, err
	}
	s.Auth.LogoutAll(ctx, actor.ID)
	s.Audit.Log(ctx, actor.ID, "user.delete_request", "", nil)
	return now.Add(deletionGrace), nil
}

func (s *Service) hRequestDeletion(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	at, err := s.RequestDeletion(r.Context(), actor(r), in.Password)
	if err != nil {
		fail(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	server.WriteJSON(w, 200, map[string]any{"delete_at": at})
}

// CancelDeletion runs on login: a pending deletion is called off.
func (s *Service) CancelDeletion(ctx context.Context, u auth.User) bool {
	tag, err := s.DB.Exec(ctx, `UPDATE users SET delete_requested_at=NULL WHERE id=$1 AND delete_requested_at IS NOT NULL AND deleted_at IS NULL`, u.ID)
	if err != nil || tag.RowsAffected() == 0 {
		return false
	}
	s.Audit.Log(ctx, u.ID, "user.delete_cancel", "", nil)
	return true
}

// PurgeDeleted removes the data of users whose grace period is over. The user row stays,
// anonymised and disabled, so orders, consents and the audit log keep their references.
func (s *Service) PurgeDeleted(ctx context.Context) error {
	rows, err := s.DB.Query(ctx, `SELECT id FROM users WHERE delete_requested_at < $1 AND deleted_at IS NULL AND role='user'`, s.now().Add(-deletionGrace))
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := s.purge(ctx, id); err != nil {
			return fmt.Errorf("purge user %d: %w", id, err)
		}
		s.Audit.Log(ctx, 0, "user.purge", "", map[string]any{"user_id": id})
	}
	return nil
}

func (s *Service) purge(ctx context.Context, id int64) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, q := range []string{
		`DELETE FROM article_feedback WHERE user_id=$1`,
		`DELETE FROM article WHERE user_id=$1`,
		`DELETE FROM job WHERE user_id=$1`,
		`DELETE FROM write_plan WHERE user_id=$1`,
		`DELETE FROM wx_account WHERE user_id=$1`,
		`DELETE FROM user_secret WHERE user_id=$1`,
		`DELETE FROM sessions WHERE user_id=$1`,
		`UPDATE users SET email='deleted-' || id || '@deleted.invalid', display_name='', phone=NULL, password_hash='!', status='disabled',
			invite_code=NULL, reg_ip='', reg_device='', bonus_articles=0, deleted_at=now(), updated_at=now() WHERE id=$1`,
	} {
		if _, err := tx.Exec(ctx, q, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ---------- invites ----------

const inviteAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newInviteCode() string {
	b := make([]byte, 8)
	rand.Read(b)
	for i := range b {
		b[i] = inviteAlphabet[int(b[i])%len(inviteAlphabet)]
	}
	return string(b)
}

// sameSourceWindow: a registration from an address or device that already registered within this
// window doesn't count as an invite.
const sameSourceWindow = 30 * 24 * time.Hour

// OnRegistered records where the user registered from and, with a valid invite code, the invite.
func (s *Service) OnRegistered(ctx context.Context, u auth.User, code, ip, device string) {
	s.DB.Exec(ctx, `UPDATE users SET reg_ip=$2, reg_device=$3 WHERE id=$1`, u.ID, ip, device)
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return
	}
	var inviter int64
	var invIP, invDev string
	err := s.DB.QueryRow(ctx, `SELECT id, reg_ip, reg_device FROM users WHERE invite_code=$1 AND role='user' AND deleted_at IS NULL AND status='active'`, code).Scan(&inviter, &invIP, &invDev)
	if err != nil || inviter == u.ID {
		return // unknown code: register normally
	}
	status, reason := "pending", ""
	var dup int
	s.DB.QueryRow(ctx, `SELECT count(*) FROM users WHERE id<>$1 AND created_at > $4 AND (($2<>'' AND reg_ip=$2) OR ($3<>'' AND reg_device=$3))`,
		u.ID, ip, device, s.now().Add(-sameSourceWindow)).Scan(&dup)
	switch {
	case (ip != "" && ip == invIP) || (device != "" && device == invDev):
		status, reason = "rejected", "和邀请人是同一网络或设备"
	case dup > 0:
		status, reason = "rejected", "这个网络或设备最近已经注册过"
	}
	s.DB.Exec(ctx, `UPDATE users SET invited_by=$2 WHERE id=$1`, u.ID, inviter)
	s.DB.Exec(ctx, `INSERT INTO invite (invitee_id, inviter_id, status, reason, created_at, decided_at)
		VALUES ($1,$2,$3,$4,$5, CASE WHEN $3='rejected' THEN $5::timestamptz END) ON CONFLICT DO NOTHING`, u.ID, inviter, status, reason, s.now())
}

// RewardInvites: a pending invite becomes valid once the new user has bound an account and got one
// article written; the inviter then gets the bonus set by the admin (0 = off, nothing added).
func (s *Service) RewardInvites(ctx context.Context) error {
	st, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	bonus := *st.InviteBonus
	rows, err := s.DB.Query(ctx, `SELECT i.invitee_id, i.inviter_id, u.deleted_at IS NOT NULL OR u.delete_requested_at IS NOT NULL FROM invite i JOIN users u ON u.id=i.invitee_id
		WHERE i.status='pending' AND (u.deleted_at IS NOT NULL OR u.delete_requested_at IS NOT NULL OR (
		  EXISTS (SELECT 1 FROM wx_account a WHERE a.user_id=u.id AND a.bind_status='bound') AND EXISTS (SELECT 1 FROM article ar WHERE ar.user_id=u.id)))`)
	if err != nil {
		return err
	}
	type cand struct {
		invitee, inviter int64
		gone             bool
	}
	var cs []cand
	for rows.Next() {
		var c cand
		rows.Scan(&c.invitee, &c.inviter, &c.gone)
		cs = append(cs, c)
	}
	rows.Close()
	for _, c := range cs {
		if c.gone {
			s.DB.Exec(ctx, `UPDATE invite SET status='rejected', reason='被邀请人已注销', decided_at=now() WHERE invitee_id=$1 AND status='pending'`, c.invitee)
			continue
		}
		tx, err := s.DB.Begin(ctx)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE invite SET status='valid', bonus=$2, decided_at=now() WHERE invitee_id=$1 AND status='pending'`, c.invitee, bonus)
		if err == nil && tag.RowsAffected() == 1 && bonus > 0 {
			_, err = tx.Exec(ctx, `UPDATE users SET bonus_articles=bonus_articles+$2 WHERE id=$1 AND deleted_at IS NULL`, c.inviter, bonus)
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		tx.Rollback(ctx)
		if err != nil {
			return err
		}
		s.Audit.Log(ctx, 0, "invite.valid", "", map[string]any{"user_id": c.inviter, "invitee_id": c.invitee, "bonus": bonus})
	}
	return nil
}

func maskEmail(e string) string {
	name, dom, ok := strings.Cut(e, "@")
	if !ok {
		return "***"
	}
	r := []rune(name)
	if len(r) <= 2 {
		return string(r[:1]) + "***@" + dom
	}
	return string(r[:2]) + "***@" + dom
}

func (s *Service) hMyInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := actor(r)
	var code *string
	var bonusLeft int
	if err := s.DB.QueryRow(ctx, `SELECT invite_code, bonus_articles FROM users WHERE id=$1`, u.ID).Scan(&code, &bonusLeft); err != nil {
		fail(w, err)
		return
	}
	for i := 0; code == nil && i < 5; i++ {
		c := newInviteCode()
		if _, err := s.DB.Exec(ctx, `UPDATE users SET invite_code=$2 WHERE id=$1 AND invite_code IS NULL`, u.ID, c); err == nil {
			s.DB.QueryRow(ctx, `SELECT invite_code FROM users WHERE id=$1`, u.ID).Scan(&code)
		}
	}
	if code == nil {
		fail(w, errors.New("invite code"))
		return
	}
	st, _ := s.GetSettings(ctx)
	rows, err := s.DB.Query(ctx, `SELECT u.email, i.status, i.reason, i.bonus, i.created_at FROM invite i JOIN users u ON u.id=i.invitee_id WHERE i.inviter_id=$1 ORDER BY i.created_at DESC LIMIT 100`, u.ID)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		Email     string    `json:"email"`
		Status    string    `json:"status"`
		Reason    string    `json:"reason"`
		Bonus     int       `json:"bonus"`
		CreatedAt time.Time `json:"created_at"`
	}
	list := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.Email, &x.Status, &x.Reason, &x.Bonus, &x.CreatedAt); err != nil {
			fail(w, err)
			return
		}
		x.Email = maskEmail(x.Email)
		list = append(list, x)
	}
	server.WriteJSON(w, 200, map[string]any{"code": *code, "bonus_per_invite": *st.InviteBonus, "bonus_articles": bonusLeft, "invites": list})
}

// RefundBonuses gives a bonus article back when the job that spent it was cancelled or failed.
// Each job refunds at most once (used_bonus is cleared in the same statement).
func (s *Service) RefundBonuses(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `WITH r AS (
			UPDATE job SET used_bonus=FALSE WHERE used_bonus AND status IN ('failed','cancelled') RETURNING user_id
		), c AS (SELECT user_id, count(*) AS n FROM r GROUP BY user_id),
		u AS (UPDATE users SET bonus_articles=bonus_articles + c.n FROM c WHERE users.id=c.user_id AND users.deleted_at IS NULL RETURNING c.n)
		SELECT COALESCE(sum(n),0) FROM c`).Scan(&n)
	return n, err
}

// GrowthTick: periodic work for invites, bonus refunds and deletions.
func (s *Service) GrowthTick(ctx context.Context) error {
	if err := s.RewardInvites(ctx); err != nil {
		return err
	}
	if _, err := s.RefundBonuses(ctx); err != nil {
		return err
	}
	// pictures of a job that never saved an article (failed or cancelled mid-way)
	if _, err := s.DB.Exec(ctx, `DELETE FROM article_image WHERE article_id IS NULL AND created_at < $1`, s.now().Add(-24*time.Hour)); err != nil {
		return err
	}
	return s.PurgeDeleted(ctx)
}
