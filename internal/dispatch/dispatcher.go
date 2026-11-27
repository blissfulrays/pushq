// Package dispatch implements the task dispatcher: an in-memory active-queue
// scheduler (min-heap + token buckets), budgeted claims against the store,
// HTTP delivery workers, the lease sweeper, and retention cleanup.
//
// Correctness never depends on this process being the only dispatcher —
// every completion is fenced by the task's lease token. Leadership (when a
// LeaderElector is configured) is purely an optimization to avoid duplicate
// claim scans.
package dispatch

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jonboulle/clockwork"

	"github.com/blissfulrays/pushq/internal/metrics"
	"github.com/blissfulrays/pushq/internal/store"
)

type Options struct {
	GlobalConcurrency  int
	PerQueueClaimCap   int
	ReconcileInterval  time.Duration
	SweepInterval      time.Duration
	CleanupInterval    time.Duration
	LeaseGrace         time.Duration
	DrainTimeout       time.Duration
	RetentionSucceeded time.Duration
	RetentionFailed    time.Duration
	DedupWindow        time.Duration

	Clock      clockwork.Clock // nil = real clock
	HTTPClient *http.Client    // nil = tuned default
	Leader     LeaderElector   // nil = always leader (single node)
}

// LeaderElector serializes the dispatcher role across replicas (Postgres
// advisory lock in production).
type LeaderElector interface {
	// Acquire blocks until leadership is held or ctx ends. The returned
	// channel closes if leadership is later lost.
	Acquire(ctx context.Context) (lost <-chan struct{}, err error)
	// Release gives leadership up (e.g. on graceful shutdown).
	Release()
}

type doneMsg struct {
	queueID  int64
	cooldown time.Time // zero unless the target asked us to back off
}

type Dispatcher struct {
	st     store.Store
	opts   Options
	log    *slog.Logger
	met    *metrics.Metrics
	clock  clockwork.Clock
	client *http.Client

	doneCh   chan doneMsg
	role     atomic.Value // "leader" | "follower" | "stopped"
	cancel   context.CancelFunc
	loopWG   sync.WaitGroup
	workerWG sync.WaitGroup

	// workerCtx outlives the scheduling loop so in-flight dispatches can
	// finish during drain; cancelled only after DrainTimeout.
	workerCtx    context.Context
	workerCancel context.CancelFunc

	// Loop-owned state (no locks; touched only by the lead goroutine).
	queues      map[int64]*queueState
	byName      map[string]*queueState
	h           queueHeap
	globalInUse int

	// inflightByQueue counts running workers per queue ID. Unlike queues it
	// survives leadership terms: workers from a previous term may still be
	// running (and will report back) after a new term starts, so their slots
	// must stay accounted for. globalInUse persists for the same reason.
	inflightByQueue map[int64]int
	// parked holds queues that have work but were skipped because every
	// global slot was busy. They wait outside the heap (so the loop doesn't
	// spin) and are re-added when a worker finishes.
	parked map[int64]*queueState
}

func New(st store.Store, opts Options, log *slog.Logger, met *metrics.Metrics) *Dispatcher {
	if opts.Clock == nil {
		opts.Clock = clockwork.NewRealClock()
	}
	if opts.GlobalConcurrency <= 0 {
		opts.GlobalConcurrency = 512
	}
	if opts.PerQueueClaimCap <= 0 {
		opts.PerQueueClaimCap = 100
	}
	if opts.ReconcileInterval <= 0 {
		opts.ReconcileInterval = 30 * time.Second
	}
	if opts.SweepInterval <= 0 {
		opts.SweepInterval = 15 * time.Second
	}
	if opts.CleanupInterval <= 0 {
		opts.CleanupInterval = time.Minute
	}
	if opts.LeaseGrace <= 0 {
		opts.LeaseGrace = time.Minute
	}
	if opts.DrainTimeout <= 0 {
		opts.DrainTimeout = 30 * time.Second
	}
	client := opts.HTTPClient
	if client == nil {
		maxIdle := opts.GlobalConcurrency
		if maxIdle > 256 {
			maxIdle = 256
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		// Go's default of 2 idle conns per host would thrash TLS handshakes
		// when thousands of queues target one API host.
		transport.MaxIdleConnsPerHost = maxIdle
		transport.MaxIdleConns = maxIdle * 2
		client = &http.Client{Transport: transport}
	}
	d := &Dispatcher{
		st: st, opts: opts, log: log, met: met,
		clock:  opts.Clock,
		client: client,
		doneCh: make(chan doneMsg, 1024),
		queues: map[int64]*queueState{},
		byName: map[string]*queueState{},

		inflightByQueue: map[int64]int{},
		parked:          map[int64]*queueState{},
	}
	d.role.Store("stopped")
	return d
}

func (d *Dispatcher) Role() string { return d.role.Load().(string) }

func (d *Dispatcher) Start(ctx context.Context) {
	ctx, d.cancel = context.WithCancel(ctx)
	d.workerCtx, d.workerCancel = context.WithCancel(context.Background())
	d.loopWG.Add(1)
	go func() {
		defer d.loopWG.Done()
		d.run(ctx)
	}()
}

// Stop resigns leadership, drains in-flight dispatches (up to DrainTimeout),
// and blocks until the loop exits.
func (d *Dispatcher) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
	d.loopWG.Wait()
	d.role.Store("stopped")
}

