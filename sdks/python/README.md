# pushq (Python)

Client for [pushq](https://github.com/blissfulrays/pushq) — a self-hosted HTTP push
task queue (open-source alternative to GCP Cloud Tasks). Tasks are delivered
as HTTP requests to your endpoints with per-queue rate limits, concurrency
caps, and Cloud Tasks-compatible retry semantics.

```bash
pip install pushq
```

## Enqueue

```python
from pushq import Pushq, RateLimits, Retry, Target

pq = Pushq(api_key="pq_...")  # hosted API at https://api.pushqueue.dev
# Self-hosting? Pushq("http://localhost:8080", api_key="pq_...")

# Queues are created/updated at runtime — idempotent, safe to call on every enqueue.
pq.upsert_queue(
    "account-42",
    rate_limits=RateLimits(max_dispatches_per_second=10, max_concurrent_dispatches=5),
    target=Target(base_url="https://api.example.com",
                  signing_secret="whsec_..."),      # omit to keep the stored secret
)

pq.create_task(
    "account-42",
    path="/hooks/send-message",        # resolved against the queue's base_url
    json_body={"user_id": 1},
    delay_seconds=3600,                # or schedule_time=datetime(...)
    task_id="follow_up_abc",           # optional: dedup (409 on collision)
    retry=Retry(max_attempts=3),       # per-task override of the queue config
)

# Operate queues at runtime
pq.pause_queue("account-42")
pq.resume_queue("account-42")
pq.purge_queue("account-42")
stats = pq.queue_stats("account-42", id_prefix="follow_up_")
print(stats.pending, stats.errors_last_1h)
```

`AsyncPushq` mirrors the same surface with `async`/`await`.

## Verify deliveries (FastAPI)

pushq signs deliveries using the [Standard Webhooks](https://www.standardwebhooks.com)
scheme (`webhook-id`, `webhook-timestamp`, `webhook-signature` headers).

```python
from fastapi import FastAPI, HTTPException, Request
from pushq import verify_signature, WebhookVerificationError

app = FastAPI()

@app.post("/hooks/send-message")
async def send_message(request: Request):
    body = await request.body()
    try:
        # Returns "queue/task_id" — stable across retries, use it as an idempotency key.
        msg_id = verify_signature("whsec_...", request.headers, body)
    except WebhookVerificationError:
        raise HTTPException(status_code=401)
    ...
```

Errors raise `PushqError` with `.status`, `.code`
(`already_exists`, `not_found`, `failed_precondition`, …), and `.detail`.
