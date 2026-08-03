"""Integration tests against a real pushq server (gated on PUSHQ_BIN)."""
import http.server
import json
import os
import subprocess
import threading
import time

import httpx
import pytest

from pushq import AsyncPushq, Pushq, PushqError, RateLimits, Retry, Target, verify_signature

BIN = os.environ.get("PUSHQ_BIN")
PORT = 18345

pytestmark = pytest.mark.skipif(not BIN, reason="PUSHQ_BIN not set")

received = []


class _Receiver(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length)
        received.append({"headers": dict(self.headers.items()), "body": body, "path": self.path})
        self.send_response(200)
        self.end_headers()

    def log_message(self, *args):
        pass


@pytest.fixture(scope="module")
def server():
    proc = subprocess.Popen(
        [BIN, "dev", "--listen", f":{PORT}", "--memory"],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    receiver = http.server.ThreadingHTTPServer(("127.0.0.1", 0), _Receiver)
    thread = threading.Thread(target=receiver.serve_forever, daemon=True)
    thread.start()

    deadline = time.time() + 10
    while time.time() < deadline:
        try:
            if httpx.get(f"http://127.0.0.1:{PORT}/healthz").status_code == 200:
                break
        except Exception:
            time.sleep(0.1)
    else:
        proc.kill()
        raise RuntimeError("pushq did not start")

    yield {"receiver_url": f"http://127.0.0.1:{receiver.server_address[1]}"}
    proc.terminate()
    receiver.shutdown()


def wait_for(cond, timeout=10.0, what="condition"):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if cond():
            return
        time.sleep(0.05)
    raise TimeoutError(f"timed out waiting for {what}")


def test_lifecycle_with_signed_delivery(server):
    secret = "whsec_dGVzdC1zZWNyZXQ="
    pq = Pushq(f"http://127.0.0.1:{PORT}")
    q = pq.upsert_queue(
        "py-e2e",
        rate_limits=RateLimits(max_dispatches_per_second=50),
        retry=Retry(max_attempts=3, min_backoff_seconds=0.05),
        target=Target(base_url=server["receiver_url"], signing_secret=secret),
    )
    assert q.name == "py-e2e" and q.has_signing_secret

    task = pq.create_task("py-e2e", path="/hook", json_body={"n": 42}, task_id="py-task-1")
    assert task.state == "pending"

    wait_for(lambda: len(received) >= 1, what="delivery")
    r = received[0]
    assert verify_signature(secret, r["headers"], r["body"]) == "py-e2e/py-task-1"
    assert json.loads(r["body"]) == {"n": 42}
    assert r["headers"].get("X-Pushq-Queue") == "py-e2e"

    wait_for(lambda: pq.get_task("py-e2e", "py-task-1").state == "succeeded", what="success")
    attempts = pq.list_attempts("py-e2e", "py-task-1")
    assert len(attempts) == 1 and attempts[0].response_status == 200
    assert pq.queue_stats("py-e2e").succeeded == 1


def test_dedup_and_batch(server):
    pq = Pushq(f"http://127.0.0.1:{PORT}")
    pq.upsert_queue("py-dedup", target=Target(base_url=server["receiver_url"]))
    pq.create_task("py-dedup", path="/x", task_id="same", delay_seconds=60)

    with pytest.raises(PushqError) as exc:
        pq.create_task("py-dedup", path="/x", task_id="same")
    assert exc.value.code == "already_exists" and exc.value.status == 409

    results = pq.create_tasks("py-dedup", [
        {"path": "/b", "task_id": "b1", "delay_seconds": 60},
        {"path": "/b", "task_id": "same", "delay_seconds": 60},
    ])
    assert results[0].task is not None and results[0].task.id == "b1"
    assert results[1].error is not None and results[1].error.code == "already_exists"


def test_pause_run_purge(server):
    pq = Pushq(f"http://127.0.0.1:{PORT}")
    pq.upsert_queue("py-ops", target=Target(base_url=server["receiver_url"]))
    pq.pause_queue("py-ops")
    pq.create_task("py-ops", path="/forced", task_id="f1", delay_seconds=3600)

    before = sum(1 for r in received if r["headers"].get("X-Pushq-Queue") == "py-ops")
    pq.run_task("py-ops", "f1")
    wait_for(
        lambda: sum(1 for r in received if r["headers"].get("X-Pushq-Queue") == "py-ops") > before,
        what="forced delivery",
    )

    pq.create_task("py-ops", path="/doomed", delay_seconds=3600)
    pq.purge_queue("py-ops")
    stats = pq.queue_stats("py-ops")
    assert stats.pending + stats.scheduled_future == 0


async def test_async_client(server):
    async with AsyncPushq(f"http://127.0.0.1:{PORT}") as pq:
        await pq.upsert_queue("py-async", target=Target(base_url=server["receiver_url"]))
        task = await pq.create_task("py-async", path="/a", json_body={"async": True})
        assert task.queue == "py-async"
        page = await pq.list_tasks("py-async")
        assert len(page.items) >= 1
