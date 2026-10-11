package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
)

type Store struct {
	DB *db.DB
}

func New(d *db.DB) *Store { return &Store{DB: d} }

func nstr(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

func parsePtr(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	t, err := schedule.Parse(v.String)
	if err != nil || t.IsZero() {
		return nil
	}
	return &t
}

func mustTime(s string) time.Time {
	t, err := schedule.Parse(s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func timeArg(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return schedule.Format(t.UTC())
}

func timeReq(t time.Time) string {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	return schedule.Format(t.UTC())
}

func bint(v bool) int {
	if v {
		return 1
	}
	return 0
}

func jsonStr(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func decodeMap(s string) map[string]any {
	if s == "" || s == "null" {
		return map[string]any{}
	}
	var m map[string]any
	if json.Unmarshal([]byte(s), &m) != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func decodeStringMap(s string) map[string]string {
	out := map[string]string{}
	raw := decodeMap(s)
	for k, v := range raw {
		if str, ok := v.(string); ok {
			out[k] = str
		}
	}
	return out
}

func decodeList(s string) []any {
	if s == "" || s == "null" {
		return []any{}
	}
	var list []any
	if json.Unmarshal([]byte(s), &list) != nil || list == nil {
		return []any{}
	}
	return list
}

func decodeInts(s string) []int {
	if s == "" || s == "null" {
		return []int{}
	}
	var list []int
	if json.Unmarshal([]byte(s), &list) != nil || list == nil {
		return []int{}
	}
	return list
}

func decodeTrace(s string) []map[string]any {
	if s == "" || s == "null" {
		return nil
	}
	var list []map[string]any
	if json.Unmarshal([]byte(s), &list) != nil {
		return nil
	}
	return list
}

func nullInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func scanNullInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}
