package dispatch

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/blissfulrays/pushq/internal/auth"
	"github.com/blissfulrays/pushq/internal/httpheader"
	"github.com/blissfulrays/pushq/internal/store"
	"github.com/blissfulrays/pushq/internal/version"
)

const (
	errSnippetCap  = 4096
	drainCap       = 256 << 10
	maxCooldown    = 30 * time.Second
	finishTimeout  = 15 * time.Second
	maxRetryAfterH = 24 * time.Hour
)

// worker performs one dispatch attempt end to end. Runs on its own goroutine;
// must not touch loop-owned state.
func (d *Dispatcher) worker(t store.Task, q store.Queue) {
	defer d.workerWG.Done()

	att, status, retryAfter, success := d.attempt(t, q)
	now := d.clock.Now()

	var cooldown time.Time
	if success {
		ctx, cancel := context.WithTimeout(context.Background(), finishTimeout)
		ok, err := d.st.FinishAttempt(ctx, now, t.PK, t.LeaseToken, att, store.OutcomeSucceeded, time.Time{})
		cancel()
		if err != nil {
			d.log.Error("finish attempt", "queue", t.Queue, "task", t.ID, "err", err)
		} else if !ok {
			d.log.Debug("lease lost; result discarded", "queue", t.Queue, "task", t.ID)
		}
	} else {
		// A task whose own headers can never be sent won't get better with
		// retries; fail it now (queue-level config can still be fixed, so
		// those errors keep retrying).
		permanent := att.ErrorKind == store.ErrKindConfig && httpheader.Check(t.Headers) != nil
		cooldown = d.finishFailure(t, q, att, status, retryAfter, now, permanent)
	}

	// Must not be dropped: inflight accounting is loop-owned and only this
	// message releases the slot.
	d.doneCh <- doneMsg{queueID: t.QueueID, cooldown: cooldown}
}

// attempt executes the HTTP request and returns the recorded attempt.
func (d *Dispatcher) attempt(t store.Task, q store.Queue) (att store.Attempt, status int, retryAfter *time.Duration, success bool) {
	start := d.clock.Now()
	att = store.Attempt{
		TaskPK: t.PK, QueueID: t.QueueID, Number: t.DispatchCount, DispatchTime: start,
	}

	req, err := d.buildRequest(t, q, start)
	if err != nil {
		att.ErrorKind = store.ErrKindConfig
		att.ErrorSnippet = err.Error()
		att.Duration = d.clock.Since(start)
		d.met.DispatchTotal.WithLabelValues("error").Inc()
		d.met.DispatchErrors.WithLabelValues(att.ErrorKind).Inc()
		return att, 0, nil, false
	}

	ctx, cancel := context.WithTimeout(d.workerCtx, t.DispatchDeadline)
	defer cancel()
	resp, err := d.client.Do(req.WithContext(ctx))
	elapsed := d.clock.Since(start)
	att.Duration = elapsed

	if err != nil {
		att.ErrorKind = classifyErr(err)
		att.ErrorSnippet = err.Error()
		d.met.DispatchTotal.WithLabelValues("error").Inc()
		d.met.DispatchErrors.WithLabelValues(att.ErrorKind).Inc()
		d.met.DispatchDuration.Observe(elapsed.Seconds())
		return att, 0, nil, false
	}
	defer func() {
		io.Copy(io.Discard, io.LimitReader(resp.Body, drainCap))
		resp.Body.Close()
	}()

	respTime := d.clock.Now()
	att.ResponseTime = &respTime
	att.ResponseStatus = resp.StatusCode
	status = resp.StatusCode
	success = status >= 200 && status < 300
	d.met.DispatchDuration.Observe(elapsed.Seconds())
	d.met.DispatchTotal.WithLabelValues(codeClass(status)).Inc()

	if !success {
		att.ErrorKind = store.ErrKindNon2xx
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, errSnippetCap))
		att.ErrorSnippet = string(snippet)
		d.met.DispatchErrors.WithLabelValues(att.ErrorKind).Inc()
		if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
			retryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), d.clock.Now())
		}
	}
	return att, status, retryAfter, success
}

func (d *Dispatcher) buildRequest(t store.Task, q store.Queue, now time.Time) (*http.Request, error) {
	urlStr, err := resolveURL(t, q)
	if err != nil {
		return nil, err
	}
	method := t.Method
	if q.Target.Method != "" {
		method = q.Target.Method
	}
	body := t.Body
	if method == "GET" || method == "HEAD" || method == "DELETE" || method == "OPTIONS" {
		body = nil // method override may strip the body (GCP parity)
	}

	// Tasks stored before header validation existed may still carry headers
	// net/http refuses to send; report that as a config error, not a
	// connection failure.
	if err := httpheader.Check(t.Headers); err != nil {
		return nil, err
	}
	if err := httpheader.Check(q.Target.HeaderOverrides); err != nil {
		return nil, fmt.Errorf("queue header override: %w", err)
	}

	req, err := http.NewRequest(method, urlStr, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range q.Target.HeaderOverrides {
		req.Header.Set(k, v) // queue-level overrides win
	}
	if len(body) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/octet-stream")
	}

	// System headers always win.
	req.Header.Set("User-Agent", "pushq/"+version.Version)
	req.Header.Set("X-Pushq-Queue", t.Queue)
	req.Header.Set("X-Pushq-Task-Id", t.ID)
	req.Header.Set("X-Pushq-Attempt", strconv.Itoa(t.DispatchCount))
	req.Header.Set("X-Pushq-Execution-Count", strconv.Itoa(t.ResponseCount))
	req.Header.Set("X-Pushq-Scheduled-Time", t.ScheduleTime.UTC().Format(time.RFC3339Nano))

	if q.Target.SigningSecret != "" {
		// webhook-id stays stable across retries so receivers can dedup.
		signed, err := auth.SignedHeaders(q.Target.SigningSecret, t.Queue+"/"+t.ID, now, body)
		if err != nil {
			return nil, fmt.Errorf("sign request: %w", err)
		}
		for k, v := range signed {
			req.Header.Set(k, v)
		}
	}
	return req, nil
}

