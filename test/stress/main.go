// Command stress is a ramped load test for a running pushq server, local or
// hosted. Unlike test/sim it needs no receiver of its own: delivery tests
// send tasks back to the server's own /healthz, so it works against a remote
// server from any machine.
//
// Every phase ramps in steps and stops early when the error rate passes 5% or
// p99 latency passes its threshold. All queues are named loadtest/<phase>-<run>
// and are purged and paused at the end (deleted too when the key may).
//
//	PUSHQ_API_KEY=pq_... go run ./test/stress                    # every phase, hosted API
//	go run ./test/stress -base http://localhost:8080 -phase enqueue -steps 8,64,256
//	go run ./test/stress -phase linktodm -queues 1000 -prefill 60000
//
// Phases:
//
//	baseline  sequential reads: network round trip plus server time
//	enqueue   one task per call into a paused queue (the write path), ramped by concurrency
//	batch     500 tasks per call, ramped by concurrency
//	dispatch  drain N tasks from one queue at a 500/s cap, then uncapped
//	fanout    drain tasks spread over 10 queues, uncapped (the global ceiling)
//	linktodm  LinktoDM's shape: many per-account queues (10/s, 5 concurrent),
//	          a backlog of delayed tasks, and open-loop traffic at 50, 150 and
//	          300 tasks/s, each task upserting its queue first like the app does
//	all       baseline, enqueue, batch, dispatch, fanout
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	base      = flag.String("base", "https://api.pushqueue.dev", "pushq server")
	keyFlag   = flag.String("key", "", "API key (default $PUSHQ_API_KEY)")
	keyFile   = flag.String("keyfile", "", "read the API key from this file")
	appID     = flag.String("app", "", "X-PushQ-App: act in this application (keys limited to one app don't need it)")
	phase     = flag.String("phase", "all", "baseline|enqueue|batch|dispatch|fanout|linktodm|all")
	steps     = flag.String("steps", "8,16,32,64,128,256", "enqueue: concurrency steps")
	stepDur   = flag.Duration("step", 20*time.Second, "enqueue/batch: duration of each step")
	nDisp     = flag.Int("dispatch-tasks", 20000, "dispatch: tasks to drain")
	nQueues   = flag.Int("queues", 1000, "linktodm: per-account queues")
	prefill   = flag.Int("prefill", 60000, "linktodm: delayed tasks to preload")
	ltRates   = flag.String("rates", "50,150,300", "linktodm: open-loop task rates (tasks/s)")
	ltDur     = flag.Duration("rate-step", 90*time.Second, "linktodm: duration of each rate")
	h1        = flag.Bool("h1", false, "force HTTP/1.1 (one connection per in-flight request)")
	runID     = flag.String("run", strconv.FormatInt(time.Now().Unix(), 36), "run id, used in queue names")
	maxErr    = 0.05
	key       string
	created   sync.Map // queues this run made, for cleanup
	transport = &http.Transport{MaxIdleConns: 4000, MaxIdleConnsPerHost: 4000, IdleConnTimeout: 90 * time.Second, ForceAttemptHTTP2: true}
	client    = &http.Client{Timeout: 30 * time.Second, Transport: transport}
)

// ---------- HTTP ----------

func do(method, path string, body any) (int, []byte, time.Duration, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, *base+path, rd)
	req.Header.Set("Authorization", "Bearer "+key)
	if *appID != "" {
		req.Header.Set("X-PushQ-App", *appID)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	t0 := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, time.Since(t0), err
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, b, time.Since(t0), nil
}

func qpath(q string) string { return "/v1/queues/" + url.PathEscape(q) }

func must(code int, b []byte, err error, what string) {
	if err != nil || code >= 300 {
		fmt.Printf("FAIL %s: %d %v %s\n", what, code, err, b)
		cleanup()
		os.Exit(1)
	}
}

func putQueue(name string, cfg map[string]any) {
	code, b, _, err := do("PUT", qpath(name), cfg)
	must(code, b, err, "put "+name)
	created.Store(name, true)
}

func action(q, verb string) {
	code, b, _, err := do("POST", qpath(q)+":"+verb, map[string]any{})
	must(code, b, err, verb+" "+q)
}

