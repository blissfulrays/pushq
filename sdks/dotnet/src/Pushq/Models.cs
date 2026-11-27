using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Pushq
{
    /// <summary>Queue run state.</summary>
    public enum QueueState
    {
        /// <summary>Dispatching normally.</summary>
        Running,
        /// <summary>Accepting tasks but not dispatching them.</summary>
        Paused,
        /// <summary>Rejecting new tasks and not dispatching.</summary>
        Disabled,
    }

    /// <summary>Task lifecycle state.</summary>
    public enum TaskState
    {
        /// <summary>Waiting for its schedule time or a dispatch slot.</summary>
        Pending,
        /// <summary>Being delivered right now.</summary>
        Running,
        /// <summary>Delivered with a 2xx response.</summary>
        Succeeded,
        /// <summary>Retries exhausted.</summary>
        Failed,
    }

    /// <summary>How much of a task to return.</summary>
    public enum TaskView
    {
        /// <summary>Everything except headers and body.</summary>
        Basic,
        /// <summary>Includes headers and body.</summary>
        Full,
    }

    /// <summary>Rate limits for a queue (Cloud Tasks semantics). Unset fields keep server defaults.</summary>
    public sealed class RateLimits
    {
        /// <summary>Tokens per second. Default 500.</summary>
        [JsonPropertyName("max_dispatches_per_second")] public double? MaxDispatchesPerSecond { get; set; }
        /// <summary>Token bucket burst. 0 or unset = auto (clamp(ceil(rate), 1, 500)).</summary>
        [JsonPropertyName("max_burst_size")] public int? MaxBurstSize { get; set; }
        /// <summary>Max concurrent in-flight dispatches. Default 1000.</summary>
        [JsonPropertyName("max_concurrent_dispatches")] public int? MaxConcurrentDispatches { get; set; }
    }

    /// <summary>Retry configuration (Cloud Tasks semantics). Unset fields keep server defaults.</summary>
    public sealed class RetryConfig
    {
        /// <summary>Includes the first attempt; -1 = unlimited. Default 100.</summary>
        [JsonPropertyName("max_attempts")] public int? MaxAttempts { get; set; }
        /// <summary>Seconds from the first attempt; 0 = unlimited.</summary>
        [JsonPropertyName("max_retry_duration_seconds")] public double? MaxRetryDurationSeconds { get; set; }
        /// <summary>Smallest delay between attempts, in seconds.</summary>
        [JsonPropertyName("min_backoff_seconds")] public double? MinBackoffSeconds { get; set; }
        /// <summary>Largest delay between attempts, in seconds.</summary>
        [JsonPropertyName("max_backoff_seconds")] public double? MaxBackoffSeconds { get; set; }
        /// <summary>The interval doubles this many times, then grows linearly, capped at the max backoff.</summary>
        [JsonPropertyName("max_doublings")] public int? MaxDoublings { get; set; }
    }

    /// <summary>Where and how a queue's tasks are delivered.</summary>
    public sealed class Target
    {
        /// <summary>Absolute http(s) URL; resolves tasks created with a relative <see cref="TaskCreate.Path"/>.</summary>
        public string? BaseUrl { get; set; }
        /// <summary>Overrides every task's method when set.</summary>
        public string? Method { get; set; }
        /// <summary>Merged over task headers at dispatch (queue wins).</summary>
        public IDictionary<string, string>? Headers { get; set; }
        /// <summary>
        /// Standard Webhooks secret ("whsec_…"). On upsert: null keeps the stored
        /// secret, "" clears it.
        /// </summary>
        public string? SigningSecret { get; set; }
    }

    /// <summary>Queue configuration for create and upsert.</summary>
    public class QueueConfig
    {
        /// <summary>Rate limits; null keeps server defaults.</summary>
        public RateLimits? RateLimits { get; set; }
        /// <summary>Retry policy; null keeps server defaults.</summary>
        public RetryConfig? Retry { get; set; }
        /// <summary>Delivery target.</summary>
        public Target? Target { get; set; }
        /// <summary>Queue that receives tasks whose retries are exhausted.</summary>
        public string? DeadLetterQueue { get; set; }
    }

    /// <summary>A partial update: only non-null fields change.</summary>
    public sealed class QueuePatch : QueueConfig
    {
        /// <summary>New run state (including <see cref="QueueState.Disabled"/>).</summary>
        public QueueState? State { get; set; }
    }

    /// <summary>A queue's delivery target as returned by the server (the secret itself is never returned).</summary>
    public sealed class QueueTarget
    {
        /// <summary>Base URL for relative task paths.</summary>
        [JsonPropertyName("base_url")] public string? BaseUrl { get; set; }
        /// <summary>Method override.</summary>
        [JsonPropertyName("method")] public string? Method { get; set; }
        /// <summary>Header overrides.</summary>
        [JsonPropertyName("headers")] public Dictionary<string, string>? Headers { get; set; }
        /// <summary>Whether deliveries are signed.</summary>
        [JsonPropertyName("has_signing_secret")] public bool HasSigningSecret { get; set; }
    }

    /// <summary>A queue.</summary>
    public sealed class Queue
    {
        /// <summary>Name; "/" separates folders (e.g. "user/instagram").</summary>
        [JsonPropertyName("name")] public string Name { get; set; } = "";
        /// <summary>Run state.</summary>
        [JsonPropertyName("state")] public QueueState State { get; set; }
        /// <summary>Effective rate limits.</summary>
        [JsonPropertyName("rate_limits")] public RateLimits RateLimits { get; set; } = new RateLimits();
        /// <summary>Effective retry policy.</summary>
        [JsonPropertyName("retry")] public RetryConfig Retry { get; set; } = new RetryConfig();
        /// <summary>Delivery target.</summary>
        [JsonPropertyName("target")] public QueueTarget Target { get; set; } = new QueueTarget();
        /// <summary>Dead-letter queue, if any.</summary>
        [JsonPropertyName("dead_letter_queue")] public string? DeadLetterQueue { get; set; }
        /// <summary>Time of the last purge.</summary>
        [JsonPropertyName("purged_at")] public DateTimeOffset? PurgedAt { get; set; }
        /// <summary>Creation time.</summary>
        [JsonPropertyName("created_at")] public DateTimeOffset CreatedAt { get; set; }
        /// <summary>Last configuration change.</summary>
        [JsonPropertyName("updated_at")] public DateTimeOffset UpdatedAt { get; set; }
    }

    /// <summary>Live queue statistics.</summary>
    public sealed class QueueStats
    {
        /// <summary>Run state.</summary>
        [JsonPropertyName("state")] public string State { get; set; } = "";
        /// <summary>Tasks due now or later.</summary>
        [JsonPropertyName("pending")] public long Pending { get; set; }
        /// <summary>Tasks being delivered.</summary>
        [JsonPropertyName("running")] public long Running { get; set; }
        /// <summary>Tasks delivered successfully (within retention).</summary>
        [JsonPropertyName("succeeded")] public long Succeeded { get; set; }
        /// <summary>Tasks that exhausted their retries (within retention).</summary>
        [JsonPropertyName("failed")] public long Failed { get; set; }
        /// <summary>Pending tasks scheduled in the future.</summary>
        [JsonPropertyName("scheduled_future")] public long ScheduledFuture { get; set; }
        /// <summary>Schedule time of the oldest due task.</summary>
        [JsonPropertyName("oldest_due_at")] public DateTimeOffset? OldestDueAt { get; set; }
        /// <summary>How long the oldest due task has waited, in seconds.</summary>
        [JsonPropertyName("oldest_due_age_seconds")] public double? OldestDueAgeSeconds { get; set; }
        /// <summary>Dispatches in the last minute.</summary>
        [JsonPropertyName("dispatched_last_1m")] public long DispatchedLast1m { get; set; }
        /// <summary>Failed dispatches in the last minute.</summary>
        [JsonPropertyName("errors_last_1m")] public long ErrorsLast1m { get; set; }
        /// <summary>Dispatches in the last hour.</summary>
        [JsonPropertyName("dispatched_last_1h")] public long DispatchedLast1h { get; set; }
        /// <summary>Failed dispatches in the last hour.</summary>
        [JsonPropertyName("errors_last_1h")] public long ErrorsLast1h { get; set; }
        /// <summary>Time of the last purge.</summary>
        [JsonPropertyName("last_purge")] public DateTimeOffset? LastPurge { get; set; }
    }

    /// <summary>A task to enqueue.</summary>
    public sealed class TaskCreate
    {
        /// <summary>Client-chosen ID for deduplication (409 on collision). Null = server-generated UUIDv7.</summary>
        public string? TaskId { get; set; }
        /// <summary>Absolute http(s) URL. Set exactly one of Url or Path.</summary>
        public string? Url { get; set; }
        /// <summary>Path resolved against the queue's target base URL at dispatch time.</summary>
        public string? Path { get; set; }
        /// <summary>HTTP method. Default POST.</summary>
        public string? Method { get; set; }
        /// <summary>Request headers.</summary>
        public IDictionary<string, string>? Headers { get; set; }
        /// <summary>Raw body bytes. Exclusive with <see cref="JsonBody"/>. POST/PUT/PATCH only.</summary>
        public byte[]? Body { get; set; }
        /// <summary>Any value serializable by System.Text.Json; sent with Content-Type: application/json.</summary>
        public object? JsonBody { get; set; }
        /// <summary>When to dispatch. Exclusive with <see cref="Delay"/>.</summary>
        public DateTimeOffset? ScheduleTime { get; set; }
        /// <summary>Dispatch after this delay. Exclusive with <see cref="ScheduleTime"/>.</summary>
        public TimeSpan? Delay { get; set; }
        /// <summary>How long one attempt may take (15s–30m; default 10m).</summary>
        public TimeSpan? DispatchDeadline { get; set; }
        /// <summary>Per-task override of the queue's retry policy (any subset).</summary>
        public RetryConfig? Retry { get; set; }
    }

    /// <summary>The outcome of a task's most recent attempt.</summary>
    public sealed class LastAttempt
    {
        /// <summary>When it was dispatched.</summary>
        [JsonPropertyName("at")] public DateTimeOffset At { get; set; }
        /// <summary>HTTP status, if a response arrived.</summary>
        [JsonPropertyName("status")] public int? Status { get; set; }
        /// <summary>Error description, if it failed.</summary>
        [JsonPropertyName("error")] public string? Error { get; set; }
    }

    /// <summary>A task. (Named PushqTask to avoid clashing with System.Threading.Tasks.Task.)</summary>
    public sealed class PushqTask
    {
        /// <summary>Task ID.</summary>
        [JsonPropertyName("id")] public string Id { get; set; } = "";
        /// <summary>Queue name.</summary>
        [JsonPropertyName("queue")] public string Queue { get; set; } = "";
        /// <summary>Lifecycle state.</summary>
        [JsonPropertyName("state")] public TaskState State { get; set; }
        /// <summary>Delivery URL.</summary>
        [JsonPropertyName("url")] public string Url { get; set; } = "";
        /// <summary>HTTP method.</summary>
        [JsonPropertyName("method")] public string Method { get; set; } = "";
        /// <summary>Request headers (only with <see cref="TaskView.Full"/>).</summary>
        [JsonPropertyName("headers")] public Dictionary<string, string>? Headers { get; set; }
        /// <summary>Base64 body as sent on the wire; see <see cref="Body"/>.</summary>
        [JsonPropertyName("body_base64")] public string? BodyBase64 { get; set; }
        /// <summary>Request body (only with <see cref="TaskView.Full"/>).</summary>
        [JsonIgnore] public byte[]? Body => BodyBase64 == null ? null : Convert.FromBase64String(BodyBase64);
        /// <summary>When it is (or was) due.</summary>
        [JsonPropertyName("schedule_time")] public DateTimeOffset ScheduleTime { get; set; }
        /// <summary>Creation time.</summary>
        [JsonPropertyName("created_at")] public DateTimeOffset CreatedAt { get; set; }
        /// <summary>Per-attempt timeout in seconds.</summary>
        [JsonPropertyName("dispatch_deadline_seconds")] public double DispatchDeadlineSeconds { get; set; }
        /// <summary>Per-task retry override, if any.</summary>
        [JsonPropertyName("retry")] public RetryConfig? Retry { get; set; }
        /// <summary>Attempts started.</summary>
        [JsonPropertyName("dispatch_count")] public int DispatchCount { get; set; }
        /// <summary>Attempts that got an HTTP response.</summary>
        [JsonPropertyName("response_count")] public int ResponseCount { get; set; }
        /// <summary>When the first attempt started.</summary>
        [JsonPropertyName("first_attempt_at")] public DateTimeOffset? FirstAttemptAt { get; set; }
        /// <summary>When it succeeded or failed for good.</summary>
        [JsonPropertyName("finished_at")] public DateTimeOffset? FinishedAt { get; set; }
        /// <summary>The most recent attempt.</summary>
        [JsonPropertyName("last_attempt")] public LastAttempt? LastAttempt { get; set; }
    }

    /// <summary>One delivery attempt.</summary>
    public sealed class Attempt
    {
        /// <summary>1-based attempt number.</summary>
        [JsonPropertyName("number")] public int Number { get; set; }
        /// <summary>When it was dispatched.</summary>
        [JsonPropertyName("dispatch_time")] public DateTimeOffset DispatchTime { get; set; }
        /// <summary>When the response arrived.</summary>
        [JsonPropertyName("response_time")] public DateTimeOffset? ResponseTime { get; set; }
        /// <summary>HTTP status, if a response arrived.</summary>
        [JsonPropertyName("response_status")] public int? ResponseStatus { get; set; }
        /// <summary>Failure class: connect, timeout, dns, tls, non_2xx, config, lease_lost…</summary>
        [JsonPropertyName("error_kind")] public string? ErrorKind { get; set; }
        /// <summary>Start of the error or response body.</summary>
        [JsonPropertyName("error_snippet")] public string? ErrorSnippet { get; set; }
        /// <summary>Duration in milliseconds.</summary>
        [JsonPropertyName("duration_ms")] public long DurationMs { get; set; }
    }

    /// <summary>Why one item of a batch failed.</summary>
    public sealed class BatchError
    {
        /// <summary>Stable error code, e.g. already_exists.</summary>
        [JsonPropertyName("code")] public string Code { get; set; } = "";
        /// <summary>Human-readable detail.</summary>
        [JsonPropertyName("detail")] public string Detail { get; set; } = "";
    }

    /// <summary>Per-item result of a batch create, in request order.</summary>
    public sealed class BatchResult
    {
        /// <summary>Index in the request.</summary>
        [JsonPropertyName("index")] public int Index { get; set; }
        /// <summary>The created task, on success.</summary>
        [JsonPropertyName("task")] public PushqTask? Task { get; set; }
        /// <summary>The error, on failure.</summary>
        [JsonPropertyName("error")] public BatchError? Error { get; set; }
    }

    /// <summary>One page of results.</summary>
    public sealed class Page<T>
    {
        internal Page(IReadOnlyList<T> items, string? nextPageToken)
        {
            Items = items;
            NextPageToken = string.IsNullOrEmpty(nextPageToken) ? null : nextPageToken;
        }

        /// <summary>Items on this page.</summary>
        public IReadOnlyList<T> Items { get; }
        /// <summary>Pass as the page token to get the next page; null on the last page.</summary>
        public string? NextPageToken { get; }
    }

    /// <summary>Options for listing tasks.</summary>
    public sealed class ListTasksOptions
    {
        /// <summary>Only tasks in this state.</summary>
        public TaskState? State { get; set; }
        /// <summary>Only task IDs starting with this.</summary>
        public string? IdPrefix { get; set; }
        /// <summary>Include headers and body.</summary>
        public TaskView? View { get; set; }
        /// <summary>Page size (server default 50).</summary>
        public int? PageSize { get; set; }
        /// <summary>Token from a previous page.</summary>
        public string? PageToken { get; set; }
    }

    /// <summary>Options for listing queues.</summary>
    public sealed class ListQueuesOptions
    {
        /// <summary>Only names starting with this, e.g. "user/" for a folder.</summary>
        public string? NamePrefix { get; set; }
        /// <summary>Page size (server default 100, max 1000).</summary>
        public int? PageSize { get; set; }
        /// <summary>Token from a previous page.</summary>
        public string? PageToken { get; set; }
    }

    internal sealed class QueueList
    {
        [JsonPropertyName("queues")] public List<Queue>? Queues { get; set; }
        [JsonPropertyName("next_page_token")] public string? NextPageToken { get; set; }
    }

    internal sealed class TaskList
    {
        [JsonPropertyName("tasks")] public List<PushqTask>? Tasks { get; set; }
        [JsonPropertyName("next_page_token")] public string? NextPageToken { get; set; }
    }

    internal sealed class AttemptList
    {
        [JsonPropertyName("attempts")] public List<Attempt>? Attempts { get; set; }
    }

    internal sealed class BatchResponse
    {
        [JsonPropertyName("results")] public List<BatchResult>? Results { get; set; }
    }

    internal sealed class Problem
    {
        [JsonPropertyName("code")] public string? Code { get; set; }
        [JsonPropertyName("detail")] public string? Detail { get; set; }
        [JsonPropertyName("title")] public string? Title { get; set; }
    }

    internal static class Json
    {
        internal static readonly JsonSerializerOptions Options = new JsonSerializerOptions
        {
            DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
            Converters = { new JsonStringEnumConverter(JsonNamingPolicy.CamelCase) },
        };
    }
}
