# pushq

**Self-hosted HTTP push task queue — an open-source alternative to GCP Cloud Tasks.**

pushq delivers tasks as HTTP requests to your endpoints, with per-queue rate
limits, concurrency caps, scheduled delivery, and Cloud Tasks-compatible retry
semantics. One static binary. SQLite by default, Postgres for HA.

```bash
pushq dev          # full local queue: SQLite, no auth, fast timers
```

```bash
# Create/update a queue at runtime (idempotent — call it on every enqueue if you like)
curl -X PUT localhost:8080/v1/queues/account-42 -d '{
  "rate_limits": {"max_dispatches_per_second": 10, "max_concurrent_dispatches": 5},
  "retry": {"max_attempts": 5, "min_backoff_seconds": 1, "max_backoff_seconds": 300},
  "target": {"base_url": "https://api.example.com", "signing_secret": "whsec_..."}
}'

# Enqueue: deliver POST /hooks/send in an hour, deduped by task_id
curl -X POST localhost:8080/v1/queues/account-42/tasks -d '{
  "path": "/hooks/send",
  "json_body": {"user_id": 1},
  "delay_seconds": 3600,
  "task_id": "follow-up-abc",
  "retry": {"max_attempts": 3}
}'
```

## Why pushq

| | GCP Cloud Tasks | pushq |
|---|---|---|
| Hosting | GCP only | Anywhere: your VPS, Docker, bare metal |
| Queues | 1,000/region hard cap | Uncapped; built for 10k+ dynamic queues (one per tenant/account) |
| Retry config | Queue-level only | Queue-level **plus per-task overrides** |
| Dead-letter queues | — | Per-queue `dead_letter_queue` |
| Completed tasks | Deleted, history lost | Retained with full attempt history (configurable TTL) |
| Queue introspection | List tasks and count | `stats` endpoint: depth by state, oldest-due age, error rates, `id_prefix` scoping |
| Delivery auth | OIDC (GCP-only) | Static headers, or Standard-Webhooks HMAC signing every SDK can verify |
| Max schedule ahead | 30 days | 1 year (configurable) |
| Local development | Emulators/hacks | `pushq dev` — the real thing |
| Batch enqueue | — | `tasks:batch` (≤500) |

Retry semantics are Cloud Tasks-faithful: token-bucket rate limiting with
burst, backoff that doubles `max_doublings` times then grows linearly capped
at `max_backoff`, the documented "retries until **both** limits hit" behavior,
`Retry-After` honored on 429/503, redirects followed without counting as
attempts, at-least-once delivery. Migrating? See
[docs/migrate-from-cloud-tasks.md](docs/migrate-from-cloud-tasks.md).

## Install

```bash
# Binary releases (macOS/Linux/Windows, amd64/arm64)
curl -fsSL https://github.com/blissfulrays/pushq/releases/latest  # see assets

# Docker
docker run -p 8080:8080 -v pushq-data:/data ghcr.io/blissfulrays/pushq serve --db sqlite:/data/pushq.db

# From source (Go 1.22+)
go install github.com/blissfulrays/pushq/cmd/pushq@latest
```

Release binaries and the Docker image bundle the dashboard. `go install` does
not — the compiled dashboard is a build artifact rather than a committed file,
and the Go module proxy never runs npm. Such a build is fully functional and
serves a short placeholder at `/ui/`. To get the dashboard from source, run
`make build`, which builds the frontend first (needs Node 20+).

## Run

```bash
pushq serve --db sqlite:/var/lib/pushq/pushq.db --listen :8080
# or Postgres (enables multi-replica HA via advisory-lock leader election):
pushq serve --db postgres://user:pass@host/pushq
```

Config file (`pushq serve -c pushq.yaml`), everything overridable via
`PUSHQ_*` env vars:

```yaml
db: sqlite:/var/lib/pushq/pushq.db     # or postgres://…
listen: :8080
api_keys:
  - { key: "pq_admin_change-me",  role: admin }   # + queue delete, list-all
  - { key: "pq_writer_change-me", role: writer }  # enqueue + queue ops
dispatcher:
  global_concurrency: 512
  reconcile_interval: 30s
limits:
  max_body_bytes: 1048576
  max_schedule_ahead: 8760h        # 1 year
  dedup_window: 1h
retention: { succeeded: 24h, failed: 168h }
```

No `api_keys` = unauthenticated (dev only; the server warns loudly).

## How delivery works

Each dispatch is an HTTP request to your endpoint carrying:

```
X-Pushq-Queue: account-42
X-Pushq-Task-Id: follow-up-abc
X-Pushq-Attempt: 2                       # 1-based
X-Pushq-Scheduled-Time: 2026-08-02T17:00:00Z
webhook-id: account-42/follow-up-abc     # if signing is enabled —
webhook-timestamp: 1754154000            # Standard Webhooks compatible,
webhook-signature: v1,MEQCIB…            # stable id across retries
```

**2xx = success.** Anything else (or a timeout past the task's
`dispatch_deadline_seconds`) retries on the queue's schedule. Handlers should
be idempotent; the `webhook-id` is your idempotency key.

## SDKs

| Language | Install | |
|---|---|---|
| TypeScript/JS | `npm install pushq` | [docs](sdks/typescript/README.md) |
| Python | `pip install pushq` | [docs](sdks/python/README.md) |
| Go | `go get github.com/blissfulrays/pushq/sdks/go` | [docs](sdks/go/README.md) |
| Java | `io.github.blissfulrays:pushq` | [docs](sdks/java/README.md) |

Every SDK ships the client **and** a constant-time `verify_signature` helper
for receivers. The REST API is fully described in
[api/openapi.yaml](api/openapi.yaml).

## Dashboard

The binary embeds a minimal dashboard at `/ui/` — queues with live stats,
pause/resume/purge, task browsing with attempt history, force-run.

## Operations

- `GET /healthz`, `GET /readyz` (reports `leader`/`follower`), `GET /metrics`
  (Prometheus, aggregate-only by design — per-queue numbers come from the
  stats API).
- **HA**: run ≥2 replicas against one Postgres. All replicas serve the API;
  an advisory lock elects one dispatcher. Correctness never depends on the
  lock — every completion is fenced by a per-task lease token, so blue/green
  deploys can't double-dispatch.
- **Retention**: succeeded 24h / failed 7d by default; task IDs stay reserved
  for `dedup_window` after completion/deletion.
- See [docs/deploy.md](docs/deploy.md) for systemd and docker-compose setups.

## Development

```bash
make test        # unit + SQLite conformance + e2e
make test-pg     # + Postgres conformance and blue/green tests (needs PUSHQ_TEST_PG_URL)
make build       # binary with embedded dashboard
```

Cutting a release (binaries, Docker image, and all four SDKs from one tag):
[docs/publishing.md](docs/publishing.md).

MIT licensed.
