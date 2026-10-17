package job

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"gonghao/internal/auth"
	"gonghao/internal/gateway"
	"gonghao/internal/server"
)

// ---------- task monitor ----------

type adminJob struct {
	ID         int64           `json:"id"`
	Status     string          `json:"status"`
	Source     string          `json:"source"`
	User       string          `json:"user_email"`
	Account    string          `json:"account_name"`
	Column     string          `json:"column_name"`
	Topic      string          `json:"topic"`
	Attempt    int             `json:"attempt"`
	Failure    string          `json:"failure"`
	Trace      json.RawMessage `json:"trace"`
	ArticleID  *int64          `json:"article_id"`
	RunAt      time.Time       `json:"run_at"`
	CreatedAt  time.Time       `json:"created_at"`
	FinishedAt *time.Time      `json:"finished_at"`
	Calls      int             `json:"calls"`
	Cost       float64         `json:"cost"`
}

const adminJobCols = `j.id, j.status, j.source, u.email, a.name, COALESCE(c.name,''), COALESCE(NULLIF(j.topic,''), j.proposal->>'topic', ''), j.attempt, j.failure, j.trace,
	(SELECT max(id) FROM article WHERE job_id=j.id), j.run_at, j.created_at, j.finished_at,
	(SELECT count(*) FROM model_call m WHERE m.job_id=j.id), (SELECT COALESCE(sum(cost),0)::float8 FROM model_call m WHERE m.job_id=j.id)
	FROM job j JOIN users u ON u.id=j.user_id JOIN wx_account a ON a.id=j.account_id LEFT JOIN account_column c ON c.id=j.column_id`

func scanAdminJob(r pgx.Row) (adminJob, error) {
	var x adminJob
	err := r.Scan(&x.ID, &x.Status, &x.Source, &x.User, &x.Account, &x.Column, &x.Topic, &x.Attempt, &x.Failure, &x.Trace,
		&x.ArticleID, &x.RunAt, &x.CreatedAt, &x.FinishedAt, &x.Calls, &x.Cost)
	return x, err
}

// hAdminJobs (staff): ?status=running|queued|failed|awaiting_topic|done|cancelled (empty = not done)
func (s *Service) hAdminJobs(w http.ResponseWriter, r *http.Request) {
	st := r.URL.Query().Get("status")
	cond, args := `j.status IN ('running','queued','awaiting_topic','failed')`, []any{}
	if st != "" {
		cond, args = `j.status=$1`, []any{st}
	}
	rows, err := s.DB.Query(r.Context(), `SELECT `+adminJobCols+` WHERE `+cond+` ORDER BY j.id DESC LIMIT 200`, args...)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	out := []adminJob{}
	for rows.Next() {
		x, err := scanAdminJob(rows)
		if err != nil {
			fail(w, err)
			return
		}
		out = append(out, x)
	}
	var counts = map[string]int{}
	cr, err := s.DB.Query(r.Context(), `SELECT status, count(*) FROM job WHERE status IN ('running','queued','awaiting_topic') OR (status='failed' AND finished_at > now() - interval '7 days') GROUP BY status`)
	if err == nil {
		for cr.Next() {
			var k string
			var n int
			cr.Scan(&k, &n)
			counts[k] = n
		}
		cr.Close()
	}
	server.WriteJSON(w, 200, map[string]any{"jobs": out, "counts": counts})
}

type modelCall struct {
	Stage     string    `json:"stage"`
	Config    string    `json:"config_name"`
	Model     string    `json:"model"`
	Attempt   int       `json:"attempt"`
	Outcome   string    `json:"outcome"`
	FirstByte *int      `json:"first_byte_ms"`
	Total     int       `json:"total_ms"`
	Finish    string    `json:"finish_reason"`
	TokensIn  int       `json:"tokens_in"`
	TokensOut int       `json:"tokens_out"`
	Cost      float64   `json:"cost"`
	Error     string    `json:"error"`
	At        time.Time `json:"at"`
}

