package store

import (
	"time"
)

type QueueState string

const (
	QueueRunning  QueueState = "running"
	QueuePaused   QueueState = "paused"
	QueueDisabled QueueState = "disabled"
)

type TaskState string

const (
	TaskPending   TaskState = "pending"
	TaskLeased    TaskState = "leased"
	TaskSucceeded TaskState = "succeeded"
	TaskFailed    TaskState = "failed"
	// TaskDeleted rows are invisible through the API; they persist only as
	// dedup tombstones until retention cleanup removes them.
	TaskDeleted TaskState = "deleted"
)

// RateLimits mirrors GCP Cloud Tasks queue rate limits.
type RateLimits struct {
	MaxDispatchesPerSecond  float64
	MaxBurstSize            int // 0 = auto: clamp(ceil(rate), 1, 500)
	MaxConcurrentDispatches int
}

// RetryConfig mirrors GCP Cloud Tasks retry semantics.
type RetryConfig struct {
	MaxAttempts      int           // includes the first attempt; -1 = unlimited
	MaxRetryDuration time.Duration // measured from the first attempt; 0 = unlimited
	MinBackoff       time.Duration
	MaxBackoff       time.Duration
	MaxDoublings     int
}

// RetryOverride is a per-task partial override of the queue RetryConfig.
type RetryOverride struct {
	MaxAttempts      *int           `json:"max_attempts,omitempty"`
	MaxRetryDuration *time.Duration `json:"max_retry_duration_ms,omitempty"`
	MinBackoff       *time.Duration `json:"min_backoff_ms,omitempty"`
	MaxBackoff       *time.Duration `json:"max_backoff_ms,omitempty"`
	MaxDoublings     *int           `json:"max_doublings,omitempty"`
}

// Merge applies the override on top of a queue RetryConfig.
func (o *RetryOverride) Merge(base RetryConfig) RetryConfig {
	if o == nil {
		return base
	}
	out := base
	if o.MaxAttempts != nil {
		out.MaxAttempts = *o.MaxAttempts
	}
	if o.MaxRetryDuration != nil {
		out.MaxRetryDuration = *o.MaxRetryDuration
	}
	if o.MinBackoff != nil {
		out.MinBackoff = *o.MinBackoff
	}
	if o.MaxBackoff != nil {
		out.MaxBackoff = *o.MaxBackoff
	}
	if o.MaxDoublings != nil {
		out.MaxDoublings = *o.MaxDoublings
	}
	return out
}

// HTTPTarget is queue-level routing/override config applied at dispatch time.
type HTTPTarget struct {
	BaseURL         string            // resolves relative task paths; "" = tasks must use absolute URLs
	Method          string            // "" = no override
	HeaderOverrides map[string]string // merged over task headers; queue wins
	SigningSecret   string            // Standard Webhooks secret ("whsec_..."); "" = unsigned
}

// DefaultRateLimits / DefaultRetryConfig mirror GCP Cloud Tasks queue defaults.
func DefaultRateLimits() RateLimits {
	return RateLimits{MaxDispatchesPerSecond: 500, MaxBurstSize: 0, MaxConcurrentDispatches: 1000}
}

func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:      100,
		MaxRetryDuration: 0,
		MinBackoff:       100 * time.Millisecond,
		MaxBackoff:       time.Hour,
		MaxDoublings:     16,
	}
}

// EffectiveBurst resolves MaxBurstSize=0 (auto) the way GCP derives it.
func (r RateLimits) EffectiveBurst() int {
	if r.MaxBurstSize > 0 {
		return r.MaxBurstSize
	}
	b := int(r.MaxDispatchesPerSecond)
	if float64(b) < r.MaxDispatchesPerSecond {
		b++
	}
	if b < 1 {
		b = 1
	}
	if b > 500 {
		b = 500
	}
	return b
}

