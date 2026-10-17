package job

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"gonghao/internal/server"
)

// hToday: the signed-in user's day at a glance — quota and what is running.
func (s *Service) hToday(w http.ResponseWriter, r *http.Request) {
	u := actor(r)
	n := s.now().In(china)
	dayStart := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, china)
	var out struct {
		DailyQuota int  `json:"daily_quota"`
		Exempt     bool `json:"quota_exempt"`
		UsedToday  int  `json:"used_today"`
		Running    int  `json:"running"`
		Queued     int  `json:"queued"`
		ReadyCount int  `json:"ready_articles"`
		Accounts   int  `json:"accounts"`
	}
	err := s.DB.QueryRow(r.Context(), `SELECT u.daily_quota, u.quota_exempt,
		(SELECT count(*) FROM job WHERE user_id=u.id AND created_at >= $2 AND status IN ('queued','running','done')),
		(SELECT count(*) FROM job WHERE user_id=u.id AND status='running'),
		(SELECT count(*) FROM job WHERE user_id=u.id AND status='queued'),
		(SELECT count(*) FROM article WHERE user_id=u.id AND status='ready'),
		(SELECT count(*) FROM wx_account WHERE user_id=u.id AND bind_status='bound')
		FROM users u WHERE u.id=$1`, u.ID, dayStart).
		Scan(&out.DailyQuota, &out.Exempt, &out.UsedToday, &out.Running, &out.Queued, &out.ReadyCount, &out.Accounts)
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, out)
}

// hUsers (staff): customer accounts with their allowance and quota.
func (s *Service) hUsers(w http.ResponseWriter, r *http.Request) {
	q := "%" + r.URL.Query().Get("q") + "%"
	rows, err := s.DB.Query(r.Context(), `SELECT u.id, u.email, u.display_name, u.tier, u.max_accounts, u.quota_exempt, u.daily_quota, u.status, u.created_at,
		(SELECT count(*) FROM wx_account a WHERE a.user_id=u.id AND a.bind_status='bound')
		FROM users u WHERE u.role='user' AND (u.email ILIKE $1 OR u.display_name ILIKE $1) ORDER BY u.id DESC LIMIT 200`, q)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID          int64     `json:"id"`
		Email       string    `json:"email"`
		Name        string    `json:"display_name"`
		Tier        string    `json:"tier"`
		MaxAccounts int       `json:"max_accounts"`
		Exempt      bool      `json:"quota_exempt"`
		DailyQuota  int       `json:"daily_quota"`
		Status      string    `json:"status"`
		CreatedAt   time.Time `json:"created_at"`
		Accounts    int       `json:"accounts"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.Email, &x.Name, &x.Tier, &x.MaxAccounts, &x.Exempt, &x.DailyQuota, &x.Status, &x.CreatedAt, &x.Accounts); err != nil {
			fail(w, err)
			return
		}
		out = append(out, x)
	}
	server.WriteJSON(w, 200, out)
}

// hAudit (staff): newest first, 100 per page.
func (s *Service) hAudit(w http.ResponseWriter, r *http.Request) {
	off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if off < 0 {
		off = 0
	}
	rows, err := s.DB.Query(r.Context(), `SELECT l.id, l.at, COALESCE(u.email,''), l.action, l.target, l.detail
		FROM audit_log l LEFT JOIN users u ON u.id=l.actor_id
		WHERE $1='' OR l.action LIKE $1 || '%' ORDER BY l.id DESC LIMIT 100 OFFSET $2`, r.URL.Query().Get("action"), off)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID     int64           `json:"id"`
		At     time.Time       `json:"at"`
		Actor  string          `json:"actor"`
		Action string          `json:"action"`
		Target string          `json:"target"`
		Detail json.RawMessage `json:"detail"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.At, &x.Actor, &x.Action, &x.Target, &x.Detail); err != nil {
			fail(w, err)
			return
		}
		out = append(out, x)
	}
	server.WriteJSON(w, 200, out)
}
