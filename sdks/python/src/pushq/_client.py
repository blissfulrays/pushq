"""Sync and async pushq clients on httpx."""
from __future__ import annotations

import base64
from datetime import datetime, timezone
from typing import Any, Dict, Iterable, List, Optional, Union

import httpx

from ._models import (
    Attempt,
    BatchResult,
    Page,
    Queue,
    QueueStats,
    RateLimits,
    Retry,
    Target,
    Task,
    _prune,
)


class PushqError(Exception):
    """Raised for any non-2xx API response (RFC 9457 problem+json)."""

    def __init__(self, status: int, code: str, detail: str):
        super().__init__(f"pushq: {code} ({status}): {detail}")
        self.status = status
        self.code = code
        self.detail = detail


def _raise_for(resp: httpx.Response) -> None:
    if resp.is_success:
        return
    code, detail = "internal", resp.text[:200]
    try:
        body = resp.json()
        code = body.get("code", code)
        detail = body.get("detail") or body.get("title") or detail
    except Exception:
        pass
    raise PushqError(resp.status_code, code, detail)


def _queue_config_wire(
    rate_limits: Optional[RateLimits],
    retry: Optional[Retry],
    target: Optional[Target],
    dead_letter_queue: Optional[str],
    state: Optional[str] = None,
) -> Dict[str, Any]:
    return _prune(
        state=state,
        rate_limits=rate_limits.to_wire() if rate_limits else None,
        retry=retry.to_wire() if retry else None,
        target=target.to_wire() if target else None,
        dead_letter_queue=dead_letter_queue,
    )


def _task_create_wire(
    *,
    task_id: Optional[str],
    url: Optional[str],
    path: Optional[str],
    method: Optional[str],
    headers: Optional[Dict[str, str]],
    body: Optional[bytes],
    json_body: Any,
    schedule_time: Optional[Union[datetime, str]],
    delay_seconds: Optional[float],
    dispatch_deadline_seconds: Optional[float],
    retry: Optional[Retry],
) -> Dict[str, Any]:
    if body is not None and json_body is not None:
        raise ValueError("pushq: provide body or json_body, not both")
    schedule: Optional[str] = None
    if isinstance(schedule_time, datetime):
        dt = schedule_time
        if dt.tzinfo is None:
            dt = dt.replace(tzinfo=timezone.utc)
        schedule = dt.isoformat()
    elif isinstance(schedule_time, str):
        schedule = schedule_time
    return _prune(
        task_id=task_id,
        url=url,
        path=path,
        method=method,
        headers=headers,
        body_base64=base64.b64encode(body).decode() if body is not None else None,
        json_body=json_body,
        schedule_time=schedule,
        delay_seconds=delay_seconds,
        dispatch_deadline_seconds=dispatch_deadline_seconds,
        retry=retry.to_wire() if retry else None,
    )