// ---------- measurements ----------

type sample struct {
	mu    sync.Mutex
	lat   []time.Duration
	codes map[string]int
}

func newSample() *sample { return &sample{codes: map[string]int{}} }

func (s *sample) add(code int, err error, d time.Duration) {
	k := strconv.Itoa(code)
	if err != nil {
		k = "neterr"
	}
	s.mu.Lock()
	s.codes[k]++
	s.lat = append(s.lat, d)
	s.mu.Unlock()
}

func (s *sample) pct(p float64) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lat) == 0 {
		return 0
	}
	sort.Slice(s.lat, func(i, j int) bool { return s.lat[i] < s.lat[j] })
	return s.lat[int(float64(len(s.lat)-1)*p)]
}

func (s *sample) errRate() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	tot, bad := 0, 0
	for k, v := range s.codes {
		tot += v
		if !strings.HasPrefix(k, "2") {
			bad += v
		}
	}
	if tot == 0 {
		return 0
	}
	return float64(bad) / float64(tot)
}

func ms(d time.Duration) string { return fmt.Sprintf("%.0fms", float64(d)/1e6) }

type qstats struct {
	State              string
	Pending, Running   int
	Succeeded, Failed  int
	ScheduledFuture    int     `json:"scheduled_future"`
	OldestDueAgeSecond float64 `json:"oldest_due_age_seconds"`
	DispatchedLast1m   int     `json:"dispatched_last_1m"`
	ErrorsLast1m       int     `json:"errors_last_1m"`
}

func stats(q string) qstats {
	_, b, _, _ := do("GET", qpath(q)+"/stats", nil)
	var s qstats
	json.Unmarshal(b, &s)
	return s
}

// ---------- phases ----------

func baseline() {
	fmt.Println("\n== baseline: 30 sequential reads per endpoint ==")
	q := "loadtest/baseline-" + *runID
	putQueue(q, map[string]any{})
	for _, ep := range []string{"/healthz", "/v1/version", qpath(q), qpath(q) + "/stats"} {
		s := newSample()
		for i := 0; i < 30; i++ {
			code, _, d, err := do("GET", ep, nil)
			s.add(code, err, d)
		}
		fmt.Printf("%-42s p50 %-6s p95 %-6s max %-6s %v\n", ep, ms(s.pct(.5)), ms(s.pct(.95)), ms(s.pct(1)), s.codes)
	}
}

// loop runs n workers calling fn until the deadline.
func loop(n int, dur time.Duration, fn func()) float64 {
	stop := time.Now().Add(dur)
	var wg sync.WaitGroup
	t0 := time.Now()
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(stop) {
				fn()
			}
		}()
	}
	wg.Wait()
	return time.Since(t0).Seconds()
}

func healthTarget() map[string]any { return map[string]any{"base_url": *base, "method": "GET"} }

func enqueue() {
	fmt.Println("\n== enqueue: one task per call into a paused queue ==")
	q := "loadtest/enqueue-" + *runID
	putQueue(q, map[string]any{"target": healthTarget()})
	action(q, "pause")
	var seq atomic.Int64
	for _, st := range strings.Split(*steps, ",") {
		n, _ := strconv.Atoi(strings.TrimSpace(st))
		s := newSample()
		el := loop(n, *stepDur, func() {
			id := seq.Add(1)
			code, _, d, err := do("POST", qpath(q)+"/tasks", map[string]any{"task_id": fmt.Sprintf("t%d", id), "path": "/healthz"})
			s.add(code, err, d)
		})
		fmt.Printf("c=%-5d %6.1f req/s  p50 %-7s p95 %-7s p99 %-7s err %.1f%%  %v\n",
			n, float64(len(s.lat))/el, ms(s.pct(.5)), ms(s.pct(.95)), ms(s.pct(.99)), s.errRate()*100, s.codes)
		if s.errRate() > maxErr || s.pct(.99) > 10*time.Second {
			fmt.Println("stopping the ramp: error rate or p99 over threshold")
			break
		}
	}
	action(q, "purge")
	fmt.Printf("%d tasks enqueued, queue purged\n", seq.Load())
}

