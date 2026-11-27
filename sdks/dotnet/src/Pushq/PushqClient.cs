using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;

namespace Pushq
{
    /// <summary>Thrown for any non-2xx API response (RFC 9457 problem+json).</summary>
    public sealed class PushqException : Exception
    {
        /// <summary>Creates the exception.</summary>
        public PushqException(int status, string code, string detail)
            : base($"pushq: {code} ({status}): {detail}")
        {
            Status = status;
            Code = code;
            Detail = detail;
        }

        /// <summary>HTTP status code.</summary>
        public int Status { get; }
        /// <summary>Stable machine code: already_exists, not_found, failed_precondition, invalid_argument, …</summary>
        public string Code { get; }
        /// <summary>Human-readable detail from the server.</summary>
        public string Detail { get; }
    }

    /// <summary>Options for <see cref="PushqClient"/>.</summary>
    public sealed class PushqClientOptions
    {
        /// <summary>
        /// Server URL, e.g. "http://localhost:8080" when self-hosting. Defaults to
        /// the hosted API, <see cref="PushqClient.DefaultBaseUrl"/>.
        /// </summary>
        public string BaseUrl { get; set; } = PushqClient.DefaultBaseUrl;
        /// <summary>API key sent as a bearer token. Null against an unauthenticated dev server.</summary>
        public string? ApiKey { get; set; }
        /// <summary>Per-request timeout. Default 30 seconds. Ignored when <see cref="HttpClient"/> is supplied.</summary>
        public TimeSpan Timeout { get; set; } = TimeSpan.FromSeconds(30);
        /// <summary>
        /// HttpClient to use (e.g. from IHttpClientFactory). The client does not
        /// dispose a supplied instance.
        /// </summary>
        public HttpClient? HttpClient { get; set; }
    }

    /// <summary>
    /// Client for the pushq REST API. Thread-safe; create one and reuse it.
    /// </summary>
    /// <example>
    /// <code>
    /// using var pushq = new PushqClient(apiKey: "pq_…"); // hosted API; or pass your server's URL first
    /// await pushq.UpsertQueueAsync("emails", new QueueConfig {
    ///     RateLimits = new RateLimits { MaxDispatchesPerSecond = 50 },
    ///     Target = new Target { BaseUrl = "https://api.example.com" },
    /// });
    /// await pushq.CreateTaskAsync("emails", new TaskCreate {
    ///     Path = "/send", JsonBody = new { to = "a@example.com" }, Delay = TimeSpan.FromMinutes(5),
    /// });
    /// </code>
    /// </example>
    public sealed class PushqClient : IDisposable
    {
        private readonly HttpClient _http;
        private readonly bool _ownsHttp;
        private readonly string _base;
        private readonly string? _apiKey;

        /// <summary>The hosted pushq API, used when no base URL is given.</summary>
        public const string DefaultBaseUrl = "https://api.pushqueue.dev";

        /// <summary>
        /// Creates a client for <paramref name="baseUrl"/>, or for the hosted API
        /// (<see cref="DefaultBaseUrl"/>) when it is null or empty.
        /// </summary>
        public PushqClient(string? baseUrl = null, string? apiKey = null)
            : this(new PushqClientOptions { BaseUrl = baseUrl ?? DefaultBaseUrl, ApiKey = apiKey })
        {
        }

        /// <summary>Creates a client from options.</summary>
        public PushqClient(PushqClientOptions options)
        {
            if (options == null) throw new ArgumentNullException(nameof(options));
            _base = (string.IsNullOrWhiteSpace(options.BaseUrl) ? DefaultBaseUrl : options.BaseUrl.Trim()).TrimEnd('/');
            _apiKey = options.ApiKey;
            if (options.HttpClient != null)
            {
                _http = options.HttpClient;
            }
            else
            {
                _http = new HttpClient { Timeout = options.Timeout };
                _ownsHttp = true;
            }
        }

        // --- queues ---

