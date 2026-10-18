package publish

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"gonghao/internal/auth"
	"gonghao/internal/wechat"
)

// Auto-publish (verified accounts, off by default). After a draft push the article waits for the
// account's review window; the user can cancel it in that time. Then the worker reads the draft
// back (it must still be in the draft box) and calls freepublish/submit once, and polls
// freepublish/get for the result.
//
// Never twice: an article moves scheduled -> submitting by a conditional update, and
// publish_submitted_at is set (also conditionally) right before the one submit call. A submit whose
// outcome is unknown (connection broke) is marked failed and never retried.
// Nothing runs unless the process is started with V2_AUTOPUBLISH=on (Service.AutoPublish).

const (
	publishPollEvery   = time.Minute
	publishPollTimeout = 24 * time.Hour
	submitStuckAfter   = 30 * time.Minute
	draftCheckRetry    = 10 * time.Minute
)

var china = time.FixedZone("CST", 8*3600)

func chinaDay(t time.Time) time.Time {
	n := t.In(china)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, china)
}

// schedule is called after a successful draft push.
func (s *Service) schedule(ctx context.Context, articleID, accountID int64) {
	var on, verified bool
	var minutes int
	if err := s.DB.QueryRow(ctx, `SELECT auto_publish, verified, review_minutes FROM wx_account WHERE id=$1`, accountID).Scan(&on, &verified, &minutes); err != nil || !on || !verified {
		return
	}
	s.DB.Exec(ctx, `UPDATE article SET publish_status='scheduled', publish_at=$2, publish_note='' WHERE id=$1 AND publish_status=''`,
		articleID, s.now().Add(time.Duration(minutes)*time.Minute))
}

// CancelPublish (owner): stop a scheduled auto-publish during the review window.
func (s *Service) CancelPublish(ctx context.Context, actor auth.User, articleID int64) error {
	var owner int64
	err := s.DB.QueryRow(ctx, `SELECT user_id FROM article WHERE id=$1`, articleID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != actor.ID) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	tag, err := s.DB.Exec(ctx, `UPDATE article SET publish_status='cancelled', publish_note='你取消了这篇的自动发表' WHERE id=$1 AND publish_status='scheduled'`, articleID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w：这篇已经不在等待发表了", ErrState)
	}
	s.Audit.Log(ctx, actor.ID, "article.publish_cancel", fmt.Sprint(articleID), nil)
	return nil
}

// PlatformPublishPaused reads the admin's platform-wide switch.
func (s *Service) PlatformPublishPaused(ctx context.Context) bool {
	var v bool
	s.DB.QueryRow(ctx, `SELECT value::text = 'true' FROM platform_setting WHERE key='publish_paused'`).Scan(&v)
	return v
}

