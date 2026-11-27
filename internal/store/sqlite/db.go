// Package sqlite implements store.Store on SQLite via the pure-Go
// modernc.org/sqlite driver. Single-node only: one dedicated write connection
// (SQLite's single-writer model) plus a read pool for queries.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"strings"
	"time"

	sqlite3 "modernc.org/sqlite"

	"github.com/blissfulrays/pushq/internal/store"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	write  *sql.DB // MaxOpenConns(1); all writes, BEGIN IMMEDIATE via _txlock
	read   *sql.DB // read pool (same handle as write for in-memory databases)
	events chan store.Event
}

// Open accepts "sqlite:PATH", "sqlite::memory:", or a bare path.
func Open(dsn string) (*Store, error) {
	path := strings.TrimPrefix(dsn, "sqlite:")
	if path == "" {
		return nil, fmt.Errorf("sqlite: empty database path")
	}

	memory := path == ":memory:" || strings.Contains(path, "mode=memory")

	pragmas := []string{
		"_pragma=busy_timeout(5000)",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=foreign_keys(1)",
	}

	var writeDSN, readDSN string
	if memory {
		// A distinct in-memory DB per connection would lose all state, so
		// everything shares one connection.
		writeDSN = "file::memory:?" + strings.Join(pragmas, "&")
		readDSN = writeDSN
	} else {
		base := "file:" + url.PathEscape(path)
		// url.PathEscape escapes "/" too; keep path separators readable.
		base = strings.ReplaceAll(base, "%2F", "/")
		writeDSN = base + "?_txlock=immediate&" + strings.Join(pragmas, "&")
		readDSN = base + "?" + strings.Join(pragmas, "&")
	}

	write, err := sql.Open("sqlite", writeDSN)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	write.SetMaxOpenConns(1)

	read := write
	if !memory {
		read, err = sql.Open("sqlite", readDSN)
		if err != nil {
			write.Close()
			return nil, fmt.Errorf("sqlite open read pool: %w", err)
		}
		read.SetMaxOpenConns(8)
	}

	s := &Store{write: write, read: read, events: make(chan store.Event, 1024)}
	if err := s.Ping(context.Background()); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	return store.RunMigrations(ctx, s.write, sub)
}

func (s *Store) Ping(ctx context.Context) error {
	return s.write.PingContext(ctx)
}

func (s *Store) Close() error {
	if s.read != s.write {
		s.read.Close()
	}
	return s.write.Close()
}

func (s *Store) Dialect() string { return "sqlite" }

// SQLHandles exposes the write connection and read pool to packages layered
// on the same database (accounts).
func (s *Store) SQLHandles() (write, read *sql.DB) { return s.write, s.read }

func (s *Store) Events() <-chan store.Event { return s.events }

// emit is a best-effort non-blocking send; the dispatcher reconcile loop
// covers dropped events.
func (s *Store) emit(e store.Event) {
	select {
	case s.events <- e:
	default:
	}
}

// --- helpers ---

func isUniqueViolation(err error) bool {
	var se *sqlite3.Error
	if errors.As(err, &se) {
		code := se.Code()
		return code == 1555 || code == 2067 || code%256 == 19 // SQLITE_CONSTRAINT family
	}
	return false
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }

func nullableMS(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

func scanNullableMS(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromMS(v.Int64)
	return &t
}

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func jsonOrNil(v any) (any, error) {
	switch x := v.(type) {
	case map[string]string:
		if len(x) == 0 {
			return nil, nil
		}
	case *store.RetryOverride:
		if x == nil {
			return nil, nil
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}
