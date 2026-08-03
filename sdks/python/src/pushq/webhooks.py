"""Receiver-side verification of pushq deliveries (Standard Webhooks scheme).

pushq signs deliveries with HMAC-SHA256 over ``"{id}.{timestamp}.{payload}"``
and sends ``webhook-id``, ``webhook-timestamp``, and ``webhook-signature``
headers, compatible with https://www.standardwebhooks.com.
"""
from __future__ import annotations

import base64
import hashlib
import hmac
import time
from typing import Mapping, Optional, Union


class WebhookVerificationError(Exception):
    pass


def _signing_key(secret: str) -> bytes:
    if secret.startswith("whsec_"):
        return base64.b64decode(secret[len("whsec_"):])
    return secret.encode()


def sign(secret: str, msg_id: str, timestamp: int, payload: Union[bytes, str]) -> str:
    """Compute a ``v1,<base64>`` signature (tooling/tests)."""
    body = payload.encode() if isinstance(payload, str) else payload
    message = f"{msg_id}.{timestamp}.".encode() + body
    mac = hmac.new(_signing_key(secret), message, hashlib.sha256).digest()
    return "v1," + base64.b64encode(mac).decode()


def verify_signature(
    secret: str,
    headers: Mapping[str, str],
    payload: Union[bytes, str],
    *,
    tolerance_seconds: float = 300,
    now: Optional[float] = None,
) -> str:
    """Verify a pushq delivery.

    ``headers`` is any case-insensitive-ish mapping of the incoming request
    headers (FastAPI/Starlette ``request.headers`` works directly); ``payload``
    is the raw request body exactly as received.

    Returns the webhook id ("queue/task_id" — stable across retries, usable as
    an idempotency key) or raises :class:`WebhookVerificationError`.

    FastAPI example::

        @app.post("/hooks/task")
        async def handle(request: Request):
            body = await request.body()
            verify_signature(SECRET, request.headers, body)
            ...
    """
    lower = {k.lower(): v for k, v in headers.items()}
    msg_id = lower.get("webhook-id")
    ts_raw = lower.get("webhook-timestamp")
    sig_header = lower.get("webhook-signature")
    if not msg_id or not ts_raw or not sig_header:
        raise WebhookVerificationError("missing webhook-id/-timestamp/-signature headers")

    try:
        ts = int(ts_raw)
    except ValueError as exc:
        raise WebhookVerificationError("invalid timestamp") from exc

    if tolerance_seconds > 0:
        current = time.time() if now is None else now
        if abs(current - ts) > tolerance_seconds:
            raise WebhookVerificationError("timestamp outside tolerance")

    expected = sign(secret, msg_id, ts, payload)
    _, _, expected_sig = expected.partition(",")
    for part in sig_header.split():
        version, _, sig = part.partition(",")
        if version != "v1" or not sig:
            continue
        if hmac.compare_digest(sig, expected_sig):
            return msg_id
    raise WebhookVerificationError("no matching signature")
