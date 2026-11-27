package dispatch

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/blissfulrays/pushq/internal/metrics"
	"github.com/blissfulrays/pushq/internal/store"
	"github.com/blissfulrays/pushq/internal/store/sqlite"
)

// eventCounter counts Events() calls — the loop calls it on every iteration,
// so it is a cheap proxy for "how often did the scheduler wake up".
type eventCounter struct {
	store.Store
	calls atomic.Int64
}

func (c *eventCounter) Events() <-chan store.Event {
	c.calls.Add(1)
	return c.Store.Events()
}

func newTestStore(t *testing.T, tasks ...string) (*sqlite.Store, *httptest.Server, chan struct{}) {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open("sqlite:" + filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	t.Cleanup(srv.Close)
	now := time.Now()
	if _, err := s.CreateQueue(ctx, store.QueueUpsert{
		Name: "q", RateLimits: store.DefaultRateLimits(), Retry: store.DefaultRetryConfig(),
	}, now); err != nil {
		t.Fatal(err)
	}
	for _, id := range tasks {
		if _, err := s.CreateTask(ctx, store.NewTask{Queue: "q", ID: id, URL: srv.URL}, now); err != nil {
			t.Fatal(err)
		}
	}
	return s, srv, release
}

func gaugeValue(g prometheus.Gauge) float64 {
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		panic(err)
	}
	return m.GetGauge().GetValue()
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// With every global slot busy and more work pending, the loop must sleep
// instead of re-checking the saturated queue in a tight loop.
func TestNoSpinWhenGloballySaturated(t *testing.T) {
	s, _, release := newTestStore(t, "a", "b")
	cs := &eventCounter{Store: s}
	d := New(cs, Options{GlobalConcurrency: 1}, quietLog(), metrics.New())
	d.Start(context.Background())
	defer d.Stop()
	defer close(release) // runs first: unblock receivers so Stop drains fast

	time.Sleep(300 * time.Millisecond) // first task claimed, slot now full
	before := cs.calls.Load()
	time.Sleep(time.Second)
	if n := cs.calls.Load() - before; n > 1000 {
		t.Fatalf("scheduler woke %d times in 1s while saturated (busy loop)", n)
	}
}

type fakeLeader struct{ terms chan chan struct{} }

func (f *fakeLeader) Acquire(ctx context.Context) (<-chan struct{}, error) {
	select {
	case lost := <-f.terms:
		return lost, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (f *fakeLeader) Release() {}

// Workers still running from a lost leadership term must be accounted for in
// the next term; their completions must not drive the counters negative.
func TestInflightAccountingAcrossLeaderTerms(t *testing.T) {
	s, _, release := newTestStore(t, "a", "b", "c")
	fl := &fakeLeader{terms: make(chan chan struct{}, 2)}
	term1 := make(chan struct{})
	fl.terms <- term1
	met := metrics.New()
	d := New(s, Options{GlobalConcurrency: 10, Leader: fl}, quietLog(), met)
	d.Start(context.Background())
	defer d.Stop()

	waitGauge := func(want float64) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if gaugeValue(met.Inflight) == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("inflight = %v, want %v", gaugeValue(met.Inflight), want)
	}

	waitGauge(3)                    // all three dispatched in term 1
	close(term1)                    // leadership lost mid-flight
	fl.terms <- make(chan struct{}) // re-acquired
	time.Sleep(200 * time.Millisecond)
	if got := gaugeValue(met.Inflight); got != 3 {
		t.Fatalf("inflight after new term = %v, want 3 (term-1 workers still running)", got)
	}
	close(release) // term-1 workers finish and report into term 2
	waitGauge(0)
	time.Sleep(100 * time.Millisecond)
	if got := gaugeValue(met.Inflight); got != 0 {
		t.Fatalf("inflight settled at %v, want 0", got)
	}
}

// cleanupStore reports a fixed backlog of deletable rows.
type cleanupStore struct {
	store.Store
	terminal, purged int64
	calls            int
}

func take(backlog *int64, limit int) int64 {
	n := min(*backlog, int64(limit))
	*backlog -= n
	return n
}

func (c *cleanupStore) CleanupTerminal(_ context.Context, _ time.Time, _, _, _ time.Duration, limit int) (int64, error) {
	c.calls++
	return take(&c.terminal, limit), nil
}

func (c *cleanupStore) HardDeletePurged(_ context.Context, limit int) (int64, error) {
	return take(&c.purged, limit), nil
}

// One cleanup pass must drain more than a single batch; a fixed batch per
// interval cannot keep up with sustained throughput.
func TestCleanupDrainsBacklog(t *testing.T) {
	cs := &cleanupStore{terminal: 2500, purged: 4200}
	d := New(cs, Options{}, quietLog(), metrics.New())
	if more := d.cleanup(context.Background(), time.Now()); more {
		t.Fatal("cleanup reported a remaining backlog")
	}
	if cs.terminal != 0 || cs.purged != 0 {
		t.Fatalf("left terminal=%d purged=%d", cs.terminal, cs.purged)
	}
	if cs.calls != 3 { // 1000 + 1000 + 500 (< batch => done)
		t.Errorf("CleanupTerminal calls = %d, want 3", cs.calls)
	}
}
