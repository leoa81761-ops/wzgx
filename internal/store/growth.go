package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/growth"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
)

type Referral struct {
	ID           int64
	InviterID    int64
	InviteeID    int64
	Status       string
	Reason       string
	DeviceHash   string
	IP           string
	ObserveUntil time.Time
	CreatedAt    time.Time
	ValidAt      *time.Time
}

type ReferralView struct {
	Referral
	InviterAccount string
	InviteeAccount string
}

type ReferralChange struct {
	InviterID int64
	InviteeID int64
	Status    string
	Reason    string
}

type TopicProposal struct {
	ID         int64
	PlanID     int64
	TopicTag   string
	Title      string
	Status     string
	ProposedAt time.Time
	ExecuteAt  time.Time
}

type HotRow struct {
	SourceName  string
	Vertical    string
	Title       string
	Summary     string
	SourceLabel string
	URL         string
	PublishedOn string
	Heat        int
	Reports     int
	FetchedAt   time.Time
}

func (s *Store) EnsureInviteCode(ctx context.Context, q db.Q, userID int64, deviceHash string) (string, error) {
	if q == nil {
		q = s.DB
	}
	var code, saved string
	err := q.QueryRow(ctx, `SELECT code, device_hash FROM user_invite WHERE user_id = ?`, userID).Scan(&code, &saved)
	if err == nil {
		if deviceHash != "" && saved == "" {
			if _, err := q.Exec(ctx, `UPDATE user_invite SET device_hash = ? WHERE user_id = ? AND device_hash = ''`, deviceHash, userID); err != nil {
				return "", err
			}
		}
		return code, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	for i := 0; i < 5; i++ {
		buf := make([]byte, 5)
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		code = hex.EncodeToString(buf)
		_, err = q.Exec(ctx, `INSERT INTO user_invite (user_id, code, device_hash) VALUES (?, ?, ?)`, userID, code, deviceHash)
		if err == nil {
			return code, nil
		}
		if !db.IsUnique(err) {
			return "", err
		}
	}
	return "", sql.ErrNoRows
}

func (s *Store) UserByInviteCode(ctx context.Context, q db.Q, code string) (int64, error) {
	if q == nil {
		q = s.DB
	}
	var id int64
	err := q.QueryRow(ctx, `SELECT user_id FROM user_invite WHERE code = ?`, code).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

func (s *Store) CountDeviceHash(ctx context.Context, q db.Q, hash string) (int, error) {
	if hash == "" {
		return 0, nil
	}
	if q == nil {
		q = s.DB
	}
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM user_invite WHERE device_hash = ?`, hash).Scan(&n)
	return n, err
}

func (s *Store) InsertReferral(ctx context.Context, q db.Q, row *Referral) error {
	if q == nil {
		q = s.DB
	}
	return q.QueryRow(ctx, `INSERT INTO referral (inviter_id, invitee_id, status, reason, device_hash, ip, observe_until, created_at, valid_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		row.InviterID, row.InviteeID, row.Status, row.Reason, row.DeviceHash, row.IP, timeReq(row.ObserveUntil), timeReq(row.CreatedAt), timeArg(row.ValidAt),
	).Scan(&row.ID)
}

func (s *Store) GetReferralByInvitee(ctx context.Context, q db.Q, inviteeID int64) (*Referral, error) {
	if q == nil {
		q = s.DB
	}
	row := q.QueryRow(ctx, `SELECT id, inviter_id, invitee_id, status, reason, device_hash, ip, observe_until, created_at, valid_at FROM referral WHERE invitee_id = ?`, inviteeID)
	item, err := scanReferral(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return item, err
}

func (s *Store) GetReferral(ctx context.Context, q db.Q, id int64) (*Referral, error) {
	if q == nil {
		q = s.DB
	}
	row := q.QueryRow(ctx, `SELECT id, inviter_id, invitee_id, status, reason, device_hash, ip, observe_until, created_at, valid_at FROM referral WHERE id = ?`, id)
	item, err := scanReferral(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return item, err
}

func scanReferral(row interface{ Scan(dest ...any) error }) (*Referral, error) {
	var item Referral
	var observe, created string
	var valid sql.NullString
	err := row.Scan(&item.ID, &item.InviterID, &item.InviteeID, &item.Status, &item.Reason, &item.DeviceHash, &item.IP, &observe, &created, &valid)
	if err != nil {
		return nil, err
	}
	item.ObserveUntil = mustTime(observe)
	item.CreatedAt = mustTime(created)
	item.ValidAt = parsePtr(valid)
	return &item, nil
}

func (s *Store) CountReferrals(ctx context.Context, q db.Q, inviterID int64, status string) (int, error) {
	if q == nil {
		q = s.DB
	}
	var n int
	var err error
	if status == "" {
		err = q.QueryRow(ctx, `SELECT COUNT(*) FROM referral WHERE inviter_id = ?`, inviterID).Scan(&n)
	} else {
		err = q.QueryRow(ctx, `SELECT COUNT(*) FROM referral WHERE inviter_id = ? AND status = ?`, inviterID, status).Scan(&n)
	}
	return n, err
}

func (s *Store) CountValidReferralsToday(ctx context.Context, q db.Q, inviterID int64, now time.Time) (int, error) {
	if q == nil {
		q = s.DB
	}
	start, end := shanghaiDayWindow(now)
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM referral WHERE inviter_id = ? AND status = ? AND valid_at >= ? AND valid_at < ?`, inviterID, growth.StatusValid, start, end).Scan(&n)
	return n, err
}

func (s *Store) CountIPPrefixToday(ctx context.Context, q db.Q, prefix string, now time.Time) (int, error) {
	if prefix == "" {
		return 0, nil
	}
	if q == nil {
		q = s.DB
	}
	start, end := shanghaiDayWindow(now)
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM referral WHERE ip = ? AND created_at >= ? AND created_at < ?`, prefix, start, end).Scan(&n)
	return n, err
}

func shanghaiDayWindow(now time.Time) (string, string) {
	local := schedule.ShanghaiNow(now)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, schedule.Shanghai)
	return schedule.Format(start), schedule.Format(start.Add(24 * time.Hour))
}

func (s *Store) CountSuccessDrafts(ctx context.Context, q db.Q, userID int64) (int, error) {
	if q == nil {
		q = s.DB
	}
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM article WHERE user_id = ? AND status IN ('in_draft', 'exported', 'held', 'publish_wait', 'publishing', 'published', 'publish_failed', 'publish_unknown')`, userID).Scan(&n)
	return n, err
}

func (s *Store) CountAccountBinds(ctx context.Context, q db.Q, userID int64) (active, total int, err error) {
	if q == nil {
		q = s.DB
	}
	if err = q.QueryRow(ctx, `SELECT COUNT(*) FROM wx_account WHERE user_id = ? AND bind_status = 'active'`, userID).Scan(&active); err != nil {
		return 0, 0, err
	}
	err = q.QueryRow(ctx, `SELECT COUNT(*) FROM wx_account WHERE user_id = ?`, userID).Scan(&total)
	return active, total, err
}

func (s *Store) ListPendingInvitees(ctx context.Context, q db.Q, limit int) ([]int64, error) {
	if q == nil {
		q = s.DB
	}
	if limit < 1 || limit > 200 {
		limit = 200
	}
	rows, err := q.Query(ctx, `SELECT invitee_id FROM referral WHERE status = ? ORDER BY id ASC LIMIT ?`, growth.StatusPending, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) SettleReferral(ctx context.Context, q db.Q, inviteeID int64, now time.Time) (*ReferralChange, error) {
	if q == nil {
		q = s.DB
	}
	ref, err := s.GetReferralByInvitee(ctx, q, inviteeID)
	if err != nil || ref == nil || ref.Status != growth.StatusPending {
		return nil, err
	}
	user, err := s.GetUser(ctx, q, inviteeID)
	if err != nil || user == nil {
		return nil, err
	}
	rules, err := growth.RulesFromSettings(func(key string) (string, error) {
		return s.GetSetting(ctx, q, key)
	})
	if err != nil {
		return nil, err
	}
	drafts, err := s.CountSuccessDrafts(ctx, q, inviteeID)
	if err != nil {
		return nil, err
	}
	active, total, err := s.CountAccountBinds(ctx, q, inviteeID)
	if err != nil {
		return nil, err
	}
	phoneReady := user.Phone != "" && !growth.VirtualPhone(user.Phone)
	observeDone := phoneReady && !now.Before(ref.ObserveUntil)
	validToday, err := s.CountValidReferralsToday(ctx, q, ref.InviterID, now)
	if err != nil {
		return nil, err
	}
	status, reason := growth.Advance(ref.Status, drafts, observeDone, total > 0 && active == 0, !user.IsActive, validToday, rules.DailyCap, rules.DraftsNeed)
	if status == ref.Status {
		return nil, nil
	}
	var validAt *time.Time
	if status == growth.StatusValid {
		stamp := now.UTC()
		validAt = &stamp
	}
	res, err := q.Exec(ctx, `UPDATE referral SET status = ?, reason = ?, valid_at = ? WHERE id = ? AND status = ?`, status, reason, timeArg(validAt), ref.ID, growth.StatusPending)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	return &ReferralChange{InviterID: ref.InviterID, InviteeID: inviteeID, Status: status, Reason: reason}, nil
}

func (s *Store) RevokeInvitee(ctx context.Context, q db.Q, inviteeID int64, reason string) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE referral SET status = ?, reason = ?, valid_at = NULL WHERE invitee_id = ? AND status IN (?, ?, ?)`,
		growth.StatusRevoked, reason, inviteeID, growth.StatusPending, growth.StatusValid, growth.StatusFrozen)
	return err
}

func (s *Store) SetReferralStatus(ctx context.Context, q db.Q, id int64, status, reason string, validAt *time.Time) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE referral SET status = ?, reason = ?, valid_at = ? WHERE id = ?`, status, reason, timeArg(validAt), id)
	return err
}

func (s *Store) ListReferrals(ctx context.Context, status string, limit int) ([]ReferralView, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	query := `SELECT r.id, r.inviter_id, r.invitee_id, r.status, r.reason, r.device_hash, r.ip, r.observe_until, r.created_at, r.valid_at,
		COALESCE(NULLIF(iu.phone, ''), iu.email), COALESCE(NULLIF(eu.phone, ''), eu.email)
		FROM referral r JOIN users iu ON iu.id = r.inviter_id JOIN users eu ON eu.id = r.invitee_id`
	args := []any{}
	if status != "" {
		query += ` WHERE r.status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY r.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReferralView
	for rows.Next() {
		var item ReferralView
		var observe, created string
		var valid sql.NullString
		if err := rows.Scan(&item.ID, &item.InviterID, &item.InviteeID, &item.Status, &item.Reason, &item.DeviceHash, &item.IP, &observe, &created, &valid, &item.InviterAccount, &item.InviteeAccount); err != nil {
			return nil, err
		}
		item.ObserveUntil = mustTime(observe)
		item.CreatedAt = mustTime(created)
		item.ValidAt = parsePtr(valid)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) CountReferralStatus(ctx context.Context, status string) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM referral WHERE status = ?`, status).Scan(&n)
	return n, err
}

func (s *Store) InsertProposal(ctx context.Context, q db.Q, row *TopicProposal) error {
	if q == nil {
		q = s.DB
	}
	return q.QueryRow(ctx, `INSERT INTO topic_proposal (plan_id, topic_tag, title, status, proposed_at, execute_at) VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		row.PlanID, row.TopicTag, row.Title, row.Status, timeReq(row.ProposedAt), timeReq(row.ExecuteAt),
	).Scan(&row.ID)
}

