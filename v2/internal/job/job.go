// Package job: enqueue with a per-user daily quota, a simple DB-backed worker (SKIP LOCKED,
// leases, restart recovery), and the article / preview endpoints.
package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"gonghao/internal/audit"
	"gonghao/internal/auth"
	"gonghao/internal/gen"
)

var (
	ErrQuota     = errors.New("今天的生成额度已用完")
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("没有权限")
)

// Day boundaries for quotas are in China time.
var china = time.FixedZone("CST", 8*3600)

type Runner interface {
	Run(ctx context.Context, j gen.Job) (gen.Result, error)
}

type Service struct {
	DB       *pgxpool.Pool
	Runner   Runner
	Audit    audit.Logger
	Log      *slog.Logger
	Now      func() time.Time
	Lease    time.Duration                 // default 30 min
	ServerIP string                        // outbound IP users add to the WeChat IP whitelist (V2_SERVER_IP); shown on the bind screen
	Tick     []func(context.Context) error // extra periodic work in the worker loop (e.g. draft read-back)
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

type EnqueueInput struct {
	ColumnID  *int64 `json:"column_id"`
	TopicHint string `json:"topic_hint"`
	Topic     string `json:"topic"` // optional: write exactly this topic (skips the topic stage)
}

// ErrInvalidTopic: a user-given topic must be 2–60 characters.
var ErrInvalidTopic = ErrInvalid{"题目请写 2 到 60 个字"}

func cleanTopic(t string) (string, error) {
	t = strings.Join(strings.Fields(t), " ")
	if t == "" {
		return "", nil
	}
	if n := utf8.RuneCountInString(t); n < 2 || n > 60 {
		return "", ErrInvalidTopic
	}
	return t, nil
}

// Enqueue creates a dry-run job for the actor's own account. The quota check and insert happen
// under the user's row lock. Failed and cancelled jobs don't count, so failures release quota.
func (s *Service) Enqueue(ctx context.Context, actor auth.User, accountID int64, in EnqueueInput, idemKey string) (int64, error) {
	if !actor.Role.CanBindAccounts() {
		return 0, ErrForbidden
	}
	topic, err := cleanTopic(in.Topic)
	if err != nil {
		return 0, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var quota int
	var exempt bool
	if err := tx.QueryRow(ctx, `SELECT daily_quota, quota_exempt FROM users WHERE id=$1 FOR UPDATE`, actor.ID).Scan(&quota, &exempt); err != nil {
		return 0, err
	}
	var owner int64
	err = tx.QueryRow(ctx, `SELECT user_id FROM wx_account WHERE id=$1 AND bind_status='bound'`, accountID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != actor.ID) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if in.ColumnID != nil {
		var n int
		tx.QueryRow(ctx, `SELECT count(*) FROM account_column WHERE id=$1 AND account_id=$2 AND enabled`, *in.ColumnID, accountID).Scan(&n)
		if n == 0 {
			return 0, ErrNotFound
		}
	}
	if !exempt {
		n := s.now().In(china)
		dayStart := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, china)
		var used int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM job WHERE user_id=$1 AND created_at >= $2 AND status IN ('queued','running','awaiting_topic','done')`,
			actor.ID, dayStart).Scan(&used); err != nil {
			return 0, err
		}
		if used >= quota {
			return 0, ErrQuota
		}
	}
	if idemKey == "" {
		idemKey = fmt.Sprintf("manual:%d:%d", accountID, s.now().UnixNano())
	}
	source := "manual"
	if strings.HasPrefix(idemKey, "plan:") {
		source = "plan"
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO job (user_id, account_id, column_id, idem_key, dry_run, topic_hint, topic, source, run_at, created_at)
		VALUES ($1,$2,$3,$4,TRUE,$5,$6,$7,$8,$8) RETURNING id`, actor.ID, accountID, in.ColumnID, idemKey, in.TopicHint, topic, source, s.now()).Scan(&id); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	detail := map[string]any{"job_id": id, "account_id": accountID}
	if topic != "" {
		detail["topic"] = topic
	}
	s.Audit.Log(ctx, actor.ID, "job.enqueue", "", detail)
	return id, nil
}

// SetDailyQuota (staff).
func (s *Service) SetDailyQuota(ctx context.Context, actor auth.User, userID int64, quota int) error {
	if !actor.Role.IsStaff() {
		return ErrForbidden
	}
	if quota < 0 || quota > 1000 {
		return errors.New("额度不正确")
	}
	t, err := s.DB.Exec(ctx, `UPDATE users SET daily_quota=$2, updated_at=now() WHERE id=$1 AND role='user'`, userID, quota)
	if err != nil {
		return err
	}
	if t.RowsAffected() == 0 {
		return ErrNotFound
	}
	s.Audit.Log(ctx, actor.ID, "user.daily_quota", "", map[string]any{"user_id": userID, "daily_quota": quota})
	return nil
}

// Recover requeues jobs whose lease expired (process died mid-run); after 3 attempts they fail.
func (s *Service) Recover(ctx context.Context) (int64, error) {
	t, err := s.DB.Exec(ctx, `UPDATE job SET status = CASE WHEN attempt >= 3 THEN 'failed' ELSE 'queued' END,
		failure = CASE WHEN attempt >= 3 THEN '多次中断，已停止' ELSE failure END, lease_until=NULL
		WHERE status='running' AND lease_until < $1`, s.now())
	if err != nil {
		return 0, err
	}
	return t.RowsAffected(), nil
}

// RunOnce claims and runs one due job. Returns false when the queue is empty.
func (s *Service) RunOnce(ctx context.Context) (bool, error) {
	lease := s.Lease
	if lease <= 0 {
		lease = 30 * time.Minute
	}
	var j gen.Job
	var proposal []byte
	var source string
	err := s.DB.QueryRow(ctx, `UPDATE job SET status='running', attempt=attempt+1, lease_until=$2
		WHERE id = (SELECT id FROM job WHERE status='queued' AND run_at <= $1 ORDER BY run_at, id FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING id, user_id, account_id, column_id, topic_hint, dry_run, topic, topic_kind, proposal, rejected, source`, s.now(), s.now().Add(lease)).
		Scan(&j.ID, &j.UserID, &j.AccountID, &j.ColumnID, &j.TopicHint, &j.DryRun, &j.Topic, &j.TopicKind, &proposal, &j.Rejected, &source)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var paused bool
	s.DB.QueryRow(ctx, `SELECT paused, confirm_topic FROM wx_account WHERE id=$1`, j.AccountID).Scan(&paused, &j.Confirm)
	if paused && source == "plan" {
		_, err = s.DB.Exec(ctx, `UPDATE job SET status='cancelled', failure='公众号已暂停，计划没有运行', finished_at=now(), lease_until=NULL WHERE id=$1`, j.ID)
		return true, err
	}
	if j.Topic != "" && len(proposal) > 0 {
		var pr gen.Proposal
		if json.Unmarshal(proposal, &pr) == nil {
			j.Seed = pr.Events
		}
	}
	res, runErr := s.Runner.Run(ctx, j)
	if res.Trace == nil {
		res.Trace = []gen.Step{}
	}
	if runErr == nil && res.Proposal != nil {
		pj, _ := json.Marshal(res.Proposal)
		var col *int64
		if res.Proposal.ColumnID > 0 {
			col = &res.Proposal.ColumnID
		}
		_, err = s.DB.Exec(context.WithoutCancel(ctx), `UPDATE job SET status='awaiting_topic', proposal=$2, column_id=COALESCE($3, column_id), trace=trace || $4::jsonb, lease_until=NULL WHERE id=$1`,
			j.ID, pj, col, res.Trace)
		if s.Log != nil {
			s.Log.Info("job", "id", j.ID, "status", "awaiting_topic")
		}
		return true, err
	}
	status, failure := "done", ""
	if runErr != nil {
		status, failure = "failed", runErr.Error()
	}
	_, err = s.DB.Exec(context.WithoutCancel(ctx), `UPDATE job SET status=$2, failure=$3, trace=trace || $4::jsonb, finished_at=now(), lease_until=NULL WHERE id=$1`,
		j.ID, status, failure, res.Trace)
	if s.Log != nil {
		s.Log.Info("job", "id", j.ID, "status", status, "article", res.ArticleID, "article_status", res.Status, "block", res.BlockReason, "failure", failure)
	}
	return true, err
}

// Work polls the queue until ctx ends.
func (s *Service) Work(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, err := s.Recover(ctx); err != nil && s.Log != nil {
			s.Log.Warn("recover", "err", err)
		}
		if _, err := s.Schedule(ctx); err != nil && s.Log != nil {
			s.Log.Warn("schedule", "err", err)
		}
		for _, f := range s.Tick {
			if err := f(ctx); err != nil && s.Log != nil {
				s.Log.Warn("tick", "err", err)
			}
		}
		if _, err := s.AutoPause(ctx); err != nil && s.Log != nil {
			s.Log.Warn("auto-pause", "err", err)
		}
		for {
			ran, err := s.RunOnce(ctx)
			if err != nil && s.Log != nil {
				s.Log.Warn("worker", "err", err)
			}
			if !ran || ctx.Err() != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
