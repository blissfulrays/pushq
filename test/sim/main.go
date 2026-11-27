// Command sim is a production-shaped load test for a running pushq server.
//
// It plays both sides of the queue: a producer that enqueues over the HTTP API
// the way a typical backend service would (one task per call, open-loop at a target rate),
// and a fake receiver with realistic latency and error rates. Every delivery is
// reconciled against every enqueue, so it reports loss, duplicates, lateness,
// per-queue rate/concurrency compliance, enqueue latency, and server resources.
//
//	go run ./test/sim -api http://127.0.0.1:8080 -key $KEY -weights weights.json
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ---------- flags ----------

var (
	apiURL     = flag.String("api", "http://127.0.0.1:8080", "pushq base URL")
	apiKey     = flag.String("key", os.Getenv("PUSHQ_KEY"), "pushq API key (admin for cleanup)")
	recvAddr   = flag.String("recv", "127.0.0.1:9099", "fake receiver listen address")
	weightsF   = flag.String("weights", "", "JSON object of per-queue 30d attempt counts (prod shape)")
	numQueues  = flag.Int("queues", 25000, "number of account queues")
	prefill    = flag.Int("prefill", 60000, "long-delay pending tasks to preload (prod holds 41k-78k)")
	plan       = flag.String("plan", "full", "full | quick")
	pushqDir   = flag.String("pushq-dir", ".", "dir to restart pushq from (chaos)")
	pushqCmd   = flag.String("pushq-cmd", "./pushq serve --db sqlite:./pushq-local.db --listen 127.0.0.1:8080", "command to (re)start pushq")
	dbFile     = flag.String("db-file", "pushq-local.db", "sqlite db file (for size/row sampling), relative to pushq-dir; empty to skip")
	outDir     = flag.String("out", ".", "where to write timeline.csv and summary.json")
	minFreeGB  = flag.Float64("min-free-gb", 8, "abort if free disk drops below this")
	deadlineS  = flag.Float64("deadline", 60, "task dispatch_deadline_seconds (prod default 600; lower = faster crash recovery in the test)")
	cleanupEnd = flag.Bool("cleanup", true, "delete loadtest queues at the end")
)

// ---------- task records ----------

const (
	kindNow = iota
	kindShort
	kindLong
	kindBurst
)

type rec struct {
	phase   int32
	queue   int32
	kind    int8
	enqNS   int64 // enqueue call start
	schedNS int64 // intended schedule time
	enqOK   atomic.Bool
	recvs   atomic.Int32
	firstNS atomic.Int64
	status  atomic.Int32
}

var (
	recMu sync.RWMutex
	recs  []*rec
)

func newRec(r *rec) int {
	recMu.Lock()
	recs = append(recs, r)
	i := len(recs) - 1
	recMu.Unlock()
	return i
}

func getRec(i int) *rec {
	recMu.RLock()
	defer recMu.RUnlock()
	if i < 0 || i >= len(recs) {
		return nil
	}
	return recs[i]
}

// ---------- queues ----------

type qcfg struct {
	name   string
	rate   float64
	conc   int
	paused bool
	weight float64
}

var queues []qcfg

const burstQ = -1 // index sentinel resolved to len(queues)-1

func qname(i int) string { return fmt.Sprintf("loadtest/q%05d", i) }

// ---------- receiver ----------

type qstat struct {
	mu        sync.Mutex
	inflight  int
	maxInfl   int
	sec       int64
	secN      int
	maxSec    int
	win       int64 // 10s window id
	winN      int
	maxWin    int
	delivered int
}

var (
	qstats       []qstat
	recvTotal    atomic.Int64
	recvUnknown  atomic.Int64
	recentLagMu  sync.Mutex
	recentLag    []float64 // ring of recent due-task lags (s)
	recentLagIdx int
)

func pushLag(s float64) {
	recentLagMu.Lock()
	if len(recentLag) < 4096 {
		recentLag = append(recentLag, s)
	} else {
		recentLag[recentLagIdx%4096] = s
	}
	recentLagIdx++
	recentLagMu.Unlock()
}

func recentLagP99() (float64, int) {
	recentLagMu.Lock()
	cp := append([]float64(nil), recentLag...)
	recentLag = recentLag[:0]
	recentLagIdx = 0
	recentLagMu.Unlock()
	if len(cp) == 0 {
		return 0, 0
	}
	sort.Float64s(cp)
	return cp[int(float64(len(cp)-1)*0.99)], len(cp)
}

