// Package publish runs the WeChat bind check and pushes a ready article into the account's draft
// box. Both happen only when a person asks for them; nothing here is called by the worker.
package publish

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"gonghao/internal/audit"
	"gonghao/internal/auth"
	"gonghao/internal/crypto"
	"gonghao/internal/wechat"
)

var (
	ErrNotFound = errors.New("不存在")
	ErrState    = errors.New("状态不允许")
)

type Service struct {
	DB    *pgxpool.Pool
	Box   *crypto.Box
	WX    *wechat.Client
	Audit audit.Logger
	Fetch *http.Client // downloads article images for upload; default 15s timeout
}

type account struct {
	id, userID   int64
	appID, name  string
	secretCipher string
	bindStatus   string
}

func (s *Service) loadAccount(ctx context.Context, actor auth.User, id int64) (account, error) {
	var a account
	err := s.DB.QueryRow(ctx, `SELECT id, user_id, app_id, name, secret_cipher, bind_status FROM wx_account WHERE id=$1`, id).
		Scan(&a.id, &a.userID, &a.appID, &a.name, &a.secretCipher, &a.bindStatus)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !actor.Role.IsStaff() && a.userID != actor.ID) {
		return a, ErrNotFound
	}
	return a, err
}

func (s *Service) token(ctx context.Context, a account, force bool) (string, error) {
	secret, err := s.Box.Open(a.secretCipher)
	if err != nil {
		return "", errors.New("AppSecret 无法解密")
	}
	return s.WX.Token(ctx, a.appID, secret, force)
}

// Report is the stored bind-check result (wx_account.check_report).
type Report struct {
	OK      bool      `json:"ok"`
	Code    int       `json:"errcode,omitempty"`
	Message string    `json:"message"`
	Hint    string    `json:"hint,omitempty"`
	At      time.Time `json:"checked_at"`
}

// Check fetches a fresh access token with the stored AppSecret and records the result.
func (s *Service) Check(ctx context.Context, actor auth.User, accountID int64) (Report, error) {
	a, err := s.loadAccount(ctx, actor, accountID)
	if err != nil {
		return Report{}, err
	}
	if a.bindStatus != "bound" {
		return Report{}, fmt.Errorf("%w：公众号已解绑", ErrState)
	}
	rep := Report{OK: true, Message: "AppID 和 AppSecret 有效，已取得接口凭证", At: time.Now()}
	if _, err := s.token(ctx, a, true); err != nil {
		rep = Report{Message: err.Error(), At: rep.At}
		var ae *wechat.APIError
		if errors.As(err, &ae) {
			rep.Code, rep.Hint = ae.Code, ae.Hint()
		}
	}
	if _, err := s.DB.Exec(ctx, `UPDATE wx_account SET check_report=$2, checked_at=now(), updated_at=now() WHERE id=$1`, a.id, rep); err != nil {
		return rep, err
	}
	s.Audit.Log(ctx, actor.ID, "account.check", a.appID, map[string]any{"account_id": a.id, "ok": rep.OK, "errcode": rep.Code})
	return rep, nil
}

var imgSrc = regexp.MustCompile(`<img\b[^>]*?\bsrc="([^"]+)"`)

