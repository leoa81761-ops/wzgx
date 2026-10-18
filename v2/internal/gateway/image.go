package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Image generation over the OpenAI images API: POST {base}/images/generations, not streamed.
// The reply carries data[0].b64_json (possibly with a data: URL prefix) or data[0].url.

// ImageTimeout is the per-call limit (a config's total timeout is used when it is shorter).
var ImageTimeout = 180 * time.Second

const maxImageBytes = 20 << 20

type ImageRequest struct {
	Stage  string // recorded stage name, e.g. "image_cover"
	JobID  int64
	Prompt string
	Aspect string // e.g. "16:9"
}

type ImageResult struct {
	Data     []byte
	Mime     string
	ConfigID int64
	Model    string
	Attempts []Attempt
}

// Image tries each config once, in route order, until one returns an image.
func (g *Gateway) Image(ctx context.Context, cfgs []Config, req ImageRequest) (ImageResult, error) {
	if len(cfgs) == 0 {
		return ImageResult{}, ErrNoConfig
	}
	var out ImageResult
	for _, c := range cfgs {
		a := Attempt{ConfigID: c.ID, Model: c.Model, N: 1}
		start := time.Now()
		data, mime, model, status, err := g.imageCall(ctx, c, req)
		a.TotalMs, a.HTTPStatus = time.Since(start).Milliseconds(), status
		if model != "" {
			a.Model = model
		}
		switch {
		case err == nil:
			a.Outcome = OK
		case ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled):
			a.Outcome, a.Err = Cancelled, err.Error()
		case errors.Is(err, context.DeadlineExceeded):
			a.Outcome, a.Err = TimeoutTotal, err.Error()
		case status >= 400:
			a.Outcome, a.Err = HTTPError, err.Error()
		case status == 200:
			a.Outcome, a.Err = Empty, err.Error()
		default:
			a.Outcome, a.Err = NetError, err.Error()
		}
		out.Attempts = append(out.Attempts, a)
		if g.Rec != nil {
			g.Rec.Record(ctx, req.Stage, req.JobID, a)
		}
		if err == nil {
			out.Data, out.Mime, out.ConfigID, out.Model = data, mime, c.ID, a.Model
			return out, nil
		}
		if a.Outcome == Cancelled {
			break
		}
	}
	return out, &FailedError{Attempts: out.Attempts}
}

func (g *Gateway) imageCall(ctx context.Context, c Config, req ImageRequest) (data []byte, mime, model string, status int, err error) {
	limit := ImageTimeout
	if c.Total > 0 && c.Total < limit {
		limit = c.Total
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	body := map[string]any{}
	for k, v := range c.Params {
		body[k] = v
	}
	body["model"], body["prompt"], body["n"], body["response_format"] = c.Model, req.Prompt, 1, "b64_json"
	if req.Aspect != "" {
		body["aspect_ratio"] = req.Aspect
	}
	b, _ := json.Marshal(body)
	hr, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.BaseURL, "/")+"/images/generations", bytes.NewReader(b))
	if err != nil {
		return nil, "", "", 0, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := g.Client.Do(hr)
	if err != nil {
		return nil, "", "", 0, scrubErr(err, c.APIKey)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 3*maxImageBytes/2))
	if err != nil {
		return nil, "", "", resp.StatusCode, scrubErr(err, c.APIKey)
	}
	if resp.StatusCode >= 400 {
		return nil, "", "", resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, clip(scrub(string(raw), c.APIKey), 300))
	}
	var d struct {
		Model string `json:"model"`
		Data  []struct {
			B64  string `json:"b64_json"`
			URL  string `json:"url"`
			Mime string `json:"mime_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &d); err != nil || len(d.Data) == 0 {
		return nil, "", "", resp.StatusCode, errors.New("回复里没有图片")
	}
	it := d.Data[0]
	switch {
	case it.B64 != "":
		s := it.B64
		if strings.HasPrefix(s, "data:") {
			if i := strings.Index(s, ","); i >= 0 {
				if m := strings.TrimSuffix(strings.TrimPrefix(s[:i], "data:"), ";base64"); it.Mime == "" {
					it.Mime = m
				}
				s = s[i+1:]
			}
		}
		data, err = base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil {
			return nil, "", "", resp.StatusCode, errors.New("图片数据无法解码")
		}
	case it.URL != "":
		gr, _ := http.NewRequestWithContext(ctx, "GET", it.URL, nil)
		r2, err := g.Client.Do(gr)
		if err != nil {
			return nil, "", "", resp.StatusCode, fmt.Errorf("下载图片失败：%w", scrubErr(err, c.APIKey))
		}
		defer r2.Body.Close()
		if r2.StatusCode != 200 {
			return nil, "", "", resp.StatusCode, fmt.Errorf("下载图片失败：HTTP %d", r2.StatusCode)
		}
		if data, err = io.ReadAll(io.LimitReader(r2.Body, maxImageBytes+1)); err != nil {
			return nil, "", "", resp.StatusCode, err
		}
	default:
		return nil, "", "", resp.StatusCode, errors.New("回复里没有图片")
	}
	if len(data) == 0 || len(data) > maxImageBytes {
		return nil, "", "", resp.StatusCode, errors.New("图片为空或过大")
	}
	mime = http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return nil, "", "", resp.StatusCode, errors.New("回复的不是图片")
	}
	return data, mime, d.Model, resp.StatusCode, nil
}

func scrub(s, key string) string {
	if key != "" {
		s = strings.ReplaceAll(s, key, "***")
	}
	return s
}

func scrubErr(err error, key string) error {
	if key == "" || !strings.Contains(err.Error(), key) {
		return err
	}
	return errors.New(scrub(err.Error(), key))
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
