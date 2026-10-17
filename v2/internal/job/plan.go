package job

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"gonghao/internal/auth"
)

// Plan is a simple schedule: weekdays and a time of day in Beijing time.
type Plan struct {
	ID         int64      `json:"id"`
	AccountID  int64      `json:"account_id"`
	ColumnID   *int64     `json:"column_id"` // nil: the account's usual column pick
	Weekdays   []int      `json:"weekdays"`  // 1 = Monday … 7 = Sunday
	Time       string     `json:"time"`      // "HH:MM"
	Enabled    bool       `json:"enabled"`
	LastRunOn  *time.Time `json:"last_run_on"`
	LastResult string     `json:"last_result"`
}

const maxPlansPerAccount = 10

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

func (p *Plan) minute() (int, error) {
	m := hhmm.FindStringSubmatch(p.Time)
	if m == nil {
		return 0, errors.New("时间格式应为 HH:MM")
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	return h*60 + mi, nil
}

func (p *Plan) cleanDays() error {
	seen := map[int]bool{}
	var out []int
	for _, d := range p.Weekdays {
		if d < 1 || d > 7 {
			return errors.New("星期只能是 1 到 7")
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return errors.New("至少选一天")
	}
	sort.Ints(out)
	p.Weekdays = out
	return nil
}

const planCols = `id, account_id, column_id, weekdays, at_minute, enabled, last_run_on, last_result`

func scanPlan(r pgx.Row) (Plan, error) {
	var p Plan
	var days []int16
	var min int
	err := r.Scan(&p.ID, &p.AccountID, &p.ColumnID, &days, &min, &p.Enabled, &p.LastRunOn, &p.LastResult)
	for _, d := range days {
		p.Weekdays = append(p.Weekdays, int(d))
	}
	p.Time = fmt.Sprintf("%02d:%02d", min/60, min%60)
	return p, err
}

func (s *Service) ownAccount(ctx context.Context, actor auth.User, accountID int64) error {
	if !actor.Role.CanBindAccounts() {
		return ErrForbidden
	}
	var owner int64
	err := s.DB.QueryRow(ctx, `SELECT user_id FROM wx_account WHERE id=$1 AND bind_status='bound'`, accountID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != actor.ID) {
		return ErrNotFound
	}
	return err
}

func (s *Service) ListPlans(ctx context.Context, actor auth.User, accountID int64) ([]Plan, error) {
	if err := s.ownAccount(ctx, actor, accountID); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT `+planCols+` FROM write_plan WHERE account_id=$1 ORDER BY id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Plan{}
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ErrInvalid wraps a user-facing validation message.
type ErrInvalid struct{ Msg string }

func (e ErrInvalid) Error() string { return e.Msg }

func (s *Service) SavePlan(ctx context.Context, actor auth.User, accountID int64, p Plan) (Plan, error) {
	if err := s.ownAccount(ctx, actor, accountID); err != nil {
		return p, err
	}
	min, err := p.minute()
	if err == nil {
		err = p.cleanDays()
	}
	if err != nil {
		return p, ErrInvalid{err.Error()}
	}
	if p.ColumnID != nil {
		var n int
		s.DB.QueryRow(ctx, `SELECT count(*) FROM account_column WHERE id=$1 AND account_id=$2`, *p.ColumnID, accountID).Scan(&n)
		if n == 0 {
			return p, ErrInvalid{"栏目不属于这个公众号"}
		}
	}
	days := make([]int16, len(p.Weekdays))
	for i, d := range p.Weekdays {
		days[i] = int16(d)
	}
	var out Plan
	if p.ID == 0 {
		var n int
		s.DB.QueryRow(ctx, `SELECT count(*) FROM write_plan WHERE account_id=$1`, accountID).Scan(&n)
		if n >= maxPlansPerAccount {
			return p, ErrInvalid{fmt.Sprintf("每个公众号最多 %d 个计划", maxPlansPerAccount)}
		}
		out, err = scanPlan(s.DB.QueryRow(ctx, `INSERT INTO write_plan (user_id, account_id, column_id, weekdays, at_minute, enabled)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+planCols, actor.ID, accountID, p.ColumnID, days, min, p.Enabled))
	} else {
		out, err = scanPlan(s.DB.QueryRow(ctx, `UPDATE write_plan SET column_id=$3, weekdays=$4, at_minute=$5, enabled=$6, updated_at=now()
			WHERE id=$1 AND account_id=$2 RETURNING `+planCols, p.ID, accountID, p.ColumnID, days, min, p.Enabled))
		if errors.Is(err, pgx.ErrNoRows) {
			return p, ErrNotFound
		}
	}
	if err != nil {
		return p, err
	}
	s.Audit.Log(ctx, actor.ID, "plan.save", "", map[string]any{"plan_id": out.ID, "account_id": accountID, "enabled": out.Enabled})
	return out, nil
}

func (s *Service) DeletePlan(ctx context.Context, actor auth.User, accountID, planID int64) error {
	if err := s.ownAccount(ctx, actor, accountID); err != nil {
		return err
	}
	t, err := s.DB.Exec(ctx, `DELETE FROM write_plan WHERE id=$1 AND account_id=$2`, planID, accountID)
	if err == nil && t.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err == nil {
		s.Audit.Log(ctx, actor.ID, "plan.delete", "", map[string]any{"plan_id": planID, "account_id": accountID})
	}
	return err
}

// Schedule queues today's job for every enabled plan whose weekday is today and whose time has
// passed (Beijing time). Each plan runs at most once a day: the day is claimed before enqueueing,
// so concurrent workers can't double-queue. Quota and account problems are recorded on the plan.
// Missed earlier days are not caught up.
func (s *Service) Schedule(ctx context.Context) (int, error) {
	now := s.now().In(china)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	wd := int(now.Weekday())
	if wd == 0 {
		wd = 7
	}
	rows, err := s.DB.Query(ctx, `SELECT p.id, p.account_id, p.column_id, u.id, u.role FROM write_plan p
		JOIN users u ON u.id=p.user_id JOIN wx_account a ON a.id=p.account_id
		WHERE p.enabled AND $1 = ANY(p.weekdays) AND p.at_minute <= $2 AND (p.last_run_on IS NULL OR p.last_run_on < $3)
		  AND u.status='active' AND a.bind_status='bound' ORDER BY p.at_minute, p.id`, int16(wd), now.Hour()*60+now.Minute(), today)
	if err != nil {
		return 0, err
	}
	type due struct {
		id, acc, uid int64
		col          *int64
		role         string
	}
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.acc, &d.col, &d.uid, &d.role); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	queued := 0
	for _, d := range list {
		t, err := s.DB.Exec(ctx, `UPDATE write_plan SET last_run_on=$2 WHERE id=$1 AND (last_run_on IS NULL OR last_run_on < $2)`, d.id, today)
		if err != nil {
			return queued, err
		}
		if t.RowsAffected() == 0 {
			continue // another worker claimed it
		}
		day := today.Format("2006-01-02")
		jid, err := s.Enqueue(ctx, auth.User{ID: d.uid, Role: auth.Role(d.role)}, d.acc, EnqueueInput{ColumnID: d.col}, fmt.Sprintf("plan:%d:%s", d.id, day))
		result := fmt.Sprintf("%s 已排队，任务 %d", day, jid)
		if err != nil {
			result = day + " 未排队：" + planErr(err)
		} else {
			queued++
		}
		s.DB.Exec(ctx, `UPDATE write_plan SET last_result=$2 WHERE id=$1`, d.id, result)
	}
	return queued, nil
}

func planErr(err error) string {
	switch {
	case errors.Is(err, ErrQuota):
		return "今日额度已用完"
	case errors.Is(err, ErrNotFound):
		return "公众号或栏目不可用"
	case errors.Is(err, ErrForbidden):
		return "账号无权生成"
	}
	return "系统错误"
}