func batch() {
	fmt.Println("\n== batch: 500 tasks per call into a paused queue ==")
	q := "loadtest/batch-" + *runID
	putQueue(q, map[string]any{"target": healthTarget()})
	action(q, "pause")
	var seq atomic.Int64
	for _, n := range []int{4, 8, 16} {
		s := newSample()
		var ok atomic.Int64
		el := loop(n, *stepDur, func() {
			ts := make([]map[string]any, 500)
			for i := range ts {
				ts[i] = map[string]any{"task_id": fmt.Sprintf("b%d", seq.Add(1)), "path": "/healthz"}
			}
			code, _, d, err := do("POST", qpath(q)+"/tasks:batch", map[string]any{"tasks": ts})
			s.add(code, err, d)
			if code == 200 {
				ok.Add(500)
			}
		})
		fmt.Printf("c=%-3d %7.0f tasks/s  p50 %-7s p95 %-7s err %.1f%%  %v\n",
			n, float64(ok.Load())/el, ms(s.pct(.5)), ms(s.pct(.95)), s.errRate()*100, s.codes)
		if s.errRate() > maxErr || s.pct(.99) > 20*time.Second {
			fmt.Println("stopping the ramp: over threshold")
			break
		}
	}
	action(q, "purge")
}

// load batch-enqueues tasks into q (delay in seconds; 0 = due now).
func load(q string, n int, prefix string, delay int) {
	ch := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for start := range ch {
				ts := []map[string]any{}
				for i := start; i < start+500 && i < n; i++ {
					t := map[string]any{"task_id": fmt.Sprintf("%s%d", prefix, i), "path": "/healthz"}
					if delay > 0 {
						t["delay_seconds"] = delay
					}
					ts = append(ts, t)
				}
				code, b, _, err := do("POST", qpath(q)+"/tasks:batch", map[string]any{"tasks": ts})
				if err != nil || code != 200 {
					fmt.Println("batch failed:", code, err, string(b[:min(len(b), 200)]))
				}
			}
		}()
	}
	for s := 0; s < n; s += 500 {
		ch <- s
	}
	close(ch)
	wg.Wait()
}

// drain resumes the queues and reports how fast they empty.
func drain(qs []string, total int) {
	t0 := time.Now()
	for _, q := range qs {
		action(q, "resume")
	}
	last, lastT, peak := 0, t0, 0.0
	for {
		time.Sleep(3 * time.Second)
		var mu sync.Mutex
		var wg sync.WaitGroup
		pend, run, done, failed := 0, 0, 0, 0
		for _, q := range qs {
			wg.Add(1)
			go func(q string) {
				defer wg.Done()
				s := stats(q)
				mu.Lock()
				pend += s.Pending
				run += s.Running
				done += s.Succeeded + s.Failed
				failed += s.Failed
				mu.Unlock()
			}(q)
		}
		wg.Wait()
		now := time.Now()
		r := float64(done-last) / now.Sub(lastT).Seconds()
		peak = math.Max(peak, r)
		fmt.Printf("t=%5.1fs pending %6d running %4d done %6d/%d failed %d  %6.0f/s\n", now.Sub(t0).Seconds(), pend, run, done, total, failed, r)
		last, lastT = done, now
		if pend == 0 && run == 0 {
			el := now.Sub(t0).Seconds()
			fmt.Printf("drained %d in %.1fs: avg %.0f/s, peak %.0f/s, failed %d\n", done, el, float64(done)/el, peak, failed)
			return
		}
		if now.Sub(t0) > 6*time.Minute {
			fmt.Println("stopping: drain took over 6 minutes")
			return
		}
	}
}

func dispatch() {
	for _, rate := range []float64{500, 5000} {
		label := fmt.Sprintf("r%.0f", rate)
		fmt.Printf("\n== dispatch (%s/s cap): %d tasks -> GET %s/healthz ==\n", strings.TrimPrefix(label, "r"), *nDisp, *base)
		q := fmt.Sprintf("loadtest/dispatch-%s-%s", label, *runID)
		putQueue(q, map[string]any{"target": healthTarget(), "retry": map[string]any{"max_attempts": 2},
			"rate_limits": map[string]any{"max_dispatches_per_second": rate, "max_burst_size": int(rate), "max_concurrent_dispatches": 1000}})
		action(q, "pause")
		load(q, *nDisp, "d", 0)
		drain([]string{q}, *nDisp)
		action(q, "purge")
	}
}

