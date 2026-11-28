package e2e

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/blissfulrays/pushq/internal/dispatch"
	"github.com/blissfulrays/pushq/internal/metrics"
	"github.com/blissfulrays/pushq/internal/store"
	"github.com/blissfulrays/pushq/internal/store/postgres"
)

// TestBlueGreenHandoff runs two dispatcher replicas against one Postgres and
// gracefully stops the leader mid-stream: every task must be delivered, none
// twice. Gated on PUSHQ_TEST_PG_URL.
func TestBlueGreenHandoff(t *testing.T) {
	url := os.Getenv("PUSHQ_TEST_PG_URL")
	if url == "" {
		t.Skip("PUSHQ_TEST_PG_URL not set; skipping")
	}
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	openPG := func() *postgres.Store {
		st, err := postgres.Open(url)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	st1 := openPG()
	defer st1.Close()
	if _, err := st1.DB().ExecContext(ctx, `DROP TABLE IF EXISTS apps, team_invites, team_members, teams, api_keys, invites, sessions, users, task_attempts, tasks, queues, schema_migrations CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := st1.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	st2 := openPG()
	defer st2.Close()

	// Receiver counts deliveries per task id.
	var mu sync.Mutex
	counts := map[string]int{}
	rc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		counts[r.Header.Get("X-Pushq-Task-Id")]++
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer rc.Close()

	newDisp := func(st *postgres.Store) *dispatch.Dispatcher {
		return dispatch.New(st, dispatch.Options{
			GlobalConcurrency: 32,
			PerQueueClaimCap:  10,
			ReconcileInterval: 150 * time.Millisecond,
			SweepInterval:     150 * time.Millisecond,
			CleanupInterval:   time.Second,
			LeaseGrace:        500 * time.Millisecond,
			DrainTimeout:      3 * time.Second,
			Leader:            st.NewElector(),
		}, logger, metrics.New())
	}

	d1 := newDisp(st1)
	d1.Start(ctx)
	defer d1.Stop()

	waitCond(t, 5*time.Second, "d1 leader", func() bool { return d1.Role() == "leader" })

	d2 := newDisp(st2)
	d2.Start(ctx)
	defer d2.Stop()

	// Queue paced at 20/s so the run spans the handoff.
	_, _, err := st1.UpsertQueue(ctx, store.QueueUpsert{
		Name:       "bg",
		RateLimits: store.RateLimits{MaxDispatchesPerSecond: 20, MaxBurstSize: 1, MaxConcurrentDispatches: 4},
		Retry:      store.DefaultRetryConfig(),
		Target:     store.HTTPTarget{BaseURL: rc.URL},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	const total = 30
	var tasks []store.NewTask
	for i := 0; i < total; i++ {
		tasks = append(tasks, store.NewTask{
			Queue: "bg", ID: fmt.Sprintf("task-%02d", i), URL: "/hook", Method: "POST",
			DispatchDeadline: 15 * time.Second, ScheduleTime: time.Now(),
		})
	}
	if _, err := st1.CreateTasks(ctx, tasks, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Let the leader deliver part of the stream, then hand off.
	waitCond(t, 10*time.Second, "first third delivered", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(counts) >= total/3
	})
	d1.Stop() // graceful: drain in-flight, release advisory lock

	waitCond(t, 10*time.Second, "d2 takes leadership", func() bool { return d2.Role() == "leader" })
	waitCond(t, 30*time.Second, "all tasks delivered", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(counts) >= total
	})

	mu.Lock()
	defer mu.Unlock()
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("task-%02d", i)
		if counts[id] == 0 {
			t.Errorf("task %s never delivered", id)
		}
		if counts[id] > 1 {
			t.Errorf("task %s delivered %d times (double dispatch!)", id, counts[id])
		}
	}
}

func waitCond(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