func (s *Store) OpenProposal(ctx context.Context, q db.Q, planID int64) (*TopicProposal, error) {
	if q == nil {
		q = s.DB
	}
	var item TopicProposal
	var proposed, execute string
	err := q.QueryRow(ctx, `SELECT id, plan_id, topic_tag, title, status, proposed_at, execute_at FROM topic_proposal WHERE plan_id = ? AND status IN ('pending', 'approved') ORDER BY id DESC LIMIT 1`, planID).
		Scan(&item.ID, &item.PlanID, &item.TopicTag, &item.Title, &item.Status, &proposed, &execute)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	item.ProposedAt = mustTime(proposed)
	item.ExecuteAt = mustTime(execute)
	return &item, nil
}

func (s *Store) UpdateProposalTopic(ctx context.Context, q db.Q, id int64, tag, title string) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE topic_proposal SET topic_tag = ?, title = ? WHERE id = ?`, tag, title, id)
	return err
}

func (s *Store) SetProposalStatus(ctx context.Context, q db.Q, id int64, status string) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE topic_proposal SET status = ? WHERE id = ?`, status, id)
	return err
}

func (s *Store) UseProposal(ctx context.Context, q db.Q, planID int64) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE topic_proposal SET status = 'used' WHERE plan_id = ? AND status IN ('pending', 'approved')`, planID)
	return err
}

func (s *Store) ListPlansAwaitingProposal(ctx context.Context, q db.Q, now, until string) ([]model.WritingPlan, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT `+planCols+` FROM writing_plan p
		WHERE p.status = 'active' AND p.confirm_mode = 1 AND p.next_run_at IS NOT NULL AND p.next_run_at > ? AND p.next_run_at <= ?
		AND NOT EXISTS (SELECT 1 FROM topic_proposal t WHERE t.plan_id = p.id AND t.status IN ('pending', 'approved'))
		ORDER BY p.next_run_at ASC`, now, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectPlans(rows)
}

