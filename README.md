<p align="center">
  <a href="https://pushqueue.dev">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="images/wordmark-dark.svg">
      <img src="images/wordmark.svg" alt="pushq" height="56">
    </picture>
  </a>
</p>

<p align="center">
  <strong>The HTTP push task queue for developers.</strong><br>
  Enqueue a task and pushq delivers it to your endpoint, with rate limits, retries, scheduling and signed delivery.<br>
  Use the hosted API, or run it yourself as one binary.
</p>

<p align="center">
  <a href="https://pushqueue.dev"><strong>Website</strong></a> ·
  <a href="https://pushqueue.dev/docs/"><strong>Documentation</strong></a> ·
  <a href="https://pushqueue.dev/docs/getting-started/quickstart/">Quickstart</a> ·
  <a href="https://pushqueue.dev/docs/reference/api/">API reference</a>
</p>

<p align="center">
  <a href="https://github.com/blissfulrays/pushq/actions/workflows/ci.yml"><img src="https://github.com/blissfulrays/pushq/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/blissfulrays/pushq/releases"><img src="https://img.shields.io/github/v/release/blissfulrays/pushq?label=release" alt="Latest release"></a>
  <a href="https://www.npmjs.com/package/pushq"><img src="https://img.shields.io/npm/v/pushq?label=npm" alt="npm"></a>
  <a href="https://pypi.org/project/pushq/"><img src="https://img.shields.io/pypi/v/pushq?label=pypi" alt="PyPI"></a>
  <a href="https://www.nuget.org/packages/Pushq"><img src="https://img.shields.io/nuget/v/Pushq?label=nuget" alt="NuGet"></a>
  <a href="https://pkg.go.dev/github.com/blissfulrays/pushq/sdks/go"><img src="https://pkg.go.dev/badge/github.com/blissfulrays/pushq/sdks/go.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/blissfulrays/pushq" alt="MIT license"></a>
</p>

---

pushq turns "do this later" into an HTTP request that actually happens. You create a **queue** that points at your app and add **tasks** to it; pushq calls your endpoint for each task at the rate you allow, retries failures with backoff, and keeps the full history of every attempt. It follows Google Cloud Tasks' semantics, so it's also a drop-in way off GCP.

## Quickstart