func (d *Dispatcher) run(ctx context.Context) {
	defer d.drain()
	d.role.Store("follower")
	for ctx.Err() == nil {
		var lost <-chan struct{}
		if d.opts.Leader != nil {
			var err error
			lost, err = d.opts.Leader.Acquire(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				d.log.Error("leader election failed; retrying", "err", err)
				select {
				case <-ctx.Done():
					return
				case <-d.clock.After(5 * time.Second):
				}
				continue
			}
		}
		d.role.Store("leader")
		d.lead(ctx, lost)
		d.role.Store("follower")
		if d.opts.Leader != nil {
			d.opts.Leader.Release()
		}
	}
}

// lead runs the scheduling loop until ctx ends or leadership is lost.
func (d *Dispatcher) lead(ctx context.Context, lost <-chan struct{}) {
	d.queues = map[int64]*queueState{}
	d.byName = map[string]*queueState{}
	d.h = queueHeap{}
	d.parked = map[int64]*queueState{}
	// globalInUse and inflightByQueue are deliberately not reset: workers
	// from an earlier term may still be running.

	d.reconcile(ctx)
	lastReconcile := d.clock.Now()
	lastSweep := d.clock.Now()
	lastCleanup := d.clock.Now()

	for {
		now := d.clock.Now()

		// Earliest of: heap wake, reconcile, sweep, cleanup.
		wake := lastReconcile.Add(d.opts.ReconcileInterval)
		if t := lastSweep.Add(d.opts.SweepInterval); t.Before(wake) {
			wake = t
		}
		if t := lastCleanup.Add(d.opts.CleanupInterval); t.Before(wake) {
			wake = t
		}
		if hw := d.h.nextWake(); !hw.IsZero() && hw.Before(wake) {
			wake = hw
		}
		delay := wake.Sub(now)
		if delay < 0 {
			delay = 0
		}
		timer := d.clock.NewTimer(delay)

		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-lost:
			timer.Stop()
			return
		case <-timer.Chan():
		case ev := <-d.st.Events():
			timer.Stop()
			d.handleEvent(ctx, ev)
		case msg := <-d.doneCh:
			timer.Stop()
			d.handleDone(msg)
		}
		d.coalesce(ctx)

		now = d.clock.Now()
		if !now.Before(lastSweep.Add(d.opts.SweepInterval)) {
			d.sweep(ctx, now)
			lastSweep = now
		}
		if !now.Before(lastCleanup.Add(d.opts.CleanupInterval)) {
			lastCleanup = now
			if d.cleanup(ctx, now) {
				// Backlog left: come back in cleanupRetry, not a full interval.
				lastCleanup = now.Add(cleanupRetry - d.opts.CleanupInterval)
			}
		}
		if !now.Before(lastReconcile.Add(d.opts.ReconcileInterval)) {
			d.reconcile(ctx)
			lastReconcile = now
		}
		d.tick(ctx, d.clock.Now())
	}
}

// coalesce drains any queued events/completions without blocking so one tick
// covers a burst.
func (d *Dispatcher) coalesce(ctx context.Context) {
	for i := 0; i < 4096; i++ {
		select {
		case ev := <-d.st.Events():
			d.handleEvent(ctx, ev)
		case msg := <-d.doneCh:
			d.handleDone(msg)
		default:
			return
		}
	}
}

