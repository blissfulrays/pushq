package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/blissfulrays/pushq/internal/api/types"
	"github.com/blissfulrays/pushq/internal/config"
	"github.com/blissfulrays/pushq/internal/store"
)

var (
	queueNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`)
	taskIDRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,500}$`)
)

var allowedMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true,
}

var bodyMethods = map[string]bool{"POST": true, "PUT": true, "PATCH": true}

// reservedHeaders cannot be set on tasks or queue overrides; the transport or
// the dispatcher owns them.
var reservedHeaders = map[string]bool{
	"host": true, "content-length": true, "transfer-encoding": true, "connection": true,
	"keep-alive": true, "upgrade": true, "te": true, "trailer": true,
}

type badRequest struct{ msg string }

func (e badRequest) Error() string { return e.msg }

func errf(format string, args ...any) error { return badRequest{fmt.Sprintf(format, args...)} }

func validQueueName(name string) error {
	if !queueNameRe.MatchString(name) {
		return errf("queue name must match [a-z0-9][a-z0-9-]{0,99}, got %q", name)
	}
	return nil
}

func validTaskID(id string) error {
	if !taskIDRe.MatchString(id) {
		return errf("task_id must match [A-Za-z0-9_-]{1,500}")
	}
	return nil
}

func secondsToDuration(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

func durationToSeconds(d time.Duration) float64 {
	return d.Seconds()
}

func validateHeaders(h map[string]string, maxBytes int64) error {
	var size int64
	for k, v := range h {
		if k == "" {
			return errf("empty header name")
		}
		if reservedHeaders[strings.ToLower(k)] {
			return errf("header %q is reserved", k)
		}
		size += int64(len(k) + len(v))
	}
	if size > maxBytes {
		return errf("headers exceed %d bytes", maxBytes)
	}
	return nil
}

func validateRetryConfig(rc store.RetryConfig) error {
	if rc.MaxAttempts < -1 || rc.MaxAttempts == 0 {
		return errf("retry.max_attempts must be -1 (unlimited) or >= 1")
	}
	if rc.MaxRetryDuration < 0 {
		return errf("retry.max_retry_duration_seconds must be >= 0")
	}
	if rc.MinBackoff < 0 {
		return errf("retry.min_backoff_seconds must be >= 0")
	}
	if rc.MaxBackoff < rc.MinBackoff {
		return errf("retry.max_backoff_seconds must be >= min_backoff_seconds")
	}
	if rc.MaxDoublings < 0 || rc.MaxDoublings > 31 {
		return errf("retry.max_doublings must be in [0, 31]")
	}
	return nil
}

// queuePutToUpsert applies PUT semantics: full desired config, defaults for
// omitted fields, signing_secret omitted = keep.
func queuePutToUpsert(name string, p types.QueuePut) (store.QueueUpsert, error) {
	u := store.QueueUpsert{
		Name:       name,
		RateLimits: store.DefaultRateLimits(),
		Retry:      store.DefaultRetryConfig(),
		KeepSecret: true,
	}
	if p.RateLimits != nil {
		if p.RateLimits.MaxDispatchesPerSecond != nil {
			u.RateLimits.MaxDispatchesPerSecond = *p.RateLimits.MaxDispatchesPerSecond
		}
		if p.RateLimits.MaxBurstSize != nil {
			u.RateLimits.MaxBurstSize = *p.RateLimits.MaxBurstSize
		}
		if p.RateLimits.MaxConcurrentDispatches != nil {
			u.RateLimits.MaxConcurrentDispatches = *p.RateLimits.MaxConcurrentDispatches
		}
	}
	if p.Retry != nil {
		if p.Retry.MaxAttempts != nil {
			u.Retry.MaxAttempts = *p.Retry.MaxAttempts
		}
		if p.Retry.MaxRetryDurationSeconds != nil {
			u.Retry.MaxRetryDuration = secondsToDuration(*p.Retry.MaxRetryDurationSeconds)
		}
		if p.Retry.MinBackoffSeconds != nil {
			u.Retry.MinBackoff = secondsToDuration(*p.Retry.MinBackoffSeconds)
		}
		if p.Retry.MaxBackoffSeconds != nil {
			u.Retry.MaxBackoff = secondsToDuration(*p.Retry.MaxBackoffSeconds)
		}
		if p.Retry.MaxDoublings != nil {
			u.Retry.MaxDoublings = *p.Retry.MaxDoublings
		}
	}
	if p.Target != nil {
		if p.Target.BaseURL != nil {
			u.Target.BaseURL = *p.Target.BaseURL
		}
		if p.Target.Method != nil {
			u.Target.Method = strings.ToUpper(*p.Target.Method)
		}
		u.Target.HeaderOverrides = p.Target.Headers
		if p.Target.SigningSecret != nil {
			u.Target.SigningSecret = *p.Target.SigningSecret
			u.KeepSecret = false
		}
	}
	if p.DeadLetterQueue != nil {
		u.DeadLetterQueue = *p.DeadLetterQueue
	}
	if err := validateUpsert(u); err != nil {
		return u, err
	}
	return u, nil
}

func validateUpsert(u store.QueueUpsert) error {
	if err := validQueueName(u.Name); err != nil {
		return err
	}
	rl := u.RateLimits
	if rl.MaxDispatchesPerSecond <= 0 || rl.MaxDispatchesPerSecond > 10000 {
		return errf("rate_limits.max_dispatches_per_second must be in (0, 10000]")
	}
	if rl.MaxBurstSize < 0 || rl.MaxBurstSize > 10000 {
		return errf("rate_limits.max_burst_size must be in [0, 10000] (0 = auto)")
	}
	if rl.MaxConcurrentDispatches < 1 || rl.MaxConcurrentDispatches > 10000 {
		return errf("rate_limits.max_concurrent_dispatches must be in [1, 10000]")
	}
	if err := validateRetryConfig(u.Retry); err != nil {
		return err
	}
	if u.Target.BaseURL != "" {
		bu, err := url.Parse(u.Target.BaseURL)
		if err != nil || (bu.Scheme != "http" && bu.Scheme != "https") || bu.Host == "" {
			return errf("target.base_url must be an absolute http(s) URL")
		}
	}
	if u.Target.Method != "" && !allowedMethods[u.Target.Method] {
		return errf("target.method %q not allowed", u.Target.Method)
	}
	if err := validateHeaders(u.Target.HeaderOverrides, 16<<10); err != nil {
		return err
	}
	if u.DeadLetterQueue != "" {
		if err := validQueueName(u.DeadLetterQueue); err != nil {
			return errf("dead_letter_queue: %v", err)
		}
		if u.DeadLetterQueue == u.Name {
			return errf("dead_letter_queue cannot be the queue itself")
		}
	}
	return nil
}

func queuePatchToStore(p types.QueuePatch) (store.QueuePatch, error) {
	var sp store.QueuePatch
	if p.State != nil {
		st := store.QueueState(*p.State)
		if st != store.QueueRunning && st != store.QueuePaused && st != store.QueueDisabled {
			return sp, errf("state must be running, paused, or disabled")
		}
		sp.State = &st
	}
	if p.RateLimits != nil {
		sp.MaxDispatchesPerSecond = p.RateLimits.MaxDispatchesPerSecond
		sp.MaxBurstSize = p.RateLimits.MaxBurstSize
		sp.MaxConcurrentDispatches = p.RateLimits.MaxConcurrentDispatches
	}
	if p.Retry != nil {
		sp.RetryMaxAttempts = p.Retry.MaxAttempts
		if p.Retry.MaxRetryDurationSeconds != nil {
			d := secondsToDuration(*p.Retry.MaxRetryDurationSeconds)
			sp.RetryMaxRetryDuration = &d
		}
		if p.Retry.MinBackoffSeconds != nil {
			d := secondsToDuration(*p.Retry.MinBackoffSeconds)
			sp.RetryMinBackoff = &d
		}
		if p.Retry.MaxBackoffSeconds != nil {
			d := secondsToDuration(*p.Retry.MaxBackoffSeconds)
			sp.RetryMaxBackoff = &d
		}
		sp.RetryMaxDoublings = p.Retry.MaxDoublings
	}
	if p.Target != nil {
		sp.TargetBaseURL = p.Target.BaseURL
		if p.Target.Method != nil {
			m := strings.ToUpper(*p.Target.Method)
			sp.TargetMethod = &m
		}
		sp.TargetHeaderOverrides = p.Target.Headers
		sp.SigningSecret = p.Target.SigningSecret
	}
	sp.DeadLetterQueue = p.DeadLetterQueue
	return sp, nil
}

func queueToResponse(q store.Queue) types.QueueResponse {
	return types.QueueResponse{
		Name:  q.Name,
		State: string(q.State),
		RateLimits: types.RateLimitsResponse{
			MaxDispatchesPerSecond:  q.RateLimits.MaxDispatchesPerSecond,
			MaxBurstSize:            q.RateLimits.EffectiveBurst(),
			MaxConcurrentDispatches: q.RateLimits.MaxConcurrentDispatches,
		},
		Retry: types.RetryResponse{
			MaxAttempts:             q.Retry.MaxAttempts,
			MaxRetryDurationSeconds: durationToSeconds(q.Retry.MaxRetryDuration),
			MinBackoffSeconds:       durationToSeconds(q.Retry.MinBackoff),
			MaxBackoffSeconds:       durationToSeconds(q.Retry.MaxBackoff),
			MaxDoublings:            q.Retry.MaxDoublings,
		},
		Target: types.TargetResponse{
			BaseURL:          q.Target.BaseURL,
			Method:           q.Target.Method,
			Headers:          q.Target.HeaderOverrides,
			HasSigningSecret: q.Target.SigningSecret != "",
		},
		DeadLetterQueue: q.DeadLetterQueue,
		PurgedAt:        q.PurgedAt,
		CreatedAt:       q.CreatedAt,
		UpdatedAt:       q.UpdatedAt,
	}
}

func retryOverrideFromDTO(r *types.Retry) (*store.RetryOverride, error) {
	if r == nil {
		return nil, nil
	}
	ro := &store.RetryOverride{MaxAttempts: r.MaxAttempts, MaxDoublings: r.MaxDoublings}
	if r.MaxRetryDurationSeconds != nil {
		d := secondsToDuration(*r.MaxRetryDurationSeconds)
		ro.MaxRetryDuration = &d
	}
	if r.MinBackoffSeconds != nil {
		d := secondsToDuration(*r.MinBackoffSeconds)
		ro.MinBackoff = &d
	}
	if r.MaxBackoffSeconds != nil {
		d := secondsToDuration(*r.MaxBackoffSeconds)
		ro.MaxBackoff = &d
	}
	// Validate the override against defaults merged, so obviously bad
	// values are rejected at create time.
	merged := ro.Merge(store.DefaultRetryConfig())
	if err := validateRetryConfig(merged); err != nil {
		return nil, err
	}
	return ro, nil
}

func retryOverrideToDTO(ro *store.RetryOverride) *types.Retry {
	if ro == nil {
		return nil
	}
	out := &types.Retry{MaxAttempts: ro.MaxAttempts, MaxDoublings: ro.MaxDoublings}
	if ro.MaxRetryDuration != nil {
		s := durationToSeconds(*ro.MaxRetryDuration)
		out.MaxRetryDurationSeconds = &s
	}
	if ro.MinBackoff != nil {
		s := durationToSeconds(*ro.MinBackoff)
		out.MinBackoffSeconds = &s
	}
	if ro.MaxBackoff != nil {
		s := durationToSeconds(*ro.MaxBackoff)
		out.MaxBackoffSeconds = &s
	}
	return out
}

// taskCreateToNewTask validates and converts a create request. hasBaseURL
// reports whether the queue currently has a target base_url for relative paths.
func taskCreateToNewTask(queue string, dto types.TaskCreate, limits config.Limits, hasBaseURL bool, now time.Time) (store.NewTask, error) {
	t := store.NewTask{Queue: queue}

	if dto.TaskID != "" {
		if err := validTaskID(dto.TaskID); err != nil {
			return t, err
		}
		t.ID = dto.TaskID
	}

	switch {
	case dto.URL != "" && dto.Path != "":
		return t, errf("provide url or path, not both")
	case dto.URL != "":
		u, err := url.Parse(dto.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return t, errf("url must be an absolute http(s) URL")
		}
		if len(dto.URL) > 2083 {
			return t, errf("url exceeds 2083 characters")
		}
		t.URL = dto.URL
	case dto.Path != "":
		if !strings.HasPrefix(dto.Path, "/") {
			return t, errf("path must start with /")
		}
		if !hasBaseURL {
			return t, fmt.Errorf("task uses a relative path but queue %q has no target.base_url: %w", queue, store.ErrFailedPrecondition)
		}
		t.URL = dto.Path
	default:
		return t, errf("url or path is required")
	}

	t.Method = strings.ToUpper(dto.Method)
	if t.Method == "" {
		t.Method = "POST"
	}
	if !allowedMethods[t.Method] {
		return t, errf("method %q not allowed", dto.Method)
	}

	if err := validateHeaders(dto.Headers, limits.MaxHeadersBytes); err != nil {
		return t, err
	}
	t.Headers = dto.Headers

	if dto.BodyBase64 != "" && dto.JSONBody != nil {
		return t, errf("provide body_base64 or json_body, not both")
	}
	if dto.BodyBase64 != "" {
		b, err := base64.StdEncoding.DecodeString(dto.BodyBase64)
		if err != nil {
			return t, errf("body_base64 is not valid base64")
		}
		t.Body = b
	} else if dto.JSONBody != nil {
		b, err := json.Marshal(dto.JSONBody)
		if err != nil {
			return t, errf("json_body is not serializable")
		}
		t.Body = b
		if t.Headers == nil {
			t.Headers = map[string]string{}
		}
		if _, ok := headerGet(t.Headers, "Content-Type"); !ok {
			t.Headers["Content-Type"] = "application/json"
		}
	}
	if len(t.Body) > 0 && !bodyMethods[t.Method] {
		return t, errf("method %s does not allow a request body", t.Method)
	}
	if int64(len(t.Body)) > limits.MaxBodyBytes {
		return t, errf("body exceeds %d bytes", limits.MaxBodyBytes)
	}

	if dto.ScheduleTime != nil && dto.DelaySeconds != nil {
		return t, errf("provide schedule_time or delay_seconds, not both")
	}
	switch {
	case dto.ScheduleTime != nil:
		t.ScheduleTime = dto.ScheduleTime.UTC()
	case dto.DelaySeconds != nil:
		if *dto.DelaySeconds < 0 {
			return t, errf("delay_seconds must be >= 0")
		}
		t.ScheduleTime = now.Add(secondsToDuration(*dto.DelaySeconds))
	default:
		t.ScheduleTime = now
	}
	if limits.MaxScheduleAhead > 0 && t.ScheduleTime.After(now.Add(limits.MaxScheduleAhead)) {
		return t, errf("schedule_time is more than %s ahead", limits.MaxScheduleAhead)
	}

	if dto.DispatchDeadlineSecond != nil {
		d := secondsToDuration(*dto.DispatchDeadlineSecond)
		if d < 15*time.Second || d > 30*time.Minute {
			return t, errf("dispatch_deadline_seconds must be in [15, 1800]")
		}
		t.DispatchDeadline = d
	}

	ro, err := retryOverrideFromDTO(dto.Retry)
	if err != nil {
		return t, err
	}
	t.RetryOverride = ro
	return t, nil
}

func headerGet(h map[string]string, name string) (string, bool) {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

func taskToResponse(t store.Task) types.TaskResponse {
	state := string(t.State)
	if t.State == store.TaskLeased {
		state = "running"
	}
	resp := types.TaskResponse{
		ID: t.ID, Queue: t.Queue, State: state,
		URL: t.URL, Method: t.Method, Headers: t.Headers,
		ScheduleTime: t.ScheduleTime, CreatedAt: t.CreatedAt,
		DispatchDeadlineSeconds: durationToSeconds(t.DispatchDeadline),
		Retry:                   retryOverrideToDTO(t.RetryOverride),
		DispatchCount:           t.DispatchCount, ResponseCount: t.ResponseCount,
		FirstAttemptAt: t.FirstAttemptAt, FinishedAt: t.FinishedAt,
	}
	if len(t.Body) > 0 {
		resp.BodyBase64 = base64.StdEncoding.EncodeToString(t.Body)
	}
	if t.LastAttemptAt != nil {
		resp.LastAttempt = &types.LastAttempt{At: *t.LastAttemptAt, Status: t.LastStatus, Error: t.LastError}
	}
	return resp
}
