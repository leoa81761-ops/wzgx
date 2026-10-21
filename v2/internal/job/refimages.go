package job

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"gonghao/internal/gen"
	"gonghao/internal/render"
	"gonghao/internal/safefetch"
	"gonghao/internal/server"
)

// Authorized reference images. The user gives an image URL and confirms they may use it; the
// picture is fetched with SSRF protection (see safefetch), re-encoded, and kept in the account's
// library (at most 30) or put straight into an article's cover or image slot. Generation uses a
// library picture as the cover when there is no generated one.

// RefStatement is what the user confirms; it is stored with each picture.
const RefStatement = "我确认拥有这张图片的使用权，或已获得权利人授权，可以在公众号文章中使用；因图片引起的版权纠纷由我负责。"

const maxRefImages = 30

type refImage struct {
	ID           int64      `json:"id"`
	URL          string     `json:"url"` // preview
	SourceURL    string     `json:"source_url"`
	Caption      string     `json:"caption"`
	Width        int        `json:"width"`
	Height       int        `json:"height"`
	AuthorizedAt time.Time  `json:"authorized_at"`
	Used         int        `json:"used"`
	LastUsedAt   *time.Time `json:"last_used_at"`
}

type refInput struct {
	URL        string `json:"url"`
	Caption    string `json:"caption"`
	Authorized bool   `json:"authorized"`
	RefID      *int64 `json:"ref_id"` // articles: a picture from the library instead of a URL
	Slot       *int   `json:"slot"`   // articles: 0 = cover, 1..3 = image slot
}

func (s *Service) fetcher() *safefetch.Fetcher {
	if s.Fetch != nil {
		return s.Fetch
	}
	return &safefetch.Fetcher{}
}

// fetchRef downloads and re-encodes a user-supplied picture.
func (s *Service) fetchRef(ctx context.Context, in refInput) ([]byte, int, int, error) {
	if !in.Authorized {
		return nil, 0, 0, ErrInvalid{"请先勾选授权声明：确认你有权使用这张图片"}
	}
	if utf8.RuneCountInString(in.Caption) > 60 {
		return nil, 0, 0, ErrInvalid{"图片说明不超过 60 字"}
	}
	raw, _, err := s.fetcher().Get(ctx, in.URL)
	if err != nil {
		return nil, 0, 0, ErrInvalid{err.Error()}
	}
	data, w, h, err := gen.NormalizeImage(raw)
	if err != nil {
		return nil, 0, 0, ErrInvalid{err.Error()}
	}
	if w < 200 || h < 100 {
		return nil, 0, 0, ErrInvalid{"图片太小（至少 200×100）"}
	}
	return data, w, h, nil
}

func newKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Service) hRefList(w http.ResponseWriter, r *http.Request) {
	accID := pid(r, "id")
	if err := s.ownAccount(r.Context(), actor(r), accID); err != nil {
		fail(w, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT id, key, source_url, caption, width, height, authorized_at, used, last_used_at
		FROM ref_image WHERE account_id=$1 ORDER BY id DESC`, accID)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	out := []refImage{}
	for rows.Next() {
		var x refImage
		var key string
		if err := rows.Scan(&x.ID, &key, &x.SourceURL, &x.Caption, &x.Width, &x.Height, &x.AuthorizedAt, &x.Used, &x.LastUsedAt); err != nil {
			fail(w, err)
			return
		}
		x.URL = render.MediaPrefix + key
		out = append(out, x)
	}
	server.WriteJSON(w, 200, map[string]any{"images": out, "statement": RefStatement})
}

func (s *Service) hRefAdd(w http.ResponseWriter, r *http.Request) {
	ctx, a := r.Context(), actor(r)
	accID := pid(r, "id")
	if err := s.ownAccount(ctx, a, accID); err != nil {
		fail(w, err)
		return
	}
	var in refInput
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		fail(w, ErrInvalid{"请求格式不正确"})
		return
	}
	var n int
	s.DB.QueryRow(ctx, `SELECT count(*) FROM ref_image WHERE account_id=$1`, accID).Scan(&n)
	if n >= maxRefImages {
		fail(w, ErrInvalid{fmt.Sprintf("每个公众号最多 %d 张授权图片，请先删掉一些", maxRefImages)})
		return
	}
	data, wd, ht, err := s.fetchRef(ctx, in)
	if err != nil {
		fail(w, err)
		return
	}
	var id int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO ref_image (key, user_id, account_id, source_url, caption, mime, data, width, height, statement, authorized_at)
		VALUES ($1,$2,$3,$4,$5,'image/jpeg',$6,$7,$8,$9,$10) RETURNING id`,
		newKey(), a.ID, accID, strings.TrimSpace(in.URL), strings.TrimSpace(in.Caption), data, wd, ht, RefStatement, s.now()).Scan(&id); err != nil {
		fail(w, err)
		return
	}
	s.Audit.Log(ctx, a.ID, "ref_image.add", fmt.Sprint(accID), map[string]any{"account_id": accID, "id": id, "source_url": in.URL, "authorized": true})
	server.WriteJSON(w, 200, map[string]any{"id": id})
}

func (s *Service) hRefDelete(w http.ResponseWriter, r *http.Request) {
	ctx, a := r.Context(), actor(r)
	accID := pid(r, "id")
	if err := s.ownAccount(ctx, a, accID); err != nil {
		fail(w, err)
		return
	}
	t, err := s.DB.Exec(ctx, `DELETE FROM ref_image WHERE id=$1 AND account_id=$2`, pid(r, "rid"), accID)
	if err != nil {
		fail(w, err)
		return
	}
	if t.RowsAffected() == 0 {
		fail(w, ErrNotFound)
		return
	}
	s.Audit.Log(ctx, a.ID, "ref_image.delete", fmt.Sprint(accID), map[string]any{"account_id": accID, "id": pid(r, "rid")})
	server.WriteJSON(w, 200, map[string]bool{"ok": true})
}

// hArticleRef puts an authorized picture into an article's cover (slot 0) or an image slot, then
// re-renders the article. Only for articles not yet in a WeChat draft.
func (s *Service) hArticleRef(w http.ResponseWriter, r *http.Request) {
	ctx, a := r.Context(), actor(r)
	artID := pid(r, "id")
	var accID, owner int64
	var status string
	var body []byte
	err := s.DB.QueryRow(ctx, `SELECT account_id, user_id, status, body_json FROM article WHERE id=$1`, artID).Scan(&accID, &owner, &status, &body)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != a.ID) {
		fail(w, ErrNotFound)
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	if status == "pushed" {
		fail(w, ErrInvalid{"这篇已经在公众号草稿箱，请在公众号后台换图"})
		return
	}
	var in refInput
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Slot == nil {
		fail(w, ErrInvalid{"请选择放在封面还是第几张配图"})
		return
	}
	var art render.Article
	json.Unmarshal(body, &art)
	if *in.Slot < 0 || *in.Slot > len(render.Slots(art)) {
		fail(w, ErrInvalid{fmt.Sprintf("这篇文章只有 %d 个配图位置", len(render.Slots(art)))})
		return
	}
	var data []byte
	var wd, ht int
	var refID any
	caption := strings.TrimSpace(in.Caption)
	if in.RefID != nil {
		err = s.DB.QueryRow(ctx, `SELECT data, width, height, caption FROM ref_image WHERE id=$1 AND account_id=$2`, *in.RefID, accID).Scan(&data, &wd, &ht, &caption)
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, ErrNotFound)
			return
		}
		refID = *in.RefID
	} else {
		data, wd, ht, err = s.fetchRef(ctx, in)
	}
	if err != nil {
		fail(w, err)
		return
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(ctx)
	tx.Exec(ctx, `DELETE FROM article_image WHERE article_id=$1 AND kind='picture' AND slot=$2`, artID, *in.Slot)
	if _, err := tx.Exec(ctx, `INSERT INTO article_image (key, article_id, slot, kind, mime, data, width, height, prompt, ref_id)
		VALUES ($1,$2,$3,'picture','image/jpeg',$4,$5,$6,$7,$8)`, newKey(), artID, *in.Slot, data, wd, ht, caption, refID); err != nil {
		fail(w, err)
		return
	}
	if refID != nil {
		tx.Exec(ctx, `UPDATE ref_image SET used=used+1, last_used_at=now() WHERE id=$1`, refID)
	}
	if err := tx.Commit(ctx); err != nil {
		fail(w, err)
		return
	}
	if *in.Slot > 0 && s.Render != nil {
		if h, err := s.Render.RenderStored(ctx, artID, ""); err == nil {
			s.DB.Exec(ctx, `UPDATE article SET html=$2, updated_at=now() WHERE id=$1`, artID, h)
		}
	}
	src := in.URL
	if refID != nil {
		src = fmt.Sprintf("library #%v", refID)
	}
	s.Audit.Log(ctx, a.ID, "article.ref_image", fmt.Sprint(artID), map[string]any{"slot": *in.Slot, "source_url": src, "authorized": true})
	server.WriteJSON(w, 200, map[string]bool{"ok": true})
}
