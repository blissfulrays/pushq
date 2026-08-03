// Package types holds the JSON DTOs mirroring api/openapi.yaml.
package types

import "time"

// --- queues ---

type RateLimits struct {
	MaxDispatchesPerSecond  *float64 `json:"max_dispatches_per_second,omitempty"`
	MaxBurstSize            *int     `json:"max_burst_size,omitempty"`
	MaxConcurrentDispatches *int     `json:"max_concurrent_dispatches,omitempty"`
}

type Retry struct {
	MaxAttempts             *int     `json:"max_attempts,omitempty"`
	MaxRetryDurationSeconds *float64 `json:"max_retry_duration_seconds,omitempty"`
	MinBackoffSeconds       *float64 `json:"min_backoff_seconds,omitempty"`
	MaxBackoffSeconds       *float64 `json:"max_backoff_seconds,omitempty"`
	MaxDoublings            *int     `json:"max_doublings,omitempty"`
}

type Target struct {
	BaseURL *string           `json:"base_url,omitempty"`
	Method  *string           `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// SigningSecret: on PUT, absent = keep existing, "" = clear.
	// Never returned in responses; see HasSigningSecret.
	SigningSecret *string `json:"signing_secret,omitempty"`
}

// QueuePut is the PUT /v1/queues/{queue} body: full desired config; omitted
// fields take server defaults (except signing_secret, which is kept).
type QueuePut struct {
	RateLimits      *RateLimits `json:"rate_limits,omitempty"`
	Retry           *Retry      `json:"retry,omitempty"`
	Target          *Target     `json:"target,omitempty"`
	DeadLetterQueue *string     `json:"dead_letter_queue,omitempty"`
}

// QueueCreate is POST /v1/queues.
type QueueCreate struct {
	Name string `json:"name"`
	QueuePut
}

// QueuePatch updates only the provided fields.
type QueuePatch struct {
	State           *string            `json:"state,omitempty"`
	RateLimits      *RateLimitsPatch   `json:"rate_limits,omitempty"`
	Retry           *Retry             `json:"retry,omitempty"`
	Target          *TargetPatch       `json:"target,omitempty"`
	DeadLetterQueue *string            `json:"dead_letter_queue,omitempty"`
}

type RateLimitsPatch = RateLimits

type TargetPatch struct {
	BaseURL       *string            `json:"base_url,omitempty"`
	Method        *string            `json:"method,omitempty"`
	Headers       *map[string]string `json:"headers,omitempty"`
	SigningSecret *string            `json:"signing_secret,omitempty"`
}

type QueueResponse struct {
	Name            string              `json:"name"`
	State           string              `json:"state"`
	RateLimits      RateLimitsResponse  `json:"rate_limits"`
	Retry           RetryResponse       `json:"retry"`
	Target          TargetResponse      `json:"target"`
	DeadLetterQueue string              `json:"dead_letter_queue,omitempty"`
	PurgedAt        *time.Time          `json:"purged_at,omitempty"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

type RateLimitsResponse struct {
	MaxDispatchesPerSecond  float64 `json:"max_dispatches_per_second"`
	MaxBurstSize            int     `json:"max_burst_size"` // resolved effective burst
	MaxConcurrentDispatches int     `json:"max_concurrent_dispatches"`
}

type RetryResponse struct {
	MaxAttempts             int     `json:"max_attempts"`
	MaxRetryDurationSeconds float64 `json:"max_retry_duration_seconds"`
	MinBackoffSeconds       float64 `json:"min_backoff_seconds"`
	MaxBackoffSeconds       float64 `json:"max_backoff_seconds"`
	MaxDoublings            int     `json:"max_doublings"`
}

type TargetResponse struct {
	BaseURL          string            `json:"base_url,omitempty"`
	Method           string            `json:"method,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	HasSigningSecret bool              `json:"has_signing_secret"`
}

type QueueList struct {
	Queues        []QueueResponse `json:"queues"`
	NextPageToken string          `json:"next_page_token,omitempty"`
}

type QueueStatsResponse struct {
	State           string     `json:"state"`
	Pending         int64      `json:"pending"`
	Running         int64      `json:"running"`
	Succeeded       int64      `json:"succeeded"`
	Failed          int64      `json:"failed"`
	ScheduledFuture int64      `json:"scheduled_future"`
	OldestDueAt     *time.Time `json:"oldest_due_at,omitempty"`
	OldestDueAge    *float64   `json:"oldest_due_age_seconds,omitempty"`
	Dispatched1m    int64      `json:"dispatched_last_1m"`
	Errors1m        int64      `json:"errors_last_1m"`
	Dispatched1h    int64      `json:"dispatched_last_1h"`
	Errors1h        int64      `json:"errors_last_1h"`
	LastPurge       *time.Time `json:"last_purge,omitempty"`
}

// --- tasks ---

type TaskCreate struct {
	TaskID                 string            `json:"task_id,omitempty"`
	URL                    string            `json:"url,omitempty"`
	Path                   string            `json:"path,omitempty"`
	Method                 string            `json:"method,omitempty"`
	Headers                map[string]string `json:"headers,omitempty"`
	BodyBase64             string            `json:"body_base64,omitempty"`
	JSONBody               any               `json:"json_body,omitempty"`
	ScheduleTime           *time.Time        `json:"schedule_time,omitempty"`
	DelaySeconds           *float64          `json:"delay_seconds,omitempty"`
	DispatchDeadlineSecond *float64          `json:"dispatch_deadline_seconds,omitempty"`
	Retry                  *Retry            `json:"retry,omitempty"`
}

type TaskBatchCreate struct {
	Tasks []TaskCreate `json:"tasks"`
}

type TaskResponse struct {
	ID                      string            `json:"id"`
	Queue                   string            `json:"queue"`
	State                   string            `json:"state"`
	URL                     string            `json:"url"`
	Method                  string            `json:"method"`
	Headers                 map[string]string `json:"headers,omitempty"`
	BodyBase64              string            `json:"body_base64,omitempty"`
	ScheduleTime            time.Time         `json:"schedule_time"`
	CreatedAt               time.Time         `json:"created_at"`
	DispatchDeadlineSeconds float64           `json:"dispatch_deadline_seconds"`
	Retry                   *Retry            `json:"retry,omitempty"`
	DispatchCount           int               `json:"dispatch_count"`
	ResponseCount           int               `json:"response_count"`
	FirstAttemptAt          *time.Time        `json:"first_attempt_at,omitempty"`
	FinishedAt              *time.Time        `json:"finished_at,omitempty"`
	LastAttempt             *LastAttempt      `json:"last_attempt,omitempty"`
}

type LastAttempt struct {
	At     time.Time `json:"at"`
	Status int       `json:"status,omitempty"`
	Error  string    `json:"error,omitempty"`
}

type TaskList struct {
	Tasks         []TaskResponse `json:"tasks"`
	NextPageToken string         `json:"next_page_token,omitempty"`
}

type BatchResult struct {
	Index int           `json:"index"`
	Task  *TaskResponse `json:"task,omitempty"`
	Error *BatchError   `json:"error,omitempty"`
}

type BatchError struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

type BatchResponse struct {
	Results []BatchResult `json:"results"`
}

type AttemptResponse struct {
	Number         int        `json:"number"`
	DispatchTime   time.Time  `json:"dispatch_time"`
	ResponseTime   *time.Time `json:"response_time,omitempty"`
	ResponseStatus int        `json:"response_status,omitempty"`
	ErrorKind      string     `json:"error_kind,omitempty"`
	ErrorSnippet   string     `json:"error_snippet,omitempty"`
	DurationMS     int64      `json:"duration_ms"`
}

type AttemptList struct {
	Attempts []AttemptResponse `json:"attempts"`
}

type VersionResponse struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Dialect string `json:"dialect"`
}

type ReadyResponse struct {
	Role    string `json:"role"`
	Dialect string `json:"dialect"`
}
