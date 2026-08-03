package postgres

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Advisory lock key for the dispatcher role. hashtext is stable per database,
// so every replica computes the same key.
const leaderLockSQL = `SELECT pg_try_advisory_lock(hashtext('pushq:dispatcher'))`
const leaderUnlockSQL = `SELECT pg_advisory_unlock(hashtext('pushq:dispatcher'))`

// Elector serializes the dispatcher role across replicas with a
// session-scoped Postgres advisory lock. Losing the connection loses the
// lock, which is exactly the failover behavior we want. Leases remain the
// correctness layer during the zombie window; this only prevents duplicate
// claim scans.
type Elector struct {
	dsn string
	log *slog.Logger

	mu     sync.Mutex
	conn   *pgx.Conn
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *Store) NewElector() *Elector {
	return &Elector{dsn: s.dsn, log: s.log}
}

// Acquire blocks until the advisory lock is held. The returned channel closes
// if the lock connection later dies.
func (e *Elector) Acquire(ctx context.Context) (<-chan struct{}, error) {
	for {
		conn, err := pgx.Connect(ctx, e.dsn)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			e.log.Warn("leader election connect failed; retrying", "err", err)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(5 * time.Second):
			}
			continue
		}

		for {
			var got bool
			if err := conn.QueryRow(ctx, leaderLockSQL).Scan(&got); err != nil {
				conn.Close(context.Background())
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				break // reconnect
			}
			if got {
				lost := make(chan struct{})
				pingCtx, cancel := context.WithCancel(context.Background())
				done := make(chan struct{})

				e.mu.Lock()
				e.conn = conn
				e.cancel = cancel
				e.done = done
				e.mu.Unlock()

				go e.ping(pingCtx, conn, lost, done)
				return lost, nil
			}
			// Someone else is leader; poll.
			select {
			case <-ctx.Done():
				conn.Close(context.Background())
				return nil, ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
	}
}

// ping keeps the lock connection verified; any failure means the session (and
// with it the advisory lock) is gone.
func (e *Elector) ping(ctx context.Context, conn *pgx.Conn, lost chan struct{}, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					e.log.Warn("leader lock connection lost", "err", err)
					close(lost)
				}
				return
			}
		}
	}
}

// Release drops the lock and closes the connection so a standby can take over
// immediately (blue/green handoff).
func (e *Elector) Release() {
	e.mu.Lock()
	conn, cancel, done := e.conn, e.cancel, e.done
	e.conn, e.cancel, e.done = nil, nil, nil
	e.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	if conn != nil {
		ctx, c := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = conn.Exec(ctx, leaderUnlockSQL)
		c()
		conn.Close(context.Background())
	}
}
