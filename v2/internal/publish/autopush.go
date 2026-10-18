package publish

import (
	"context"
	"time"

	"gonghao/internal/auth"
)

// AutoPushDue (worker loop): push articles written by a plan that passed the checks, for accounts
// with 自动推送草稿箱 on. One attempt per article (claimed first); a failure is kept as the article's
// push_error and the user can push again by hand. Auto-publish, if on, follows as for a manual push.
func (s *Service) AutoPushDue(ctx context.Context) error {
	rows, err := s.DB.Query(ctx, `SELECT ar.id, ar.user_id FROM article ar
		JOIN job j ON j.id=ar.job_id JOIN wx_account a ON a.id=ar.account_id JOIN users u ON u.id=ar.user_id
		WHERE ar.status='ready' AND ar.auto_push_at IS NULL AND j.source='plan' AND a.auto_push AND a.bind_status='bound'
		  AND NOT a.paused AND u.status='active' AND u.delete_requested_at IS NULL AND ar.created_at > $1
		ORDER BY ar.id LIMIT 10`, s.now().Add(-24*time.Hour))
	if err != nil {
		return err
	}
	type due struct{ id, uid int64 }
	var ds []due
	for rows.Next() {
		var d due
		rows.Scan(&d.id, &d.uid)
		ds = append(ds, d)
	}
	rows.Close()
	for _, d := range ds {
		t, err := s.DB.Exec(ctx, `UPDATE article SET auto_push_at=$2 WHERE id=$1 AND auto_push_at IS NULL`, d.id, s.now())
		if err != nil {
			return err
		}
		if t.RowsAffected() == 0 {
			continue
		}
		if _, err := s.Push(ctx, auth.User{ID: d.uid, Role: auth.RoleUser}, d.id); err != nil {
			s.Audit.Log(ctx, d.uid, "article.auto_push_failed", "", map[string]any{"article_id": d.id})
		}
	}
	return nil
}