Get an API key from the [console](https://console.pushqueue.dev/), then:

```bash
npm install pushq
```

```ts
import { Pushq, verifySignature } from "pushq";

const pq = new Pushq({ apiKey: process.env.PUSHQ_API_KEY }); // hosted API by default

// One queue per destination (or per customer), with its own rate limit.
await pq.upsertQueue("emails", {
  rateLimits: { maxDispatchesPerSecond: 10, maxConcurrentDispatches: 5 },
  target: { baseUrl: "https://api.example.com", signingSecret: process.env.PUSHQ_SIGNING_SECRET },
});

// Deliver POST https://api.example.com/hooks/send-welcome in an hour, at most once per task ID.
await pq.createTask("emails", {
  taskId: "welcome-user-42",
  path: "/hooks/send-welcome",
  jsonBody: { user_id: 42 },
  delaySeconds: 3600,
});

// In your app: verify the delivery, do the work, return 2xx.
app.post("/hooks/send-welcome", express.raw({ type: "*/*" }), async (req, res) => {
  const id = await verifySignature({ secret: process.env.PUSHQ_SIGNING_SECRET!, headers: req.headers, payload: req.body })
    .catch(() => null);
  if (!id) return res.sendStatus(401);
  await sendWelcomeEmail(JSON.parse(req.body).user_id, id); // id is stable across retries
  res.sendStatus(204);
});
```

The same flow in Python, Go, Java, .NET and curl is in the [quickstart](https://pushqueue.dev/docs/getting-started/quickstart/).

## Features

- **Per-queue rate limits and concurrency caps:** token-bucket rate limiting with bursts, so a backlog never floods a downstream API. `Retry-After` on 429/503 slows the queue down.
- **Cloud Tasks–compatible retries:** exponential-then-linear backoff, `max_attempts` and `max_retry_duration`, plus **per-task overrides**.
- **Scheduled and delayed delivery:** deliver now or up to a year ahead, with `delay_seconds` or `schedule_time`.
- **Deduplication:** a client-chosen `task_id` makes enqueueing safe to retry.
- **Dead-letter queues:** tasks that run out of retries land in a queue you can inspect and replay.
- **Signed delivery:** Standard Webhooks HMAC signatures, verified in constant time by every SDK, or static auth headers.
- **10k+ dynamic queues:** create queues at runtime, one per tenant if you like, organized in folders (`tenants/acme`).
- **Batch enqueue:** up to 500 tasks per request.
- **Dashboard:** queues by folder with live stats, task attempt history, pause, resume, purge and run-now.
- **Runs anywhere:** one static binary with the dashboard built in. SQLite by default; Postgres for multi-replica high availability.

## SDKs

| Language | Install | |
| --- | --- | --- |
| TypeScript / JavaScript | `npm install pushq` | [source](sdks/typescript) |
| Python | `pip install pushq` | [source](sdks/python) |
| Go | `go get github.com/blissfulrays/pushq/sdks/go` | [source](sdks/go) |
| Java | `io.github.blissfulrays:pushq` (Maven Central coming; [build from source](sdks/java)) | [source](sdks/java) |
| .NET | `dotnet add package Pushq` | [source](sdks/dotnet) |
| Anything else | Plain HTTP, described by an [OpenAPI 3.1 spec](https://api.pushqueue.dev/openapi.json) | [API reference](https://pushqueue.dev/docs/reference/api/) |

Every SDK includes the client and a `verify_signature` helper for receivers. [Install and configure an SDK →](https://pushqueue.dev/docs/getting-started/sdks/)

## Run it yourself

Try the full queue locally, with no account and no auth:

```bash
pushq dev   # SQLite in a temp file, fast timers, dashboard at http://localhost:8080
```

Install for production:

```bash
# Docker (dashboard included)
docker run -p 8080:8080 -v pushq-data:/data ghcr.io/blissfulrays/pushq serve --db sqlite:/data/pushq.db

# Or a binary for macOS, Linux or Windows from GitHub Releases
#   https://github.com/blissfulrays/pushq/releases

# Or from source (no dashboard; use `make build` to include it)
go install github.com/blissfulrays/pushq/cmd/pushq@latest
```

Point an SDK at your server with its base URL, for example `new Pushq({ baseUrl: "http://localhost:8080" })`. Guides: [install](https://pushqueue.dev/docs/self-hosting/install/) · [configuration](https://pushqueue.dev/docs/self-hosting/configuration/) · [deploy with SQLite or Postgres](https://pushqueue.dev/docs/self-hosting/deploy/) · [operations](https://pushqueue.dev/docs/self-hosting/operations/).

## pushq vs Google Cloud Tasks

| | Google Cloud Tasks | pushq |
| --- | --- | --- |
| Hosting | GCP only | Hosted, or anywhere you run a binary or container |
| Queues | 1,000 per region | Uncapped; built for 10k+ dynamic queues |
| Retry config | Queue-level only | Queue-level **plus per-task overrides** |
| Dead-letter queues | No | Per-queue `dead_letter_queue` |
| Completed tasks | Deleted | Kept with full attempt history (configurable) |
| Queue introspection | List and count | `stats`: depth by state, oldest-due age, error rates |
| Delivery auth | OIDC (GCP only) | Standard Webhooks signatures or static headers |
| Schedule ahead | 30 days | 1 year (configurable) |
| Local development | Emulators | `pushq dev`, the real thing |
| Batch enqueue | No | Up to 500 per request |

Backoff math, the "retry until both limits are hit" rule, `Retry-After` handling and at-least-once delivery all match Cloud Tasks. See [migrate from Cloud Tasks](https://pushqueue.dev/docs/guides/migrate-from-cloud-tasks/).

## Documentation

| | |
| --- | --- |
| **Get started** | [Quickstart](https://pushqueue.dev/docs/getting-started/quickstart/) · [Install an SDK](https://pushqueue.dev/docs/getting-started/sdks/) · [Local development](https://pushqueue.dev/docs/getting-started/local-development/) |
| **Concepts** | [Queues](https://pushqueue.dev/docs/concepts/queues/) · [Tasks](https://pushqueue.dev/docs/concepts/tasks/) · [Retries](https://pushqueue.dev/docs/concepts/retries/) · [Delivery & signatures](https://pushqueue.dev/docs/concepts/delivery/) · [Dead-letter queues](https://pushqueue.dev/docs/concepts/dead-letter-queues/) |
| **Guides** | [Rate limits](https://pushqueue.dev/docs/guides/rate-limits/) · [API keys](https://pushqueue.dev/docs/guides/api-keys/) · [Team members](https://pushqueue.dev/docs/guides/team-members/) · [Going to production](https://pushqueue.dev/docs/guides/going-to-production/) · [Troubleshooting](https://pushqueue.dev/docs/guides/troubleshooting/) |
| **Patterns** | [Idempotent handlers](https://pushqueue.dev/docs/patterns/idempotent-handlers/) · [Per-tenant queues](https://pushqueue.dev/docs/patterns/per-tenant-queues/) · [Throttle a third-party API](https://pushqueue.dev/docs/patterns/throttle-a-third-party-api/) · [All patterns](https://pushqueue.dev/docs/patterns/idempotent-handlers/) |
| **Reference** | [REST API](https://pushqueue.dev/docs/reference/api/) · [Error codes](https://pushqueue.dev/docs/reference/errors/) · [Server configuration](https://pushqueue.dev/docs/self-hosting/configuration/) |

Docs for LLMs: every page is also plain Markdown (add `.md` to its URL), and [`llms.txt`](https://pushqueue.dev/docs/llms.txt) indexes them all.

## Development

```bash
make test        # unit + SQLite conformance + end-to-end
make test-pg     # + Postgres conformance and blue/green tests (needs PUSHQ_TEST_PG_URL)
make build       # binary with the embedded dashboard (needs Node 20+)
```

Binaries, the Docker image and all five SDKs are released automatically when a change merges to `main`; see [docs/publishing.md](docs/publishing.md). Bug reports and pull requests are welcome in [issues](https://github.com/blissfulrays/pushq/issues).

## License

[MIT](LICENSE)
