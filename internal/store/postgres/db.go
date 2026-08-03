// Package postgres implements store.Store on PostgreSQL via pgx. Supports
// multiple server replicas: claims use FOR UPDATE SKIP LOCKED, dispatcher
// wakeups flow through LISTEN/NOTIFY, and the dispatcher role is serialized
// with an advisory-lock elector.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	"github.com/blissfulrays/pushq/internal/store"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const notifyChannel = "pushq_events"

type Store struct {
	db     *sql.DB
	dsn    string
	events chan store.Event
	log    *slog.Logger

	listenCancel context.CancelFunc
	listenDone   chan struct{}
}

func Open(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres open: %w", err)
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(time.Hour)

	s := &Store{
		db:     db,
		dsn:    dsn,
		events: make(chan store.Event, 1024),
		log:    slog.Default(),
	}
	if err := s.Ping(context.Background()); err != nil {
		db.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.listenCancel = cancel
	s.listenDone = make(chan struct{})
	go s.listenLoop(ctx)
	return s, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	return store.RunMigrations(ctx, s.db, sub)
}

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) Close() error {
	if s.listenCancel != nil {
		s.listenCancel()
		<-s.listenDone
	}
	return s.db.Close()
}

func (s *Store) Dialect() string { return "postgres" }

// DB exposes the underlying pool (used by tests).
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Events() <-chan store.Event { return s.events }

// --- LISTEN loop ---

type notifyPayload struct {
	Kind    int    `json:"k"`
	QueueID int64  `json:"q,omitempty"`
	Queue   string `json:"n,omitempty"`
	SchedMS int64  `json:"s,omitempty"`
	TaskPK  int64  `json:"t,omitempty"`
}

func (s *Store) listenLoop(ctx context.Context) {
	defer close(s.listenDone)
	for ctx.Err() == nil {
		if err := s.listenOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("pushq listen connection lost; reconnecting", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (s *Store) listenOnce(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, s.dsn)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, "LISTEN "+notifyChannel); err != nil {
		return err
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var p notifyPayload
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			continue
		}
		ev := store.Event{
			Kind:    store.EventKind(p.Kind),
			QueueID: p.QueueID,
			Queue:   p.Queue,
			TaskPK:  p.TaskPK,
		}
		if p.SchedMS != 0 {
			ev.ScheduleTime = time.UnixMilli(p.SchedMS).UTC()
		}
		select {
		case s.events <- ev:
		default: // best-effort; reconcile is the backstop
		}
	}
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// notify emits an event through Postgres so every replica's listener sees it.
// Call inside the mutating transaction (delivery happens on commit).
func notify(ctx context.Context, db execer, ev store.Event) {
	p := notifyPayload{Kind: int(ev.Kind), QueueID: ev.QueueID, Queue: ev.Queue, TaskPK: ev.TaskPK}
	if !ev.ScheduleTime.IsZero() {
		p.SchedMS = ev.ScheduleTime.UnixMilli()
	}
	b, err := json.Marshal(p)
	if err != nil {
		return
	}
	db.ExecContext(ctx, `SELECT pg_notify($1, $2)`, notifyChannel, string(b))
}

// --- helpers ---

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func scanNullableTime(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time.UTC()
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
