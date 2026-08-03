"""pushq — client for the self-hosted HTTP push task queue.

Quickstart::

    from pushq import Pushq, RateLimits, Retry, Target

    pq = Pushq("http://localhost:8080", api_key="pq_...")
    pq.upsert_queue(
        "account-42",
        rate_limits=RateLimits(max_dispatches_per_second=10),
        target=Target(base_url="https://api.example.com"),
    )
    pq.create_task(
        "account-42",
        path="/hooks/send-message",
        json_body={"user_id": 1},
        delay_seconds=3600,
        task_id="follow_up_abc",          # dedup
        retry=Retry(max_attempts=3),      # per-task override
    )
"""
from ._client import AsyncPushq, Pushq, PushqError
from ._models import (
    Attempt,
    BatchError,
    BatchResult,
    LastAttempt,
    Page,
    Queue,
    QueueStats,
    RateLimits,
    Retry,
    Target,
    Task,
)
from .webhooks import WebhookVerificationError, sign, verify_signature

__version__ = "0.1.0"

__all__ = [
    "Pushq",
    "AsyncPushq",
    "PushqError",
    "RateLimits",
    "Retry",
    "Target",
    "Queue",
    "QueueStats",
    "Task",
    "Attempt",
    "BatchResult",
    "BatchError",
    "LastAttempt",
    "Page",
    "verify_signature",
    "sign",
    "WebhookVerificationError",
]
