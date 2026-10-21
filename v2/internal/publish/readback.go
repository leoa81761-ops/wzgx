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

// Read-back: some time after a push we fetch the draft again to see whether the user edited it.
// Only reads; nothing in the draft box is ever changed. A draft that has been published or deleted
// can no longer be read (WeChat answers 40007); that is recorded as "gone".
const (
	defaultReadBackAfter = 2 * time.Hour  // first look
	defaultReadBackEvery = 12 * time.Hour // later looks
	readBackWindow       = 7 * 24 * time.Hour
	editedThreshold      = 0.01 // under 1 % changed text counts as untouched (spacing, a typo)
)

type ReadBackResult struct {
	Status string   `json:"readback_status"` // unedited | edited | gone | error
	Ratio  *float64 `json:"change_ratio"`
	Note   string   `json:"readback_note"`
}

// ReadBackDue checks pushed articles that are due; called from the worker loop.
func (s *Service) ReadBackDue(ctx context.Context) error {
	after, every := s.ReadBackAfter, s.ReadBackEvery
	if after <= 0 {
		after = defaultReadBackAfter
	}
	if every <= 0 {
		every = defaultReadBackEvery
	}
	now := s.now()
	rows, err := s.DB.Query(ctx, `SELECT id FROM article WHERE status='pushed' AND draft_media_id<>''
		AND pushed_at <= $1 AND pushed_at > $2 AND readback_status NOT IN ('gone','edited')
		AND (readback_at IS NULL OR readback_at <= $3) ORDER BY pushed_at LIMIT 20`, now.Add(-after), now.Add(-readBackWindow), now.Add(-every))
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
		if _, err := s.readBack(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// ReadBack (user, own article): check now.
func (s *Service) ReadBack(ctx context.Context, actor auth.User, articleID int64) (ReadBackResult, error) {
	var owner int64
	var status string
	err := s.DB.QueryRow(ctx, `SELECT user_id, status FROM article WHERE id=$1`, articleID).Scan(&owner, &status)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != actor.ID) {
		return ReadBackResult{}, ErrNotFound
	}
	if err != nil {
		return ReadBackResult{}, err
	}
	if status != "pushed" {
		return ReadBackResult{}, fmt.Errorf("%w：这篇还没有推送到草稿箱", ErrState)
	}
	return s.readBack(ctx, articleID)
}

func (s *Service) readBack(ctx context.Context, articleID int64) (ReadBackResult, error) {
	var accID int64
	var mediaID, title, pushed, prev string
	if err := s.DB.QueryRow(ctx, `SELECT account_id, draft_media_id, title, pushed_content, readback_status FROM article WHERE id=$1`, articleID).
		Scan(&accID, &mediaID, &title, &pushed, &prev); err != nil {
		return ReadBackResult{}, err
	}
	var a account
	if err := s.DB.QueryRow(ctx, `SELECT id, user_id, app_id, name, secret_cipher, bind_status FROM wx_account WHERE id=$1`, accID).
		Scan(&a.id, &a.userID, &a.appID, &a.name, &a.secretCipher, &a.bindStatus); err != nil {
		return ReadBackResult{}, err
	}
	res := ReadBackResult{}
	items, err := s.fetchDraft(ctx, a, mediaID)
	var ae *wechat.APIError
	switch {
	case errors.As(err, &ae) && (ae.Code == 40007 || ae.Code == 40008):
		res.Status, res.Note = "gone", "草稿箱里已经没有这篇（可能已发表或删除）"
	case err != nil:
		res.Status, res.Note = "error", "回读失败："+err.Error()
		if prev == "unedited" { // keep the last good answer
			res.Status = prev
		}
	case len(items) == 0:
		res.Status, res.Note = "gone", "草稿里没有文章"
	default:
		r := changeRatio(pushed, items[0].Content)
		res.Ratio = &r
		titleChanged := strings.TrimSpace(items[0].Title) != strings.TrimSpace(title)
		switch {
		case r >= editedThreshold || titleChanged:
			res.Status = "edited"
			if titleChanged {
				res.Note = "标题改过"
			}
		default:
			res.Status = "unedited"
		}
	}
	_, err = s.DB.Exec(ctx, `UPDATE article SET readback_status=$2, change_ratio=COALESCE($3, change_ratio), readback_note=$4, readback_at=$5 WHERE id=$1`,
		articleID, res.Status, res.Ratio, res.Note, s.now())
	return res, err
}

func (s *Service) fetchDraft(ctx context.Context, a account, mediaID string) ([]wechat.DraftItem, error) {
	tok, err := s.token(ctx, a, false)
	if err != nil {
		return nil, err
	}
	var items []wechat.DraftItem
	_, err = s.withRetry(ctx, a, &tok, func(t string) (string, error) {
		var e error
		items, e = s.WX.GetDraft(ctx, t, mediaID)
		return "", e
	})
	return items, err
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
