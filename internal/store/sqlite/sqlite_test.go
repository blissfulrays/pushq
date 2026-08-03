package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/blissfulrays/pushq/internal/store"
	"github.com/blissfulrays/pushq/internal/store/storetest"
)

func newStore(t *testing.T) store.Store {
	t.Helper()
	s, err := Open("sqlite:" + filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestConformance(t *testing.T) {
	storetest.Run(t, newStore)
}

func TestMemoryMode(t *testing.T) {
	s, err := Open("sqlite::memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}
