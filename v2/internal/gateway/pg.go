package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Opener decrypts stored credentials (crypto.Box satisfies it).
type Opener interface {
	Open(sealed string) (string, error)
}

// LoadStage returns the enabled text/search configs routed to a stage, primary first.
// A tier-specific route wins over the '*' route.
func LoadStage(ctx context.Context, db *pgxpool.Pool, box Opener, stage, tier string) ([]Config, error) {
	var ids []int64
	err := db.QueryRow(ctx, `SELECT config_ids FROM stage_route WHERE stage=$1 AND tier IN ($2,'*')
		ORDER BY (tier='*') LIMIT 1`, stage, tier).Scan(&ids)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoConfig
	}
	if err != nil {
		return nil, err
	}
	var out []Config
	for _, id := range ids {
		var c Config
		var keyCipher string
		var params []byte
		var fb, idle, total int
		err := db.QueryRow(ctx, `SELECT id, name, protocol, base_url, key_cipher, model, reasoning_effort, params,
			first_byte_timeout_s, idle_timeout_s, total_timeout_s FROM service_config
			WHERE id=$1 AND enabled AND kind IN ('text','search')`, id).
			Scan(&c.ID, &c.Name, &c.Protocol, &c.BaseURL, &keyCipher, &c.Model, &c.ReasoningEffort, &params, &fb, &idle, &total)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // disabled or deleted: skip silently in routing, admin sees it in the route editor
		}
		if err != nil {
			return nil, err
		}
		if keyCipher != "" {
			if c.APIKey, err = box.Open(keyCipher); err != nil {
				return nil, err
			}
		}
		_ = json.Unmarshal(params, &c.Params)
		c.FirstByte, c.Idle, c.Total = time.Duration(fb)*time.Second, time.Duration(idle)*time.Second, time.Duration(total)*time.Second
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, ErrNoConfig
	}
	return out, nil
}

// PGRecorder writes one model_call row per attempt and logs it.
type PGRecorder struct {
	DB  *pgxpool.Pool
	Log *slog.Logger
}

func (r PGRecorder) Record(ctx context.Context, stage string, jobID int64, a Attempt) {
	if r.Log != nil {
		r.Log.Info("model_call", "stage", stage, "job", jobID, "config", a.ConfigID, "model", a.Model, "effort", a.Effort,
			"attempt", a.N, "outcome", a.Outcome, "first_byte_ms", a.FirstByteMs, "total_ms", a.TotalMs, "wait", a.WaitBefore.String())
	}
	if r.DB == nil {
		return
	}
	var job any
	if jobID > 0 {
		job = jobID
	}
	var fb any
	if a.FirstByteMs > 0 {
		fb = a.FirstByteMs
	}
	_, err := r.DB.Exec(context.WithoutCancel(ctx), `INSERT INTO model_call (job_id, stage, config_id, model, effort, attempt, outcome,
		first_byte_ms, total_ms, finish_reason, tokens_in, tokens_out, error) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		job, stage, a.ConfigID, a.Model, a.Effort, a.N, string(a.Outcome), fb, a.TotalMs, a.Finish, a.TokensIn, a.TokensOut, a.Err)
	if err != nil && r.Log != nil {
		r.Log.Warn("model_call insert failed", "err", err)
	}
}
