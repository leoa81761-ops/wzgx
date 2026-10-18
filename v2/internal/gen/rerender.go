package gen

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"gonghao/internal/render"
)

// RenderStored renders a saved article again from its stored structure with the given layout
// (empty = the article's account layout). Nothing is rewritten: same blocks, same cited official
// sources; the account's current opening, ending, signature and AI label are applied.
func (p *Pipeline) RenderStored(ctx context.Context, articleID int64, layout string) (string, error) {
	var accID int64
	var body []byte
	var accLayout string
	var aiImages bool
	err := p.DB.QueryRow(ctx, `SELECT a.account_id, a.body_json, w.layout, a.ai_images FROM article a JOIN wx_account w ON w.id=a.account_id WHERE a.id=$1`, articleID).
		Scan(&accID, &body, &accLayout, &aiImages)
	if err != nil {
		return "", err
	}
	if layout == "" {
		layout = accLayout
	}
	var art render.Article
	if err := json.Unmarshal(body, &art); err != nil {
		return "", err
	}
	var sig, opening, ending string
	var ai bool
	err = p.DB.QueryRow(ctx, `SELECT signature, opening, ending, ai_label FROM account_profile WHERE account_id=$1`, accID).Scan(&sig, &opening, &ending, &ai)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	srcs := map[int]render.Source{}
	rows, err := p.DB.Query(ctx, `SELECT n, kind, title, issuer, doc_number, COALESCE(to_char(published_on,'YYYY-MM-DD'),''), url FROM article_source WHERE article_id=$1`, articleID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var s render.Source
		if err := rows.Scan(&s.N, &s.Kind, &s.Title, &s.Issuer, &s.DocNumber, &s.Date, &s.URL); err != nil {
			return "", err
		}
		srcs[s.N] = s
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	pics, charts := map[int]string{}, map[int]string{}
	irows, err := p.DB.Query(ctx, `SELECT kind, slot, COALESCE(block,-1), key FROM article_image WHERE article_id=$1 AND (kind='chart' OR slot > 0)`, articleID)
	if err != nil {
		return "", err
	}
	for irows.Next() {
		var kind, key string
		var slot, block int
		if err := irows.Scan(&kind, &slot, &block, &key); err != nil {
			irows.Close()
			return "", err
		}
		if kind == "chart" {
			charts[block] = key
		} else {
			pics[slot] = key
		}
	}
	irows.Close()
	out := render.Render(art, render.Options{Opening: opening, Ending: ending, Signature: sig, AILabel: ai,
		DefaultImageURL: p.DefaultImageURL, Sources: srcs, Layout: layout, AIImages: aiImages, Images: mediaURLs(pics), Charts: chartURLs(charts)})
	return out.HTML, nil
}

// Rerender applies the account's layout to its articles that are not in a WeChat draft yet.
// Pushed articles are left alone (they are already in the user's draft box).
func (p *Pipeline) Rerender(ctx context.Context, accountID int64) (int, error) {
	var layout string
	if err := p.DB.QueryRow(ctx, `SELECT layout FROM wx_account WHERE id=$1`, accountID).Scan(&layout); err != nil {
		return 0, err
	}
	rows, err := p.DB.Query(ctx, `SELECT id FROM article WHERE account_id=$1 AND status<>'pushed' AND draft_media_id='' AND layout<>$2`, accountID, layout)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	n := 0
	for _, id := range ids {
		h, err := p.RenderStored(ctx, id, layout)
		if err != nil {
			return n, err
		}
		if _, err := p.DB.Exec(ctx, `UPDATE article SET html=$2, layout=$3, updated_at=now() WHERE id=$1 AND status<>'pushed' AND draft_media_id=''`, id, h, layout); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