type Queue struct {
	ID              int64
	Name            string
	State           QueueState
	RateLimits      RateLimits
	Retry           RetryConfig
	Target          HTTPTarget
	DeadLetterQueue string
	PurgedAt        *time.Time
	ConfigVersion   int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// QueueUpsert is the full desired configuration for PUT semantics. State is
// intentionally absent: upserts never flip running/paused/disabled, so a
// config upsert on every enqueue cannot accidentally resume a paused queue.
type QueueUpsert struct {
	Name            string
	RateLimits      RateLimits
	Retry           RetryConfig
	Target          HTTPTarget
	KeepSecret      bool // true = leave stored signing secret untouched (secret omitted from request)
	DeadLetterQueue string
}

// QueuePatch applies only non-nil fields.
type QueuePatch struct {
	State                   *QueueState
	MaxDispatchesPerSecond  *float64
	MaxBurstSize            *int
	MaxConcurrentDispatches *int
	RetryMaxAttempts        *int
	RetryMaxRetryDuration   *time.Duration
	RetryMinBackoff         *time.Duration
	RetryMaxBackoff         *time.Duration
	RetryMaxDoublings       *int
	TargetBaseURL           *string
	TargetMethod            *string
	TargetHeaderOverrides   *map[string]string
	SigningSecret           *string
	DeadLetterQueue         *string

	// Validate, when set, is called with the merged config inside the
	// transaction before anything is written; a non-nil error aborts the
	// patch and is returned as-is.
	Validate func(QueueUpsert) error
}

type UpsertOutcome int

const (
	UpsertCreated UpsertOutcome = iota
	UpsertUpdated
	UpsertNoop
)

type Task struct {
	PK               int64
	QueueID          int64
	Queue            string // queue name (joined for convenience)
	ID               string
	State            TaskState
	URL              string // absolute URL or path starting with "/" (resolved at dispatch)
	Method           string
	Headers          map[string]string
	Body             []byte
	DispatchDeadline time.Duration
	RetryOverride    *RetryOverride
	ScheduleTime     time.Time
	CreatedAt        time.Time
	FirstAttemptAt   *time.Time
	FinishedAt       *time.Time
	DispatchCount    int
	ResponseCount    int
	LeaseToken       string
	LeaseExpiresAt   *time.Time
	LastAttemptAt    *time.Time
	LastStatus       int // 0 = none
	LastError        string
}

type NewTask struct {
	Queue            string
	ID               string // "" = server-generated
	URL              string
	Method           string
	Headers          map[string]string
	Body             []byte
	DispatchDeadline time.Duration
	RetryOverride    *RetryOverride
	ScheduleTime     time.Time
}

type Attempt struct {
	ID             int64
	TaskPK         int64
	QueueID        int64
	Number         int
	DispatchTime   time.Time
	ResponseTime   *time.Time
	ResponseStatus int // 0 = no response
	ErrorKind      string
	ErrorSnippet   string
	Duration       time.Duration
}

// Attempt error kinds.
const (
	ErrKindTimeout   = "timeout"
	ErrKindConnect   = "connect"
	ErrKindDNS       = "dns"
	ErrKindTLS       = "tls"
	ErrKindNon2xx    = "non_2xx"
	ErrKindLeaseLost = "lease_lost"
	ErrKindConfig    = "config"
)

type View string

const (
	ViewBasic View = "basic" // omits headers and body
	ViewFull  View = "full"
)

type ListQueuesOpts struct {
	NamePrefix string
	// RootOnly skips namespaced names ("<ns>:name").
	RootOnly  bool
	PageSize  int
	PageToken string
}

type ListTasksOpts struct {
	Queue     string
	State     TaskState // "" = all non-deleted
	IDPrefix  string
	View      View
	PageSize  int
	PageToken string
}

type QueueStats struct {
	Pending         int64 // due now or overdue
	Running         int64
	Succeeded       int64
	Failed          int64
	ScheduledFuture int64 // pending with schedule_time in the future
	OldestDueAt     *time.Time
	Dispatched1m    int64
	Errors1m        int64
	Dispatched1h    int64
	Errors1h        int64
}

// Dispatcher-facing types.

type QueueSeed struct {
	QueueID      int64
	Pending      int64
	Leased       int64
	NextSchedule *time.Time
}

// ClaimBudget asks ClaimTasks for up to N due tasks from one queue. Stores
// apply the queue's current purge watermark themselves.
type ClaimBudget struct {
	QueueID int64
	N       int
}

type Outcome int

const (
	OutcomeSucceeded Outcome = iota
	OutcomeFailed
	OutcomeRetry
)

// Event kinds emitted by the store for dispatcher wakeup. Events are a latency
// optimization only — the dispatcher's periodic reconcile is the correctness
// backstop.
type EventKind int

const (
	EventTaskReady EventKind = iota
	EventQueueChanged
	EventRunTask
)

type Event struct {
	Kind         EventKind
	QueueID      int64
	Queue        string
	ScheduleTime time.Time // EventTaskReady
	TaskPK       int64     // EventRunTask
}
