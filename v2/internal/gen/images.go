package gen

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"gonghao/internal/gateway"
	"gonghao/internal/render"
)

// Images: the system decides whether an article gets pictures and where. A small planning call
// reads the finished article (with the renderer's candidate image slots marked) and returns a
// cover description plus the slots worth a picture (none is fine). Pictures are generated on the
// image route, re-encoded as JPEG and stored in article_image; the HTML points at /api/media/{key}.
// Any failure only drops that picture: a missing cover falls back to the default cover at push.

const (
	maxImageWidth  = 1280
	maxStoredBytes = 900 << 10 // WeChat body images must stay under 1 MB
)

const imagePlanSystem = `你为一篇公众号文章安排配图。根据文章内容：
1. 写一段封面图的画面描述（横版 16:9）。
2. 看文中标出的【候选配图位置】，只挑真正需要图片帮助读者理解或看着舒服的位置，写出画面描述；不需要就不挑，可以一个都不挑。
画面描述用中文，具体写出画面里有什么、什么场景；画面里不要出现文字、字母、标志、水印，不画真实存在的名人。
只输出 JSON：{"cover":"封面画面描述","images":[{"slot":位置编号,"prompt":"画面描述"}]}`

// Imager generates pictures (*gateway.Gateway satisfies it).
type Imager interface {
	Image(ctx context.Context, cfgs []gateway.Config, req gateway.ImageRequest) (gateway.ImageResult, error)
}

type picture struct {
	slot   int
	prompt string
	key    string
}

// TierImages reports whether a plan tier gets AI pictures (admin setting on plan_tier).
func (p *Pipeline) TierImages(ctx context.Context, tier string) bool {
	var on bool
	err := p.DB.QueryRow(ctx, `SELECT ai_images FROM plan_tier WHERE key=$1`, tier).Scan(&on)
	if errors.Is(err, pgx.ErrNoRows) {
		return tier != "free"
	}
	return err == nil && on
}

// pictures plans and generates the article's pictures. It returns slot -> media key (0 = cover)
// and whether the article uses AI pictures at all. It never fails the article.
func (p *Pipeline) pictures(ctx context.Context, j Job, tier string, art render.Article,
	load func(string) ([]gateway.Config, error),
	chat func(stage string, cfgs []gateway.Config, sys, user string) (gateway.Result, error),
	trace func(Step)) (map[int]string, bool) {
	if !p.TierImages(ctx, tier) {
		return nil, false
	}
	ig, ok := p.GW.(Imager)
	if !ok {
		return nil, false
	}
	icfgs, err := load("image")
	if err != nil {
		trace(Step{Stage: "image", Note: "no image route: " + err.Error()})
		return nil, false
	}
	slots := render.Slots(art)
	plan := []picture{}
	cover := ""
	if tcfgs, err := load("topic"); err == nil {
		user := fmt.Sprintf("标题：%s\n摘要：%s\n\n%s", art.Title, art.Summary, render.Outline(art, slots))
		r, err := chat("image_prompt", tcfgs, imagePlanSystem, user)
		var out struct {
			Cover  string `json:"cover"`
			Images []struct {
				Slot   int    `json:"slot"`
				Prompt string `json:"prompt"`
			} `json:"images"`
		}
		if err == nil && decodeJSON(r.Text, &out) == nil {
			cover = clipRunes(out.Cover, 500)
			seen := map[int]bool{}
			for _, im := range out.Images {
				if im.Slot < 1 || im.Slot > len(slots) || seen[im.Slot] || strings.TrimSpace(im.Prompt) == "" {
					continue
				}
				seen[im.Slot] = true
				plan = append(plan, picture{slot: im.Slot, prompt: clipRunes(im.Prompt, 500)})
			}
		} else {
			trace(Step{Stage: "image_prompt", Note: "no usable picture plan; cover from the title"})
		}
	}
	if cover == "" {
		cover = clipRunes("公众号文章封面配图，画面里不要有文字。文章：《"+art.Title+"》"+art.Summary, 500)
	}
	plan = append([]picture{{slot: 0, prompt: cover}}, plan...)

	var wg sync.WaitGroup
	var mu sync.Mutex
	keys := map[int]string{}
	for _, pc := range plan {
		wg.Add(1)
		go func(pc picture) {
			defer wg.Done()
			stage, aspect := "image_inline", "4:3"
			if pc.slot == 0 {
				stage, aspect = "image_cover", "16:9"
			}
			res, err := ig.Image(ctx, icfgs, gateway.ImageRequest{Stage: stage, JobID: j.ID, Prompt: pc.prompt, Aspect: aspect})
			note := ""
			var key string
			if err == nil {
				key, err = p.storeImage(ctx, j, pc, res)
			}
			if err != nil {
				note = fmt.Sprintf("slot %d dropped: %v", pc.slot, err)
				var fe *gateway.FailedError
				if errors.As(err, &fe) && len(fe.Attempts) > 0 {
					note = fmt.Sprintf("slot %d dropped: %s", pc.slot, fe.Attempts[len(fe.Attempts)-1].Err)
				}
			} else {
				mu.Lock()
				keys[pc.slot] = key
				mu.Unlock()
				note = fmt.Sprintf("slot %d ok", pc.slot)
			}
			mu.Lock()
			trace(Step{Stage: stage, Note: note, Attempts: res.Attempts})
			mu.Unlock()
		}(pc)
	}
	wg.Wait()
	return keys, true
}

