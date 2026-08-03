// Integration tests against a real pushq server. Gated on PUSHQ_BIN pointing
// at a built binary (see repo Makefile: `make sdk-test-ts`).
import { spawn, type ChildProcess } from "node:child_process";
import { createServer, type Server } from "node:http";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { Pushq, PushqError, verifySignature } from "../src/index.js";

const BIN = process.env.PUSHQ_BIN;
const PORT = 18234;

describe.skipIf(!BIN)("pushq integration", () => {
  let proc: ChildProcess;
  let receiver: Server;
  let received: { headers: Record<string, string | string[] | undefined>; body: string }[] = [];
  let receiverURL: string;
  const client = new Pushq({ baseUrl: `http://127.0.0.1:${PORT}` });

  beforeAll(async () => {
    proc = spawn(BIN!, ["dev", "--listen", `:${PORT}`, "--memory"], { stdio: "ignore" });
    receiver = createServer((req, res) => {
      let body = "";
      req.on("data", (c) => (body += c));
      req.on("end", () => {
        received.push({ headers: req.headers, body });
        res.writeHead(200).end();
      });
    });
    await new Promise<void>((resolve) => receiver.listen(0, "127.0.0.1", resolve));
    const addr = receiver.address() as { port: number };
    receiverURL = `http://127.0.0.1:${addr.port}`;

    // Wait for readiness.
    const deadline = Date.now() + 10_000;
    for (;;) {
      try {
        const r = await fetch(`http://127.0.0.1:${PORT}/healthz`);
        if (r.ok) break;
      } catch {}
      if (Date.now() > deadline) throw new Error("pushq did not start");
      await sleep(100);
    }
  }, 20_000);

  afterAll(() => {
    proc?.kill("SIGTERM");
    receiver?.close();
  });

  it("full queue + task lifecycle with signed delivery", async () => {
    const secret = "whsec_dGVzdC1zZWNyZXQ=";
    const q = await client.upsertQueue("sdk-e2e", {
      rateLimits: { maxDispatchesPerSecond: 50 },
      retry: { maxAttempts: 3, minBackoffSeconds: 0.05 },
      target: { baseUrl: receiverURL, signingSecret: secret },
    });
    expect(q.name).toBe("sdk-e2e");
    expect(q.target.hasSigningSecret).toBe(true);

    const task = await client.createTask("sdk-e2e", {
      taskId: "sdk-task-1",
      path: "/hook",
      jsonBody: { n: 42 },
    });
    expect(task.id).toBe("sdk-task-1");
    expect(task.state).toBe("pending");

    // Delivered and verifiable.
    await waitFor(() => received.length >= 1, 10_000, "delivery");
    const r = received[0];
    const verifiedId = await verifySignature({ secret, headers: r.headers, payload: r.body });
    expect(verifiedId).toBe("sdk-e2e/sdk-task-1");
    expect(JSON.parse(r.body)).toEqual({ n: 42 });
    expect(r.headers["x-pushq-queue"]).toBe("sdk-e2e");

    await waitFor(async () => (await client.getTask("sdk-e2e", "sdk-task-1")).state === "succeeded",
      10_000, "succeeded");

    const attempts = await client.listAttempts("sdk-e2e", "sdk-task-1");
    expect(attempts).toHaveLength(1);
    expect(attempts[0].responseStatus).toBe(200);

    const stats = await client.queueStats("sdk-e2e");
    expect(stats.succeeded).toBe(1);
  });

  it("dedup collision surfaces as PushqError already_exists", async () => {
    await client.upsertQueue("sdk-dedup", { target: { baseUrl: receiverURL } });
    await client.createTask("sdk-dedup", { taskId: "same", path: "/x", delaySeconds: 60 });
    const err = await client
      .createTask("sdk-dedup", { taskId: "same", path: "/x" })
      .catch((e) => e);
    expect(err).toBeInstanceOf(PushqError);
    expect(err.code).toBe("already_exists");
    expect(err.status).toBe(409);
  });

  it("batch create returns per-item results", async () => {
    await client.upsertQueue("sdk-batch", { target: { baseUrl: receiverURL } });
    const results = await client.createTasks("sdk-batch", [
      { taskId: "b1", path: "/b", delaySeconds: 60 },
      { taskId: "b1", path: "/b", delaySeconds: 60 }, // dup of previous line
      { taskId: "b3", path: "/b", delaySeconds: 60 },
    ]);
    expect(results[0].task?.id).toBe("b1");
    expect(results[1].error?.code).toBe("already_exists");
    expect(results[2].task?.id).toBe("b3");
  });

  it("pause blocks dispatch; runTask bypasses it", async () => {
    await client.upsertQueue("sdk-pause", { target: { baseUrl: receiverURL } });
    await client.pauseQueue("sdk-pause");
    await client.createTask("sdk-pause", { taskId: "forced", path: "/forced", delaySeconds: 3600 });
    const before = received.filter((r) => r.headers["x-pushq-queue"] === "sdk-pause").length;
    await client.runTask("sdk-pause", "forced");
    await waitFor(
      () => received.filter((r) => r.headers["x-pushq-queue"] === "sdk-pause").length > before,
      10_000, "forced delivery",
    );
    const st = await client.getQueue("sdk-pause");
    expect(st.state).toBe("paused");
  });

  it("purge empties the queue", async () => {
    await client.upsertQueue("sdk-purge", { target: { baseUrl: receiverURL } });
    await client.createTask("sdk-purge", { path: "/p", delaySeconds: 3600 });
    await client.purgeQueue("sdk-purge");
    const stats = await client.queueStats("sdk-purge");
    expect(stats.pending + stats.scheduledFuture).toBe(0);
  });
});

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}

async function waitFor(cond: () => boolean | Promise<boolean>, timeoutMs: number, what: string) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await cond()) return;
    await sleep(50);
  }
  throw new Error(`timed out waiting for ${what}`);
}
