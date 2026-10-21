package gen

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// refCover: when an article has no generated cover, use a picture from the account's library of
// authorized reference images (the least used one). Without one the push falls back as before.
func (p *Pipeline) refCover(ctx context.Context, j Job, trace func(Step)) string {
	if p.DB == nil || j.AccountID == 0 {
		return ""
	}
	kb := make([]byte, 16)
	rand.Read(kb)
	key := hex.EncodeToString(kb)
	var jobID any
	if j.ID > 0 {
		jobID = j.ID
	}
	var refID int64
	err := p.DB.QueryRow(ctx, `WITH r AS (
			SELECT id, data, width, height, caption FROM ref_image WHERE account_id=$1
			ORDER BY used, last_used_at NULLS FIRST, id LIMIT 1)
		INSERT INTO article_image (key, job_id, slot, kind, mime, data, width, height, prompt, ref_id)
		SELECT $2, $3, 0, 'picture', 'image/jpeg', data, width, height, caption, id FROM r RETURNING ref_id`,
		j.AccountID, key, jobID).Scan(&refID)
	if err != nil {
		return "" // no library picture
	}
	p.DB.Exec(ctx, `UPDATE ref_image SET used=used+1, last_used_at=now() WHERE id=$1`, refID)
	trace(Step{Stage: "image_cover", Note: fmt.Sprintf("cover from the authorized library (#%d)", refID)})
	return key
}