class Pushq:
    """Synchronous client.

    >>> pq = Pushq("http://localhost:8080", api_key="pq_...")
    >>> pq.upsert_queue("account-42", rate_limits=RateLimits(max_dispatches_per_second=10))
    >>> pq.create_task("account-42", path="/hooks/send", json_body={"user": 1},
    ...                delay_seconds=3600, task_id="follow_up_abc",
    ...                retry=Retry(max_attempts=3))
    """

    def __init__(
        self,
        base_url: str,
        api_key: Optional[str] = None,
        *,
        timeout: float = 30.0,
        client: Optional[httpx.Client] = None,
    ):
        headers = {"Authorization": f"Bearer {api_key}"} if api_key else {}
        self._http = client or httpx.Client(
            base_url=base_url.rstrip("/"), headers=headers, timeout=timeout
        )

    def close(self) -> None:
        self._http.close()

    def __enter__(self) -> "Pushq":
        return self

    def __exit__(self, *exc: Any) -> None:
        self.close()

    def _req(self, method: str, path: str, json: Any = None, params: Any = None) -> Any:
        resp = self._http.request(method, path, json=json, params=params)
        _raise_for(resp)
        if resp.status_code == 204 or not resp.content:
            return None
        return resp.json()

    # --- queues ---

    def upsert_queue(
        self,
        name: str,
        *,
        rate_limits: Optional[RateLimits] = None,
        retry: Optional[Retry] = None,
        target: Optional[Target] = None,
        dead_letter_queue: Optional[str] = None,
    ) -> Queue:
        """Create or replace queue config. Never changes run state; a signing
        secret is kept unless explicitly set (use "" to clear)."""
        wire = _queue_config_wire(rate_limits, retry, target, dead_letter_queue)
        return Queue.from_wire(self._req("PUT", f"/v1/queues/{name}", json=wire))

    def create_queue(self, name: str, **kwargs: Any) -> Queue:
        wire = _queue_config_wire(
            kwargs.get("rate_limits"), kwargs.get("retry"), kwargs.get("target"),
            kwargs.get("dead_letter_queue"),
        )
        wire["name"] = name
        return Queue.from_wire(self._req("POST", "/v1/queues", json=wire))

    def get_queue(self, name: str) -> Queue:
        return Queue.from_wire(self._req("GET", f"/v1/queues/{name}"))

    def patch_queue(
        self,
        name: str,
        *,
        state: Optional[str] = None,
        rate_limits: Optional[RateLimits] = None,
        retry: Optional[Retry] = None,
        target: Optional[Target] = None,
        dead_letter_queue: Optional[str] = None,
    ) -> Queue:
        wire = _queue_config_wire(rate_limits, retry, target, dead_letter_queue, state)
        return Queue.from_wire(self._req("PATCH", f"/v1/queues/{name}", json=wire))

    def list_queues(
        self,
        *,
        name_prefix: Optional[str] = None,
        page_size: Optional[int] = None,
        page_token: Optional[str] = None,
    ) -> Page:
        w = self._req("GET", "/v1/queues", params=_prune(
            name_prefix=name_prefix, page_size=page_size, page_token=page_token))
        return Page(
            items=[Queue.from_wire(q) for q in w.get("queues", [])],
            next_page_token=w.get("next_page_token"),
        )

    def delete_queue(self, name: str) -> None:
        self._req("DELETE", f"/v1/queues/{name}")

    def pause_queue(self, name: str) -> Queue:
        return Queue.from_wire(self._req("POST", f"/v1/queues/{name}:pause"))

    def resume_queue(self, name: str) -> Queue:
        return Queue.from_wire(self._req("POST", f"/v1/queues/{name}:resume"))

    def purge_queue(self, name: str) -> Queue:
        """Delete every task created up to now (exact watermark)."""
        return Queue.from_wire(self._req("POST", f"/v1/queues/{name}:purge"))

    def queue_stats(self, name: str, *, id_prefix: Optional[str] = None) -> QueueStats:
        w = self._req("GET", f"/v1/queues/{name}/stats", params=_prune(id_prefix=id_prefix))
        return QueueStats.from_wire(w)

    # --- tasks ---

    def create_task(
        self,
        queue: str,
        *,
        url: Optional[str] = None,
        path: Optional[str] = None,
        method: Optional[str] = None,
        headers: Optional[Dict[str, str]] = None,
        body: Optional[bytes] = None,
        json_body: Any = None,
        schedule_time: Optional[Union[datetime, str]] = None,
        delay_seconds: Optional[float] = None,
        dispatch_deadline_seconds: Optional[float] = None,
        task_id: Optional[str] = None,
        retry: Optional[Retry] = None,
    ) -> Task:
        wire = _task_create_wire(
            task_id=task_id, url=url, path=path, method=method, headers=headers,
            body=body, json_body=json_body, schedule_time=schedule_time,
            delay_seconds=delay_seconds, dispatch_deadline_seconds=dispatch_deadline_seconds,
            retry=retry,
        )
        return Task.from_wire(self._req("POST", f"/v1/queues/{queue}/tasks", json=wire))

    def create_tasks(self, queue: str, tasks: Iterable[Dict[str, Any]]) -> List[BatchResult]:
        """Batch enqueue (≤500). Each item is the kwargs of create_task."""
        wire = [_task_create_wire(
            task_id=t.get("task_id"), url=t.get("url"), path=t.get("path"),
            method=t.get("method"), headers=t.get("headers"), body=t.get("body"),
            json_body=t.get("json_body"), schedule_time=t.get("schedule_time"),
            delay_seconds=t.get("delay_seconds"),
            dispatch_deadline_seconds=t.get("dispatch_deadline_seconds"),
            retry=t.get("retry"),
        ) for t in tasks]
        w = self._req("POST", f"/v1/queues/{queue}/tasks:batch", json={"tasks": wire})
        return [BatchResult.from_wire(r) for r in w.get("results", [])]

    def get_task(self, queue: str, task_id: str, *, view: str = "basic") -> Task:
        w = self._req("GET", f"/v1/queues/{queue}/tasks/{task_id}", params={"view": view})
        return Task.from_wire(w)

    def list_tasks(
        self,
        queue: str,
        *,
        state: Optional[str] = None,
        id_prefix: Optional[str] = None,
        view: Optional[str] = None,
        page_size: Optional[int] = None,
        page_token: Optional[str] = None,
    ) -> Page:
        w = self._req("GET", f"/v1/queues/{queue}/tasks", params=_prune(
            state=state, id_prefix=id_prefix, view=view,
            page_size=page_size, page_token=page_token))
        return Page(
            items=[Task.from_wire(t) for t in w.get("tasks", [])],
            next_page_token=w.get("next_page_token"),
        )

    def delete_task(self, queue: str, task_id: str) -> None:
        self._req("DELETE", f"/v1/queues/{queue}/tasks/{task_id}")

    def run_task(self, queue: str, task_id: str) -> Task:
        """Force immediate dispatch (bypasses rate limits and PAUSED)."""
        return Task.from_wire(self._req("POST", f"/v1/queues/{queue}/tasks/{task_id}:run"))

    def list_attempts(self, queue: str, task_id: str) -> List[Attempt]:
        w = self._req("GET", f"/v1/queues/{queue}/tasks/{task_id}/attempts")
        return [Attempt.from_wire(a) for a in w.get("attempts", [])]


