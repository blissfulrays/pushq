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

> **First launch:** open `/ui/` and create the admin account before exposing
> the server publicly — until then a server without `api_keys` accepts
> anonymous requests. Serve the dashboard over HTTPS; behind a TLS-terminating
> proxy, forward `X-Forwarded-Proto: https` so the session cookie is marked
> `Secure`.

> **Reverse proxies:** queue names in folders travel as `%2F` in the path
> (`/v1/queues/user%2Finstagram`). The proxy must forward the path unmodified.
> Caddy does by default; with nginx use `proxy_pass http://127.0.0.1:8080;`
> with **no** URI part (a trailing `/` makes nginx decode `%2F` into `/`, which
> then returns 404).

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

## SaaS mode (WorkOS)

Set `WORKOS_CLIENT_ID` and `WORKOS_API_KEY` (environment, or a `.env` file
next to where `pushq serve` runs; see `.env.example`) and pushq becomes
multi-tenant:

- **Sign-in is WorkOS AuthKit only.** No setup screen, no local passwords.
  AuthKit's hosted pages handle sign-up, password reset, social login and MFA.
- **Teams are WorkOS Organizations.** A new user creates a team; admins invite
  people from *Settings → Team* and WorkOS emails the invitation. A person can
  belong to several teams and switch between them in the account menu.
- **Each team's data is isolated.** Queues, tasks, attempts, stats and API keys
  are scoped to the team; two teams can both have a queue called `emails`.
  Internally a team's queues are stored as `<namespace>:<name>`, which no
  client-supplied name can reach, and receivers only ever see the short name.
- **Roles live in pushq** (admin / member / viewer, per team). In WorkOS a
  pushq admin is an organization `admin` and members and viewers are `member`.
- **Config-file `api_keys` are operator keys**: not tied to a team, they see
  every team's queues and can read `/metrics` (which team users can't, since it
  names every team's queues). Keep them for your own tooling.

### WorkOS dashboard checklist

1. **Redirects → Sign-in callback**: add
   `https://<your-pushq-host>/v1/auth/workos/callback`
   (for local testing in the staging environment:
   `http://localhost:8080/v1/auth/workos/callback`). If pushq sits behind a
   proxy that changes the host or scheme, set `WORKOS_REDIRECT_URI` to the
   exact URL instead.
2. **Authentication → Sign up**: allow sign-ups, so new customers can create
   accounts.
3. **Roles**: keep the default `admin` and `member` roles (pushq uses those
   slugs; if `admin` is missing it falls back to the default role).
4. Use the **staging** environment while testing; it's free.

Pushq re-syncs a person's teams from WorkOS every time they sign in, and a
team's members whenever its Team page is opened, so changes made directly in
the WorkOS dashboard are picked up.

Switching an existing self-hosted server to SaaS mode leaves its existing
queues untouched but outside every team (only operator keys see them).
