package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"gonghao/internal/auth"
	"gonghao/internal/gen"
	"gonghao/internal/server"
)

// ---------- pause / resume, topic confirmation mode ----------

type Controls struct {
	Paused       *bool `json:"paused"`
	ConfirmTopic *bool `json:"confirm_topic"`
}

// SetControls (owner): pause or resume the whole account, switch topic confirmation on or off.
// A paused account runs no plans; "write now" still works because the user asks for it explicitly.
func (s *Service) SetControls(ctx context.Context, actor auth.User, accountID int64, c Controls) error {
	var owner int64
	err := s.DB.QueryRow(ctx, `SELECT user_id FROM wx_account WHERE id=$1 AND bind_status='bound'`, accountID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != actor.ID) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if c.Paused != nil {
		if *c.Paused {
			_, err = s.DB.Exec(ctx, `UPDATE wx_account SET paused=TRUE, paused_reason='user', paused_at=$2, updated_at=now() WHERE id=$1`, accountID, s.now())
		} else {
			_, err = s.DB.Exec(ctx, `UPDATE wx_account SET paused=FALSE, paused_reason='', resumed_at=$2, updated_at=now() WHERE id=$1`, accountID, s.now())
		}
		if err != nil {
			return err
		}
		s.Audit.Log(ctx, actor.ID, map[bool]string{true: "account.pause", false: "account.resume"}[*c.Paused], "", map[string]any{"account_id": accountID})
	}
	if c.ConfirmTopic != nil {
		if _, err := s.DB.Exec(ctx, `UPDATE wx_account SET confirm_topic=$2, updated_at=now() WHERE id=$1`, accountID, *c.ConfirmTopic); err != nil {
			return err
		}
		s.Audit.Log(ctx, actor.ID, "account.confirm_topic", "", map[string]any{"account_id": accountID, "enabled": *c.ConfirmTopic})
	}
	return nil
}

// ---------- topic decisions ----------

const maxRejects = 3

type PendingTopic struct {
	JobID     int64        `json:"job_id"`
	AccountID int64        `json:"account_id"`
	Account   string       `json:"account_name"`
	Column    string       `json:"column_name"`
	Proposal  gen.Proposal `json:"proposal"`
	Rejected  []string     `json:"rejected"`
	Left      int          `json:"rejects_left"`
	CreatedAt time.Time    `json:"created_at"`
}

