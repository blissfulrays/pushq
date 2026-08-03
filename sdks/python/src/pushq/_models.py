"""Dataclass models mirroring the pushq API (snake_case wire format)."""
from __future__ import annotations

import re
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, Dict, List, Optional

_ISO_RE = re.compile(r"^(.*?)(?:\.(\d+))?([+-]\d{2}:\d{2})$")


def _dt(value: Optional[str]) -> Optional[datetime]:
    if not value:
        return None
    v = value.replace("Z", "+00:00")
    # Python < 3.11 only accepts 3- or 6-digit fractions; RFC 3339 allows any.
    m = _ISO_RE.match(v)
    if m:
        frac = ((m.group(2) or "") + "000000")[:6]
        v = f"{m.group(1)}.{frac}{m.group(3)}"
    return datetime.fromisoformat(v)


@dataclass
class RateLimits:
    max_dispatches_per_second: Optional[float] = None
    max_burst_size: Optional[int] = None
    max_concurrent_dispatches: Optional[int] = None

    def to_wire(self) -> Dict[str, Any]:
        return _prune(
            max_dispatches_per_second=self.max_dispatches_per_second,
            max_burst_size=self.max_burst_size,
            max_concurrent_dispatches=self.max_concurrent_dispatches,
        )


@dataclass
class Retry:
    """GCP Cloud Tasks retry semantics. On a task, any subset overrides the queue config."""

    max_attempts: Optional[int] = None
    max_retry_duration_seconds: Optional[float] = None
    min_backoff_seconds: Optional[float] = None
    max_backoff_seconds: Optional[float] = None
    max_doublings: Optional[int] = None

    def to_wire(self) -> Dict[str, Any]:
        return _prune(
            max_attempts=self.max_attempts,
            max_retry_duration_seconds=self.max_retry_duration_seconds,
            min_backoff_seconds=self.min_backoff_seconds,
            max_backoff_seconds=self.max_backoff_seconds,
            max_doublings=self.max_doublings,
        )

    @classmethod
    def from_wire(cls, w: Optional[Dict[str, Any]]) -> Optional["Retry"]:
        if not w:
            return None
        return cls(
            max_attempts=w.get("max_attempts"),
            max_retry_duration_seconds=w.get("max_retry_duration_seconds"),
            min_backoff_seconds=w.get("min_backoff_seconds"),
            max_backoff_seconds=w.get("max_backoff_seconds"),
            max_doublings=w.get("max_doublings"),
        )


@dataclass
class Target:
    base_url: Optional[str] = None
    method: Optional[str] = None
    headers: Optional[Dict[str, str]] = None
    signing_secret: Optional[str] = None
    """On upsert: None keeps the stored secret, "" clears it."""

    def to_wire(self) -> Dict[str, Any]:
        return _prune(
            base_url=self.base_url,
            method=self.method,
            headers=self.headers,
            signing_secret=self.signing_secret,
        )


@dataclass
class Queue:
    name: str
    state: str
    rate_limits: RateLimits
    retry: Retry
    target_base_url: Optional[str]
    target_method: Optional[str]
    target_headers: Optional[Dict[str, str]]
    has_signing_secret: bool
    dead_letter_queue: Optional[str]
    purged_at: Optional[datetime]
    created_at: Optional[datetime]
    updated_at: Optional[datetime]

    @classmethod
    def from_wire(cls, w: Dict[str, Any]) -> "Queue":
        rl = w.get("rate_limits") or {}
        tg = w.get("target") or {}
        return cls(
            name=w["name"],
            state=w["state"],
            rate_limits=RateLimits(
                max_dispatches_per_second=rl.get("max_dispatches_per_second"),
                max_burst_size=rl.get("max_burst_size"),
                max_concurrent_dispatches=rl.get("max_concurrent_dispatches"),
            ),
            retry=Retry.from_wire(w.get("retry")) or Retry(),
            target_base_url=tg.get("base_url"),
            target_method=tg.get("method"),
            target_headers=tg.get("headers"),
            has_signing_secret=bool(tg.get("has_signing_secret")),
            dead_letter_queue=w.get("dead_letter_queue"),
            purged_at=_dt(w.get("purged_at")),
            created_at=_dt(w.get("created_at")),
            updated_at=_dt(w.get("updated_at")),
        )


