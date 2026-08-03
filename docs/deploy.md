# Deploying pushq

pushq is one static binary (dashboard included). Two deployment shapes:

- **Single node + SQLite** — zero dependencies. Right for one-box setups and
  up to ~500 sustained dispatches/sec.
- **N replicas + Postgres** — all replicas serve the API; an advisory lock
  elects one dispatcher; per-task lease fencing makes overlapping replicas
  (blue/green, crash failover) safe.

## systemd (single node, SQLite)

```ini
# /etc/systemd/system/pushq.service
[Unit]
Description=pushq task queue
After=network-online.target

[Service]
ExecStart=/usr/local/bin/pushq serve -c /etc/pushq/pushq.yaml
Restart=always
RestartSec=2
User=pushq
StateDirectory=pushq
# SIGTERM → graceful drain (finishes in-flight dispatches, ≤ drain_timeout)
TimeoutStopSec=45

[Install]
WantedBy=multi-user.target
```

```yaml
# /etc/pushq/pushq.yaml
db: sqlite:/var/lib/pushq/pushq.db
listen: 127.0.0.1:8080          # put Caddy/nginx in front for TLS
api_keys:
  - { key: "pq_admin_<generate>",  role: admin }
  - { key: "pq_writer_<generate>", role: writer }
```

## docker-compose (Postgres, 2 replicas)

```yaml
services:
  db:
    image: postgres:16
    environment: { POSTGRES_USER: pushq, POSTGRES_PASSWORD: pushq, POSTGRES_DB: pushq }
    volumes: [pgdata:/var/lib/postgresql/data]
    healthcheck: { test: ["CMD-SHELL", "pg_isready -U pushq"], interval: 2s, retries: 30 }

  pushq-a: &pushq
    image: ghcr.io/blissfulrays/pushq:latest
    command: serve
    environment:
      PUSHQ_DB: postgres://pushq:pushq@db/pushq?sslmode=disable
      PUSHQ_LISTEN: ":8080"
      PUSHQ_API_KEYS: "admin:pq_admin_<generate>,writer:pq_writer_<generate>"
    depends_on: { db: { condition: service_healthy } }
    ports: ["8081:8080"]

  pushq-b:
    <<: *pushq
    ports: ["8082:8080"]

volumes:
  pgdata:
```

Load-balance the two replicas however you like; `GET /readyz` tells you which
one currently runs the dispatcher (`"role": "leader"`).

## Blue/green deploys

1. Start the new instance (it serves the API immediately; dispatcher role is
   `follower`).
2. SIGTERM the old one: it releases dispatcher leadership instantly, then
   drains in-flight dispatches (up to `dispatcher.drain_timeout`, default 30s).
3. The new instance acquires leadership within ~5s and claims from where the
   old one left off. Lease fencing guarantees no task is delivered twice
   during the overlap.

## Sizing notes

- Memory ceiling ≈ `dispatcher.global_concurrency × limits.max_body_bytes`
  (defaults: 512 × 1 MiB = 512 MiB worst case) plus ~a few MB per 10k active
  queues.
- SQLite (pure-Go driver, single writer): plan for ~500 sustained
  dispatches/sec with headroom. Beyond that, or for HA, use Postgres.
- Postgres pool: 16 conns per replica by default; add PgBouncer only in
  session mode (LISTEN/NOTIFY and advisory locks need real sessions).

Smoke benchmark (`PUSHQ_LOAD=1 go test ./test/load`, one laptop, local
instant receiver, 1,000 dynamic queues × 50,000 tasks):

| Backend | Queue creation | Enqueue | End-to-end dispatch |
|---|---|---|---|
| SQLite | 1,000 in 0.2s | ~11,000/s | ~2,300 tasks/s |
| Postgres 15 | 1,000 in 0.6s | ~2,600/s (batched) | ~3,600 tasks/s |

Treat these as upper-bound smoke numbers, not SLOs — real throughput is
usually bounded by your receivers.

## Monitoring

- `GET /metrics` — Prometheus: dispatch counts by code class, error kinds,
  latency histogram, in-flight gauge, active queues, retries, expired leases,
  dead-lettered counts. Deliberately no per-queue labels (cardinality);
  per-queue numbers come from `GET /v1/queues/{q}/stats`.
- Log format: `log: { format: json }` for structured logs.

## Backup

SQLite: back up the `.db` file (WAL mode — use `sqlite3 .backup` or stop the
service). Postgres: normal `pg_dump`. All state lives in three tables
(`queues`, `tasks`, `task_attempts`).
