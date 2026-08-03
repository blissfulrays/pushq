package pushq

import "time"

// RateLimits mirrors GCP Cloud Tasks queue rate limits. Nil fields = server
// defaults.
type RateLimits struct {
	MaxDispatchesPerSecond  *float64 `json:"max_dispatches_per_second,omitempty"`
	MaxBurstSize            *int     `json:"max_burst_size,omitempty"`
	MaxConcurrentDispatches *int     `json:"max_concurrent_dispatches,omitempty"`
}

// Retry mirrors GCP Cloud Tasks retry semantics. On a task, any subset
// overrides the queue config.
type Retry struct {
	MaxAttempts             *int     `json:"max_attempts,omitempty"`
	MaxRetryDurationSeconds *float64 `json:"max_retry_duration_seconds,omitempty"`
	MinBackoffSeconds       *float64 `json:"min_backoff_seconds,omitempty"`
	MaxBackoffSeconds       *float64 `json:"max_backoff_seconds,omitempty"`
	MaxDoublings            *int     `json:"max_doublings,omitempty"`
}

type Target struct {
	BaseURL string            `json:"base_url,omitempty"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// SigningSecret: on upsert, nil pointer semantics don't apply to strings —
	// leave empty to KEEP the stored secret; set ClearSigningSecret to clear.
	SigningSecret string `json:"-"`
	// ClearSigningSecret sends an explicit empty secret.
	ClearSigningSecret bool `json:"-"`
}

type QueueConfig struct {
	RateLimits      *RateLimits `json:"rate_limits,omitempty"`
	Retry           *Retry      `json:"retry,omitempty"`
	Target          *Target     `json:"target,omitempty"`
	DeadLetterQueue string      `json:"dead_letter_queue,omitempty"`
}

type Queue struct {
	Name  string `json:"name"`
	State string `json:"state"`
	RateLimitsSet struct {
		MaxDispatchesPerSecond  float64 `json:"max_dispatches_per_second"`
		MaxBurstSize            int     `json:"max_burst_size"`
		MaxConcurrentDispatches int     `json:"max_concurrent_dispatches"`
	} `json:"rate_limits"`
	RetrySet struct {
		MaxAttempts             int     `json:"max_attempts"`
		MaxRetryDurationSeconds float64 `json:"max_retry_duration_seconds"`
		MinBackoffSeconds       float64 `json:"min_backoff_seconds"`
		MaxBackoffSeconds       float64 `json:"max_backoff_seconds"`
		MaxDoublings            int     `json:"max_doublings"`
	} `json:"retry"`
	TargetSet struct {
		BaseURL          string            `json:"base_url"`
		Method           string            `json:"method"`
		Headers          map[string]string `json:"headers"`
		HasSigningSecret bool              `json:"has_signing_secret"`
	} `json:"target"`
	DeadLetterQueue string     `json:"dead_letter_queue"`
	PurgedAt        *time.Time `json:"purged_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type QueueStats struct {
	State               string     `json:"state"`
	Pending             int64      `json:"pending"`
	Running             int64      `json:"running"`
	Succeeded           int64      `json:"succeeded"`
	Failed              int64      `json:"failed"`
	ScheduledFuture     int64      `json:"scheduled_future"`
	OldestDueAt         *time.Time `json:"oldest_due_at"`
	OldestDueAgeSeconds *float64   `json:"oldest_due_age_seconds"`
	DispatchedLast1m    int64      `json:"dispatched_last_1m"`
	ErrorsLast1m        int64      `json:"errors_last_1m"`
	DispatchedLast1h    int64      `json:"dispatched_last_1h"`
	ErrorsLast1h        int64      `json:"errors_last_1h"`
	LastPurge           *time.Time `json:"last_purge"`
}

// TaskCreate is a task creation request. Exactly one of URL or Path; at most
// one of Body or JSONBody; at most one of ScheduleTime or DelaySeconds.
type TaskCreate struct {
	TaskID                  string            `json:"task_id,omitempty"`
	URL                     string            `json:"url,omitempty"`
	Path                    string            `json:"path,omitempty"`
	Method                  string            `json:"method,omitempty"`
	Headers                 map[string]string `json:"headers,omitempty"`
	Body                    []byte            `json:"-"`
	JSONBody                any               `json:"json_body,omitempty"`
	ScheduleTime            *time.Time        `json:"schedule_time,omitempty"`
	DelaySeconds            *float64          `json:"delay_seconds,omitempty"`
	DispatchDeadlineSeconds *float64          `json:"dispatch_deadline_seconds,omitempty"`
	Retry                   *Retry            `json:"retry,omitempty"`
}

type Task struct {
	ID                      string            `json:"id"`
	Queue                   string            `json:"queue"`
	State                   string            `json:"state"`
	URL                     string            `json:"url"`
	Method                  string            `json:"method"`
	Headers                 map[string]string `json:"headers"`
	BodyBase64              string            `json:"body_base64"`
	ScheduleTime            time.Time         `json:"schedule_time"`
	CreatedAt               time.Time         `json:"created_at"`
	DispatchDeadlineSeconds float64           `json:"dispatch_deadline_seconds"`
	Retry                   *Retry            `json:"retry"`
	DispatchCount           int               `json:"dispatch_count"`
	ResponseCount           int               `json:"response_count"`
	FirstAttemptAt          *time.Time        `json:"first_attempt_at"`
	FinishedAt              *time.Time        `json:"finished_at"`
	LastAttempt             *struct {
		At     time.Time `json:"at"`
		Status int       `json:"status"`
		Error  string    `json:"error"`
	} `json:"last_attempt"`
}

type Attempt struct {
	Number         int        `json:"number"`
	DispatchTime   time.Time  `json:"dispatch_time"`
	ResponseTime   *time.Time `json:"response_time"`
	ResponseStatus int        `json:"response_status"`
	ErrorKind      string     `json:"error_kind"`
	ErrorSnippet   string     `json:"error_snippet"`
	DurationMS     int64      `json:"duration_ms"`
}

type BatchResult struct {
	Index int   `json:"index"`
	Task  *Task `json:"task"`
	Error *struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	} `json:"error"`
}

type ListTasksParams struct {
	State     string
	IDPrefix  string
	View      string
	PageSize  int
	PageToken string
}

type ListQueuesParams struct {
	NamePrefix string
	PageSize   int
	PageToken  string
}

// Helpers for optional scalars.
func Float64(v float64) *float64 { return &v }
func Int(v int) *int             { return &v }