        /// <summary>Creates the queue or replaces its configuration (idempotent; never changes run state).</summary>
        public Task<Queue> UpsertQueueAsync(string name, QueueConfig? config = null, CancellationToken ct = default)
            => SendAsync<Queue>(HttpMethod.Put, QueuePath(name), Wire.QueueConfig(config ?? new QueueConfig(), null), ct);

        /// <summary>Creates a queue; throws <see cref="PushqException"/> with code already_exists if it exists.</summary>
        public Task<Queue> CreateQueueAsync(string name, QueueConfig? config = null, CancellationToken ct = default)
        {
            var body = Wire.QueueConfig(config ?? new QueueConfig(), null);
            body["name"] = name;
            return SendAsync<Queue>(HttpMethod.Post, "/v1/queues", body, ct);
        }

        /// <summary>Gets a queue.</summary>
        public Task<Queue> GetQueueAsync(string name, CancellationToken ct = default)
            => SendAsync<Queue>(HttpMethod.Get, QueuePath(name), null, ct);

        /// <summary>Updates only the fields that are set (can set the state, including Disabled).</summary>
        public Task<Queue> PatchQueueAsync(string name, QueuePatch patch, CancellationToken ct = default)
        {
            if (patch == null) throw new ArgumentNullException(nameof(patch));
            return SendAsync<Queue>(new HttpMethod("PATCH"), QueuePath(name), Wire.QueueConfig(patch, patch.State), ct);
        }

        /// <summary>Lists queues. Needs a key or role allowed to list (admin key, or any dashboard user).</summary>
        public async Task<Page<Queue>> ListQueuesAsync(ListQueuesOptions? options = null, CancellationToken ct = default)
        {
            var o = options ?? new ListQueuesOptions();
            var w = await SendAsync<QueueList>(HttpMethod.Get, "/v1/queues" + Query(
                ("name_prefix", o.NamePrefix),
                ("page_size", o.PageSize?.ToString(CultureInfo.InvariantCulture)),
                ("page_token", o.PageToken)), null, ct).ConfigureAwait(false);
            return new Page<Queue>(w.Queues ?? new List<Queue>(), w.NextPageToken);
        }

        /// <summary>Deletes a queue and every task in it. Needs permission to delete queues.</summary>
        public Task DeleteQueueAsync(string name, CancellationToken ct = default)
            => SendAsync<object>(HttpMethod.Delete, QueuePath(name), null, ct);

        /// <summary>Pauses dispatch (tasks are still accepted).</summary>
        public Task<Queue> PauseQueueAsync(string name, CancellationToken ct = default)
            => SendAsync<Queue>(HttpMethod.Post, QueuePath(name) + ":pause", null, ct);

        /// <summary>Resumes dispatch.</summary>
        public Task<Queue> ResumeQueueAsync(string name, CancellationToken ct = default)
            => SendAsync<Queue>(HttpMethod.Post, QueuePath(name) + ":resume", null, ct);

        /// <summary>Deletes every task created up to now (task IDs become reusable).</summary>
        public Task<Queue> PurgeQueueAsync(string name, CancellationToken ct = default)
            => SendAsync<Queue>(HttpMethod.Post, QueuePath(name) + ":purge", null, ct);

        /// <summary>Gets live statistics, optionally only for task IDs starting with <paramref name="idPrefix"/>.</summary>
        public Task<QueueStats> GetQueueStatsAsync(string name, string? idPrefix = null, CancellationToken ct = default)
            => SendAsync<QueueStats>(HttpMethod.Get, QueuePath(name) + "/stats" + Query(("id_prefix", idPrefix)), null, ct);

        // --- tasks ---

        /// <summary>Enqueues a task. Throws already_exists when <see cref="TaskCreate.TaskId"/> was used recently.</summary>
        public Task<PushqTask> CreateTaskAsync(string queue, TaskCreate task, CancellationToken ct = default)
            => SendAsync<PushqTask>(HttpMethod.Post, QueuePath(queue) + "/tasks", Wire.TaskCreate(task), ct);

