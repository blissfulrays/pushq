// Package load is a smoke benchmark: many dynamic queues, tens of thousands
// of tasks, measured end-to-end through the real dispatcher. Gated on
// PUSHQ_LOAD=1 (and optionally PUSHQ_TEST_PG_URL to run against Postgres).
//
//	PUSHQ_LOAD=1 go test ./test/load -v -run TestLoadSmoke -timeout 10m
package load

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blissfulrays/pushq/internal/dispatch"
	"github.com/blissfulrays/pushq/internal/metrics"
	"github.com/blissfulrays/pushq/internal/store"
	"github.com/blissfulrays/pushq/internal/store/postgres"
	"github.com/blissfulrays/pushq/internal/store/sqlite"
)

func TestLoadSmoke(t *testing.T) {
	if os.Getenv("PUSHQ_LOAD") == "" {
		t.Skip("PUSHQ_LOAD not set; skipping load smoke")
	}
	const (
		numQueues = 1000
		numTasks  = 50000
		batchSize = 500
	)

	var st store.Store
	var err error
	if url := os.Getenv("PUSHQ_TEST_PG_URL"); url != "" {
		pg, perr := postgres.Open(url)
		if perr != nil {
			t.Fatal(perr)
		}
		if _, perr := pg.DB().ExecContext(context.Background(),
			`DROP TABLE IF EXISTS task_attempts, tasks, queues, schema_migrations CASCADE`); perr != nil {
			t.Fatal(perr)
		}
		st = pg
	} else {
		st, err = sqlite.Open("sqlite:" + filepath.Join(t.TempDir(), "load.db"))
		if err != nil {
			t.Fatal(err)
		}
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	var delivered atomic.Int64
	rc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		delivered.Add(1)
		w.WriteHeader(200)
	}))
	defer rc.Close()

	// 1000 dynamic queues, generous limits.
	now := time.Now()
	setup := time.Now()
	for i := 0; i < numQueues; i++ {
		_, _, err := st.UpsertQueue(ctx, store.QueueUpsert{
			Name: fmt.Sprintf("load-%04d", i),
			RateLimits: store.RateLimits{
				MaxDispatchesPerSecond: 500, MaxBurstSize: 100, MaxConcurrentDispatches: 100,
			},
			Retry:  store.DefaultRetryConfig(),
			Target: store.HTTPTarget{BaseURL: rc.URL},
		}, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("created %d queues in %v", numQueues, time.Since(setup))

	// 50k tasks spread across the queues, enqueued in batches.
	enqueue := time.Now()
	batch := make([]store.NewTask, 0, batchSize)
	for i := 0; i < numTasks; i++ {
		batch = append(batch, store.NewTask{
			Queue: fmt.Sprintf("load-%04d", i%numQueues),
			URL:   "/hook", Method: "POST",
			DispatchDeadline: 15 * time.Second,
			ScheduleTime:     now,
		})
		if len(batch) == batchSize {
			if _, err := st.CreateTasks(ctx, batch, now); err != nil {
				t.Fatal(err)
			}
			batch = batch[:0]
		}
	}
	enqueueDur := time.Since(enqueue)
	t.Logf("enqueued %d tasks in %v (%.0f/s)", numTasks, enqueueDur,
		float64(numTasks)/enqueueDur.Seconds())

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	disp := dispatch.New(st, dispatch.Options{
		GlobalConcurrency: 512,
		PerQueueClaimCap:  100,
		ReconcileInterval: 2 * time.Second,
		SweepInterval:     5 * time.Second,
		CleanupInterval:   time.Minute,
		LeaseGrace:        time.Minute,
		DrainTimeout:      10 * time.Second,
	}, logger, metrics.New())

	start := time.Now()
	disp.Start(ctx)
	defer disp.Stop()

	deadline := time.Now().Add(8 * time.Minute)
	last := int64(0)
	for time.Now().Before(deadline) {
		n := delivered.Load()
		if n >= numTasks {
			break
		}
		if time.Since(start) > 10*time.Second && n == last {
			// no forward progress for a tick — keep waiting but log
			t.Logf("progress: %d/%d", n, numTasks)
		}
		last = n
		time.Sleep(500 * time.Millisecond)
	}
	total := time.Since(start)
	n := delivered.Load()
	if n < numTasks {
		t.Fatalf("only %d/%d delivered in %v", n, numTasks, total)
	}
	t.Logf("dispatched %d tasks across %d queues in %v (%.0f tasks/s)",
		n, numQueues, total, float64(n)/total.Seconds())
}