func (p *Pipeline) storeImage(ctx context.Context, j Job, pc picture, res gateway.ImageResult) (string, error) {
	data, w, h, err := NormalizeImage(res.Data)
	if err != nil {
		return "", err
	}
	kb := make([]byte, 16)
	rand.Read(kb)
	key := hex.EncodeToString(kb)
	var jobID any
	if j.ID > 0 {
		jobID = j.ID
	}
	_, err = p.DB.Exec(ctx, `INSERT INTO article_image (key, job_id, slot, mime, data, width, height, prompt, config_id)
		VALUES ($1,$2,$3,'image/jpeg',$4,$5,$6,$7,$8)`, key, jobID, pc.slot, data, w, h, pc.prompt, res.ConfigID)
	return key, err
}

// NormalizeImage decodes a JPEG/PNG/GIF, scales it to at most 1280 px wide and re-encodes it as a
// JPEG small enough for a WeChat body image.
func NormalizeImage(b []byte) ([]byte, int, int, error) {
	src, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("图片无法解码：%w", err)
	}
	sb := src.Bounds()
	w, h := sb.Dx(), sb.Dy()
	if w < 16 || h < 16 {
		return nil, 0, 0, errors.New("图片太小")
	}
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(rgba, rgba.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	draw.Draw(rgba, rgba.Bounds(), src, sb.Min, draw.Over)
	img := image.Image(rgba)
	if w > maxImageWidth {
		nh := h * maxImageWidth / w
		img = downscale(rgba, maxImageWidth, nh)
		w, h = maxImageWidth, nh
	}
	for _, q := range []int{88, 80, 70, 60, 50} {
		var out bytes.Buffer
		if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: q}); err != nil {
			return nil, 0, 0, err
		}
		if out.Len() <= maxStoredBytes {
			return out.Bytes(), w, h, nil
		}
	}
	return nil, 0, 0, errors.New("图片压缩后仍超过 900KB")
}

// downscale averages the source pixels that fall into each destination pixel (box filter).
func downscale(src *image.RGBA, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	for y := 0; y < h; y++ {
		y0, y1 := y*sh/h, max((y+1)*sh/h, y*sh/h+1)
		for x := 0; x < w; x++ {
			x0, x1 := x*sw/w, max((x+1)*sw/w, x*sw/w+1)
			var r, g, b, n int
			for yy := y0; yy < y1; yy++ {
				i := src.PixOffset(x0, yy)
				for xx := x0; xx < x1; xx++ {
					r, g, b, n = r+int(src.Pix[i]), g+int(src.Pix[i+1]), b+int(src.Pix[i+2]), n+1
					i += 4
				}
			}
			o := dst.PixOffset(x, y)
			dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = uint8(r/n), uint8(g/n), uint8(b/n), 255
		}
	}
	return dst
}

func clipRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// mediaURLs maps inline slots to their preview URLs.
func mediaURLs(keys map[int]string) map[int]string {
	out := map[int]string{}
	for slot, k := range keys {
		if slot > 0 {
			out[slot] = render.MediaPrefix + k
		}
	}
	return out
}