        /// <summary>Enqueues up to 500 tasks. Not atomic: each item succeeds or fails on its own.</summary>
        public async Task<IReadOnlyList<BatchResult>> CreateTasksAsync(string queue, IEnumerable<TaskCreate> tasks, CancellationToken ct = default)
        {
            if (tasks == null) throw new ArgumentNullException(nameof(tasks));
            var body = new Dictionary<string, object?> { ["tasks"] = tasks.Select(Wire.TaskCreate).ToList() };
            var w = await SendAsync<BatchResponse>(HttpMethod.Post, QueuePath(queue) + "/tasks:batch", body, ct).ConfigureAwait(false);
            return w.Results ?? new List<BatchResult>();
        }

        /// <summary>Gets a task.</summary>
        public Task<PushqTask> GetTaskAsync(string queue, string id, TaskView view = TaskView.Basic, CancellationToken ct = default)
            => SendAsync<PushqTask>(HttpMethod.Get,
                TaskPath(queue, id) + Query(("view", view == TaskView.Full ? "full" : null)), null, ct);

        /// <summary>Lists tasks in a queue, newest first.</summary>
        public async Task<Page<PushqTask>> ListTasksAsync(string queue, ListTasksOptions? options = null, CancellationToken ct = default)
        {
            var o = options ?? new ListTasksOptions();
            var w = await SendAsync<TaskList>(HttpMethod.Get, QueuePath(queue) + "/tasks" + Query(
                ("state", o.State == null ? null : Wire.Lower(o.State.Value)),
                ("id_prefix", o.IdPrefix),
                ("view", o.View == null ? null : Wire.Lower(o.View.Value)),
                ("page_size", o.PageSize?.ToString(CultureInfo.InvariantCulture)),
                ("page_token", o.PageToken)), null, ct).ConfigureAwait(false);
            return new Page<PushqTask>(w.Tasks ?? new List<PushqTask>(), w.NextPageToken);
        }

        /// <summary>Deletes a task; its ID stays reserved for the dedup window.</summary>
        public Task DeleteTaskAsync(string queue, string id, CancellationToken ct = default)
            => SendAsync<object>(HttpMethod.Delete, TaskPath(queue, id), null, ct);

        /// <summary>Dispatches a task now, bypassing rate limits and the paused state.</summary>
        public Task<PushqTask> RunTaskAsync(string queue, string id, CancellationToken ct = default)
            => SendAsync<PushqTask>(HttpMethod.Post, TaskPath(queue, id) + ":run", null, ct);

        /// <summary>Lists a task's delivery attempts, newest first.</summary>
        public async Task<IReadOnlyList<Attempt>> ListAttemptsAsync(string queue, string id, CancellationToken ct = default)
        {
            var w = await SendAsync<AttemptList>(HttpMethod.Get, TaskPath(queue, id) + "/attempts", null, ct).ConfigureAwait(false);
            return w.Attempts ?? new List<Attempt>();
        }

        // --- plumbing ---

        // Queue names may contain "/" (folders); EscapeDataString sends them as %2F
        // so the whole name stays one path segment.
        private static string QueuePath(string name)
        {
            if (string.IsNullOrEmpty(name)) throw new ArgumentException("queue name is required", nameof(name));
            return "/v1/queues/" + Uri.EscapeDataString(name);
        }

        private static string TaskPath(string queue, string id)
        {
            if (string.IsNullOrEmpty(id)) throw new ArgumentException("task id is required", nameof(id));
            return QueuePath(queue) + "/tasks/" + Uri.EscapeDataString(id);
        }

        private static string Query(params (string Key, string? Value)[] pairs)
        {
            var parts = pairs.Where(p => !string.IsNullOrEmpty(p.Value))
                .Select(p => p.Key + "=" + Uri.EscapeDataString(p.Value!)).ToList();
            return parts.Count == 0 ? "" : "?" + string.Join("&", parts);
        }

        private async Task<T> SendAsync<T>(HttpMethod method, string path, object? body, CancellationToken ct)
        {
            using var req = new HttpRequestMessage(method, _base + path);
            req.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue("application/json"));
            if (!string.IsNullOrEmpty(_apiKey))
                req.Headers.Authorization = new AuthenticationHeaderValue("Bearer", _apiKey);
            if (body != null)
                req.Content = new StringContent(JsonSerializer.Serialize(body, Json.Options), Encoding.UTF8, "application/json");

