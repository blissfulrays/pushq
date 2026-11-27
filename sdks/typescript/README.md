# pushq (TypeScript / JavaScript)

Client for [pushq](https://github.com/blissfulrays/pushq) — a self-hosted HTTP push
task queue (open-source alternative to GCP Cloud Tasks). Zero dependencies,
`fetch`-based: works in Node 18+, Bun, Deno, and edge runtimes.

```bash
npm install pushq
```

## Enqueue

```ts
import { Pushq } from "pushq";

const pq = new Pushq({ apiKey: "pq_..." }); // hosted API at https://api.pushqueue.dev
// Self-hosting? new Pushq({ baseUrl: "http://localhost:8080", apiKey: "pq_..." })

// Queues are created/updated at runtime — idempotent, safe to call on every enqueue.
await pq.upsertQueue("account-42", {
  rateLimits: { maxDispatchesPerSecond: 10, maxConcurrentDispatches: 5 },
  target: { baseUrl: "https://api.example.com", signingSecret: "whsec_..." },
});

await pq.createTask("account-42", {
  path: "/hooks/send-message",     // resolved against the queue's baseUrl
  jsonBody: { userId: 1 },
  delaySeconds: 3600,              // or scheduleTime: new Date(...)
  taskId: "follow_up_abc",         // optional: dedup (409 on collision)
  retry: { maxAttempts: 3 },       // per-task override of the queue config
});

// Operate queues at runtime
await pq.pauseQueue("account-42");
await pq.resumeQueue("account-42");
await pq.purgeQueue("account-42");
const stats = await pq.queueStats("account-42", { idPrefix: "follow_up_" });
```

## Verify deliveries (Express / Hono / edge)

pushq signs deliveries using the [Standard Webhooks](https://www.standardwebhooks.com)
scheme (`webhook-id`, `webhook-timestamp`, `webhook-signature` headers).

```ts
import { verifySignature, WebhookVerificationError } from "pushq";

app.post("/hooks/send-message", async (req, res) => {
  try {
    // Returns "queue/task_id" — stable across retries, use as an idempotency key.
    const id = await verifySignature({
      secret: "whsec_...",
      headers: req.headers,
      payload: rawBody, // the exact raw request body
    });
  } catch (err) {
    return res.status(401).end();
  }
  // ...
});
```

Errors reject with `PushqError` carrying `.status`, `.code`
(`already_exists`, `not_found`, `failed_precondition`, …), and `.detail`.
