package gen

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image/png"

	"gonghao/internal/chart"
	"gonghao/internal/render"
)

// charts draws a bar chart for each numeric comparison table the renderer picks (at most two)
// and stores it. It returns block -> media key; a chart that fails is skipped.
func (p *Pipeline) charts(ctx context.Context, j Job, art render.Article, trace func(Step)) map[int]string {
	specs := render.ChartTables(art)
	if len(specs) == 0 {
		return nil
	}
	out := map[int]string{}
	for _, sp := range specs {
		b, err := chart.Bar(sp)
		if err == nil {
			var key string
			if key, err = p.storeChart(ctx, j, sp, b); err == nil {
				out[sp.Block] = key
				trace(Step{Stage: "chart", Note: fmt.Sprintf("block %d: %s", sp.Block, sp.Title)})
				continue
			}
		}
		trace(Step{Stage: "chart", Note: fmt.Sprintf("block %d skipped: %v", sp.Block, err)})
	}
	return out
}

func (p *Pipeline) storeChart(ctx context.Context, j Job, sp render.ChartSpec, b []byte) (string, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	if len(b) > maxStoredBytes {
		return "", fmt.Errorf("图表图片过大（%d KB）", len(b)/1024)
	}
	kb := make([]byte, 16)
	rand.Read(kb)
	key := hex.EncodeToString(kb)
	var jobID any
	if j.ID > 0 {
		jobID = j.ID
	}
	_, err = p.DB.Exec(ctx, `INSERT INTO article_image (key, job_id, slot, kind, block, mime, data, width, height, prompt)
		VALUES ($1,$2,0,'chart',$3,'image/png',$4,$5,$6,$7)`, key, jobID, sp.Block, b, cfg.Width, cfg.Height, sp.Title)
	return key, err
}

func chartURLs(keys map[int]string) map[int]string {
	out := map[int]string{}
	for b, k := range keys {
		out[b] = render.MediaPrefix + k
	}
	return out
}