func lognormalLatency(rng *rand.Rand) time.Duration {
	// median 400ms, sigma 1.0 -> p90 ~1.4s, p99 ~4.1s; capped at 15s.
	ms := 400 * math.Exp(rng.NormFloat64()*1.0)
	if ms > 15000 {
		ms = 15000
	}
	return time.Duration(ms * float64(time.Millisecond))
}

var rngPool = sync.Pool{New: func() any { return rand.New(rand.NewSource(time.Now().UnixNano())) }}

func receiver(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	io.Copy(io.Discard, r.Body)
	recvTotal.Add(1)
	qn := r.Header.Get("X-Pushq-Queue")
	tid := r.Header.Get("X-Pushq-Task-Id")
	qi := -1
	if strings.HasPrefix(qn, "loadtest/q") {
		qi, _ = strconv.Atoi(strings.TrimPrefix(qn, "loadtest/q"))
	}
	ri := -1
	if strings.HasPrefix(tid, "t") {
		ri, _ = strconv.Atoi(tid[1:])
	}
	rc := getRec(ri)
	if rc == nil || qi < 0 || qi >= len(qstats) {
		recvUnknown.Add(1)
		w.WriteHeader(200)
		return
	}
	n := rc.recvs.Add(1)
	if n == 1 {
		rc.firstNS.Store(now.UnixNano())
		if rc.kind == kindNow || rc.kind == kindShort {
			if s, err := time.Parse(time.RFC3339Nano, r.Header.Get("X-Pushq-Scheduled-Time")); err == nil {
				pushLag(now.Sub(s).Seconds())
			}
		}
	}

	qs := &qstats[qi]
	sec := now.Unix()
	qs.mu.Lock()
	qs.inflight++
	if qs.inflight > qs.maxInfl {
		qs.maxInfl = qs.inflight
	}
	if sec != qs.sec {
		qs.sec, qs.secN = sec, 0
	}
	qs.secN++
	if qs.secN > qs.maxSec {
		qs.maxSec = qs.secN
	}
	if sec/10 != qs.win {
		qs.win, qs.winN = sec/10, 0
	}
	qs.winN++
	if qs.winN > qs.maxWin {
		qs.maxWin = qs.winN
	}
	qs.delivered++
	qs.mu.Unlock()

	rng := rngPool.Get().(*rand.Rand)
	lat := lognormalLatency(rng)
	roll := rng.Float64()
	rngPool.Put(rng)
	time.Sleep(lat)

	status := 200
	switch {
	case roll < 0.02:
		status = 500 // Meta error surfaced as 500, like prod handlers
	case roll < 0.025:
		status = 429
		w.Header().Set("Retry-After", "30")
	}
	rc.status.Store(int32(status))
	qs.mu.Lock()
	qs.inflight--
	qs.mu.Unlock()
	w.WriteHeader(status)
}

// ---------- pushq client ----------

var client = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns: 1024, MaxIdleConnsPerHost: 1024, MaxConnsPerHost: 0,
		IdleConnTimeout: 90 * time.Second,
		DialContext:     (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
	},
}

func call(method, path string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, *apiURL+path, rd)
	req.Header.Set("Authorization", "Bearer "+*apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, nil
}

func qpath(name string) string { return "/v1/queues/" + url.PathEscape(name) }

var payload = func() map[string]any {
	// ~2.5KB JSON like a Meta webhook entry.
	b := make([]byte, 2400)
	for i := range b {
		b[i] = "abcdefghijklmnopqrstuvwxyz0123456789"[i%36]
	}
	return map[string]any{"object": "instagram", "entry": string(b)}
}()

// ---------- enqueue stats ----------

type phaseStats struct {
	name      string
	rate      float64
	start     time.Time
	end       time.Time
	mu        sync.Mutex
	enqLat    []float64 // ms
	enqErr    atomic.Int64
	enqErrMsg sync.Map
	dropped   atomic.Int64 // producer couldn't keep up
	sent      atomic.Int64
}

var (
	phasesMu sync.Mutex
	phases   = make([]*phaseStats, 0, 64) // no realloc: workers index it concurrently
)

type job struct {
	ri int
	rc *rec
	q  int
	d  float64
}