func resolveURL(t store.Task, q store.Queue) (string, error) {
	if strings.HasPrefix(t.URL, "/") {
		if q.Target.BaseURL == "" {
			return "", fmt.Errorf("task uses a relative path but queue has no target.base_url")
		}
		return strings.TrimSuffix(q.Target.BaseURL, "/") + t.URL, nil
	}
	return t.URL, nil
}

// finishFailure applies retry semantics after a failed attempt and returns a
// queue cooldown when the target asked us to back off. Shared by workers and
// the lease sweeper.
func (d *Dispatcher) finishFailure(t store.Task, q store.Queue, att store.Attempt, status int, retryAfter *time.Duration, now time.Time, permanent bool) time.Time {
	rc := t.RetryOverride.Merge(q.Retry)
	attempts := t.DispatchCount

	ctx, cancel := context.WithTimeout(context.Background(), finishTimeout)
	defer cancel()

	if permanent || Exhausted(rc, attempts, t.FirstAttemptAt, now) {
		ok, err := d.st.FinishAttempt(ctx, now, t.PK, t.LeaseToken, att, store.OutcomeFailed, time.Time{})
		if err != nil {
			d.log.Error("finish attempt", "queue", t.Queue, "task", t.ID, "err", err)
			return time.Time{}
		}
		if ok {
			d.log.Info("task failed permanently", "queue", t.Queue, "task", t.ID,
				"attempts", attempts, "last_status", status, "error", att.ErrorKind)
			if q.DeadLetterQueue != "" {
				d.deadLetter(ctx, t, q, att, now)
			}
		}
		return time.Time{}
	}

	backoff := Backoff(rc, attempts)
	var cooldown time.Time
	if retryAfter != nil && (status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable) {
		ra := *retryAfter
		if ra > rc.MaxBackoff {
			ra = rc.MaxBackoff
		}
		if ra > 0 {
			backoff = ra
		}
		cd := *retryAfter
		if cd > maxCooldown {
			cd = maxCooldown
		}
		cooldown = now.Add(cd)
	}

	ok, err := d.st.FinishAttempt(ctx, now, t.PK, t.LeaseToken, att, store.OutcomeRetry, now.Add(backoff))
	if err != nil {
		d.log.Error("finish attempt", "queue", t.Queue, "task", t.ID, "err", err)
		return time.Time{}
	}
	if ok {
		d.met.RetriesTotal.Inc()
	}
	return cooldown
}

// deadLetter copies a permanently failed task into the queue's DLQ with
// provenance headers.
func (d *Dispatcher) deadLetter(ctx context.Context, t store.Task, q store.Queue, att store.Attempt, now time.Time) {
	headers := map[string]string{}
	for k, v := range t.Headers {
		headers[k] = v
	}
	headers["X-Pushq-Deadletter-Source-Queue"] = t.Queue
	headers["X-Pushq-Deadletter-Task-Id"] = t.ID
	if att.ResponseStatus != 0 {
		headers["X-Pushq-Deadletter-Final-Status"] = strconv.Itoa(att.ResponseStatus)
	} else if att.ErrorKind != "" {
		headers["X-Pushq-Deadletter-Final-Status"] = att.ErrorKind
	}

	_, err := d.st.CreateTask(ctx, store.NewTask{
		Queue:            q.DeadLetterQueue,
		URL:              t.URL,
		Method:           t.Method,
		Headers:          headers,
		Body:             t.Body,
		DispatchDeadline: t.DispatchDeadline,
		ScheduleTime:     now,
	}, now)
	if err != nil {
		d.log.Error("dead-letter failed", "queue", t.Queue, "task", t.ID,
			"dead_letter_queue", q.DeadLetterQueue, "err", err)
		return
	}
	d.met.DeadLettered.Inc()
}

func classifyErr(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return store.ErrKindDNS
	}
	var tlsRecordErr tls.RecordHeaderError
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &tlsRecordErr) || errors.As(err, &certErr) {
		return store.ErrKindTLS
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return store.ErrKindTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return store.ErrKindTimeout
	}
	return store.ErrKindConnect
}

func codeClass(status int) string {
	switch {
	case status >= 200 && status < 300:
		return "2xx"
	case status >= 300 && status < 400:
		return "3xx"
	case status >= 400 && status < 500:
		return "4xx"
	default:
		return "5xx"
	}
}

// parseRetryAfter handles both delta-seconds and HTTP-date forms.
func parseRetryAfter(v string, now time.Time) *time.Duration {
	if v == "" {
		return nil
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		if secs < 0 {
			return nil
		}
		d := time.Duration(secs) * time.Second
		if d > maxRetryAfterH {
			d = maxRetryAfterH
		}
		return &d
	}
	if t, err := http.ParseTime(v); err == nil {
		d := t.Sub(now)
		if d < 0 {
			d = 0
		}
		if d > maxRetryAfterH {
			d = maxRetryAfterH
		}
		return &d
	}
	return nil
}