// Push uploads the article's images and creates a WeChat draft. Only the owning user may push,
// only a ready article, and only once.
func (s *Service) Push(ctx context.Context, actor auth.User, articleID int64) (string, error) {
	var accID, owner int64
	var title, summary, html, status string
	err := s.DB.QueryRow(ctx, `SELECT account_id, user_id, title, summary, html, status FROM article WHERE id=$1`, articleID).
		Scan(&accID, &owner, &title, &summary, &html, &status)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != actor.ID) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	switch status {
	case "ready":
	case "pushed":
		return "", fmt.Errorf("%w：这篇已经推送到草稿箱", ErrState)
	default:
		return "", fmt.Errorf("%w：只有通过检查的文章可以推送", ErrState)
	}
	if n := len([]rune(title)); n == 0 || n > 64 {
		return "", fmt.Errorf("%w：标题需要 1 到 64 个字（现在 %d 个）", ErrState, n)
	}
	a, err := s.loadAccount(ctx, actor, accID)
	if err != nil {
		return "", err
	}
	if a.bindStatus != "bound" {
		return "", fmt.Errorf("%w：公众号已解绑", ErrState)
	}
	mediaID, err := s.push(ctx, a, title, summary, html)
	if err != nil {
		msg := err.Error()
		var ae *wechat.APIError
		if errors.As(err, &ae) && ae.Hint() != "" {
			msg += "（" + ae.Hint() + "）"
		}
		s.DB.Exec(ctx, `UPDATE article SET push_error=$2, updated_at=now() WHERE id=$1`, articleID, msg)
		s.Audit.Log(ctx, actor.ID, "article.push_failed", fmt.Sprint(articleID), map[string]any{"account_id": a.id})
		return "", errors.New(msg)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE article SET status='pushed', draft_media_id=$2, push_error='', pushed_at=now(), updated_at=now() WHERE id=$1`,
		articleID, mediaID); err != nil {
		return "", err
	}
	s.Audit.Log(ctx, actor.ID, "article.push", fmt.Sprint(articleID), map[string]any{"account_id": a.id, "media_id": mediaID})
	return mediaID, nil
}

func (s *Service) push(ctx context.Context, a account, title, summary, html string) (string, error) {
	tok, err := s.token(ctx, a, false)
	if err != nil {
		return "", err
	}
	var thumb []byte
	var thumbName string
	uploaded := map[string]string{}
	for _, m := range imgSrc.FindAllStringSubmatch(html, -1) {
		src := m[1]
		if _, done := uploaded[src]; done {
			continue
		}
		img, name, err := s.download(ctx, src)
		if err != nil {
			return "", fmt.Errorf("下载文中图片失败：%w", err)
		}
		if thumb == nil {
			thumb, thumbName = img, name
		}
		u, err := s.withRetry(ctx, a, &tok, func(t string) (string, error) { return s.WX.UploadContentImage(ctx, t, name, img) })
		if err != nil {
			return "", err
		}
		uploaded[src] = u
	}
	if thumb == nil {
		return "", errors.New("文章没有图片，无法设置封面")
	}
	for src, u := range uploaded {
		html = strings.ReplaceAll(html, `src="`+src+`"`, `src="`+u+`"`)
	}
	thumbID, err := s.withRetry(ctx, a, &tok, func(t string) (string, error) { return s.WX.UploadThumb(ctx, t, thumbName, thumb) })
	if err != nil {
		return "", err
	}
	digest := []rune(strings.TrimSpace(summary))
	if len(digest) > 120 {
		digest = digest[:120]
	}
	return s.withRetry(ctx, a, &tok, func(t string) (string, error) {
		return s.WX.AddDraft(ctx, t, wechat.Draft{Title: title, Digest: string(digest), Content: html, ThumbMediaID: thumbID})
	})
}

// withRetry refreshes the token once when WeChat says it is invalid or expired.
func (s *Service) withRetry(ctx context.Context, a account, tok *string, f func(string) (string, error)) (string, error) {
	out, err := f(*tok)
	var ae *wechat.APIError
	if errors.As(err, &ae) && (ae.Code == 40001 || ae.Code == 42001 || ae.Code == 40014) {
		s.WX.Forget(a.appID)
		if *tok, err = s.token(ctx, a, true); err != nil {
			return "", err
		}
		return f(*tok)
	}
	return out, err
}

const maxImage = 2 << 20

func (s *Service) download(ctx context.Context, src string) ([]byte, string, error) {
	u, err := url.Parse(src)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, "", errors.New("图片地址无效")
	}
	c := s.Fetch
	if c == nil {
		c = &http.Client{Timeout: 15 * time.Second}
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", src, nil)
	resp, err := c.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	ext := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif"}[strings.Split(ct, ";")[0]]
	if ext == "" {
		return nil, "", fmt.Errorf("不支持的图片类型 %q", ct)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxImage+1))
	if err != nil {
		return nil, "", err
	}
	if len(b) > maxImage {
		return nil, "", errors.New("图片超过 2MB")
	}
	name := strings.TrimSuffix(path.Base(u.Path), path.Ext(u.Path))
	if name == "" || name == "." || name == "/" {
		name = "image"
	}
	return b, name + ext, nil
}