func (s *Service) PendingTopics(ctx context.Context, actor auth.User) ([]PendingTopic, error) {
	rows, err := s.DB.Query(ctx, `SELECT j.id, j.account_id, a.name, COALESCE(c.name,''), j.proposal, j.rejected, j.created_at
		FROM job j JOIN wx_account a ON a.id=j.account_id LEFT JOIN account_column c ON c.id=j.column_id
		WHERE j.user_id=$1 AND j.status='awaiting_topic' ORDER BY j.id`, actor.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PendingTopic{}
	for rows.Next() {
		var p PendingTopic
		var raw []byte
		if err := rows.Scan(&p.JobID, &p.AccountID, &p.Account, &p.Column, &raw, &p.Rejected, &p.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal(raw, &p.Proposal)
		p.Proposal.Events = nil
		p.Left = maxRejects - len(p.Rejected)
		out = append(out, p)
	}
	return out, rows.Err()
}

type TopicDecision struct {
	Action string `json:"action"` // approve | change | reject
	Topic  string `json:"topic"`  // the new topic for "change"
}

// DecideTopic: approve the proposal, replace it with the user's own topic, or reject it and get
// another one (after the third rejection the job is cancelled and its quota released).
func (s *Service) DecideTopic(ctx context.Context, actor auth.User, jobID int64, d TopicDecision) (string, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var owner int64
	var status string
	var raw []byte
	var rejected []string
	err = tx.QueryRow(ctx, `SELECT user_id, status, proposal, rejected FROM job WHERE id=$1 FOR UPDATE`, jobID).Scan(&owner, &status, &raw, &rejected)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != actor.ID) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if status != "awaiting_topic" {
		return "", ErrInvalid{"这个任务不在等待确认选题"}
	}
	var p gen.Proposal
	json.Unmarshal(raw, &p)
	next := "queued"
	switch d.Action {
	case "approve":
		_, err = tx.Exec(ctx, `UPDATE job SET status='queued', topic=$2, topic_kind=$3, run_at=$4 WHERE id=$1`, jobID, p.Topic, p.Kind, s.now())
	case "change":
		t, e := cleanTopic(d.Topic)
		if e != nil {
			return "", e
		}
		if t == "" {
			return "", ErrInvalid{"请写上你想要的题目"}
		}
		_, err = tx.Exec(ctx, `UPDATE job SET status='queued', topic=$2, topic_kind='', run_at=$3 WHERE id=$1`, jobID, t, s.now())
	case "reject":
		rejected = append(rejected, p.Topic)
		if len(rejected) >= maxRejects {
			next = "cancelled"
			_, err = tx.Exec(ctx, `UPDATE job SET status='cancelled', rejected=$2, proposal=NULL, failure=$3, finished_at=now() WHERE id=$1`,
				jobID, rejected, fmt.Sprintf("选题被否决 %d 次，这次不写了", maxRejects))
		} else {
			_, err = tx.Exec(ctx, `UPDATE job SET status='queued', rejected=$2, proposal=NULL, run_at=$3 WHERE id=$1`, jobID, rejected, s.now())
		}
	default:
		return "", ErrInvalid{"操作不正确"}
	}
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	s.Audit.Log(ctx, actor.ID, "topic."+d.Action, "", map[string]any{"job_id": jobID, "topic": p.Topic})
	return next, nil
}

// ---------- feedback ----------

type Feedback struct {
	Rating int    `json:"rating"` // 1 = 有用, -1 = 不行
	Note   string `json:"note"`
}

func (s *Service) SaveFeedback(ctx context.Context, actor auth.User, articleID int64, f Feedback) error {
	if f.Rating != 1 && f.Rating != -1 {
		return ErrInvalid{"请选择「好」或「不好」"}
	}
	f.Note = strings.TrimSpace(f.Note)
	if utf8.RuneCountInString(f.Note) > 500 {
		return ErrInvalid{"备注不超过 500 字"}
	}
	var owner int64
	err := s.DB.QueryRow(ctx, `SELECT user_id FROM article WHERE id=$1`, articleID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != actor.ID) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO article_feedback (article_id, user_id, rating, note) VALUES ($1,$2,$3,$4)
		ON CONFLICT (article_id) DO UPDATE SET rating=$3, note=$4, updated_at=now()`, articleID, actor.ID, f.Rating, f.Note)
	return err
}

// ---------- platform settings ----------

// Settings the admin can change. AutoPauseAfter = 0 means off.
type Settings struct {
	AutoPauseAfter int     `json:"auto_pause_after"` // pause an account's plans after this many pushed-but-untouched articles with no feedback
	DailyCap       float64 `json:"daily_cap"`        // platform-wide daily model spend cap in yuan; 0 = off
}

func (s *Service) GetSettings(ctx context.Context) (Settings, error) {
	var st Settings
	rows, err := s.DB.Query(ctx, `SELECT key, value FROM platform_setting WHERE key IN ('auto_pause_after','daily_cap')`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var raw []byte
		if err := rows.Scan(&k, &raw); err != nil {
			return st, err
		}
		switch k {
		case "auto_pause_after":
			json.Unmarshal(raw, &st.AutoPauseAfter)
		case "daily_cap":
			json.Unmarshal(raw, &st.DailyCap)
		}
	}
	return st, rows.Err()
}

func (s *Service) SaveSettings(ctx context.Context, actor auth.User, st Settings) error {
	if !actor.Role.IsStaff() {
		return ErrForbidden
	}
	if st.AutoPauseAfter < 0 || st.AutoPauseAfter > 50 {
		return ErrInvalid{"篇数请填 0 到 50（0 表示不自动暂停）"}
	}
	if cur, err := s.GetSettings(ctx); err != nil {
		return err
	} else if cur.DailyCap != st.DailyCap && actor.Role != auth.RoleSuperAdmin {
		return ErrInvalid{"每日花费上限只有超级管理员可以修改"}
	}
	if st.DailyCap < 0 || st.DailyCap > 1e7 {
		return ErrInvalid{"每日花费上限请填 0 或正数（0 表示不限）"}
	}
	for k, v := range map[string]any{"auto_pause_after": st.AutoPauseAfter, "daily_cap": st.DailyCap} {
		b, _ := json.Marshal(v)
		if _, err := s.DB.Exec(ctx, `INSERT INTO platform_setting (key, value, updated_by) VALUES ($1,$2,$3)
			ON CONFLICT (key) DO UPDATE SET value=$2, updated_by=$3, updated_at=now()`, k, b, actor.ID); err != nil {
			return err
		}
	}
	s.Audit.Log(ctx, actor.ID, "settings.save", "", map[string]any{"auto_pause_after": st.AutoPauseAfter, "daily_cap": st.DailyCap})
	return nil
}

// ---------- auto-pause ----------

// untouchedSettle: a pushed article counts as untouched only once it has sat in the draft box this
// long, unedited (by read-back) and without feedback.
const untouchedSettle = 72 * time.Hour

// AutoPause pauses accounts whose last N pushed articles (since the last resume) were all left
// untouched with no feedback. Off when the setting is 0.
func (s *Service) AutoPause(ctx context.Context) (int, error) {
	st, err := s.GetSettings(ctx)
	if err != nil || st.AutoPauseAfter <= 0 {
		return 0, err
	}
	n := st.AutoPauseAfter
	rows, err := s.DB.Query(ctx, `SELECT a.id, a.user_id FROM wx_account a
		WHERE a.bind_status='bound' AND NOT a.paused AND EXISTS (SELECT 1 FROM write_plan p WHERE p.account_id=a.id AND p.enabled)
		AND (SELECT count(*) FROM (
			SELECT ar.readback_status, (SELECT count(*) FROM article_feedback f WHERE f.article_id=ar.id) AS fb
			FROM article ar WHERE ar.account_id=a.id AND ar.status='pushed' AND ar.pushed_at <= $2
			  AND ar.pushed_at > COALESCE(a.resumed_at, '-infinity'::timestamptz)
			ORDER BY ar.pushed_at DESC LIMIT $1) last
		  WHERE last.readback_status='unedited' AND last.fb=0) = $1`, n, s.now().Add(-untouchedSettle))
	if err != nil {
		return 0, err
	}
	type acc struct{ id, uid int64 }
	var list []acc
	for rows.Next() {
		var a acc
		rows.Scan(&a.id, &a.uid)
		list = append(list, a)
	}
	rows.Close()
	for _, a := range list {
		if _, err := s.DB.Exec(ctx, `UPDATE wx_account SET paused=TRUE, paused_reason='auto', paused_at=$2, updated_at=now() WHERE id=$1 AND NOT paused`, a.id, s.now()); err != nil {
			return 0, err
		}
		s.Audit.Log(ctx, 0, "account.auto_pause", "", map[string]any{"account_id": a.id, "untouched": n})
		if s.Log != nil {
			s.Log.Info("auto-pause", "account", a.id, "untouched", n)
		}
	}
	return len(list), nil
}

// ---------- handlers ----------

func (s *Service) hControls(w http.ResponseWriter, r *http.Request) {
	var c Controls
	if json.NewDecoder(r.Body).Decode(&c) != nil {
		server.WriteError(w, 400, "请求格式不正确")
		return
	}
	if err := s.SetControls(r.Context(), actor(r), pid(r, "id"), c); err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Service) hPendingTopics(w http.ResponseWriter, r *http.Request) {
	out, err := s.PendingTopics(r.Context(), actor(r))
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, out)
}

func (s *Service) hDecideTopic(w http.ResponseWriter, r *http.Request) {
	var d TopicDecision
	if json.NewDecoder(r.Body).Decode(&d) != nil {
		server.WriteError(w, 400, "请求格式不正确")
		return
	}
	st, err := s.DecideTopic(r.Context(), actor(r), pid(r, "id"), d)
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, map[string]string{"status": st})
}

func (s *Service) hFeedback(w http.ResponseWriter, r *http.Request) {
	var f Feedback
	if json.NewDecoder(r.Body).Decode(&f) != nil {
		server.WriteError(w, 400, "请求格式不正确")
		return
	}
	if err := s.SaveFeedback(r.Context(), actor(r), pid(r, "id"), f); err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, map[string]bool{"ok": true})
}

// hAdminFeedback (staff): newest feedback with the article, account, owner and read-back result.
func (s *Service) hAdminFeedback(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT f.article_id, ar.title, w.name, u.email, f.rating, f.note, f.updated_at, ar.readback_status, ar.change_ratio
		FROM article_feedback f JOIN article ar ON ar.id=f.article_id JOIN wx_account w ON w.id=ar.account_id JOIN users u ON u.id=f.user_id
		WHERE $1 = 0 OR f.rating = $1 ORDER BY f.updated_at DESC LIMIT 200`, atoiQ(r, "rating"))
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ArticleID int64     `json:"article_id"`
		Title     string    `json:"title"`
		Account   string    `json:"account_name"`
		Owner     string    `json:"owner_email"`
		Rating    int       `json:"rating"`
		Note      string    `json:"note"`
		At        time.Time `json:"at"`
		Readback  string    `json:"readback_status"`
		Ratio     *float64  `json:"change_ratio"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		var rating int16
		if err := rows.Scan(&x.ArticleID, &x.Title, &x.Account, &x.Owner, &rating, &x.Note, &x.At, &x.Readback, &x.Ratio); err != nil {
			fail(w, err)
			return
		}
		x.Rating = int(rating)
		out = append(out, x)
	}
	server.WriteJSON(w, 200, out)
}

func atoiQ(r *http.Request, k string) int {
	var n int
	fmt.Sscan(r.URL.Query().Get(k), &n)
	return n
}

func (s *Service) hGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.GetSettings(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, st)
}

func (s *Service) hSaveSettings(w http.ResponseWriter, r *http.Request) {
	var st Settings
	if json.NewDecoder(r.Body).Decode(&st) != nil {
		server.WriteError(w, 400, "请求格式不正确")
		return
	}
	if err := s.SaveSettings(r.Context(), actor(r), st); err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, st)
}