func (s *Store) SetTopicLock(ctx context.Context, q db.Q, taskID int64, tag string) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO topic_lock (task_id, topic_tag) VALUES (?, ?)`, taskID, tag)
	return err
}

func (s *Store) GetTopicLock(ctx context.Context, q db.Q, taskID int64) (string, error) {
	if q == nil {
		q = s.DB
	}
	var tag string
	err := q.QueryRow(ctx, `SELECT topic_tag FROM topic_lock WHERE task_id = ?`, taskID).Scan(&tag)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return tag, err
}

func (s *Store) ListHotSources(ctx context.Context, q db.Q, enabledOnly bool) ([]model.ServiceConfig, error) {
	return s.listModels(ctx, q, "hot_source", enabledOnly)
}

func (s *Store) ReplaceHotItems(ctx context.Context, q db.Q, items []HotRow) error {
	if q == nil {
		q = s.DB
	}
	if _, err := q.Exec(ctx, `DELETE FROM hot_item`); err != nil {
		return err
	}
	for _, item := range items {
		if item.Reports < 1 {
			item.Reports = 1
		}
		if _, err := q.Exec(ctx, `INSERT INTO hot_item (source_name, vertical_key, title, summary, source_label, url, published_on, heat, reports, fetched_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.SourceName, item.Vertical, item.Title, item.Summary, item.SourceLabel, item.URL, item.PublishedOn, item.Heat, item.Reports, timeReq(item.FetchedAt)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListHotItems(ctx context.Context, q db.Q) ([]HotRow, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT source_name, vertical_key, title, summary, source_label, url, published_on, heat, reports, fetched_at FROM hot_item ORDER BY heat DESC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HotRow
	for rows.Next() {
		var item HotRow
		var fetched string
		if err := rows.Scan(&item.SourceName, &item.Vertical, &item.Title, &item.Summary, &item.SourceLabel, &item.URL, &item.PublishedOn, &item.Heat, &item.Reports, &fetched); err != nil {
			return nil, err
		}
		item.FetchedAt = mustTime(fetched)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) LatestHotFetch(ctx context.Context, q db.Q) (time.Time, bool, error) {
	if q == nil {
		q = s.DB
	}
	var raw sql.NullString
	if err := q.QueryRow(ctx, `SELECT MAX(fetched_at) FROM hot_item`).Scan(&raw); err != nil {
		return time.Time{}, false, err
	}
	if !raw.Valid || raw.String == "" {
		return time.Time{}, false, nil
	}
	return mustTime(raw.String), true, nil
}