            using var resp = await _http.SendAsync(req, ct).ConfigureAwait(false);
            var text = resp.Content == null ? "" : await resp.Content.ReadAsStringAsync().ConfigureAwait(false);
            if (!resp.IsSuccessStatusCode)
                throw ToException(resp.StatusCode, text);
            if (resp.StatusCode == HttpStatusCode.NoContent || text.Length == 0)
                return default!;
            return JsonSerializer.Deserialize<T>(text, Json.Options)!;
        }

        private static PushqException ToException(HttpStatusCode status, string text)
        {
            Problem? p = null;
            try
            {
                p = JsonSerializer.Deserialize<Problem>(text, Json.Options);
            }
            catch (JsonException)
            {
                // not problem+json (e.g. a proxy error page)
            }
            var detail = p?.Detail ?? p?.Title ?? (text.Length > 200 ? text.Substring(0, 200) : text);
            return new PushqException((int)status, p?.Code ?? "internal", detail);
        }

        /// <summary>Disposes the HttpClient if this client created it.</summary>
        public void Dispose()
        {
            if (_ownsHttp) _http.Dispose();
        }
    }

    // Converts SDK request types to the snake_case wire format, omitting unset fields.
    internal static class Wire
    {
        internal static string Lower<TEnum>(TEnum value) where TEnum : struct
            => value.ToString()!.ToLowerInvariant();

        internal static Dictionary<string, object?> QueueConfig(QueueConfig cfg, QueueState? state)
        {
            var w = new Dictionary<string, object?>();
            if (state != null) w["state"] = Lower(state.Value);
            if (cfg.RateLimits != null) w["rate_limits"] = cfg.RateLimits;
            if (cfg.Retry != null) w["retry"] = cfg.Retry;
            if (cfg.Target != null)
            {
                var t = new Dictionary<string, object?>();
                if (cfg.Target.BaseUrl != null) t["base_url"] = cfg.Target.BaseUrl;
                if (cfg.Target.Method != null) t["method"] = cfg.Target.Method;
                if (cfg.Target.Headers != null) t["headers"] = cfg.Target.Headers;
                if (cfg.Target.SigningSecret != null) t["signing_secret"] = cfg.Target.SigningSecret;
                w["target"] = t;
            }
            if (cfg.DeadLetterQueue != null) w["dead_letter_queue"] = cfg.DeadLetterQueue;
            return w;
        }

        internal static Dictionary<string, object?> TaskCreate(TaskCreate t)
        {
            if (t == null) throw new ArgumentNullException(nameof(t));
            if (t.Body != null && t.JsonBody != null)
                throw new ArgumentException("pushq: set Body or JsonBody, not both");
            if (t.ScheduleTime != null && t.Delay != null)
                throw new ArgumentException("pushq: set ScheduleTime or Delay, not both");

            var w = new Dictionary<string, object?>();
            if (t.TaskId != null) w["task_id"] = t.TaskId;
            if (t.Url != null) w["url"] = t.Url;
            if (t.Path != null) w["path"] = t.Path;
            if (t.Method != null) w["method"] = t.Method;
            if (t.Headers != null) w["headers"] = t.Headers;
            if (t.Body != null) w["body_base64"] = Convert.ToBase64String(t.Body);
            if (t.JsonBody != null) w["json_body"] = t.JsonBody;
            if (t.ScheduleTime != null)
                w["schedule_time"] = t.ScheduleTime.Value.ToUniversalTime().ToString("yyyy-MM-dd'T'HH:mm:ss.fffffff'Z'", CultureInfo.InvariantCulture);
            if (t.Delay != null) w["delay_seconds"] = t.Delay.Value.TotalSeconds;
            if (t.DispatchDeadline != null) w["dispatch_deadline_seconds"] = t.DispatchDeadline.Value.TotalSeconds;
            if (t.Retry != null) w["retry"] = t.Retry;
            return w;
        }
    }
}
