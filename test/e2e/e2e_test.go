// Package e2e boots the real server (API + dispatcher + SQLite store) against
// an httptest receiver and verifies end-to-end queue behavior.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/blissfulrays/pushq/internal/api"
	"github.com/blissfulrays/pushq/internal/auth"
	"github.com/blissfulrays/pushq/internal/config"
	"github.com/blissfulrays/pushq/internal/dispatch"
	"github.com/blissfulrays/pushq/internal/metrics"
	"github.com/blissfulrays/pushq/internal/store"
	"github.com/blissfulrays/pushq/internal/store/sqlite"
)

// --- harness ---

type recorded struct {
	Path    string
	Method  string
	Headers http.Header
	Body    []byte
	At      time.Time
}

type receiver struct {
	*httptest.Server
	mu         sync.Mutex
	reqs       []recorded
	inflight   int
	maxSeen    int
	handler    func(n int, w http.ResponseWriter, r *http.Request) // n = 1-based request count for that path
	pathCounts map[string]int
}

func newReceiver() *receiver {
	rc := &receiver{pathCounts: map[string]int{}}
	rc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rc.mu.Lock()
		rc.inflight++
		if rc.inflight > rc.maxSeen {
			rc.maxSeen = rc.inflight
		}
		rc.pathCounts[r.URL.Path]++
		n := rc.pathCounts[r.URL.Path]
		rc.reqs = append(rc.reqs, recorded{
			Path: r.URL.Path, Method: r.Method, Headers: r.Header.Clone(), Body: body, At: time.Now(),
		})
		h := rc.handler
		rc.mu.Unlock()

		if h != nil {
			h(n, w, r)
		} else {
			w.WriteHeader(200)
		}

		rc.mu.Lock()
		rc.inflight--
		rc.mu.Unlock()
	}))
	return rc
}

func (rc *receiver) count(path string) int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if path == "" {
		return len(rc.reqs)
	}
	return rc.pathCounts[path]
}

func (rc *receiver) requests() []recorded {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	out := make([]recorded, len(rc.reqs))
	copy(out, rc.reqs)
	return out
}

type harness struct {
	t        *testing.T
	st       store.Store
	disp     *dispatch.Dispatcher
	api      *httptest.Server
	receiver *receiver
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := sqlite.Open("sqlite:" + filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	met := metrics.New()
	disp := dispatch.New(st, dispatch.Options{
		GlobalConcurrency:  64,
		PerQueueClaimCap:   50,
		ReconcileInterval:  200 * time.Millisecond,
		SweepInterval:      100 * time.Millisecond,
		CleanupInterval:    time.Second,
		LeaseGrace:         200 * time.Millisecond,
		DrainTimeout:       2 * time.Second,
		RetentionSucceeded: time.Hour,
		RetentionFailed:    time.Hour,
		DedupWindow:        time.Hour,
	}, logger, met)

	srv := &api.Server{
		Store:  st,
		Auth:   auth.New(nil, nil, false),
		Log:    logger,
		Limits: config.Default().Limits,
		Now:    time.Now,
		Role:   disp.Role,
	}
	apiSrv := httptest.NewServer(srv.Router())

	ctx, cancel := context.WithCancel(context.Background())
	disp.Start(ctx)

	rc := newReceiver()
	h := &harness{t: t, st: st, disp: disp, api: apiSrv, receiver: rc}
	t.Cleanup(func() {
		cancel()
		disp.Stop()
		apiSrv.Close()
		rc.Close()
		st.Close()
	})
	return h
}

// do issues an API request and returns status + parsed body.
func (h *harness) do(method, path string, body any) (int, map[string]any) {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.api.URL+path, rd)
	if err != nil {
		h.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &parsed)
	}
	return resp.StatusCode, parsed
}

func (h *harness) mustDo(method, path string, body any, wantStatus int) map[string]any {
	h.t.Helper()
	status, parsed := h.do(method, path, body)
	if status != wantStatus {
		h.t.Fatalf("%s %s = %d (want %d): %v", method, path, status, wantStatus, parsed)
	}
	return parsed
}