func (d *Dispatcher) drain() {
	done := make(chan struct{})
	go func() {
		d.workerWG.Wait()
		close(done)
	}()
	// Keep consuming completions: the loop has exited, and a worker blocked
	// sending to a full doneCh would otherwise never return.
	timeout := time.After(d.opts.DrainTimeout)
	for {
		select {
		case <-done:
			d.workerCancel()
			return
		case <-d.doneCh:
		case <-timeout:
			d.log.Warn("drain timeout; aborting in-flight dispatches")
			d.workerCancel()
			timeout = nil
		}
	}
}

// ensure returns the cached state for a queue, loading config on demand.
func (d *Dispatcher) ensure(ctx context.Context, queueID int64) *queueState {
	if qs, ok := d.queues[queueID]; ok {
		return qs
	}
	cfgs, err := d.st.GetQueuesByID(ctx, []int64{queueID})
	if err != nil {
		d.log.Error("load queue config", "queue_id", queueID, "err", err)
		return nil
	}
	if len(cfgs) == 0 {
		return nil
	}
	qs := newQueueState(cfgs[0])
	qs.inflight = d.inflightByQueue[queueID]
	d.queues[queueID] = qs
	d.byName[cfgs[0].Name] = qs
	d.met.ActiveQueues.Set(float64(len(d.queues)))
	return qs
}

func (d *Dispatcher) dropQueue(qs *queueState) {
	d.h.remove(qs)
	delete(d.queues, qs.cfg.ID)
	delete(d.byName, qs.cfg.Name)
	d.met.ActiveQueues.Set(float64(len(d.queues)))
}

func (d *Dispatcher) handleEvent(ctx context.Context, ev store.Event) {
	now := d.clock.Now()
	switch ev.Kind {
	case store.EventTaskReady:
		qs := d.ensure(ctx, ev.QueueID)
		if qs == nil {
			return
		}
		if !qs.hasWork || ev.ScheduleTime.Before(qs.nextReady) {
			qs.nextReady = ev.ScheduleTime
		}
		qs.hasWork = true
		d.h.update(qs, now)

	case store.EventQueueChanged:
		var qs *queueState
		if ev.QueueID != 0 {
			qs = d.queues[ev.QueueID]
		} else if ev.Queue != "" {
			qs = d.byName[ev.Queue]
		}
		if qs == nil {
			// Not tracked; nothing to refresh — a later task event or
			// reconcile picks it up.
			return
		}
		cfgs, err := d.st.GetQueuesByID(ctx, []int64{qs.cfg.ID})
		if err != nil {
			d.log.Error("reload queue config", "queue", qs.cfg.Name, "err", err)
			return
		}
		if len(cfgs) == 0 { // deleted
			d.dropQueue(qs)
			return
		}
		qs.applyConfig(cfgs[0])
		// Purge or resume can change what is pending; refresh precisely.
		next, err := d.st.NextPending(ctx, qs.cfg.ID, now)
		if err == nil {
			if next != nil {
				qs.nextReady = *next
				qs.hasWork = true
			} else {
				qs.hasWork = false
				qs.nextReady = time.Time{}
			}
		}
		d.h.update(qs, now)

	case store.EventRunTask:
		d.forceRun(ctx, ev)
	}
}

func (d *Dispatcher) handleDone(msg doneMsg) {
	d.globalInUse--
	d.met.Inflight.Set(float64(d.globalInUse))
	if n := d.inflightByQueue[msg.queueID] - 1; n > 0 {
		d.inflightByQueue[msg.queueID] = n
	} else {
		delete(d.inflightByQueue, msg.queueID)
	}
	now := d.clock.Now()
	// A global slot just freed up: let parked queues compete for it.
	for id, pq := range d.parked {
		delete(d.parked, id)
		d.h.update(pq, now)
	}
	qs, ok := d.queues[msg.queueID]
	if !ok {
		return
	}
	qs.inflight = d.inflightByQueue[msg.queueID]
	if msg.cooldown.After(qs.cooldown) {
		qs.cooldown = msg.cooldown
	}
	d.h.update(qs, now)
}