// PublishDue runs in the worker loop.
func (s *Service) PublishDue(ctx context.Context) error {
	if !s.AutoPublish {
		return nil
	}
	now := s.now()
	// a submit that never finished: outcome unknown, do not retry
	s.DB.Exec(ctx, `UPDATE article SET publish_status='failed', publish_note='提交发表时中断，结果不明，请到公众号后台查看；系统不会再次提交'
		WHERE publish_status='submitting' AND publish_submitted_at IS NOT NULL AND publish_submitted_at < $1`, now.Add(-submitStuckAfter))
	// claimed but never submitted (process stopped while checking the draft): safe to retry
	s.DB.Exec(ctx, `UPDATE article SET publish_status='scheduled' WHERE publish_status='submitting' AND publish_submitted_at IS NULL`)
	if err := s.pollPublishing(ctx); err != nil {
		return err
	}
	if s.PlatformPublishPaused(ctx) {
		return nil
	}
	rows, err := s.DB.Query(ctx, `SELECT ar.id, ar.account_id, a.auto_publish AND a.verified AND a.bind_status='bound', u.publish_paused OR u.status<>'active' OR u.delete_requested_at IS NOT NULL
		FROM article ar JOIN wx_account a ON a.id=ar.account_id JOIN users u ON u.id=ar.user_id
		WHERE ar.publish_status='scheduled' AND ar.publish_at <= $1 ORDER BY ar.publish_at LIMIT 20`, now)
	if err != nil {
		return err
	}
	type due struct {
		id, acc       int64
		allowed, held bool
	}
	var ds []due
	for rows.Next() {
		var d due
		rows.Scan(&d.id, &d.acc, &d.allowed, &d.held)
		ds = append(ds, d)
	}
	rows.Close()
	for _, d := range ds {
		switch {
		case !d.allowed:
			s.DB.Exec(ctx, `UPDATE article SET publish_status='cancelled', publish_note='公众号已关闭自动发表、未认证或已解绑' WHERE id=$1 AND publish_status='scheduled'`, d.id)
			continue
		case d.held:
			continue // user paused publishing: waits until resumed
		}
		if err := s.publishOne(ctx, d.id, d.acc); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) publishOne(ctx context.Context, articleID, accountID int64) error {
	now := s.now()
	var cap, done int
	if err := s.DB.QueryRow(ctx, `SELECT a.publish_cap, (SELECT count(*) FROM article x WHERE x.account_id=a.id AND x.publish_submitted_at >= $2)
		FROM wx_account a WHERE a.id=$1`, accountID, chinaDay(now)).Scan(&cap, &done); err != nil {
		return err
	}
	if done >= cap {
		next := chinaDay(now).Add(24*time.Hour + 8*time.Hour)
		_, err := s.DB.Exec(ctx, `UPDATE article SET publish_at=$2, publish_note=$3 WHERE id=$1 AND publish_status='scheduled'`,
			articleID, next, fmt.Sprintf("今天已自动发表 %d 篇（上限 %d 篇），明天 8 点后发表", done, cap))
		return err
	}
	// claim
	tag, err := s.DB.Exec(ctx, `UPDATE article SET publish_status='submitting' WHERE id=$1 AND publish_status='scheduled' AND publish_submitted_at IS NULL`, articleID)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	var mediaID string
	var a account
	if err := s.DB.QueryRow(ctx, `SELECT ar.draft_media_id, a.id, a.user_id, a.app_id, a.name, a.secret_cipher, a.bind_status
		FROM article ar JOIN wx_account a ON a.id=ar.account_id WHERE ar.id=$1`, articleID).
		Scan(&mediaID, &a.id, &a.userID, &a.appID, &a.name, &a.secretCipher, &a.bindStatus); err != nil {
		return err
	}
	fail := func(note string) error {
		_, err := s.DB.Exec(ctx, `UPDATE article SET publish_status='failed', publish_note=$2 WHERE id=$1`, articleID, note)
		s.Audit.Log(ctx, 0, "article.publish_failed", fmt.Sprint(articleID), map[string]any{"account_id": a.id, "reason": note})
		return err
	}
	// 1. the draft must still be there (the user may have published or deleted it by hand)
	items, err := s.fetchDraft(ctx, a, mediaID)
	var ae *wechat.APIError
	switch {
	case errors.As(err, &ae) && (ae.Code == 40007 || ae.Code == 40008), err == nil && len(items) == 0:
		return fail("草稿箱里已经没有这篇（可能已手动发表或删除），不再自动发表")
	case err != nil: // nothing was submitted: safe to try again later
		_, e := s.DB.Exec(ctx, `UPDATE article SET publish_status='scheduled', publish_at=$2, publish_note=$3 WHERE id=$1 AND publish_submitted_at IS NULL`,
			articleID, now.Add(draftCheckRetry), "检查草稿失败，稍后重试："+err.Error())
		return e
	}
	// 2. submit, exactly once
	tag, err = s.DB.Exec(ctx, `UPDATE article SET publish_submitted_at=$2 WHERE id=$1 AND publish_status='submitting' AND publish_submitted_at IS NULL`, articleID, now)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	tok, err := s.token(ctx, a, false)
	if err != nil {
		return fail("取得接口凭证失败：" + err.Error())
	}
	pid, err := s.withRetry(ctx, a, &tok, func(t string) (string, error) { return s.WX.SubmitPublish(ctx, t, mediaID) })
	if err != nil {
		if errors.As(err, &ae) {
			note := "微信拒绝发表：" + err.Error()
			if h := ae.Hint(); h != "" {
				note += "（" + h + "）"
			}
			return fail(note)
		}
		return fail("提交发表时连接中断，结果不明，请到公众号后台查看；系统不会再次提交")
	}
	if _, err := s.DB.Exec(ctx, `UPDATE article SET publish_status='publishing', publish_id=$2, publish_note='已提交，等待微信处理' WHERE id=$1`, articleID, pid); err != nil {
		return err
	}
	s.Audit.Log(ctx, 0, "article.publish_submit", fmt.Sprint(articleID), map[string]any{"account_id": a.id, "publish_id": pid})
	return nil
}

var publishFail = map[int]string{
	2: "原创校验未通过", 3: "发表失败", 4: "平台审核不通过", 5: "发表后被删除", 6: "发表后被封禁",
}

func (s *Service) pollPublishing(ctx context.Context) error {
	now := s.now()
	rows, err := s.DB.Query(ctx, `SELECT ar.id, ar.publish_id, ar.publish_submitted_at, a.id, a.user_id, a.app_id, a.name, a.secret_cipher, a.bind_status
		FROM article ar JOIN wx_account a ON a.id=ar.account_id
		WHERE ar.publish_status='publishing' AND (ar.publish_checked_at IS NULL OR ar.publish_checked_at <= $1) ORDER BY ar.publish_submitted_at LIMIT 20`, now.Add(-publishPollEvery))
	if err != nil {
		return err
	}
	type item struct {
		id        int64
		pid       string
		submitted time.Time
		a         account
	}
	var its []item
	for rows.Next() {
		var x item
		rows.Scan(&x.id, &x.pid, &x.submitted, &x.a.id, &x.a.userID, &x.a.appID, &x.a.name, &x.a.secretCipher, &x.a.bindStatus)
		its = append(its, x)
	}
	rows.Close()
	for _, x := range its {
		tok, err := s.token(ctx, x.a, false)
		var st wechat.PublishStatus
		if err == nil {
			_, err = s.withRetry(ctx, x.a, &tok, func(t string) (string, error) {
				var e error
				st, e = s.WX.GetPublish(ctx, t, x.pid)
				return "", e
			})
		}
		switch {
		case err != nil && now.Sub(x.submitted) > publishPollTimeout:
			s.DB.Exec(ctx, `UPDATE article SET publish_status='failed', publish_note=$2, publish_checked_at=$3 WHERE id=$1`, x.id, "超过 24 小时查不到发表结果："+err.Error(), now)
		case err != nil:
			s.DB.Exec(ctx, `UPDATE article SET publish_checked_at=$2, publish_note=$3 WHERE id=$1`, x.id, now, "查询发表结果失败，稍后再查："+err.Error())
		case st.Status == 0:
			url := ""
			if len(st.URLs) > 0 {
				url = st.URLs[0]
			}
			s.DB.Exec(ctx, `UPDATE article SET publish_status='published', publish_url=$2, published_at=$3, publish_checked_at=$3, publish_note='已发表' WHERE id=$1`, x.id, url, now)
			s.Audit.Log(ctx, 0, "article.published", fmt.Sprint(x.id), map[string]any{"account_id": x.a.id, "publish_id": x.pid})
		case st.Status == 1 && now.Sub(x.submitted) > publishPollTimeout:
			s.DB.Exec(ctx, `UPDATE article SET publish_status='failed', publish_note='超过 24 小时仍在发表中，请到公众号后台查看', publish_checked_at=$2 WHERE id=$1`, x.id, now)
		case st.Status == 1:
			s.DB.Exec(ctx, `UPDATE article SET publish_checked_at=$2 WHERE id=$1`, x.id, now)
		default:
			note := publishFail[st.Status]
			if note == "" {
				note = fmt.Sprintf("发表失败（状态 %d）", st.Status)
			}
			if len(st.FailIdx) > 0 {
				note += fmt.Sprintf("，第 %s 篇", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(st.FailIdx)), "、"), "[]"))
			}
			s.DB.Exec(ctx, `UPDATE article SET publish_status='failed', publish_note=$2, publish_checked_at=$3 WHERE id=$1`, x.id, note, now)
			s.Audit.Log(ctx, 0, "article.publish_failed", fmt.Sprint(x.id), map[string]any{"account_id": x.a.id, "reason": note})
		}
	}
	return nil
}