@dataclass
class QueueStats:
    state: str
    pending: int
    running: int
    succeeded: int
    failed: int
    scheduled_future: int
    oldest_due_at: Optional[datetime]
    oldest_due_age_seconds: Optional[float]
    dispatched_last_1m: int
    errors_last_1m: int
    dispatched_last_1h: int
    errors_last_1h: int
    last_purge: Optional[datetime]

    @classmethod
    def from_wire(cls, w: Dict[str, Any]) -> "QueueStats":
        return cls(
            state=w.get("state", ""),
            pending=w.get("pending", 0),
            running=w.get("running", 0),
            succeeded=w.get("succeeded", 0),
            failed=w.get("failed", 0),
            scheduled_future=w.get("scheduled_future", 0),
            oldest_due_at=_dt(w.get("oldest_due_at")),
            oldest_due_age_seconds=w.get("oldest_due_age_seconds"),
            dispatched_last_1m=w.get("dispatched_last_1m", 0),
            errors_last_1m=w.get("errors_last_1m", 0),
            dispatched_last_1h=w.get("dispatched_last_1h", 0),
            errors_last_1h=w.get("errors_last_1h", 0),
            last_purge=_dt(w.get("last_purge")),
        )


@dataclass
class LastAttempt:
    at: Optional[datetime]
    status: Optional[int]
    error: Optional[str]


@dataclass
class Task:
    id: str
    queue: str
    state: str
    url: str
    method: str
    headers: Optional[Dict[str, str]]
    body: Optional[bytes]
    schedule_time: Optional[datetime]
    created_at: Optional[datetime]
    dispatch_deadline_seconds: float
    retry: Optional[Retry]
    dispatch_count: int
    response_count: int
    first_attempt_at: Optional[datetime]
    finished_at: Optional[datetime]
    last_attempt: Optional[LastAttempt]

    @classmethod
    def from_wire(cls, w: Dict[str, Any]) -> "Task":
        import base64

        la = w.get("last_attempt")
        return cls(
            id=w["id"],
            queue=w["queue"],
            state=w["state"],
            url=w.get("url", ""),
            method=w.get("method", ""),
            headers=w.get("headers"),
            body=base64.b64decode(w["body_base64"]) if w.get("body_base64") else None,
            schedule_time=_dt(w.get("schedule_time")),
            created_at=_dt(w.get("created_at")),
            dispatch_deadline_seconds=w.get("dispatch_deadline_seconds", 0),
            retry=Retry.from_wire(w.get("retry")),
            dispatch_count=w.get("dispatch_count", 0),
            response_count=w.get("response_count", 0),
            first_attempt_at=_dt(w.get("first_attempt_at")),
            finished_at=_dt(w.get("finished_at")),
            last_attempt=LastAttempt(
                at=_dt(la.get("at")), status=la.get("status"), error=la.get("error")
            )
            if la
            else None,
        )


@dataclass
class Attempt:
    number: int
    dispatch_time: Optional[datetime]
    response_time: Optional[datetime]
    response_status: Optional[int]
    error_kind: Optional[str]
    error_snippet: Optional[str]
    duration_ms: int

    @classmethod
    def from_wire(cls, w: Dict[str, Any]) -> "Attempt":
        return cls(
            number=w.get("number", 0),
            dispatch_time=_dt(w.get("dispatch_time")),
            response_time=_dt(w.get("response_time")),
            response_status=w.get("response_status"),
            error_kind=w.get("error_kind"),
            error_snippet=w.get("error_snippet"),
            duration_ms=w.get("duration_ms", 0),
        )


@dataclass
class BatchError:
    code: str
    detail: str


@dataclass
class BatchResult:
    index: int
    task: Optional[Task]
    error: Optional[BatchError]

    @classmethod
    def from_wire(cls, w: Dict[str, Any]) -> "BatchResult":
        err = w.get("error")
        return cls(
            index=w.get("index", 0),
            task=Task.from_wire(w["task"]) if w.get("task") else None,
            error=BatchError(code=err["code"], detail=err.get("detail", "")) if err else None,
        )


@dataclass
class Page:
    items: List[Any] = field(default_factory=list)
    next_page_token: Optional[str] = None


def _prune(**kwargs: Any) -> Dict[str, Any]:
    return {k: v for k, v in kwargs.items() if v is not None}