// forceRun dispatches one task immediately, bypassing rate limits and PAUSED
// state (GCP :run parity). Disabled queues and purged tasks still refuse.
func (d *Dispatcher) forceRun(ctx context.Context, ev store.Event) {
	qs := d.ensure(ctx, ev.QueueID)
	if qs == nil {
		return
	}
	now := d.clock.Now()
	token := uuid.NewString()
	t, claimed, err := d.st.ClaimTaskForRun(ctx, now, token, d.opts.LeaseGrace, ev.TaskPK)
	if err != nil {
		d.log.Error("claim for run", "task_pk", ev.TaskPK, "err", err)
		return
	}
	if !claimed {
		return
	}
	d.spawn(t, qs)
}

// spawn launches a worker for a claimed task. Loop goroutine only.
func (d *Dispatcher) spawn(t store.Task, qs *queueState) {
	d.inflightByQueue[qs.cfg.ID]++
	qs.inflight = d.inflightByQueue[qs.cfg.ID]
	d.globalInUse++
	d.met.Inflight.Set(float64(d.globalInUse))
	d.met.ClaimedTotal.Inc()
	cfg := qs.cfg
	d.workerWG.Add(1)
	go d.worker(t, cfg)
}

func (d *Dispatcher) tick(ctx context.Context, now time.Time) {
	due := d.h.due(now)
	if len(due) == 0 {
		return
	}

	var budgets []store.ClaimBudget
	requested := map[int64]int{}
	byID := map[int64]*queueState{}
	globalAvail := d.opts.GlobalConcurrency - d.globalInUse

	for _, qs := range due {
		if globalAvail <= 0 {
			// Every global slot is busy. Re-adding the queue to the heap
			// would make it due again immediately and spin the loop; park
			// it until handleDone frees a slot.
			d.parked[qs.cfg.ID] = qs
			continue
		}
		n := qs.budget(now, d.opts.PerQueueClaimCap, globalAvail)
		if n <= 0 {
			d.h.update(qs, now)
			continue
		}
		globalAvail -= n
		budgets = append(budgets, store.ClaimBudget{QueueID: qs.cfg.ID, N: n})
		requested[qs.cfg.ID] = n
		byID[qs.cfg.ID] = qs
	}
	if len(budgets) == 0 {
		return
	}

	token := uuid.NewString()
	tasks, err := d.st.ClaimTasks(ctx, now, token, d.opts.LeaseGrace, budgets)
	if err != nil {
		d.log.Error("claim tasks", "err", err)
		for _, qs := range byID {
			// Back off instead of retrying (and logging) in a tight loop.
			qs.nextReady = now.Add(time.Second)
			qs.hasWork = true
			d.h.update(qs, now)
		}
		return
	}

	got := map[int64]int{}
	for _, t := range tasks {
		got[t.QueueID]++
	}
	for id, qs := range byID {
		if n := got[id]; n > 0 {
			qs.limiter.AllowN(now, n) // consume the tokens actually used
		}
	}
	for _, t := range tasks {
		d.spawn(t, byID[t.QueueID])
	}

	for id, qs := range byID {
		if got[id] < requested[id] {
			// Queue ran dry: look up the exact next schedule (if any).
			next, err := d.st.NextPending(ctx, id, now)
			if err != nil {
				d.log.Error("next pending", "queue", qs.cfg.Name, "err", err)
				qs.nextReady = now.Add(time.Second) // retry shortly
				qs.hasWork = true
			} else if next != nil {
				qs.nextReady = *next
				qs.hasWork = true
			} else {
				qs.hasWork = false
				qs.nextReady = time.Time{}
			}
		} else {
			// Full budget claimed — likely more work ready now.
			qs.nextReady = now
			qs.hasWork = true
		}
		d.h.update(qs, now)
	}
}