func enqueueWorker(ch <-chan job) {
	for j := range ch {
		ps := phases[j.rc.phase]
		body := map[string]any{
			"task_id": "t" + strconv.Itoa(j.ri), "path": "/hook", "json_body": payload,
			"dispatch_deadline_seconds": *deadlineS,
		}
		if j.d > 0 {
			body["delay_seconds"] = j.d
		}
		t0 := time.Now()
		j.rc.enqNS = t0.UnixNano()
		j.rc.schedNS = t0.Add(time.Duration(j.d * float64(time.Second))).UnixNano()
		code, b, err := call("POST", qpath(queues[j.q].name)+"/tasks", body)
		ms := float64(time.Since(t0).Microseconds()) / 1000
		if err != nil || code/100 != 2 {
			ps.enqErr.Add(1)
			msg := ""
			if err != nil {
				msg = err.Error()
				if i := strings.LastIndex(msg, ": "); i > 0 {
					msg = msg[i+2:]
				}
			} else {
				msg = fmt.Sprintf("%d %s", code, truncate(string(b), 80))
			}
			v, _ := ps.enqErrMsg.LoadOrStore(msg, new(atomic.Int64))
			v.(*atomic.Int64).Add(1)
			continue
		}
		j.rc.enqOK.Store(true)
		ps.mu.Lock()
		ps.enqLat = append(ps.enqLat, ms)
		ps.mu.Unlock()
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// weighted queue picker (alias-free: cumulative + binary search)
var cum []float64

func buildPicker() {
	cum = make([]float64, len(queues)-1) // exclude burst queue
	t := 0.0
	for i := range cum {
		t += queues[i].weight
		cum[i] = t
	}
}

func pick(rng *rand.Rand) int {
	x := rng.Float64() * cum[len(cum)-1]
	return sort.SearchFloat64s(cum, x)
}

func pickKind(rng *rand.Rand) (int8, float64) {
	r := rng.Float64()
	switch {
	case r < 0.85:
		return kindNow, 0
	case r < 0.95:
		return kindShort, 5 + rng.Float64()*295 // 5s-5min: delayed replies, Delay steps
	default:
		return kindLong, 86400 + rng.Float64()*6*86400 // 1-7 days: follow-ups
	}
}

// runRate drives an open-loop producer at `rate` tasks/s for `dur`.
// stop() may end it early (returns true if stopped early).
func runRate(pi int, rate float64, dur time.Duration, ch chan<- job, stop func(elapsed time.Duration) bool) bool {
	ps := phases[pi]
	rng := rand.New(rand.NewSource(int64(pi) * 7919))
	start := time.Now()
	sent := 0.0
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	lastCheck := start
	for now := range tick.C {
		el := now.Sub(start)
		if el >= dur {
			return false
		}
		if stop != nil && now.Sub(lastCheck) >= 10*time.Second {
			lastCheck = now
			if stop(el) {
				return true
			}
		}
		want := rate * el.Seconds()
		for sent < want {
			sent++
			q := pick(rng)
			k, d := pickKind(rng)
			rc := &rec{phase: int32(pi), queue: int32(q), kind: k}
			ri := newRec(rc)
			select {
			case ch <- job{ri, rc, q, d}:
				ps.sent.Add(1)
			default:
				ps.dropped.Add(1)
			}
		}
	}
	return false
}

func newPhase(name string, rate float64) int {
	phasesMu.Lock()
	defer phasesMu.Unlock()
	phases = append(phases, &phaseStats{name: name, rate: rate, start: time.Now()})
	return len(phases) - 1
}

// ---------- pushq process control ----------

func pushqPID() int {
	out, _ := exec.Command("lsof", "-tiTCP:"+portOf(*apiURL), "-sTCP:LISTEN").Output()
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return 0
	}
	p, _ := strconv.Atoi(f[0])
	return p
}

func portOf(u string) string {
	pu, _ := url.Parse(u)
	return pu.Port()
}

func startPushq(env []string) {
	logf, _ := os.OpenFile(filepath.Join(*outDir, "pushq.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	cmd := exec.Command("/bin/sh", "-c", "exec "+*pushqCmd)
	cmd.Dir = *pushqDir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // survive the harness
	if err := cmd.Start(); err != nil {
		logln("pushq start failed:", err)
		return
	}
	go cmd.Wait()
	for i := 0; i < 200; i++ {
		if code, _, err := call("GET", "/readyz", nil); err == nil && code == 200 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	logln("pushq did not become ready in 20s")
}

func stopPushq(sig syscall.Signal) time.Duration {
	pid := pushqPID()
	if pid == 0 {
		return 0
	}
	t0 := time.Now()
	syscall.Kill(pid, sig)
	for i := 0; i < 600; i++ {
		if syscall.Kill(pid, 0) != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return time.Since(t0)
}

// ---------- sampling ----------

type sample struct {
	t                           time.Time
	cpu, rssMB, dbMB, walMB     float64
	pending, leased, succ, fail int64
	recv                        int64
	freeGB                      float64
}

var (
	samplesMu sync.Mutex
	samples   []sample
	abortDisk atomic.Bool
)

func freeGB(path string) float64 {
	var s syscall.Statfs_t
	if syscall.Statfs(path, &s) != nil {
		return -1
	}
	return float64(s.Bavail) * float64(s.Bsize) / 1e9
}

func sampler(ctx context.Context) {
	tk := time.NewTicker(5 * time.Second)
	defer tk.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
		n++
		s := sample{t: time.Now(), recv: recvTotal.Load(), freeGB: freeGB(*pushqDir)}
		if pid := pushqPID(); pid > 0 {
			out, _ := exec.Command("ps", "-o", "%cpu=,rss=", "-p", strconv.Itoa(pid)).Output()
			f := strings.Fields(string(out))
			if len(f) == 2 {
				s.cpu, _ = strconv.ParseFloat(f[0], 64)
				r, _ := strconv.ParseFloat(f[1], 64)
				s.rssMB = r / 1024
			}
		}
		if *dbFile != "" {
			p := filepath.Join(*pushqDir, *dbFile)
			if st, err := os.Stat(p); err == nil {
				s.dbMB = float64(st.Size()) / 1e6
			}
			if st, err := os.Stat(p + "-wal"); err == nil {
				s.walMB = float64(st.Size()) / 1e6
			}
			if n%6 == 0 { // every 30s
				out, err := exec.Command("sqlite3", "-readonly", p,
					"select state, count(*) from tasks group by state").Output()
				if err == nil {
					for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
						kv := strings.Split(line, "|")
						if len(kv) != 2 {
							continue
						}
						v, _ := strconv.ParseInt(kv[1], 10, 64)
						switch kv[0] {
						case "pending":
							s.pending = v
						case "leased":
							s.leased = v
						case "succeeded":
							s.succ = v
						case "failed":
							s.fail = v
						}
					}
				}
			}
		}
		if s.freeGB >= 0 && s.freeGB < *minFreeGB {
			abortDisk.Store(true)
		}
		samplesMu.Lock()
		samples = append(samples, s)
		samplesMu.Unlock()
	}
}

// ---------- logging ----------

var logStart = time.Now()

func logln(a ...any) {
	fmt.Printf("[%6.0fs] %s\n", time.Since(logStart).Seconds(), fmt.Sprint(a...))
}

// ---------- reporting ----------

// pct returns -1 for an empty sample (JSON can't encode NaN).
func pct(v []float64, p float64) float64 {
	if len(v) == 0 {
		return -1
	}
	return math.Round(v[int(float64(len(v)-1)*p)]*1000) / 1000
}

type phaseReport struct {
	Name            string           `json:"name"`
	TargetRate      float64          `json:"target_rate"`
	AchievedEnqRate float64          `json:"achieved_enqueue_rate"`
	DurationS       float64          `json:"duration_s"`
	Sent            int64            `json:"sent"`
	Dropped         int64            `json:"producer_dropped"`
	EnqErrors       int64            `json:"enqueue_errors"`
	EnqErrorKinds   map[string]int64 `json:"enqueue_error_kinds,omitempty"`
	EnqP50ms        float64          `json:"enqueue_p50_ms"`
	EnqP99ms        float64          `json:"enqueue_p99_ms"`
	EnqMaxms        float64          `json:"enqueue_max_ms"`
	DueExpected     int              `json:"due_expected"`
	Delivered       int              `json:"delivered"`
	Lost            int              `json:"lost"`
	Duplicated      int              `json:"duplicated"`
	LagP50s         float64          `json:"lag_p50_s"`
	LagP99s         float64          `json:"lag_p99_s"`
	LagMaxs         float64          `json:"lag_max_s"`
	LagP99NonSatS   float64          `json:"lag_p99_unsaturated_queues_s"`
	ShortLagP99s    float64          `json:"delayed_task_lag_p99_s"`
	SaturatedQueues int              `json:"saturated_queues"`
}

func report(drainEnd time.Time) []phaseReport {
	recMu.RLock()
	all := append([]*rec(nil), recs...)
	recMu.RUnlock()
	type acc struct {
		due, deliv, lost, dup int
		lag, lagNS, short     []float64
	}
	accs := make([]acc, len(phases))
	for _, rc := range all {
		if !rc.enqOK.Load() || rc.kind == kindLong || queues[rc.queue].paused {
			continue
		}
		if rc.schedNS > drainEnd.UnixNano() {
			continue
		}
		a := &accs[rc.phase]
		a.due++
		n := rc.recvs.Load()
		if n == 0 {
			a.lost++
			continue
		}
		a.deliv++
		if n > 1 {
			a.dup++
		}
		lag := float64(rc.firstNS.Load()-rc.schedNS) / 1e9
		if rc.kind == kindShort {
			a.short = append(a.short, lag)
		}
		if rc.kind == kindBurst {
			continue
		}
		a.lag = append(a.lag, lag)
		ps := phases[rc.phase]
		if ps.rate == 0 || queues[rc.queue].weight/totalWeight*ps.rate < 0.8*queueCap(queues[rc.queue]) {
			a.lagNS = append(a.lagNS, lag)
		}
	}
	var out []phaseReport
	for i, ps := range phases {
		a := accs[i]
		sort.Float64s(a.lag)
		sort.Float64s(a.lagNS)
		sort.Float64s(a.short)
		ps.mu.Lock()
		el := append([]float64(nil), ps.enqLat...)
		ps.mu.Unlock()
		sort.Float64s(el)
		dur := ps.end.Sub(ps.start).Seconds()
		kinds := map[string]int64{}
		ps.enqErrMsg.Range(func(k, v any) bool { kinds[k.(string)] = v.(*atomic.Int64).Load(); return true })
		sat := 0
		if ps.rate > 0 {
			for _, q := range queues[:len(queues)-1] {
				if !q.paused && q.weight/totalWeight*ps.rate >= 0.8*queueCap(q) {
					sat++
				}
			}
		}
		out = append(out, phaseReport{
			Name: ps.name, TargetRate: ps.rate, DurationS: dur,
			AchievedEnqRate: float64(len(el)) / math.Max(dur, 1),
			Sent:            ps.sent.Load(), Dropped: ps.dropped.Load(), EnqErrors: ps.enqErr.Load(), EnqErrorKinds: kinds,
			EnqP50ms: pct(el, .5), EnqP99ms: pct(el, .99), EnqMaxms: pct(el, 1),
			DueExpected: a.due, Delivered: a.deliv, Lost: a.lost, Duplicated: a.dup,
			LagP50s: pct(a.lag, .5), LagP99s: pct(a.lag, .99), LagMaxs: pct(a.lag, 1),
			LagP99NonSatS: pct(a.lagNS, .99), ShortLagP99s: pct(a.short, .99), SaturatedQueues: sat,
		})
	}
	return out
}

var totalWeight float64

// queueCap is a queue's sustainable throughput: its rate limit, or what its
// concurrency limit allows at the receiver's mean latency (~0.66s), if lower.
func queueCap(q qcfg) float64 {
	return math.Min(q.rate, float64(q.conc)/0.66)
}

// ---------- main ----------

func main() {
	flag.Parse()
	if *apiKey == "" {
		fmt.Fprintln(os.Stderr, "need -key or PUSHQ_KEY")
		os.Exit(2)
	}
	os.MkdirAll(*outDir, 0o755)

	// Prod-shaped weights.
	var weights []float64
	if *weightsF != "" {
		b, err := os.ReadFile(*weightsF)
		if err != nil {
			panic(err)
		}
		var m map[string]float64
		if err := json.Unmarshal(b, &m); err != nil {
			panic(err)
		}
		for _, v := range m {
			weights = append(weights, v)
		}
		sort.Sort(sort.Reverse(sort.Float64Slice(weights)))
	}
	rng := rand.New(rand.NewSource(42))
	queues = make([]qcfg, *numQueues+1)
	for i := 0; i < *numQueues; i++ {
		w := 0.0
		if i < len(weights) {
			w = weights[i]
		}
		queues[i] = qcfg{name: qname(i), rate: 10, conc: 5, weight: w}
	}
	// ~850 auto-throttled at 1/s and 30 paused, like prod; keep the top 20 normal.
	perm := rng.Perm(*numQueues - 20)
	for k, p := range perm {
		i := p + 20
		switch {
		case k < 850:
			queues[i].rate, queues[i].conc = 1, 1
		case k < 880:
			queues[i].paused = true
		}
	}
	queues[*numQueues] = qcfg{name: "loadtest/q99999", rate: 10, conc: 5} // burst queue
	for _, q := range queues[:*numQueues] {
		totalWeight += q.weight
	}
	buildPicker()
	qstats = make([]qstat, 100000)

	// Receiver.
	srv := &http.Server{Addr: *recvAddr, Handler: http.HandlerFunc(receiver)}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sampler(ctx)

	// Setup queues.
	logln(fmt.Sprintf("creating %d queues (+1 burst)...", *numQueues))
	t0 := time.Now()
	var qerr atomic.Int64
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	for i := range queues {
		wg.Add(1)
		sem <- struct{}{}
		go func(q qcfg) {
			defer wg.Done()
			defer func() { <-sem }()
			code, b, err := call("PUT", qpath(q.name), map[string]any{
				"rate_limits": map[string]any{"max_dispatches_per_second": q.rate, "max_concurrent_dispatches": q.conc},
				"retry":       map[string]any{"max_attempts": 1},
				"target":      map[string]any{"base_url": "http://" + *recvAddr},
			})
			if err != nil || code/100 != 2 {
				if qerr.Add(1) < 3 {
					logln("queue upsert failed:", code, err, truncate(string(b), 200))
				}
				return
			}
			if q.paused {
				call("POST", qpath(q.name)+":pause", nil)
			}
		}(queues[i])
	}
	wg.Wait()
	setupS := time.Since(t0).Seconds()
	logln(fmt.Sprintf("queues ready in %.1fs (%.0f/s), errors=%d", setupS, float64(len(queues))/setupS, qerr.Load()))
	if qerr.Load() > 0 {
		os.Exit(1)
	}

	// Prefill long-delay pending tasks via batch (like the standing follow-up backlog).
	t0 = time.Now()
	pre := newPhase("prefill", 0)
	{
		per := map[int][]map[string]any{}
		for i := 0; i < *prefill; i++ {
			q := pick(rng)
			rc := &rec{phase: int32(pre), queue: int32(q), kind: kindLong}
			ri := newRec(rc)
			per[q] = append(per[q], map[string]any{
				"task_id": "t" + strconv.Itoa(ri), "path": "/hook", "json_body": payload,
				"delay_seconds": 86400 + rng.Float64()*6*86400, "dispatch_deadline_seconds": *deadlineS,
			})
		}
		for q, ts := range per {
			for len(ts) > 0 {
				n := min(len(ts), 500)
				wg.Add(1)
				sem <- struct{}{}
				go func(q int, b []map[string]any) {
					defer wg.Done()
					defer func() { <-sem }()
					call("POST", qpath(queues[q].name)+"/tasks:batch", map[string]any{"tasks": b})
				}(q, ts[:n])
				ts = ts[n:]
			}
		}
		wg.Wait()
	}
	phases[pre].end = time.Now()
	logln(fmt.Sprintf("prefilled %d long-delay tasks in %.1fs", *prefill, time.Since(t0).Seconds()))

	// Producer pool.
	ch := make(chan job, 200000)
	for i := 0; i < 384; i++ {
		go enqueueWorker(ch)
	}

	type step struct {
		name  string
		rate  float64
		dur   time.Duration
		ramp  bool
		burst bool
		chaos bool
	}
	var steps []step
	if *plan == "quick" {
		steps = []step{{name: "1x", rate: 50, dur: 60 * time.Second}}
	} else if *plan == "chaos" {
		steps = []step{
			{name: "3x warmup", rate: 150, dur: 2 * time.Minute},
			{name: "3x + SIGTERM restart + kill -9", rate: 150, dur: 6 * time.Minute, chaos: true},
		}
	} else {
		steps = []step{
			{name: "1x (50/s)", rate: 50, dur: 5 * time.Minute},
			{name: "3x (150/s) + viral burst", rate: 150, dur: 10 * time.Minute, burst: true},
			{name: "10x (500/s)", rate: 500, dur: 5 * time.Minute},
			{name: "ramp 1000/s", rate: 1000, dur: 2 * time.Minute, ramp: true},
			{name: "ramp 1500/s", rate: 1500, dur: 2 * time.Minute, ramp: true},
			{name: "ramp 2000/s", rate: 2000, dur: 2 * time.Minute, ramp: true},
			{name: "3x + SIGTERM restart + kill -9", rate: 150, dur: 6 * time.Minute, chaos: true},
		}
	}

	progress := func(pi int) {
		ps := phases[pi]
		p99, n := recentLagP99()
		samplesMu.Lock()
		var s sample
		if len(samples) > 0 {
			s = samples[len(samples)-1]
		}
		samplesMu.Unlock()
		logln(fmt.Sprintf("%-32s sent=%d drop=%d enqErr=%d recv=%d lagP99(10s)=%.2fs n=%d cpu=%.0f%% rss=%.0fMB db=%.0fMB wal=%.0fMB free=%.1fGB",
			ps.name, ps.sent.Load(), ps.dropped.Load(), ps.enqErr.Load(), recvTotal.Load(), p99, n, s.cpu, s.rssMB, s.dbMB, s.walMB, s.freeGB))
	}

	brokeAt := ""
	for _, st := range steps {
		if abortDisk.Load() {
			logln("ABORT: free disk below threshold")
			break
		}
		pi := newPhase(st.name, st.rate)
		logln("=== phase:", st.name)
		done := make(chan struct{})
		if st.burst {
			go func() {
				time.Sleep(4 * time.Minute)
				logln("viral burst: 5000 tasks into one queue over 10s")
				bq := len(queues) - 1
				for b := 0; b < 10; b++ {
					var batch []map[string]any
					for k := 0; k < 500; k++ {
						rc := &rec{phase: int32(pi), queue: int32(bq), kind: kindBurst}
						ri := newRec(rc)
						rc.enqNS = time.Now().UnixNano()
						rc.schedNS = rc.enqNS
						rc.enqOK.Store(true)
						batch = append(batch, map[string]any{"task_id": "t" + strconv.Itoa(ri), "path": "/hook",
							"json_body": payload, "dispatch_deadline_seconds": *deadlineS})
					}
					call("POST", qpath(queues[bq].name)+"/tasks:batch", map[string]any{"tasks": batch})
					time.Sleep(time.Second)
				}
			}()
		}
		if st.chaos {
			go func() {
				time.Sleep(2 * time.Minute)
				logln("chaos: SIGTERM (graceful) + restart")
				d := stopPushq(syscall.SIGTERM)
				logln(fmt.Sprintf("chaos: old process exited after %.1fs", d.Seconds()))
				startPushq(nil)
				logln("chaos: restarted")
				time.Sleep(2 * time.Minute)
				logln("chaos: kill -9 + restart")
				stopPushq(syscall.SIGKILL)
				startPushq(nil)
				logln("chaos: restarted after kill -9")
			}()
		}
		go func() {
			tk := time.NewTicker(10 * time.Second)
			defer tk.Stop()
			for {
				select {
				case <-done:
					return
				case <-tk.C:
					progress(pi)
				}
			}
		}()
		var stop func(time.Duration) bool
		if st.ramp {
			stop = func(el time.Duration) bool {
				ps := phases[pi]
				sent := ps.sent.Load() + ps.dropped.Load()
				if sent > 0 && float64(ps.dropped.Load()+ps.enqErr.Load())/float64(sent) > 0.01 {
					brokeAt = fmt.Sprintf("%s: >1%% enqueue drops/errors", st.name)
					return true
				}
				return abortDisk.Load()
			}
		}
		early := runRate(pi, st.rate, st.dur, ch, stop)
		phases[pi].end = time.Now()
		close(done)
		progress(pi)
		if early {
			logln("stopping ramp:", brokeAt)
			break
		}
	}

	// Drain: wait for every due task to arrive (or stall), past lease expiry.
	logln("=== drain")
	dpi := newPhase("drain", 0)
	_ = dpi
	for len(ch) > 0 {
		time.Sleep(time.Second)
	}
	time.Sleep(2 * time.Second)
	minWait := time.Duration(*deadlineS+60+30) * time.Second // lease = deadline + grace, + sweep
	drainStart := time.Now()
	last := recvTotal.Load()
	stall := time.Now()
	for {
		time.Sleep(10 * time.Second)
		cur := recvTotal.Load()
		if cur != last {
			last, stall = cur, time.Now()
		}
		pending := 0
		recMu.RLock()
		now := time.Now().UnixNano()
		for _, rc := range recs {
			if rc.enqOK.Load() && rc.kind != kindLong && !queues[rc.queue].paused && rc.schedNS < now && rc.recvs.Load() == 0 {
				pending++
			}
		}
		recMu.RUnlock()
		logln(fmt.Sprintf("drain: due-but-undelivered=%d recv=%d", pending, cur))
		if time.Since(drainStart) > minWait && (pending == 0 || time.Since(stall) > 3*time.Minute) {
			break
		}
		if time.Since(drainStart) > 25*time.Minute {
			logln("drain: giving up after 25m")
			break
		}
	}
	phases[dpi].end = time.Now()
	drainEnd := time.Now()

	// Cleanup-throughput check: shrink retention so the cleanup loop must keep
	// up with live completions, and watch the succeeded-row count.
	if (*plan == "full" || *plan == "chaos") && !abortDisk.Load() {
		logln("=== retention check: restart with retention/dedup = 1m, run 3x for 6m")
		stopPushq(syscall.SIGTERM)
		startPushq([]string{"PUSHQ_RETENTION_SUCCEEDED=1m", "PUSHQ_RETENTION_FAILED=1m", "PUSHQ_DEDUP_WINDOW=1m"})
		pi := newPhase("retention check 3x (excluded from loss)", 150)
		done := make(chan struct{})
		go func() {
			tk := time.NewTicker(30 * time.Second)
			defer tk.Stop()
			for {
				select {
				case <-done:
					return
				case <-tk.C:
					progress(pi)
					samplesMu.Lock()
					if len(samples) > 0 {
						s := samples[len(samples)-1]
						logln(fmt.Sprintf("rows: succeeded=%d failed=%d pending=%d", s.succ, s.fail, s.pending))
					}
					samplesMu.Unlock()
				}
			}
		}()
		runRate(pi, 150, 6*time.Minute, ch, nil)
		phases[pi].end = time.Now()
		close(done)
		time.Sleep(35 * time.Second)
		logln("retention check done; restarting pushq with its original settings")
		stopPushq(syscall.SIGTERM)
		startPushq(nil)
	}

	// Paused queues must have received nothing.
	pausedDeliv := 0
	viol := []string{}
	burstMaxSec, burstMaxWin, burstInfl := 0, 0, 0
	for i := range queues {
		qi := i
		if i == len(queues)-1 {
			qi = 99999
		}
		qs := &qstats[qi]
		qs.mu.Lock()
		if queues[i].paused {
			pausedDeliv += qs.delivered
		}
		// token bucket: a 1s window may see burst+rate; a 10s window <= burst + 10*rate
		limSec := int(queues[i].rate)*2 + 1
		limWin := int(queues[i].rate*10) + int(queues[i].rate) + 1
		if qs.maxInfl > queues[i].conc || qs.maxSec > limSec || qs.maxWin > limWin {
			if len(viol) < 20 {
				viol = append(viol, fmt.Sprintf("%s rate=%.0f conc=%d: maxInflight=%d max1s=%d max10s=%d",
					queues[i].name, queues[i].rate, queues[i].conc, qs.maxInfl, qs.maxSec, qs.maxWin))
			}
		}
		if i == len(queues)-1 {
			burstMaxSec, burstMaxWin, burstInfl = qs.maxSec, qs.maxWin, qs.maxInfl
		}
		qs.mu.Unlock()
	}

	reps := report(drainEnd)
	samplesMu.Lock()
	ss := append([]sample(nil), samples...)
	samplesMu.Unlock()
	f, _ := os.Create(filepath.Join(*outDir, "timeline.csv"))
	fmt.Fprintln(f, "t_s,cpu_pct,rss_mb,db_mb,wal_mb,pending,leased,succeeded,failed,recv_total,free_gb")
	for _, s := range ss {
		fmt.Fprintf(f, "%.0f,%.0f,%.0f,%.0f,%.0f,%d,%d,%d,%d,%d,%.1f\n", s.t.Sub(logStart).Seconds(), s.cpu, s.rssMB, s.dbMB, s.walMB, s.pending, s.leased, s.succ, s.fail, s.recv, s.freeGB)
	}
	f.Close()
	maxCPU, maxRSS, maxDB := 0.0, 0.0, 0.0
	for _, s := range ss {
		maxCPU, maxRSS, maxDB = math.Max(maxCPU, s.cpu), math.Max(maxRSS, s.rssMB), math.Max(maxDB, s.dbMB+s.walMB)
	}
	summary := map[string]any{
		"queues": len(queues), "queue_setup_s": setupS, "prefill": *prefill,
		"phases": reps, "rate_or_concurrency_violations": viol, "paused_queue_deliveries": pausedDeliv,
		"burst_queue":        map[string]int{"max_per_1s": burstMaxSec, "max_per_10s": burstMaxWin, "max_inflight": burstInfl},
		"unknown_deliveries": recvUnknown.Load(), "ramp_break": brokeAt,
		"max_cpu_pct": maxCPU, "max_rss_mb": maxRSS, "max_db_plus_wal_mb": maxDB,
	}
	b, _ := json.MarshalIndent(summary, "", "  ")
	os.WriteFile(filepath.Join(*outDir, "summary.json"), b, 0o644)
	fmt.Println(string(b))

	if *cleanupEnd {
		logln("cleanup: deleting loadtest queues")
		var derr atomic.Int64
		for i := range queues {
			wg.Add(1)
			sem <- struct{}{}
			go func(n string) {
				defer wg.Done()
				defer func() { <-sem }()
				if code, _, err := call("DELETE", qpath(n), nil); err != nil || code/100 != 2 {
					derr.Add(1)
				}
			}(queues[i].name)
		}
		wg.Wait()
		logln(fmt.Sprintf("cleanup done, delete errors=%d", derr.Load()))
	}
	srv.Close()
}
