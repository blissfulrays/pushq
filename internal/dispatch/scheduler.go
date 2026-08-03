package dispatch

import (
	"container/heap"
	"math"
	"time"

	"golang.org/x/time/rate"

	"github.com/blissfulrays/pushq/internal/store"
)

// queueState is the scheduler's in-memory view of one queue. Owned exclusively
// by the dispatcher loop goroutine — no locking.
type queueState struct {
	cfg      store.Queue
	limiter  *rate.Limiter
	inflight int
	cooldown time.Time // 429/503 claim cooldown
	// nextReady is the earliest known pending schedule_time. zero + !hasWork
	// means "no known pending work" (queue left the heap; events or
	// reconcile re-add it).
	nextReady time.Time
	hasWork   bool

	heapIdx int // -1 when not in heap
}

func newQueueState(cfg store.Queue) *queueState {
	qs := &queueState{cfg: cfg, heapIdx: -1}
	qs.applyConfig(cfg)
	return qs
}

func (qs *queueState) applyConfig(cfg store.Queue) {
	qs.cfg = cfg
	limit := rate.Limit(cfg.RateLimits.MaxDispatchesPerSecond)
	burst := cfg.RateLimits.EffectiveBurst()
	if qs.limiter == nil {
		qs.limiter = rate.NewLimiter(limit, burst)
	} else {
		qs.limiter.SetLimit(limit)
		qs.limiter.SetBurst(burst)
	}
}

// effectiveWake is the earliest instant this queue could dispatch: known work,
// cooldown, and token availability all satisfied. Returns zero time when the
// queue has nothing to do (should not be in the heap).
func (qs *queueState) effectiveWake(now time.Time) time.Time {
	if !qs.hasWork || qs.cfg.State != store.QueueRunning {
		return time.Time{}
	}
	if qs.inflight >= qs.cfg.RateLimits.MaxConcurrentDispatches {
		return time.Time{} // re-added when a worker completes
	}
	wake := qs.nextReady
	if qs.cooldown.After(wake) {
		wake = qs.cooldown
	}
	if t := qs.tokenReadyAt(now); t.After(wake) {
		wake = t
	}
	return wake
}

// tokenReadyAt reports when at least one token will be available.
func (qs *queueState) tokenReadyAt(now time.Time) time.Time {
	tokens := qs.limiter.TokensAt(now)
	if tokens >= 1 {
		return now
	}
	limit := float64(qs.limiter.Limit())
	if limit <= 0 || math.IsInf(limit, 1) {
		return now
	}
	wait := (1 - tokens) / limit
	return now.Add(time.Duration(wait * float64(time.Second)))
}

// budget computes how many tasks this queue may claim right now.
func (qs *queueState) budget(now time.Time, claimCap, globalAvail int) int {
	if !qs.hasWork || qs.cfg.State != store.QueueRunning {
		return 0
	}
	if qs.cooldown.After(now) || qs.nextReady.After(now) {
		return 0
	}
	n := int(qs.limiter.TokensAt(now))
	if c := qs.cfg.RateLimits.MaxConcurrentDispatches - qs.inflight; c < n {
		n = c
	}
	if claimCap < n {
		n = claimCap
	}
	if globalAvail < n {
		n = globalAvail
	}
	if n < 0 {
		n = 0
	}
	return n
}

// --- min-heap of queues by wake time ---

type queueHeap struct {
	items []*heapItem
}

type heapItem struct {
	qs   *queueState
	wake time.Time
}

func (h *queueHeap) Len() int { return len(h.items) }
func (h *queueHeap) Less(i, j int) bool {
	return h.items[i].wake.Before(h.items[j].wake)
}
func (h *queueHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.items[i].qs.heapIdx = i
	h.items[j].qs.heapIdx = j
}
func (h *queueHeap) Push(x any) {
	it := x.(*heapItem)
	it.qs.heapIdx = len(h.items)
	h.items = append(h.items, it)
}
func (h *queueHeap) Pop() any {
	old := h.items
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	h.items = old[:n-1]
	it.qs.heapIdx = -1
	return it
}

// update inserts, moves, or removes a queue according to its effectiveWake.
func (h *queueHeap) update(qs *queueState, now time.Time) {
	wake := qs.effectiveWake(now)
	if wake.IsZero() {
		h.remove(qs)
		return
	}
	if qs.heapIdx >= 0 {
		h.items[qs.heapIdx].wake = wake
		heap.Fix(h, qs.heapIdx)
		return
	}
	heap.Push(h, &heapItem{qs: qs, wake: wake})
}

func (h *queueHeap) remove(qs *queueState) {
	if qs.heapIdx >= 0 {
		heap.Remove(h, qs.heapIdx)
	}
}

// nextWake returns the earliest wake in the heap (zero when empty).
func (h *queueHeap) nextWake() time.Time {
	if len(h.items) == 0 {
		return time.Time{}
	}
	return h.items[0].wake
}

// due pops every queue whose wake is at or before now and returns them.
func (h *queueHeap) due(now time.Time) []*queueState {
	var out []*queueState
	for len(h.items) > 0 && !h.items[0].wake.After(now) {
		it := heap.Pop(h).(*heapItem)
		out = append(out, it.qs)
	}
	return out
}
