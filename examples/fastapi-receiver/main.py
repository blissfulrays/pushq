"""A pushq task receiver in FastAPI with signature verification.

Run:
    pip install fastapi uvicorn pushq
    uvicorn main:app --port 9000

Then point a queue at it:
    curl -X PUT localhost:8080/v1/queues/demo -d '{
      "target": {"base_url": "http://localhost:9000",
                 "signing_secret": "whsec_dGVzdC1zZWNyZXQ="}}'
    curl -X POST localhost:8080/v1/queues/demo/tasks -d '{
      "path": "/tasks/send-email", "json_body": {"to": "a@example.com"}}'
"""
import os

from fastapi import FastAPI, HTTPException, Request
from pushq import WebhookVerificationError, verify_signature

SECRET = os.environ.get("PUSHQ_SIGNING_SECRET", "whsec_dGVzdC1zZWNyZXQ=")

app = FastAPI()
seen: set[str] = set()  # use a persistent store in production


@app.post("/tasks/send-email")
async def send_email(request: Request):
    body = await request.body()
    try:
        # msg_id is "queue/task_id" — stable across retries.
        msg_id = verify_signature(SECRET, request.headers, body)
    except WebhookVerificationError as exc:
        raise HTTPException(status_code=401, detail=str(exc))

    # pushq delivers at-least-once: dedupe on the stable id.
    if msg_id in seen:
        return {"status": "duplicate ignored"}
    seen.add(msg_id)

    payload = await request.json()
    attempt = request.headers.get("x-pushq-attempt")
    print(f"sending email to {payload['to']} (attempt {attempt}, id {msg_id})")

    # Any 2xx marks the task succeeded; a non-2xx (or raising) retries it on
    # the queue's backoff schedule.
    return {"status": "sent"}