func fanout() {
	const nq, per = 10, 5000
	fmt.Printf("\n== fanout: %d queues x %d tasks, uncapped ==\n", nq, per)
	qs := []string{}
	for i := 0; i < nq; i++ {
		q := fmt.Sprintf("loadtest/fan-%s-%02d", *runID, i)
		qs = append(qs, q)
		putQueue(q, map[string]any{"target": healthTarget(), "retry": map[string]any{"max_attempts": 2},
			"rate_limits": map[string]any{"max_dispatches_per_second": 5000, "max_burst_size": 5000, "max_concurrent_dispatches": 1000}})
		action(q, "pause")
	}
	var wg sync.WaitGroup
	for _, q := range qs {
		wg.Add(1)
		go func(q string) { defer wg.Done(); load(q, per, "f", 0) }(q)
	}
	wg.Wait()
	drain(qs, nq*per)
	for _, q := range qs {
		action(q, "purge")
	}
}

// linktodm replays LinktoDM's traffic shape: one queue per connected
// Instagram/Facebook account at the app's defaults (10/s, 5 concurrent), a
// backlog of delayed tasks, and webhook-driven tasks arriving open-loop. Like
// the app's InstagramQueue.add_task, every task first upserts its queue's
// rate limits and then creates the task: two calls per task.
func linktodm() {
	fmt.Printf("\n== linktodm: %d account queues, %d delayed tasks, rates %s tasks/s ==\n", *nQueues, *prefill, *ltRates)
	cfg := map[string]any{"target": healthTarget(),
		"rate_limits": map[string]any{"max_dispatches_per_second": 10, "max_concurrent_dispatches": 5},
		"retry":       map[string]any{"max_attempts": 1}}
	qs := make([]string, *nQueues)
	t0 := time.Now()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 64)
	for i := range qs {
		qs[i] = fmt.Sprintf("loadtest/ig-%s-%04d", *runID, i)
		wg.Add(1)
		sem <- struct{}{}
		go func(q string) { defer wg.Done(); putQueue(q, cfg); <-sem }(qs[i])
	}
	wg.Wait()
	fmt.Printf("created %d queues in %.1fs\n", len(qs), time.Since(t0).Seconds())

	// The backlog: delayed tasks (scheduled replies, follow-ups) spread over the busiest queues.
	t0 = time.Now()
	per := *prefill / min(len(qs), 50)
	for i := 0; i < min(len(qs), 50); i++ {
		wg.Add(1)
		go func(q string) { defer wg.Done(); load(q, per, "p", 3600) }(qs[i])
	}
	wg.Wait()
	fmt.Printf("preloaded %d delayed tasks in %.1fs\n", per*min(len(qs), 50), time.Since(t0).Seconds())

	// Traffic is skewed: a few big accounts get most webhooks (Zipf over queues).
	zipf := rand.NewZipf(rand.New(rand.NewSource(1)), 1.1, 1, uint64(len(qs)-1))
	var zmu sync.Mutex
	pick := func() string { zmu.Lock(); defer zmu.Unlock(); return qs[zipf.Uint64()] }

	var seq atomic.Int64
	for _, rs := range strings.Split(*ltRates, ",") {
		rate, _ := strconv.ParseFloat(strings.TrimSpace(rs), 64)
		up, task := newSample(), newSample()
		var dropped atomic.Int64
		jobs := make(chan struct{}, 4096)
		var wg sync.WaitGroup
		for w := 0; w < 512; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range jobs {
					q := pick()
					code, _, d, err := do("PUT", qpath(q), cfg) // the app re-applies the queue's speed first
					up.add(code, err, d)
					id := seq.Add(1)
					code, _, d, err = do("POST", qpath(q)+"/tasks", map[string]any{"task_id": fmt.Sprintf("w%d", id), "path": "/healthz",
						"json_body": map[string]any{"object": "instagram", "entry": id}})
					task.add(code, err, d)
				}
			}()
		}
		// Open loop: tasks arrive at the target rate whether or not the server keeps up.
		start := time.Now()
		sent := 0
		for el := time.Duration(0); el < *ltDur; el = time.Since(start) {
			want := int(rate * el.Seconds())
			for ; sent < want; sent++ {
				select {
				case jobs <- struct{}{}:
				default:
					dropped.Add(1)
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
		close(jobs)
		wg.Wait()
		el := time.Since(start).Seconds()

		// How far behind is delivery? Sample the hottest queues.
		lag, inflight, d1m := 0.0, 0, 0
		for _, q := range qs[:20] {
			s := stats(q)
			lag = math.Max(lag, s.OldestDueAgeSecond)
			inflight += s.Running
			d1m += s.DispatchedLast1m
		}
		fmt.Printf("%4.0f tasks/s for %.0fs: achieved %.0f/s (%d dropped by the producer)\n", rate, el, float64(len(task.lat))/el, dropped.Load())
		fmt.Printf("     upsert queue p50 %-6s p99 %-6s err %.1f%% | create task p50 %-6s p99 %-6s err %.1f%%  %v\n",
			ms(up.pct(.5)), ms(up.pct(.99)), up.errRate()*100, ms(task.pct(.5)), ms(task.pct(.99)), task.errRate()*100, task.codes)
		fmt.Printf("     top-20 queues: %d delivered in the last minute, oldest due task waiting %.1fs (hot accounts are capped at 10/s by design)\n", d1m, lag)
		if task.errRate() > maxErr || up.errRate() > maxErr {
			fmt.Println("stopping: error rate over threshold")
			break
		}
	}
	for _, q := range qs {
		action(q, "purge")
	}
	fmt.Println("purged every linktodm queue")
}

// cleanup deletes the run's queues when the key may, else purges and pauses them.
func cleanup() {
	var qs []string
	created.Range(func(k, _ any) bool { qs = append(qs, k.(string)); return true })
	if len(qs) == 0 {
		return
	}
	var deleted atomic.Int64
	var canDelete atomic.Bool
	canDelete.Store(true)
	sem := make(chan struct{}, 32)
	var wg sync.WaitGroup
	for _, q := range qs {
		wg.Add(1)
		sem <- struct{}{}
		go func(q string) {
			defer func() { <-sem; wg.Done() }()
			if canDelete.Load() {
				code, _, _, _ := do("DELETE", qpath(q), nil)
				if code == 204 {
					deleted.Add(1)
					return
				}
				if code == 403 {
					canDelete.Store(false) // a writer key: don't try again for every queue
				}
			}
			do("POST", qpath(q)+":purge", map[string]any{})
			do("POST", qpath(q)+":pause", map[string]any{})
		}(q)
	}
	wg.Wait()
	fmt.Printf("\ncleanup: %d queues, %d deleted, %d purged and paused (deleting needs an admin key)\n",
		len(qs), deleted.Load(), int64(len(qs))-deleted.Load())
}

func main() {
	flag.Parse()
	key = *keyFlag
	if key == "" {
		key = os.Getenv("PUSHQ_API_KEY")
	}
	if *keyFile != "" {
		b, err := os.ReadFile(*keyFile)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		key = strings.TrimSpace(string(b))
	}
	if *h1 {
		transport.ForceAttemptHTTP2 = false
		transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	}
	fmt.Printf("stress run %s against %s\n", *runID, *base)
	phases := map[string]func(){"baseline": baseline, "enqueue": enqueue, "batch": batch, "dispatch": dispatch, "fanout": fanout, "linktodm": linktodm}
	if *phase == "all" {
		for _, p := range []string{"baseline", "enqueue", "batch", "dispatch", "fanout"} {
			phases[p]()
		}
	} else if f, ok := phases[*phase]; ok {
		f()
	} else {
		fmt.Println("unknown phase", *phase)
		os.Exit(2)
	}
	cleanup()
}