// SetPublishPaused (user): one switch that holds every auto-publish of this user.
func (s *Service) SetPublishPaused(ctx context.Context, actor auth.User, paused bool) error {
	if !actor.Role.CanBindAccounts() {
		return ErrNotFound
	}
	if _, err := s.DB.Exec(ctx, `UPDATE users SET publish_paused=$2 WHERE id=$1`, actor.ID, paused); err != nil {
		return err
	}
	s.Audit.Log(ctx, actor.ID, map[bool]string{true: "publish.pause", false: "publish.resume"}[paused], "", nil)
	return nil
}

// SetPlatformPublishPaused (staff): the platform-wide switch.
func (s *Service) SetPlatformPublishPaused(ctx context.Context, actor auth.User, paused bool) error {
	if !actor.Role.IsStaff() {
		return ErrNotFound
	}
	v := "false"
	if paused {
		v = "true"
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO platform_setting (key, value, updated_by) VALUES ('publish_paused', $1::jsonb, $2)
		ON CONFLICT (key) DO UPDATE SET value=$1::jsonb, updated_by=$2, updated_at=now()`, v, actor.ID); err != nil {
		return err
	}
	s.Audit.Log(ctx, actor.ID, map[bool]string{true: "publish.platform_pause", false: "publish.platform_resume"}[paused], "", nil)
	return nil
}
