// Package gateway calls OpenAI-compatible chat endpoints configured in the admin console.
//
// Every call streams. Each config has three independent timeouts: first byte, idle (gap between
// any two received lines, including keep-alives and reasoning deltas) and total. There is no
// per-request "2 minute" cap. Reasoning effort is passed through from the config untouched, and
// no model name appears in code: model, endpoint and effort all come from Config.
//
// Retry policy (Leo, D13):
//   - empty reply (HTTP 200, no text): same config up to 4 attempts, waiting 3s, 6s, 12s; then the next config
//   - 429 / 5xx: one more try on the same config after the first wait; then the next config
//   - timeout, broken stream, other 4xx: the next config immediately (partial text is discarded)
//   - never wait or start an attempt past the overall budget
package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Config struct {
	ID              int64
	Name            string // display only
	BaseURL         string // e.g. https://host/v1
	APIKey          string // decrypted just before the call; never logged
	Model           string
	ReasoningEffort string         // "" = not sent
	Params          map[string]any // extra request fields (temperature, ...); cannot override model/messages/stream
	FirstByte       time.Duration
	Idle            time.Duration
	Total           time.Duration
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Request struct {
	Stage     string // topic / research / write / review (for logs and model_call rows)
	JobID     int64
	Messages  []Message
	MaxTokens int  // 0 = not sent
	JSON      bool // ask for response_format json_object
}

type Outcome string

const (
	OK               Outcome = "ok"
	Empty            Outcome = "empty"
	TimeoutFirstByte Outcome = "timeout_first_byte"
	TimeoutIdle      Outcome = "timeout_idle"
	TimeoutTotal     Outcome = "timeout_total"
	StreamBroken     Outcome = "stream_broken"
	HTTPError        Outcome = "http_error"
	NetError         Outcome = "error"
	Cancelled        Outcome = "cancelled"
)

// Attempt is one HTTP call. Every attempt is recorded (model_call row).
type Attempt struct {
	ConfigID    int64         `json:"config_id"`
	Model       string        `json:"model"` // the model the upstream reported, else the configured one
	Effort      string        `json:"effort"`
	N           int           `json:"n"` // attempt number on this config, from 1
	Outcome     Outcome       `json:"outcome"`
	HTTPStatus  int           `json:"http_status,omitempty"`
	FirstByteMs int64         `json:"first_byte_ms,omitempty"`
	TotalMs     int64         `json:"total_ms"`
	Finish      string        `json:"finish_reason,omitempty"`
	TokensIn    int           `json:"tokens_in,omitempty"`
	TokensOut   int           `json:"tokens_out,omitempty"`
	WaitBefore  time.Duration `json:"wait_before,omitempty"`
	Err         string        `json:"error,omitempty"`
}

type Result struct {
	Text         string
	FinishReason string // "stop", "length", ... gates decide what to do with "length"
	ConfigID     int64
	Model        string
	Effort       string
	TokensIn     int
	TokensOut    int
	Citations    []string // URLs returned by search-capable models, if any
	Attempts     []Attempt
}

// Recorder persists attempts. May be nil.
type Recorder interface {
	Record(ctx context.Context, stage string, jobID int64, a Attempt)
}

type Gateway struct {
	Client *http.Client
	Rec    Recorder
	// EmptyWaits are the waits between empty-reply attempts on one config; len+1 = attempts.
	EmptyWaits []time.Duration
	// MinCall is the least time an attempt is worth starting with.
	MinCall time.Duration
	Now     func() time.Time
	Sleep   func(ctx context.Context, d time.Duration) error
}

func New(rec Recorder) *Gateway {
	return &Gateway{
		Client:     &http.Client{}, // no client-wide timeout: per-attempt timers below
		Rec:        rec,
		EmptyWaits: []time.Duration{3 * time.Second, 6 * time.Second, 12 * time.Second},
		MinCall:    10 * time.Second,
		Now:        time.Now,
		Sleep:      sleepCtx,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

var ErrNoConfig = errors.New("gateway: no model config for this stage")

// FailedError lists every attempt when all configs failed.
type FailedError struct{ Attempts []Attempt }

func (e *FailedError) Error() string {
	var parts []string
	for _, a := range e.Attempts {
		parts = append(parts, fmt.Sprintf("cfg%d#%d:%s", a.ConfigID, a.N, a.Outcome))
	}
	return "gateway: all attempts failed (" + strings.Join(parts, ", ") + ")"
}

// Chat tries configs in order (primary first) within budget (0 = no overall budget beyond each config's Total).
func (g *Gateway) Chat(ctx context.Context, cfgs []Config, req Request, budget time.Duration) (Result, error) {
	if len(cfgs) == 0 {
		return Result{}, ErrNoConfig
	}
	var deadline time.Time
	if budget > 0 {
		deadline = g.Now().Add(budget)
	}
	remaining := func() time.Duration {
		if deadline.IsZero() {
			return 1<<62 - 1
		}
		return deadline.Sub(g.Now())
	}
	var all []Attempt
	for _, cfg := range cfgs {
		var wait time.Duration
		httpRetried := false
		for n := 1; ; n++ {
			if wait > 0 {
				if remaining() < wait+g.MinCall {
					return Result{Attempts: all}, &FailedError{all}
				}
				if err := g.Sleep(ctx, wait); err != nil {
					return Result{Attempts: all}, err
				}
			}
			left := remaining()
			if left < g.MinCall {
				return Result{Attempts: all}, &FailedError{all}
			}
			res, a := g.once(ctx, cfg, req, left)
			a.N, a.WaitBefore = n, wait
			all = append(all, a)
			if g.Rec != nil {
				g.Rec.Record(ctx, req.Stage, req.JobID, a)
			}
			switch a.Outcome {
			case OK:
				res.Attempts = all
				return res, nil
			case Cancelled:
				return Result{Attempts: all}, ctx.Err()
			case Empty:
				if n <= len(g.EmptyWaits) {
					wait = g.EmptyWaits[n-1]
					continue
				}
			case HTTPError:
				if (a.HTTPStatus == 429 || a.HTTPStatus >= 500) && !httpRetried && len(g.EmptyWaits) > 0 {
					httpRetried = true
					wait = g.EmptyWaits[0]
					continue
				}
			}
			break // next config
		}
	}
	return Result{Attempts: all}, &FailedError{all}
}
