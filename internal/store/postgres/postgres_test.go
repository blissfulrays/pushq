package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/blissfulrays/pushq/internal/store"
	"github.com/blissfulrays/pushq/internal/store/storetest"
)

// Tests are gated on PUSHQ_TEST_PG_URL (e.g.
// postgres://pushq@127.0.0.1:54329/pushq_test). Each factory call resets the
// schema for isolation.
func pgURL(t *testing.T) string {
	url := os.Getenv("PUSHQ_TEST_PG_URL")
	if url == "" {
		t.Skip("PUSHQ_TEST_PG_URL not set; skipping Postgres tests")
	}
	return url
}

func newStore(t *testing.T) store.Store {
	t.Helper()
	s, err := Open(pgURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`DROP TABLE IF EXISTS api_keys, invites, sessions, users, task_attempts, tasks, queues, schema_migrations CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestConformance(t *testing.T) {
	storetest.Run(t, newStore)
}