func (h *harness) upsertQueue(name string, cfg map[string]any) {
	h.t.Helper()
	if cfg == nil {
		cfg = map[string]any{}
	}
	if _, ok := cfg["target"]; !ok {
		cfg["target"] = map[string]any{"base_url": h.receiver.URL}
	}
	status, body := h.do("PUT", "/v1/queues/"+name, cfg)
	if status != 200 && status != 201 {
		h.t.Fatalf("upsert queue %s = %d: %v", name, status, body)
	}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// --- tests ---

func TestDeliveryHeadersAndBody(t *testing.T) {
	h := newHarness(t)
	h.upsertQueue("orders", nil)

	h.mustDo("POST", "/v1/queues/orders/tasks", map[string]any{
		"task_id":   "job-1",
		"path":      "/hooks/order",
		"json_body": map[string]any{"order_id": 42},
	}, 201)

	waitFor(t, 5*time.Second, "delivery", func() bool { return h.receiver.count("/hooks/order") >= 1 })

	reqs := h.receiver.requests()
	r := reqs[0]
	if r.Method != "POST" {
		t.Errorf("method = %s", r.Method)
	}
	if got := r.Headers.Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type = %q", got)
	}
	if got := r.Headers.Get("X-Pushq-Queue"); got != "orders" {
		t.Errorf("X-Pushq-Queue = %q", got)
	}
	if got := r.Headers.Get("X-Pushq-Task-Id"); got != "job-1" {
		t.Errorf("X-Pushq-Task-Id = %q", got)
	}
	if got := r.Headers.Get("X-Pushq-Attempt"); got != "1" {
		t.Errorf("X-Pushq-Attempt = %q", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(r.Body, &payload); err != nil || payload["order_id"] != float64(42) {
		t.Errorf("body = %s", r.Body)
	}

	// Task reaches succeeded with one attempt recorded.
	waitFor(t, 5*time.Second, "succeeded state", func() bool {
		_, body := h.do("GET", "/v1/queues/orders/tasks/job-1", nil)
		return body["state"] == "succeeded"
	})
	_, atts := h.do("GET", "/v1/queues/orders/tasks/job-1/attempts", nil)
	list := atts["attempts"].([]any)
	if len(list) != 1 {
		t.Fatalf("attempts = %d", len(list))
	}
	first := list[0].(map[string]any)
	if first["response_status"] != float64(200) {
		t.Errorf("attempt status = %v", first["response_status"])
	}
}

func TestSignedDelivery(t *testing.T) {
	h := newHarness(t)
	secret := "whsec_dGVzdC1zZWNyZXQ="
	h.upsertQueue("signed", map[string]any{
		"target": map[string]any{"base_url": h.receiver.URL, "signing_secret": secret},
	})
	h.mustDo("POST", "/v1/queues/signed/tasks", map[string]any{
		"task_id": "sig-1", "path": "/hook", "json_body": map[string]any{"x": 1},
	}, 201)

	waitFor(t, 5*time.Second, "signed delivery", func() bool { return h.receiver.count("/hook") >= 1 })
	r := h.receiver.requests()[0]

	id := r.Headers.Get("webhook-id")
	tsStr := r.Headers.Get("webhook-timestamp")
	sig := r.Headers.Get("webhook-signature")
	if id != "signed/sig-1" || tsStr == "" || sig == "" {
		t.Fatalf("missing standard-webhooks headers: id=%q ts=%q sig=%q", id, tsStr, sig)
	}
	tsUnix, _ := strconv.ParseInt(tsStr, 10, 64)
	if err := auth.VerifySignature(secret, id, time.Unix(tsUnix, 0), r.Body, sig, 5*time.Minute, time.Now()); err != nil {
		t.Fatalf("signature verify failed: %v", err)
	}
	// Wrong secret must fail.
	if err := auth.VerifySignature("whsec_d3Jvbmc=", id, time.Unix(tsUnix, 0), r.Body, sig, 5*time.Minute, time.Now()); err == nil {
		t.Fatal("wrong secret verified")
	}
}

func TestRetryThenSuccess(t *testing.T) {
	h := newHarness(t)
	h.receiver.handler = func(n int, w http.ResponseWriter, r *http.Request) {
		if n <= 2 {
			w.WriteHeader(500)
			fmt.Fprint(w, "boom")
			return
		}
		w.WriteHeader(200)
	}
	h.upsertQueue("retries", map[string]any{
		"retry": map[string]any{"min_backoff_seconds": 0.05, "max_backoff_seconds": 0.1, "max_attempts": 10},
	})
	h.mustDo("POST", "/v1/queues/retries/tasks", map[string]any{"task_id": "r1", "path": "/flaky"}, 201)

	waitFor(t, 10*time.Second, "3 attempts", func() bool { return h.receiver.count("/flaky") >= 3 })
	waitFor(t, 5*time.Second, "succeeded", func() bool {
		_, body := h.do("GET", "/v1/queues/retries/tasks/r1", nil)
		return body["state"] == "succeeded"
	})

	reqs := h.receiver.requests()
	for i, want := range []string{"1", "2", "3"} {
		if got := reqs[i].Headers.Get("X-Pushq-Attempt"); got != want {
			t.Errorf("attempt %d header = %q", i, got)
		}
	}
	_, atts := h.do("GET", "/v1/queues/retries/tasks/r1/attempts", nil)
	if n := len(atts["attempts"].([]any)); n != 3 {
		t.Errorf("attempt history = %d", n)
	}
}

func TestPerTaskRetryOverride(t *testing.T) {
	h := newHarness(t)
	h.receiver.handler = func(n int, w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }
	h.upsertQueue("override", map[string]any{
		"retry": map[string]any{"max_attempts": 100, "min_backoff_seconds": 0.05, "max_backoff_seconds": 0.05},
	})
	h.mustDo("POST", "/v1/queues/override/tasks", map[string]any{
		"task_id": "once", "path": "/x", "retry": map[string]any{"max_attempts": 1},
	}, 201)

	waitFor(t, 5*time.Second, "permanent failure", func() bool {
		_, body := h.do("GET", "/v1/queues/override/tasks/once", nil)
		return body["state"] == "failed"
	})
	if n := h.receiver.count("/x"); n != 1 {
		t.Errorf("dispatches = %d, want exactly 1 (per-task max_attempts=1)", n)
	}
}

func TestRateLimitPacing(t *testing.T) {
	h := newHarness(t)
	h.upsertQueue("slow", map[string]any{
		"rate_limits": map[string]any{"max_dispatches_per_second": 10, "max_burst_size": 1},
	})
	for i := 0; i < 5; i++ {
		h.mustDo("POST", "/v1/queues/slow/tasks", map[string]any{"path": "/paced"}, 201)
	}
	waitFor(t, 10*time.Second, "5 paced deliveries", func() bool { return h.receiver.count("/paced") >= 5 })

	reqs := h.receiver.requests()
	span := reqs[len(reqs)-1].At.Sub(reqs[0].At)
	// 5 tasks at 10/s with burst 1 → ≥ ~400ms of pacing. Generous bounds
	// against CI jitter.
	if span < 250*time.Millisecond {
		t.Errorf("deliveries too fast for 10 dps burst 1: span=%v", span)
	}
}

func TestConcurrencyCap(t *testing.T) {
	h := newHarness(t)
	h.receiver.handler = func(n int, w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(200)
	}
	h.upsertQueue("serial", map[string]any{
		"rate_limits": map[string]any{"max_dispatches_per_second": 100, "max_concurrent_dispatches": 1},
	})
	for i := 0; i < 3; i++ {
		h.mustDo("POST", "/v1/queues/serial/tasks", map[string]any{"path": "/serial"}, 201)
	}
	waitFor(t, 10*time.Second, "3 serial deliveries", func() bool { return h.receiver.count("/serial") >= 3 })
	h.receiver.mu.Lock()
	max := h.receiver.maxSeen
	h.receiver.mu.Unlock()
	if max > 1 {
		t.Errorf("max concurrent = %d, want 1", max)
	}
}

func TestPauseResume(t *testing.T) {
	h := newHarness(t)
	h.upsertQueue("pausable", nil)
	h.mustDo("POST", "/v1/queues/pausable:pause", nil, 200)
	h.mustDo("POST", "/v1/queues/pausable/tasks", map[string]any{"path": "/p"}, 201)

	time.Sleep(500 * time.Millisecond)
	if n := h.receiver.count("/p"); n != 0 {
		t.Fatalf("paused queue dispatched %d tasks", n)
	}
	h.mustDo("POST", "/v1/queues/pausable:resume", nil, 200)
	waitFor(t, 5*time.Second, "delivery after resume", func() bool { return h.receiver.count("/p") >= 1 })
}

func TestPurge(t *testing.T) {
	h := newHarness(t)
	h.upsertQueue("purgeme", nil)
	h.mustDo("POST", "/v1/queues/purgeme/tasks", map[string]any{"task_id": "doomed", "path": "/purged", "delay_seconds": 0.7}, 201)
	h.mustDo("POST", "/v1/queues/purgeme:purge", nil, 202)

	time.Sleep(1200 * time.Millisecond)
	if n := h.receiver.count("/purged"); n != 0 {
		t.Fatalf("purged task was dispatched %d times", n)
	}
	_, stats := h.do("GET", "/v1/queues/purgeme/stats", nil)
	if stats["pending"] != float64(0) || stats["scheduled_future"] != float64(0) {
		t.Errorf("stats after purge: %v", stats)
	}
	// Task created after purge flows normally.
	h.mustDo("POST", "/v1/queues/purgeme/tasks", map[string]any{"path": "/alive"}, 201)
	waitFor(t, 5*time.Second, "post-purge delivery", func() bool { return h.receiver.count("/alive") >= 1 })
}

func TestForceRunBypassesPauseAndSchedule(t *testing.T) {
	h := newHarness(t)
	h.upsertQueue("forced", nil)
	h.mustDo("POST", "/v1/queues/forced:pause", nil, 200)
	h.mustDo("POST", "/v1/queues/forced/tasks", map[string]any{
		"task_id": "later", "path": "/forced", "delay_seconds": 3600,
	}, 201)

	time.Sleep(300 * time.Millisecond)
	if h.receiver.count("/forced") != 0 {
		t.Fatal("dispatched while paused")
	}
	h.mustDo("POST", "/v1/queues/forced/tasks/later:run", nil, 200)
	waitFor(t, 5*time.Second, "forced delivery", func() bool { return h.receiver.count("/forced") >= 1 })
}

func TestDedup409(t *testing.T) {
	h := newHarness(t)
	h.upsertQueue("dedup", nil)
	h.mustDo("POST", "/v1/queues/dedup/tasks", map[string]any{"task_id": "same", "path": "/d", "delay_seconds": 60}, 201)
	body := h.mustDo("POST", "/v1/queues/dedup/tasks", map[string]any{"task_id": "same", "path": "/d"}, 409)
	if body["code"] != "already_exists" {
		t.Errorf("problem code = %v", body["code"])
	}
}

func TestLeaseRecoveryRedelivers(t *testing.T) {
	h := newHarness(t)
	h.upsertQueue("crashy", map[string]any{
		"retry": map[string]any{"min_backoff_seconds": 0.05, "max_backoff_seconds": 0.05, "max_attempts": 5},
	})
	// Pause so the live dispatcher cannot win the claim race while we
	// simulate a dispatcher that claimed the task and died.
	h.mustDo("POST", "/v1/queues/crashy:pause", nil, 200)

	q, err := h.st.GetQueue(context.Background(), "crashy")
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.st.CreateTask(context.Background(), store.NewTask{
		Queue: "crashy", ID: "abandoned", URL: "/recovered", Method: "POST",
		DispatchDeadline: 100 * time.Millisecond, ScheduleTime: time.Now(),
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := h.st.ClaimTasks(context.Background(), time.Now(), "dead-dispatcher", 100*time.Millisecond,
		[]store.ClaimBudget{{QueueID: q.ID, N: 1}})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %d err=%v", len(claimed), err)
	}

	// Lease = deadline(100ms) + grace(100ms) ≈ 200ms. The sweeper (100ms
	// interval) reclaims it → synthetic lease_lost attempt → back to
	// pending. Resume and the redelivery flows.
	waitFor(t, 10*time.Second, "lease reclaimed", func() bool {
		got, err := h.st.GetTask(context.Background(), "crashy", "abandoned", store.ViewBasic)
		return err == nil && got.State == store.TaskPending && got.DispatchCount >= 1
	})
	h.mustDo("POST", "/v1/queues/crashy:resume", nil, 200)

	waitFor(t, 10*time.Second, "redelivery after lease expiry", func() bool {
		return h.receiver.count("/recovered") >= 1
	})
	// The receiver sees the request before the dispatcher records the
	// result, so wait for the state rather than reading it immediately.
	var body map[string]any
	waitFor(t, 5*time.Second, "succeeded state", func() bool {
		_, body = h.do("GET", "/v1/queues/crashy/tasks/abandoned?view=full", nil)
		return body["state"] == "succeeded"
	})
	// The abandoned claim counts as an attempt (lease_lost), then redelivery.
	if dc := body["dispatch_count"].(float64); dc < 2 {
		t.Errorf("dispatch_count = %v, want >= 2", dc)
	}
	// Attempt history shows the synthetic lease_lost attempt.
	_, atts := h.do("GET", "/v1/queues/crashy/tasks/abandoned/attempts", nil)
	foundLeaseLost := false
	for _, a := range atts["attempts"].([]any) {
		if a.(map[string]any)["error_kind"] == "lease_lost" {
			foundLeaseLost = true
		}
	}
	if !foundLeaseLost {
		t.Errorf("no lease_lost attempt recorded: %v", atts)
	}
}

func TestDeadLetterRouting(t *testing.T) {
	h := newHarness(t)
	h.receiver.handler = func(n int, w http.ResponseWriter, r *http.Request) {
		// Original deliveries fail; DLQ redeliveries (provenance header
		// present) succeed.
		if r.Header.Get("X-Pushq-Deadletter-Source-Queue") != "" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(500)
	}
	h.upsertQueue("dead", nil)
	h.upsertQueue("flaky", map[string]any{
		"retry":             map[string]any{"max_attempts": 2, "min_backoff_seconds": 0.05, "max_backoff_seconds": 0.05},
		"dead_letter_queue": "dead",
	})
	h.mustDo("POST", "/v1/queues/flaky/tasks", map[string]any{"task_id": "will-die-2", "path": "/failing", "json_body": map[string]any{"v": 1}}, 201)

	waitFor(t, 10*time.Second, "dead-letter delivery", func() bool {
		for _, r := range h.receiver.requests() {
			if r.Headers.Get("X-Pushq-Deadletter-Source-Queue") == "flaky" {
				return true
			}
		}
		return false
	})
	var dlq recorded
	for _, r := range h.receiver.requests() {
		if r.Headers.Get("X-Pushq-Deadletter-Source-Queue") == "flaky" {
			dlq = r
		}
	}
	if dlq.Headers.Get("X-Pushq-Deadletter-Task-Id") != "will-die-2" {
		t.Errorf("provenance task id = %q", dlq.Headers.Get("X-Pushq-Deadletter-Task-Id"))
	}
	if dlq.Headers.Get("X-Pushq-Deadletter-Final-Status") != "500" {
		t.Errorf("final status = %q", dlq.Headers.Get("X-Pushq-Deadletter-Final-Status"))
	}
}

func TestBatchCreate(t *testing.T) {
	h := newHarness(t)
	h.upsertQueue("batch", nil)
	h.mustDo("POST", "/v1/queues/batch/tasks", map[string]any{"task_id": "dup", "path": "/b", "delay_seconds": 60}, 201)

	body := h.mustDo("POST", "/v1/queues/batch/tasks:batch", map[string]any{
		"tasks": []map[string]any{
			{"task_id": "b1", "path": "/b"},
			{"task_id": "dup", "path": "/b"},
			{"task_id": "bad id!", "path": "/b"},
			{"task_id": "b2", "path": "/b"},
		},
	}, 200)
	results := body["results"].([]any)
	if len(results) != 4 {
		t.Fatalf("results = %d", len(results))
	}
	get := func(i int) map[string]any { return results[i].(map[string]any) }
	if get(0)["task"] == nil || get(3)["task"] == nil {
		t.Errorf("fresh tasks not created: %v %v", get(0), get(3))
	}
	if get(1)["error"].(map[string]any)["code"] != "already_exists" {
		t.Errorf("dup code = %v", get(1)["error"])
	}
	if get(2)["error"].(map[string]any)["code"] != "invalid_argument" {
		t.Errorf("bad id code = %v", get(2)["error"])
	}
	waitFor(t, 5*time.Second, "batch deliveries", func() bool { return h.receiver.count("/b") >= 2 })
}

func TestRetryAfterHonored(t *testing.T) {
	h := newHarness(t)
	h.receiver.handler = func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(200)
	}
	h.upsertQueue("throttled", map[string]any{
		"retry": map[string]any{"min_backoff_seconds": 0.05, "max_backoff_seconds": 30, "max_attempts": 5},
	})
	h.mustDo("POST", "/v1/queues/throttled/tasks", map[string]any{"task_id": "ra", "path": "/ra"}, 201)

	waitFor(t, 10*time.Second, "2 deliveries", func() bool { return h.receiver.count("/ra") >= 2 })
	reqs := h.receiver.requests()
	gap := reqs[1].At.Sub(reqs[0].At)
	// Retry-After: 1s should override the 50ms min backoff.
	if gap < 700*time.Millisecond {
		t.Errorf("retry gap = %v, want ~1s from Retry-After", gap)
	}
}

func TestStatsEndpoint(t *testing.T) {
	h := newHarness(t)
	h.upsertQueue("stats", nil)
	h.mustDo("POST", "/v1/queues/stats/tasks", map[string]any{"task_id": "s1", "path": "/ok"}, 201)
	h.mustDo("POST", "/v1/queues/stats/tasks", map[string]any{"task_id": "future_x", "path": "/ok", "delay_seconds": 3600}, 201)

	waitFor(t, 5*time.Second, "delivery", func() bool { return h.receiver.count("/ok") >= 1 })
	waitFor(t, 5*time.Second, "stats settle", func() bool {
		_, st := h.do("GET", "/v1/queues/stats/stats", nil)
		return st["succeeded"] == float64(1)
	})
	_, st := h.do("GET", "/v1/queues/stats/stats", nil)
	if st["scheduled_future"] != float64(1) || st["dispatched_last_1m"] != float64(1) || st["errors_last_1m"] != float64(0) {
		t.Errorf("stats: %v", st)
	}
	// id_prefix scoping
	_, st = h.do("GET", "/v1/queues/stats/stats?id_prefix=future_", nil)
	if st["scheduled_future"] != float64(1) || st["succeeded"] != float64(0) {
		t.Errorf("prefix stats: %v", st)
	}
}

func TestAuthRoles(t *testing.T) {
	// Separate harness with keys configured.
	st, err := sqlite.Open("sqlite:" + filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := &api.Server{
		Store: st,
		Auth: auth.New([]config.APIKey{
			{Key: "admin-key", Role: config.RoleAdmin},
			{Key: "writer-key", Role: config.RoleWriter},
		}, nil, false),
		Log: logger, Limits: config.Default().Limits, Now: time.Now,
	}
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	req := func(key, method, path string) int {
		r, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader([]byte("{}")))
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if got := req("", "PUT", "/v1/queues/q1"); got != 401 {
		t.Errorf("no key = %d, want 401", got)
	}
	if got := req("wrong", "PUT", "/v1/queues/q1"); got != 401 {
		t.Errorf("bad key = %d, want 401", got)
	}
	if got := req("writer-key", "PUT", "/v1/queues/q1"); got != 201 {
		t.Errorf("writer upsert = %d, want 201", got)
	}
	if got := req("writer-key", "GET", "/v1/queues"); got != 403 {
		t.Errorf("writer list-all = %d, want 403", got)
	}
	if got := req("writer-key", "DELETE", "/v1/queues/q1"); got != 403 {
		t.Errorf("writer delete = %d, want 403", got)
	}
	if got := req("admin-key", "GET", "/v1/queues"); got != 200 {
		t.Errorf("admin list = %d, want 200", got)
	}
	if got := req("admin-key", "DELETE", "/v1/queues/q1"); got != 204 {
		t.Errorf("admin delete = %d, want 204", got)
	}
}