func (s *Service) hAdminJob(w http.ResponseWriter, r *http.Request) {
	x, err := scanAdminJob(s.DB.QueryRow(r.Context(), `SELECT `+adminJobCols+` WHERE j.id=$1`, pid(r, "id")))
	if err != nil {
		fail(w, ErrNotFound)
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT m.stage, COALESCE(c.name,'（已删除）'), m.model, m.attempt, m.outcome, m.first_byte_ms, m.total_ms, m.finish_reason,
		m.tokens_in, m.tokens_out, m.cost::float8, m.error, m.created_at FROM model_call m LEFT JOIN service_config c ON c.id=m.config_id
		WHERE m.job_id=$1 ORDER BY m.id`, x.ID)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	calls := []modelCall{}
	for rows.Next() {
		var m modelCall
		if err := rows.Scan(&m.Stage, &m.Config, &m.Model, &m.Attempt, &m.Outcome, &m.FirstByte, &m.Total, &m.Finish, &m.TokensIn, &m.TokensOut, &m.Cost, &m.Error, &m.At); err != nil {
			fail(w, err)
			return
		}
		if r := []rune(m.Error); len(r) > 300 {
			m.Error = string(r[:300]) + "…"
		}
		calls = append(calls, m)
	}
	server.WriteJSON(w, 200, map[string]any{"job": x, "calls": calls})
}

// running jobs in this process, so an admin can cancel one mid-run
type runningJobs struct {
	mu   sync.Mutex
	stop map[int64]context.CancelFunc
	gone map[int64]bool
}

func (rj *runningJobs) add(id int64, c context.CancelFunc) {
	rj.mu.Lock()
	defer rj.mu.Unlock()
	if rj.stop == nil {
		rj.stop, rj.gone = map[int64]context.CancelFunc{}, map[int64]bool{}
	}
	rj.stop[id] = c
}

func (rj *runningJobs) done(id int64) (cancelled bool) {
	rj.mu.Lock()
	defer rj.mu.Unlock()
	cancelled = rj.gone[id]
	delete(rj.stop, id)
	delete(rj.gone, id)
	return
}

func (rj *runningJobs) cancel(id int64) bool {
	rj.mu.Lock()
	defer rj.mu.Unlock()
	c, ok := rj.stop[id]
	if ok {
		rj.gone[id] = true
		c()
	}
	return ok
}

// AdminRetry puts a failed or cancelled job back in the queue (it skips the user's daily quota check).
func (s *Service) AdminRetry(ctx context.Context, actor auth.User, id int64) error {
	t, err := s.DB.Exec(ctx, `UPDATE job SET status='queued', attempt=0, failure='', run_at=$2, finished_at=NULL, lease_until=NULL,
		proposal=CASE WHEN topic='' THEN NULL ELSE proposal END
		WHERE id=$1 AND status IN ('failed','cancelled')`, id, s.now())
	if err != nil {
		return err
	}
	if t.RowsAffected() == 0 {
		return ErrInvalid{"只有失败或已取消的任务可以重试"}
	}
	s.Audit.Log(ctx, actor.ID, "job.retry", "", map[string]any{"job_id": id})
	return nil
}

// AdminCancel stops a queued or waiting job; a running one is interrupted if it runs in this process.
func (s *Service) AdminCancel(ctx context.Context, actor auth.User, id int64) error {
	t, err := s.DB.Exec(ctx, `UPDATE job SET status='cancelled', failure='管理员取消', finished_at=now(), lease_until=NULL
		WHERE id=$1 AND status IN ('queued','awaiting_topic')`, id)
	if err != nil {
		return err
	}
	if t.RowsAffected() == 0 {
		var st string
		s.DB.QueryRow(ctx, `SELECT status FROM job WHERE id=$1`, id).Scan(&st)
		if st != "running" {
			return ErrInvalid{"这个任务已经结束，不能取消"}
		}
		if !s.running.cancel(id) {
			return ErrInvalid{"任务正在另一个进程里运行，结束后再处理"}
		}
	}
	s.Audit.Log(ctx, actor.ID, "job.cancel", "", map[string]any{"job_id": id})
	return nil
}

func (s *Service) hAdminRetry(w http.ResponseWriter, r *http.Request) {
	if err := s.AdminRetry(r.Context(), actor(r), pid(r, "id")); err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Service) hAdminCancel(w http.ResponseWriter, r *http.Request) {
	if err := s.AdminCancel(r.Context(), actor(r), pid(r, "id")); err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- costs and the spend-cap banner ----------

type costRow struct {
	Key       string  `json:"key"`
	Label     string  `json:"label"`
	Calls     int     `json:"calls"`
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
	Cost      float64 `json:"cost"`
	Priced    bool    `json:"priced"` // false: the config has no price, cost shows 0
}

// hCosts (staff): ?days=7 — tokens and estimated cost by config, by user and by day (China days).
func (s *Service) hCosts(w http.ResponseWriter, r *http.Request) {
	days := atoiQ(r, "days")
	if days <= 0 || days > 90 {
		days = 7
	}
	since := gateway.DayStart(s.now()).AddDate(0, 0, -(days - 1))
	q := func(sql string) ([]costRow, error) {
		rows, err := s.DB.Query(r.Context(), sql, since)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []costRow{}
		for rows.Next() {
			var c costRow
			if err := rows.Scan(&c.Key, &c.Label, &c.Calls, &c.TokensIn, &c.TokensOut, &c.Cost, &c.Priced); err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, rows.Err()
	}
	byConfig, err := q(`SELECT COALESCE(m.config_id::text,'-'), COALESCE(c.name,'（已删除）') || COALESCE(' · ' || NULLIF(c.model,''),''), count(*),
		sum(m.tokens_in), sum(m.tokens_out), sum(m.cost)::float8, bool_or(c.price_in_per_m IS NOT NULL OR c.price_out_per_m IS NOT NULL)
		FROM model_call m LEFT JOIN service_config c ON c.id=m.config_id WHERE m.created_at >= $1 GROUP BY 1,2 ORDER BY 6 DESC, 4 DESC`)
	if err != nil {
		fail(w, err)
		return
	}
	byUser, err := q(`SELECT COALESCE(u.id::text,'-'), COALESCE(u.email,'（无任务）'), count(*), sum(m.tokens_in), sum(m.tokens_out), sum(m.cost)::float8, TRUE
		FROM model_call m LEFT JOIN job j ON j.id=m.job_id LEFT JOIN users u ON u.id=j.user_id WHERE m.created_at >= $1 GROUP BY 1,2 ORDER BY 6 DESC, 4 DESC LIMIT 100`)
	if err != nil {
		fail(w, err)
		return
	}
	byDay, err := q(`SELECT to_char(m.created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD') d, to_char(m.created_at AT TIME ZONE 'Asia/Shanghai', 'MM-DD'), count(*),
		sum(m.tokens_in), sum(m.tokens_out), sum(m.cost)::float8, TRUE FROM model_call m WHERE m.created_at >= $1 GROUP BY 1,2 ORDER BY 1`)
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, map[string]any{"days": days, "by_config": byConfig, "by_user": byUser, "by_day": byDay})
}

// hBudget (staff): today's spend against the caps, for the admin banner.
func (s *Service) hBudget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cap, spent, err := gateway.PlatformCap(ctx, s.DB, s.now())
	if err != nil {
		fail(w, err)
		return
	}
	type cfg struct {
		ID    int64   `json:"id"`
		Name  string  `json:"name"`
		Cap   float64 `json:"daily_cap"`
		Spent float64 `json:"spent"`
		Hit   bool    `json:"hit"`
	}
	rows, err := s.DB.Query(ctx, `SELECT c.id, c.name, c.daily_cap::float8, COALESCE((SELECT sum(cost) FROM model_call m WHERE m.config_id=c.id AND m.created_at >= $1),0)::float8
		FROM service_config c WHERE c.daily_cap IS NOT NULL ORDER BY c.id`, gateway.DayStart(s.now()))
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	cfgs := []cfg{}
	anyHit := cap > 0 && spent >= cap
	for rows.Next() {
		var c cfg
		rows.Scan(&c.ID, &c.Name, &c.Cap, &c.Spent)
		c.Hit = c.Spent >= c.Cap
		anyHit = anyHit || c.Hit
		cfgs = append(cfgs, c)
	}
	var waiting int
	s.DB.QueryRow(ctx, `SELECT count(*) FROM job WHERE status='queued' AND failure=$1`, gateway.ErrBudget.Error()).Scan(&waiting)
	server.WriteJSON(w, 200, map[string]any{"platform_cap": cap, "platform_spent": spent, "platform_hit": cap > 0 && spent >= cap,
		"configs": cfgs, "any_hit": anyHit, "waiting_jobs": waiting})
}

// ---------- consent records ----------

func (s *Service) hConsents(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT doc_type, version, ip, device, agreed_at, revoked_at FROM consent_log WHERE user_id=$1 ORDER BY agreed_at DESC`, pid(r, "uid"))
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		Doc       string     `json:"doc_type"`
		Version   string     `json:"version"`
		IP        string     `json:"ip"`
		Device    string     `json:"device"`
		AgreedAt  time.Time  `json:"agreed_at"`
		RevokedAt *time.Time `json:"revoked_at"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.Doc, &x.Version, &x.IP, &x.Device, &x.AgreedAt, &x.RevokedAt); err != nil {
			fail(w, err)
			return
		}
		out = append(out, x)
	}
	server.WriteJSON(w, 200, out)
}

// ---------- orders: manual plan activation ----------

type OrderInput struct {
	UserID      int64    `json:"user_id"`
	Tier        string   `json:"tier"`
	MaxAccounts int      `json:"max_accounts"`
	DailyQuota  int      `json:"daily_quota"`
	Months      *int     `json:"months"` // nil or 0 = no end date
	Amount      *float64 `json:"amount"` // recorded only
	Note        string   `json:"note"`
}

var tiers = map[string]bool{"free": true, "standard": true, "premium": true, "buyout": true, "custom": true}

// Activate records an order and applies the plan to the user in one transaction.
func (s *Service) Activate(ctx context.Context, actor auth.User, in OrderInput) (int64, error) {
	if !actor.Role.IsStaff() {
		return 0, ErrForbidden
	}
	in.Note = strings.TrimSpace(in.Note)
	switch {
	case !tiers[in.Tier]:
		return 0, ErrInvalid{"请选择套餐"}
	case in.MaxAccounts < 0 || in.MaxAccounts > 100:
		return 0, ErrInvalid{"可绑公众号数量请填 0 到 100"}
	case in.DailyQuota < 0 || in.DailyQuota > 1000:
		return 0, ErrInvalid{"每日篇数请填 0 到 1000"}
	case in.Months != nil && (*in.Months < 0 || *in.Months > 120):
		return 0, ErrInvalid{"时长请填 1 到 120 个月，或留空表示长期"}
	case in.Amount != nil && *in.Amount < 0:
		return 0, ErrInvalid{"金额不能为负"}
	case utf8.RuneCountInString(in.Note) > 200:
		return 0, ErrInvalid{"备注不超过 200 字"}
	}
	if in.Months != nil && *in.Months == 0 {
		in.Months = nil
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var role string
	if err := tx.QueryRow(ctx, `SELECT role FROM users WHERE id=$1 FOR UPDATE`, in.UserID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if role != "user" {
		return 0, ErrInvalid{"只能给普通用户开通套餐"}
	}
	var expires *time.Time
	if in.Months != nil {
		t := s.now().AddDate(0, *in.Months, 0)
		expires = &t
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO plan_order (user_id, tier, max_accounts, daily_quota, months, expires_at, amount, note, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, in.UserID, in.Tier, in.MaxAccounts, in.DailyQuota, in.Months, expires, in.Amount, in.Note, actor.ID).Scan(&id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET tier=$2, max_accounts=$3, daily_quota=$4, tier_expires_at=$5, updated_at=now() WHERE id=$1`,
		in.UserID, in.Tier, in.MaxAccounts, in.DailyQuota, expires); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	s.Audit.Log(ctx, actor.ID, "order.activate", "", map[string]any{"order_id": id, "user_id": in.UserID, "tier": in.Tier,
		"max_accounts": in.MaxAccounts, "daily_quota": in.DailyQuota, "months": in.Months, "amount": in.Amount})
	return id, nil
}

