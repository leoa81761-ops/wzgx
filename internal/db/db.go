package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type DB struct {
	SQL     *sql.DB
	Dialect string
}

type Q interface {
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
	Query(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) *sql.Row
}

type Tx struct {
	tx      *sql.Tx
	dialect string
}

func Open(raw string, maxOpen, maxIdle int) (*DB, error) {
	dialect, dsn, err := ParseURL(raw)
	if err != nil {
		return nil, err
	}
	driver := "sqlite"
	if dialect == "postgres" {
		driver = "pgx"
	}
	sqlDB, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	if maxOpen <= 0 {
		if dialect == "postgres" {
			maxOpen = 16
		} else {
			maxOpen = 8
		}
	}
	if maxIdle <= 0 {
		maxIdle = maxOpen / 2
		if maxIdle < 1 {
			maxIdle = 1
		}
	}
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	ctx := context.Background()
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	d := &DB{SQL: sqlDB, Dialect: dialect}
	if dialect == "sqlite" {
		if _, err := sqlDB.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
		_, _ = sqlDB.ExecContext(ctx, "PRAGMA busy_timeout=5000")
		_, _ = sqlDB.ExecContext(ctx, "PRAGMA journal_mode=WAL")
	}
	if err := d.migrate(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) Close() error {
	if d == nil || d.SQL == nil {
		return nil
	}
	return d.SQL.Close()
}

func (d *DB) rebind(query string) string {
	if d.Dialect != "postgres" {
		return query
	}
	return rebind(query)
}

func (t *Tx) rebind(query string) string {
	if t.dialect != "postgres" {
		return query
	}
	return rebind(query)
}

func rebind(query string) string {
	var b strings.Builder
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}

func (d *DB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.SQL.ExecContext(ctx, d.rebind(query), args...)
}

func (d *DB) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.SQL.QueryContext(ctx, d.rebind(query), args...)
}

func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return d.SQL.QueryRowContext(ctx, d.rebind(query), args...)
}

func (d *DB) Begin(ctx context.Context) (*Tx, error) {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Tx{tx: tx, dialect: d.Dialect}, nil
}

func (d *DB) WithTx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := d.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.tx.Rollback()
		return err
	}
	return tx.tx.Commit()
}

func (t *Tx) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, t.rebind(query), args...)
}

func (t *Tx) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, t.rebind(query), args...)
}

func (t *Tx) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, t.rebind(query), args...)
}

func (t *Tx) Savepoint(ctx context.Context, fn func() error) error {
	name := "sp_gonghao"
	if _, err := t.Exec(ctx, "SAVEPOINT "+name); err != nil {
		return err
	}
	if err := fn(); err != nil {
		_, _ = t.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name)
		_, _ = t.Exec(ctx, "RELEASE SAVEPOINT "+name)
		return err
	}
	_, err := t.Exec(ctx, "RELEASE SAVEPOINT "+name)
	return err
}

func IsUnique(err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "unique constraint") {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true
	}
	return false
}

func ParseURL(raw string) (dialect, dsn string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "sqlite:///./data/dev.db"
	}
	low := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(low, "postgres://"), strings.HasPrefix(low, "postgresql://"):
		return "postgres", raw, nil
	case strings.HasPrefix(low, "postgresql+asyncpg://"):
		return "postgres", "postgres://" + raw[len("postgresql+asyncpg://"):], nil
	case strings.HasPrefix(low, "sqlite+aiosqlite://"), strings.HasPrefix(low, "sqlite://"):
		path := raw
		if i := strings.Index(low, "://"); i >= 0 {
			path = raw[i+3:]
		}
		if strings.HasPrefix(path, "//") {
			path = path[1:]
		} else if strings.HasPrefix(path, "/./") || strings.HasPrefix(path, "/../") {
			path = path[1:]
		}
		return sqliteDSN(path)
	case strings.HasPrefix(low, "file:"):
		return "sqlite", raw, nil
	default:
		return "", "", fmt.Errorf("无法识别的数据库地址")
	}
}

func sqliteDSN(path string) (string, string, error) {
	if path == "" || path == ":memory:" || strings.Contains(path, "mode=memory") {
		return "sqlite", "file::memory:?cache=shared&_busy_timeout=10000&_txlock=immediate&_pragma=foreign_keys(ON)", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return "", "", err
	}
	// file:./data/dev.db must not be treated as a host. _txlock=immediate makes writers wait on busy_timeout instead of failing when the deferred lock upgrades.
	dsn := "file:" + path + "?_busy_timeout=10000&_txlock=immediate&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)"
	return "sqlite", dsn, nil
}