class AsyncPushq:
    """Async twin of Pushq with the same method surface."""

    def __init__(
        self,
        base_url: str,
        api_key: Optional[str] = None,
        *,
        timeout: float = 30.0,
        client: Optional[httpx.AsyncClient] = None,
    ):
        headers = {"Authorization": f"Bearer {api_key}"} if api_key else {}
        self._http = client or httpx.AsyncClient(
            base_url=base_url.rstrip("/"), headers=headers, timeout=timeout
        )

    async def aclose(self) -> None:
        await self._http.aclose()

    async def __aenter__(self) -> "AsyncPushq":
        return self

    async def __aexit__(self, *exc: Any) -> None:
        await self.aclose()

    async def _req(self, method: str, path: str, json: Any = None, params: Any = None) -> Any:
        resp = await self._http.request(method, path, json=json, params=params)
        _raise_for(resp)
        if resp.status_code == 204 or not resp.content:
            return None
        return resp.json()

    async def upsert_queue(
        self,
        name: str,
        *,
        rate_limits: Optional[RateLimits] = None,
        retry: Optional[Retry] = None,
        target: Optional[Target] = None,
        dead_letter_queue: Optional[str] = None,
    ) -> Queue:
        wire = _queue_config_wire(rate_limits, retry, target, dead_letter_queue)
        return Queue.from_wire(await self._req("PUT", f"/v1/queues/{name}", json=wire))

    async def get_queue(self, name: str) -> Queue:
        return Queue.from_wire(await self._req("GET", f"/v1/queues/{name}"))

    async def pause_queue(self, name: str) -> Queue:
        return Queue.from_wire(await self._req("POST", f"/v1/queues/{name}:pause"))

    async def resume_queue(self, name: str) -> Queue:
        return Queue.from_wire(await self._req("POST", f"/v1/queues/{name}:resume"))

    async def purge_queue(self, name: str) -> Queue:
        return Queue.from_wire(await self._req("POST", f"/v1/queues/{name}:purge"))

    async def delete_queue(self, name: str) -> None:
        await self._req("DELETE", f"/v1/queues/{name}")

    async def queue_stats(self, name: str, *, id_prefix: Optional[str] = None) -> QueueStats:
        w = await self._req("GET", f"/v1/queues/{name}/stats", params=_prune(id_prefix=id_prefix))
        return QueueStats.from_wire(w)

    async def create_task(self, queue: str, **kwargs: Any) -> Task:
        wire = _task_create_wire(
            task_id=kwargs.get("task_id"), url=kwargs.get("url"), path=kwargs.get("path"),
            method=kwargs.get("method"), headers=kwargs.get("headers"), body=kwargs.get("body"),
            json_body=kwargs.get("json_body"), schedule_time=kwargs.get("schedule_time"),
            delay_seconds=kwargs.get("delay_seconds"),
            dispatch_deadline_seconds=kwargs.get("dispatch_deadline_seconds"),
            retry=kwargs.get("retry"),
        )
        return Task.from_wire(await self._req("POST", f"/v1/queues/{queue}/tasks", json=wire))

    async def create_tasks(self, queue: str, tasks: Iterable[Dict[str, Any]]) -> List[BatchResult]:
        wire = [_task_create_wire(
            task_id=t.get("task_id"), url=t.get("url"), path=t.get("path"),
            method=t.get("method"), headers=t.get("headers"), body=t.get("body"),
            json_body=t.get("json_body"), schedule_time=t.get("schedule_time"),
            delay_seconds=t.get("delay_seconds"),
            dispatch_deadline_seconds=t.get("dispatch_deadline_seconds"),
            retry=t.get("retry"),
        ) for t in tasks]
        w = await self._req("POST", f"/v1/queues/{queue}/tasks:batch", json={"tasks": wire})
        return [BatchResult.from_wire(r) for r in w.get("results", [])]

    async def get_task(self, queue: str, task_id: str, *, view: str = "basic") -> Task:
        w = await self._req("GET", f"/v1/queues/{queue}/tasks/{task_id}", params={"view": view})
        return Task.from_wire(w)

    async def list_tasks(self, queue: str, **kwargs: Any) -> Page:
        w = await self._req("GET", f"/v1/queues/{queue}/tasks", params=_prune(
            state=kwargs.get("state"), id_prefix=kwargs.get("id_prefix"),
            view=kwargs.get("view"), page_size=kwargs.get("page_size"),
            page_token=kwargs.get("page_token")))
        return Page(
            items=[Task.from_wire(t) for t in w.get("tasks", [])],
            next_page_token=w.get("next_page_token"),
        )

    async def delete_task(self, queue: str, task_id: str) -> None:
        await self._req("DELETE", f"/v1/queues/{queue}/tasks/{task_id}")

    async def run_task(self, queue: str, task_id: str) -> Task:
        return Task.from_wire(await self._req("POST", f"/v1/queues/{queue}/tasks/{task_id}:run"))

    async def list_attempts(self, queue: str, task_id: str) -> List[Attempt]:
        w = await self._req("GET", f"/v1/queues/{queue}/tasks/{task_id}/attempts")
        return [Attempt.from_wire(a) for a in w.get("attempts", [])]


__all__ = ["Pushq", "AsyncPushq", "PushqError"]
