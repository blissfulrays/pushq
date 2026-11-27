# Migrating from GCP Cloud Tasks

pushq keeps Cloud Tasks' semantics (rate limits, token-bucket bursts, retry
backoff math, pause/purge, named-task dedup, at-least-once delivery) so a
migration is mostly mechanical renaming. This guide maps every
`google-cloud-tasks` concept to its pushq equivalent.

## Concept map

| Cloud Tasks | pushq |
|---|---|
| Project / location / queue path | Queue name (`[a-z0-9-]` segments joined by `/` for folders, e.g. `billing/invoices`; ≤200 chars) |
| `CreateQueue` / `UpdateQueue` | `PUT /v1/queues/{queue}` — one idempotent upsert, cheap to call on every enqueue |
| `queue.rateLimits.maxDispatchesPerSecond` | `rate_limits.max_dispatches_per_second` |
| `queue.rateLimits.maxBurstSize` | `rate_limits.max_burst_size` (0 = auto, like GCP) |
| `queue.rateLimits.maxConcurrentDispatches` | `rate_limits.max_concurrent_dispatches` |
| `queue.retryConfig.maxAttempts` | `retry.max_attempts` (-1 = unlimited) |
| `queue.retryConfig.maxRetryDuration` | `retry.max_retry_duration_seconds` |
| `queue.retryConfig.minBackoff` / `maxBackoff` | `retry.min_backoff_seconds` / `max_backoff_seconds` |
| `queue.retryConfig.maxDoublings` | `retry.max_doublings` (same doubling-then-linear schedule) |
| `PauseQueue` / `ResumeQueue` / `PurgeQueue` | `POST /v1/queues/{q}:pause` / `:resume` / `:purge` |
| `DeleteQueue` | `DELETE /v1/queues/{q}` (admin; name reusable immediately, GCP blocks ~7 days) |
| `CreateTask` with `task.name` | `POST /v1/queues/{q}/tasks` with `task_id` (dedup window default 1h, configurable) |
| `task.httpRequest.url` | `url`, or `path` + queue-level `target.base_url` |
| `task.httpRequest.headers` / `body` | `headers` / `body_base64` — or `json_body` for JSON payloads (no manual encoding) |
| `task.scheduleTime` | `schedule_time`, or the friendlier `delay_seconds` |
| `task.dispatchDeadline` | `dispatch_deadline_seconds` (same 15s–30m range, 10m default) |
| `RunTask` | `POST /v1/queues/{q}/tasks/{id}:run` (same bypass semantics) |
| `ListTasks` / `GetTask` / `DeleteTask` | Same, plus `state`/`id_prefix` filters and cursor pagination |
| OIDC/OAuth tokens on dispatch | Static header injection (`target.headers`) or Standard-Webhooks HMAC signing (`target.signing_secret`) |
| `X-CloudTasks-QueueName` etc. | `X-Pushq-Queue`, `X-Pushq-Task-Id`, `X-Pushq-Attempt`, `X-Pushq-Execution-Count`, `X-Pushq-Scheduled-Time` |
| App Engine targets, pull queues, BufferTask | Not supported (HTTP push only) |

## What pushq adds

- **Per-task retry overrides** — `retry` on the task, not just the queue.
  Note: the GCP Python client's `client.create_task(..., retry=Retry(...))`
  parameter is an *RPC retry policy for the enqueue call itself*, not a
  delivery policy — a common source of silent misconfiguration. In pushq,
  `retry` on a task genuinely controls delivery retries.
- **No queue cap** — GCP limits 1,000 queues/region; pushq is designed for
  10k+ dynamic queues (one per tenant/account works fine).
- **Dead-letter queues** — `dead_letter_queue` on any queue.
- **Stats API** — `GET /v1/queues/{q}/stats?id_prefix=` for depth by state,
  oldest-due age, and recent error rates, without listing tasks.
- **Batch enqueue** — `POST /v1/queues/{q}/tasks:batch` (≤500).
- **Retained history** — completed tasks and full attempt logs kept for a
  configurable window (GCP deletes them).
- **Schedules up to 1 year** ahead (GCP: 30 days), configurable.

## Semantics kept bit-for-bit (worth knowing)

- **Backoff schedule**: interval starts at `min_backoff`, doubles
  `max_doublings` times, then grows linearly by `2^max_doublings × min_backoff`
  per retry, capped at `max_backoff`.
- **The both-limits quirk**: when `max_attempts` AND `max_retry_duration` are
  both set, the task retries until **both** are reached — exactly like GCP.
- **2xx = success**; redirects are followed and don't count as attempts;
  429/503 also honor `Retry-After` and briefly cool the queue down.
- **At-least-once delivery**: make handlers idempotent. The signed
  `webhook-id` header (`queue/task_id`) is stable across retries — use it as
  an idempotency key.
- `:run` works on PAUSED queues and bypasses rate limits; disabled queues
  refuse task creation and dispatch.

## Typical client migration (Python shown)

Before (google-cloud-tasks):

```python
from google.cloud import tasks_v2

client = tasks_v2.CloudTasksClient()
parent = client.queue_path(project, location, "account-42")
client.create_task(parent=parent, task={
    "http_request": {
        "http_method": tasks_v2.HttpMethod.POST,
        "url": f"{base_url}/hooks/send",
        "headers": {"Content-Type": "application/json", "x-internal-key": KEY},
        "body": json.dumps(payload).encode(),
    },
    "schedule_time": timestamp_pb2.Timestamp(seconds=int(time.time() + 3600)),
    "name": f"{parent}/tasks/follow-up-{uid}",
})
```

After (pushq):

```python
from pushq import Pushq, RateLimits, Retry, Target

pq = Pushq(PUSHQ_URL, api_key=PUSHQ_KEY)
pq.upsert_queue("account-42",                       # replaces create/update queue
    rate_limits=RateLimits(max_dispatches_per_second=10),
    target=Target(base_url=base_url,
                  headers={"x-internal-key": KEY},  # or signing_secret="whsec_..."
                  ))
pq.create_task("account-42",
    path="/hooks/send",
    json_body=payload,
    delay_seconds=3600,
    task_id=f"follow-up-{uid}",
    retry=Retry(max_attempts=3),
)
```

## Authenticating deliveries without OIDC

Two options, combinable:

1. **Static headers** (drop-in for an internal shared-secret header):
   `target.headers = {"x-internal-key": "..."}` — injected on every dispatch.
2. **Signing (recommended)**: set `target.signing_secret = "whsec_..."`.
   Deliveries carry Standard-Webhooks headers verifiable with any
   standard-webhooks library or the `verify_signature` helper in every pushq
   SDK — constant-time, with replay protection via the timestamp.

## Local development

`pushq dev` runs a full real queue (SQLite, no auth, fast timers) — delays,
retries, and rate limits behave exactly like production instead of being
faked with synchronous calls.
