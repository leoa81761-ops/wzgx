// Package audit writes the admin/credential audit trail. Details must never contain secrets.
package audit

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Logger interface {
	Log(ctx context.Context, actorID int64, action, target string, detail map[string]any)
}

type PG struct{ DB *pgxpool.Pool }

func (p PG) Log(ctx context.Context, actorID int64, action, target string, detail map[string]any) {
	if detail == nil {
		detail = map[string]any{}
	}
	b, _ := json.Marshal(detail)
	var actor any
	if actorID > 0 {
		actor = actorID
	}
	_, _ = p.DB.Exec(context.WithoutCancel(ctx), `INSERT INTO audit_log (actor_id, action, target, detail) VALUES ($1,$2,$3,$4)`, actor, action, target, b)
}
