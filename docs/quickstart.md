# Quickstart

## 1. Run the server

```bash
pushq dev
```

That's a complete local queue: SQLite in a temp file, no auth, fast timers,
dashboard at http://localhost:8080/. (Production: `pushq serve` — see
[deploy.md](deploy.md).)

## 2. Create a queue

Queues are created dynamically at runtime — no console, no provisioning. The
upsert is idempotent and cheap; calling it on every enqueue is a supported
pattern.

```bash
curl -X PUT localhost:8080/v1/queues/emails -d '{
  "rate_limits": {"max_dispatches_per_second": 5},
  "retry": {"max_attempts": 5, "min_backoff_seconds": 1, "max_backoff_seconds": 60},
  "target": {"base_url": "http://localhost:9000"}
}'
```

## 3. Receive tasks

Any HTTP endpoint works. Minimal receiver:

```bash
# terminal 2
python3 -m http.server 9000   # 501s POSTs — use the examples/ receivers for a real one
```

See [examples/fastapi-receiver](../examples/fastapi-receiver) and
[examples/express-receiver](../examples/express-receiver) for receivers that
verify signatures.

## 4. Enqueue

```bash
# now
curl -X POST localhost:8080/v1/queues/emails/tasks -d '{
  "path": "/send", "json_body": {"to": "a@example.com"}}'

# in 2 hours, deduped, with a per-task retry override
curl -X POST localhost:8080/v1/queues/emails/tasks -d '{
  "path": "/send",
  "json_body": {"to": "b@example.com"},
  "delay_seconds": 7200,
  "task_id": "welcome-user-42",
  "retry": {"max_attempts": 1}
}'
```

A duplicate `task_id` returns `409 already_exists` — that's your dedup.

## 5. Watch it

```bash
curl localhost:8080/v1/queues/emails/stats
curl "localhost:8080/v1/queues/emails/tasks?state=succeeded"
curl localhost:8080/v1/queues/emails/tasks/welcome-user-42/attempts
```

…or open the dashboard: http://localhost:8080/

## 6. Operate

```bash
curl -X POST localhost:8080/v1/queues/emails:pause
curl -X POST localhost:8080/v1/queues/emails:resume
curl -X POST localhost:8080/v1/queues/emails:purge                # exact, instant
curl -X POST localhost:8080/v1/queues/emails/tasks/welcome-user-42:run   # force now
```

Next: [SDKs](../README.md#sdks) ·
[Cloud Tasks migration](migrate-from-cloud-tasks.md) ·
[deployment](deploy.md) · API reference: `/docs` on your server
