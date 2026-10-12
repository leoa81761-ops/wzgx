package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	"github.com/leoa81761-ops/wzgx/internal/db"
)

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// File is a JSON snapshot of every application table. Values keep their JSON types.
type File struct {
	Tables map[string][]map[string]any `json:"tables"`
}

// Dump reads every application table. SQLite internal tables are left out.
func Dump(ctx context.Context, database *db.DB) ([]byte, error) {
	names, err := tableNames(ctx, database)
	if err != nil {
		return nil, err
	}
	out := File{Tables: map[string][]map[string]any{}}
	for _, name := range names {
		rows, err := readTable(ctx, database, name)
		if err != nil {
			return nil, err
		}
		out.Tables[name] = rows
	}
	return json.MarshalIndent(out, "", "  ")
}

// Restore replaces the rows of every table in the snapshot. Other tables are left as they are.
// SQLite foreign keys are turned off for the write. This schema does not declare foreign keys.
func Restore(ctx context.Context, database *db.DB, raw []byte) error {
	var file File
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&file); err != nil {
		return fmt.Errorf("备份文件无法读取: %w", err)
	}
	if file.Tables == nil {
		return fmt.Errorf("备份文件没有表")
	}
	names := make([]string, 0, len(file.Tables))
	for name := range file.Tables {
		if !identRe.MatchString(name) {
			return fmt.Errorf("备份里的表名不正确")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if database.Dialect == "sqlite" {
		if _, err := database.SQL.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
			return err
		}
		defer func() { _, _ = database.SQL.ExecContext(context.Background(), "PRAGMA foreign_keys=ON") }()
	}
	err := database.WithTx(ctx, func(tx *db.Tx) error {
		for _, name := range names {
			exists, err := tableExists(ctx, tx, database.Dialect, name)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("备份里的表 %s 在当前数据库不存在", name)
			}
			if _, err := tx.Exec(ctx, `DELETE FROM "`+name+`"`); err != nil {
				return err
			}
		}
		for _, name := range names {
			for _, row := range file.Tables[name] {
				if err := insertRow(ctx, tx, name, row); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return syncSequences(ctx, database, names)
}

func tableNames(ctx context.Context, database *db.DB) ([]string, error) {
	query := `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`
	if database.Dialect == "postgres" {
		query = `SELECT tablename FROM pg_tables WHERE schemaname = current_schema() AND tablename NOT LIKE 'pg_%' ORDER BY tablename`
	}
	rows, err := database.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if identRe.MatchString(name) {
			names = append(names, name)
		}
	}
	return names, rows.Err()
}

func readTable(ctx context.Context, database *db.DB, name string) ([]map[string]any, error) {
	rows, err := database.Query(ctx, `SELECT * FROM "`+name+`"`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := map[string]any{}
		for i, col := range cols {
			row[col] = normalize(vals[i])
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func normalize(value any) any {
	switch v := value.(type) {
	case []byte:
		return string(v)
	default:
		return v
	}
}

func tableExists(ctx context.Context, q db.Q, dialect, name string) (bool, error) {
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`
	if dialect == "postgres" {
		query = `SELECT COUNT(*) FROM pg_tables WHERE schemaname = current_schema() AND tablename = ?`
	}
	var n int
	err := q.QueryRow(ctx, query, name).Scan(&n)
	return n > 0, err
}

func insertRow(ctx context.Context, q db.Q, table string, row map[string]any) error {
	if len(row) == 0 {
		return nil
	}
	cols := make([]string, 0, len(row))
	for col := range row {
		if !identRe.MatchString(col) {
			return fmt.Errorf("备份里的列名不正确")
		}
		cols = append(cols, col)
	}
	sort.Strings(cols)
	holders := make([]string, len(cols))
	args := make([]any, len(cols))
	for i, col := range cols {
		holders[i] = "?"
		args[i] = jsonValue(row[col])
	}
	stmt := `INSERT INTO "` + table + `" (`
	for i, col := range cols {
		if i > 0 {
			stmt += ", "
		}
		stmt += `"` + col + `"`
	}
	stmt += `) VALUES (` + join(holders) + `)`
	_, err := q.Exec(ctx, stmt, args...)
	return err
}

func jsonValue(value any) any {
	number, ok := value.(json.Number)
	if !ok {
		return value
	}
	if i, err := number.Int64(); err == nil {
		return i
	}
	if f, err := number.Float64(); err == nil {
		return f
	}
	return number.String()
}

func join(parts []string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += ", "
		}
		out += part
	}
	return out
}

func syncSequences(ctx context.Context, database *db.DB, tables []string) error {
	if database.Dialect == "sqlite" {
		for _, name := range tables {
			var maxID sql.NullInt64
			err := database.QueryRow(ctx, `SELECT MAX(id) FROM "`+name+`"`).Scan(&maxID)
			if err != nil || !maxID.Valid {
				continue
			}
			_, err = database.Exec(ctx, `INSERT INTO sqlite_sequence (name, seq) VALUES (?, ?) ON CONFLICT(name) DO UPDATE SET seq = excluded.seq`, name, maxID.Int64)
			if err != nil {
				continue
			}
		}
		return nil
	}
	for _, name := range tables {
		var seq sql.NullString
		if err := database.QueryRow(ctx, `SELECT pg_get_serial_sequence(?, 'id')`, name).Scan(&seq); err != nil || !seq.Valid || seq.String == "" {
			continue
		}
		var maxID int64
		if err := database.QueryRow(ctx, `SELECT COALESCE(MAX(id), 1) FROM "`+name+`"`).Scan(&maxID); err != nil {
			return err
		}
		if maxID < 1 {
			maxID = 1
		}
		if _, err := database.Exec(ctx, `SELECT setval(?, ?, true)`, seq.String, maxID); err != nil {
			return err
		}
	}
	return nil
}