func (d *Dispatcher) reconcile(ctx context.Context) {
	now := d.clock.Now()
	seeds, err := d.st.SeedDispatch(ctx)
	if err != nil {
		d.log.Error("seed dispatch", "err", err)
		return
	}
	ids := make([]int64, 0, len(seeds))
	for _, s := range seeds {
		ids = append(ids, s.QueueID)
	}
	cfgs, err := d.st.GetQueuesByID(ctx, ids)
	if err != nil {
		d.log.Error("load queue configs", "err", err)
		return
	}
	cfgByID := map[int64]store.Queue{}
	for _, c := range cfgs {
		cfgByID[c.ID] = c
	}

	seen := map[int64]bool{}
	for _, s := range seeds {
		cfg, ok := cfgByID[s.QueueID]
		if !ok {
			continue
		}
		seen[s.QueueID] = true
		qs, ok := d.queues[s.QueueID]
		if !ok {
			qs = newQueueState(cfg)
			qs.inflight = d.inflightByQueue[s.QueueID]
			d.queues[s.QueueID] = qs
			d.byName[cfg.Name] = qs
		} else {
			delete(d.byName, qs.cfg.Name)
			qs.applyConfig(cfg)
			d.byName[cfg.Name] = qs
		}
		if s.NextSchedule != nil {
			qs.nextReady = *s.NextSchedule
			qs.hasWork = true
		} else {
			qs.hasWork = false
			qs.nextReady = time.Time{}
		}
		d.h.update(qs, now)
	}
	// Drop idle queues that no longer have work.
	for id, qs := range d.queues {
		if !seen[id] && qs.inflight == 0 && !qs.hasWork {
			d.dropQueue(qs)
		}
	}
	d.met.ActiveQueues.Set(float64(len(d.queues)))
}

func (d *Dispatcher) sweep(ctx context.Context, now time.Time) {
	expired, err := d.st.ListExpiredLeases(ctx, now, 500)
	if err != nil {
		d.log.Error("sweep leases", "err", err)
		return
	}
	if len(expired) == 0 {
		return
	}
	// Resolve queue configs for retry decisions.
	need := map[int64]bool{}
	for _, t := range expired {
		if _, ok := d.queues[t.QueueID]; !ok {
			need[t.QueueID] = true
		}
	}
	cfgByID := map[int64]store.Queue{}
	for id, qs := range d.queues {
		cfgByID[id] = qs.cfg
	}
	if len(need) > 0 {
		ids := make([]int64, 0, len(need))
		for id := range need {
			ids = append(ids, id)
		}
		cfgs, err := d.st.GetQueuesByID(ctx, ids)
		if err != nil {
			d.log.Error("sweep load configs", "err", err)
			return
		}
		for _, c := range cfgs {
			cfgByID[c.ID] = c
		}
	}

	for _, t := range expired {
		cfg, ok := cfgByID[t.QueueID]
		if !ok {
			continue
		}
		d.met.LeasesExpired.Inc()
		att := store.Attempt{
			TaskPK: t.PK, QueueID: t.QueueID, Number: t.DispatchCount,
			DispatchTime: now, ErrorKind: store.ErrKindLeaseLost,
		}
		// The dispatch may well have happened (crashed worker) — counting
		// it preserves max_attempts semantics; at-least-once same as GCP.
		d.finishFailure(t, cfg, att, 0, nil, now, false)
	}
}

const (
	cleanupBatch  = 1000
	cleanupBudget = 250 * time.Millisecond // per pass; runs on the loop goroutine
	cleanupRetry  = time.Second            // next pass when a backlog remains
)

// cleanup deletes expired terminal tasks and purged rows in batches until
// none are left or the time budget is spent. It reports whether a backlog
// remains, in which case the caller should run it again soon rather than
// after a full CleanupInterval — one fixed batch per interval cannot keep up
// with sustained throughput.
func (d *Dispatcher) cleanup(ctx context.Context, now time.Time) (more bool) {
	deadline := time.Now().Add(cleanupBudget)
	terminalDone, purgedDone := false, false
	for !(terminalDone && purgedDone) {
		if time.Now().After(deadline) || ctx.Err() != nil {
			return true
		}
		if !terminalDone {
			// Up to cleanupBatch rows per state; fewer in total means every
			// state is drained.
			n, err := d.st.CleanupTerminal(ctx, now, d.opts.RetentionSucceeded, d.opts.RetentionFailed, d.opts.DedupWindow, cleanupBatch)
			if err != nil {
				d.log.Error("cleanup terminal", "err", err)
				terminalDone = true
			} else if n < cleanupBatch {
				terminalDone = true
			}
		}
		if !purgedDone {
			n, err := d.st.HardDeletePurged(ctx, cleanupBatch)
			if err != nil {
				d.log.Error("hard delete purged", "err", err)
				purgedDone = true
			} else if n < cleanupBatch {
				purgedDone = true
			}
		}
	}
	return false
}