func (s *Service) hOrders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT o.id, u.email, o.tier, o.max_accounts, o.daily_quota, o.months, o.expires_at, o.amount::float8, o.note, b.email, o.created_at
		FROM plan_order o JOIN users u ON u.id=o.user_id JOIN users b ON b.id=o.created_by ORDER BY o.id DESC LIMIT 200`)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID          int64      `json:"id"`
		User        string     `json:"user_email"`
		Tier        string     `json:"tier"`
		MaxAccounts int        `json:"max_accounts"`
		DailyQuota  int        `json:"daily_quota"`
		Months      *int       `json:"months"`
		ExpiresAt   *time.Time `json:"expires_at"`
		Amount      *float64   `json:"amount"`
		Note        string     `json:"note"`
		By          string     `json:"created_by"`
		At          time.Time  `json:"created_at"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.User, &x.Tier, &x.MaxAccounts, &x.DailyQuota, &x.Months, &x.ExpiresAt, &x.Amount, &x.Note, &x.By, &x.At); err != nil {
			fail(w, err)
			return
		}
		out = append(out, x)
	}
	server.WriteJSON(w, 200, out)
}

func (s *Service) hActivate(w http.ResponseWriter, r *http.Request) {
	var in OrderInput
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		server.WriteError(w, 400, "请求格式不正确")
		return
	}
	id, err := s.Activate(r.Context(), actor(r), in)
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 201, map[string]any{"id": id})
}
