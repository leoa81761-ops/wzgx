package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	errFirstByte = errors.New("first byte timeout")
	errIdle      = errors.New("idle timeout")
	errTotal     = errors.New("total timeout")
)

func (g *Gateway) body(cfg Config, req Request) ([]byte, error) {
	m := map[string]any{}
	for k, v := range cfg.Params {
		m[k] = v
	}
	m["model"] = cfg.Model
	m["messages"] = req.Messages
	m["stream"] = true
	m["stream_options"] = map[string]any{"include_usage": true}
	if cfg.ReasoningEffort != "" {
		m["reasoning_effort"] = cfg.ReasoningEffort
	}
	if req.MaxTokens > 0 {
		m["max_tokens"] = req.MaxTokens
	}
	if req.JSON {
		m["response_format"] = map[string]string{"type": "json_object"}
	}
	return json.Marshal(m)
}

type chunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Citations []json.RawMessage `json:"citations"`
	Error     *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// watchdog cancels the attempt when a timer fires, remembering why.
type watchdog struct {
	mu     sync.Mutex
	cancel context.CancelCauseFunc
	timer  *time.Timer
	idle   time.Duration
	total  *time.Timer
}

func (w *watchdog) arm(d time.Duration, cause error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(d, func() { w.cancel(cause) })
}

func (w *watchdog) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
	}
	w.total.Stop()
}

func (g *Gateway) once(parent context.Context, cfg Config, req Request, left time.Duration) (Result, Attempt) {
	start := g.Now()
	res, a := g.stream(parent, cfg, req, left, start)
	a.TotalMs = g.Now().Sub(start).Milliseconds()
	return res, a
}

func (g *Gateway) stream(parent context.Context, cfg Config, req Request, left time.Duration, start time.Time) (Result, Attempt) {
	a := Attempt{ConfigID: cfg.ID, Model: cfg.Model, Effort: cfg.ReasoningEffort}

	total := cfg.Total
	if total <= 0 || total > left {
		total = left
	}
	firstByte, idle := cfg.FirstByte, cfg.Idle
	if firstByte <= 0 {
		firstByte = 30 * time.Second
	}
	if idle <= 0 {
		idle = 180 * time.Second
	}

	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	wd := &watchdog{cancel: cancel, idle: idle}
	wd.total = time.AfterFunc(total, func() { cancel(errTotal) })
	wd.arm(firstByte, errFirstByte)
	defer wd.stop()

	fail := func(o Outcome, err error) (Result, Attempt) {
		if cause := context.Cause(ctx); cause != nil && ctx.Err() != nil {
			switch {
			case errors.Is(cause, errFirstByte):
				o = TimeoutFirstByte
			case errors.Is(cause, errIdle):
				o = TimeoutIdle
			case errors.Is(cause, errTotal):
				o = TimeoutTotal
			case parent.Err() != nil:
				o = Cancelled
			}
			err = cause
		}
		a.Outcome = o
		if err != nil {
			a.Err = trimErr(err.Error())
		}
		return Result{}, a
	}

	body, err := g.body(cfg, req)
	if err != nil {
		return fail(NetError, err)
	}
	anth := cfg.Protocol == "anthropic"
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	if anth {
		if body, err = anthropicBody(cfg, req); err != nil {
			return fail(NetError, err)
		}
		endpoint = strings.TrimSuffix(strings.TrimRight(cfg.BaseURL, "/"), "/v1") + "/v1/messages"
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fail(NetError, err)
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "text/event-stream")
	if anth {
		hr.Header.Set("anthropic-version", "2023-06-01")
		if cfg.APIKey != "" {
			hr.Header.Set("x-api-key", cfg.APIKey)
		}
	} else if cfg.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := g.Client.Do(hr)
	if err != nil {
		return fail(NetError, err)
	}
	defer resp.Body.Close()
	a.HTTPStatus = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fail(HTTPError, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(b))))
	}

	var (
		text    strings.Builder
		res     = Result{ConfigID: cfg.ID, Model: cfg.Model, Effort: cfg.ReasoningEffort}
		gotByte bool
		done    bool
		seen    = map[string]bool{}
	)
	rd := bufio.NewReaderSize(resp.Body, 64<<10)
	for {
		line, err := rd.ReadBytes('\n')
		if len(line) > 0 {
			if !gotByte {
				gotByte = true
				a.FirstByteMs = g.Now().Sub(start).Milliseconds()
			}
			wd.arm(idle, errIdle) // any byte (keep-alive comment, reasoning delta) keeps the stream alive
			l := bytes.TrimSpace(line)
			if bytes.HasPrefix(l, []byte("data:")) {
				data := bytes.TrimSpace(l[5:])
				if string(data) == "[DONE]" {
					done = true
					break
				}
				if anth {
					ev, perr := parseAnthropic(data)
					if perr != nil {
						return fail(StreamBroken, perr)
					}
					text.WriteString(ev.text)
					if ev.model != "" {
						res.Model = ev.model
					}
					if ev.finish != "" {
						res.FinishReason = ev.finish
					}
					if ev.in > 0 {
						res.TokensIn = ev.in
					}
					if ev.out > 0 {
						res.TokensOut = ev.out
					}
					if ev.stop {
						done = true
						break
					}
					continue
				}
				var c chunk
				if json.Unmarshal(data, &c) == nil {
					if c.Error != nil {
						return fail(StreamBroken, errors.New("upstream error: "+c.Error.Message))
					}
					if c.Model != "" {
						res.Model = c.Model
					}
					for _, ch := range c.Choices {
						text.WriteString(ch.Delta.Content)
						if ch.FinishReason != nil && *ch.FinishReason != "" {
							res.FinishReason = *ch.FinishReason
						}
					}
					if c.Usage != nil {
						res.TokensIn, res.TokensOut = c.Usage.PromptTokens, c.Usage.CompletionTokens
					}
					for _, raw := range c.Citations {
						if u := citationURL(raw); u != "" && !seen[u] {
							seen[u] = true
							res.Citations = append(res.Citations, u)
						}
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) && ctx.Err() == nil {
				break
			}
			return fail(StreamBroken, err)
		}
	}
	a.Model, a.Finish, a.TokensIn, a.TokensOut = res.Model, res.FinishReason, res.TokensIn, res.TokensOut
	if !done && res.FinishReason == "" {
		return fail(StreamBroken, errors.New("stream ended without finish_reason or [DONE]"))
	}
	res.Text = text.String()
	if strings.TrimSpace(res.Text) == "" {
		a.Outcome = Empty
		return Result{}, a
	}
	a.Outcome = OK
	return res, a
}

// citationURL accepts either "https://..." or {"url": "..."}.
func citationURL(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return o.URL
	}
	return ""
}

func trimErr(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
