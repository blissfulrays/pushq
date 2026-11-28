# pushq .NET SDK

Official .NET client for [pushq](https://github.com/blissfulrays/pushq), a
self-hosted HTTP push task queue (an open-source alternative to Google Cloud
Tasks). Tasks are delivered to your endpoints as HTTP requests with per-queue
rate limits, concurrency caps, and Cloud Tasks-compatible retries.

- Targets **netstandard2.0** (.NET Framework 4.6.2+, .NET Core, .NET 5–10) and
  **net10.0**. Async throughout, with `CancellationToken` on every call.
- One dependency on older frameworks (`System.Text.Json`), none on .NET 10.
- Works with `IHttpClientFactory`: pass your own `HttpClient`.
- Errors surface as `PushqException` with the server's problem+json `Code`,
  `Status` and `Detail`.
- `Webhooks` verifies deliveries signed with a queue's signing secret
  (Standard Webhooks, constant-time).

## Install

```bash
dotnet add package Pushq
```

## Quickstart

```csharp
using Pushq;

// Hosted API (https://api.pushqueue.dev) by default. Self-hosting? Pass your
// server first, e.g. new PushqClient("http://localhost:8080", apiKey: ...),
// and omit the key against `pushq dev`.
using var pushq = new PushqClient(
    apiKey: Environment.GetEnvironmentVariable("PUSHQ_API_KEY"));

// Create or update a queue (idempotent; never changes its run state).
await pushq.UpsertQueueAsync("emails", new QueueConfig
{
    RateLimits = new RateLimits { MaxDispatchesPerSecond = 50, MaxConcurrentDispatches = 10 },
    Retry = new RetryConfig { MaxAttempts = 8, MinBackoffSeconds = 1, MaxBackoffSeconds = 300 },
    Target = new Target
    {
        BaseUrl = "https://api.example.com",       // resolves tasks that use Path
        Headers = new Dictionary<string, string> { ["Authorization"] = "Bearer worker-token" },
        SigningSecret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", // sign deliveries
    },
});

// Enqueue a task: POST https://api.example.com/send in 5 minutes.
PushqTask task = await pushq.CreateTaskAsync("emails", new TaskCreate
{
    TaskId = "welcome-user-42",                    // optional: deduplicates retries of this call
    Path = "/send",
    JsonBody = new { to = "user@example.com", template = "welcome" },
    Delay = TimeSpan.FromMinutes(5),
});
```

### Queues in folders

Queue names can contain `/` to group queues: `user/instagram` is the queue
`instagram` in the folder `user`. The SDK encodes the name for you.

```csharp
await pushq.UpsertQueueAsync("user/instagram", new QueueConfig { Target = new Target { BaseUrl = "https://ig.example.com" } });
var inFolder = await pushq.ListQueuesAsync(new ListQueuesOptions { NamePrefix = "user/" });
```

### More operations

```csharp
// Enqueue up to 500 at once; each item succeeds or fails on its own.
IReadOnlyList<BatchResult> results = await pushq.CreateTasksAsync("emails", tasks);

// Page through failed tasks.
string? token = null;
do
{
    var page = await pushq.ListTasksAsync("emails", new ListTasksOptions { State = TaskState.Failed, PageToken = token });
    foreach (var t in page.Items) Console.WriteLine($"{t.Id}: {t.LastAttempt?.Error}");
    token = page.NextPageToken;
} while (token != null);

await pushq.RunTaskAsync("emails", "welcome-user-42");   // dispatch now
await pushq.PauseQueueAsync("emails");
await pushq.ResumeQueueAsync("emails");
QueueStats stats = await pushq.GetQueueStatsAsync("emails");
IReadOnlyList<Attempt> attempts = await pushq.ListAttemptsAsync("emails", "welcome-user-42");
```

### Errors

```csharp
try
{
    await pushq.CreateTaskAsync("emails", new TaskCreate { TaskId = "welcome-user-42", Path = "/send" });
}
catch (PushqException e) when (e.Code == "already_exists")
{
    // Already enqueued: safe to ignore.
}
```

### With IHttpClientFactory

```csharp
builder.Services.AddHttpClient("pushq");
builder.Services.AddSingleton(sp => new PushqClient(new PushqClientOptions
{
    BaseUrl = builder.Configuration["Pushq:Url"] ?? PushqClient.DefaultBaseUrl,
    ApiKey = builder.Configuration["Pushq:ApiKey"],
    HttpClient = sp.GetRequiredService<IHttpClientFactory>().CreateClient("pushq"),
}));
```

## Verifying deliveries

When a queue has a signing secret, every delivery carries Standard Webhooks
headers. Verify them against the **raw** body before trusting it:

```csharp
app.MapPost("/send", async (HttpRequest req) =>
{
    using var ms = new MemoryStream();
    await req.Body.CopyToAsync(ms);
    try
    {
        // Returns "queue/task_id": stable across retries, so use it as an idempotency key.
        string id = Webhooks.VerifySignature(signingSecret, name => req.Headers[name].FirstOrDefault(), ms.ToArray());
        // ... handle the task ...
        return Results.Ok();
    }
    catch (WebhookVerificationException)
    {
        return Results.Unauthorized();
    }
});
```

Respond with any 2xx to mark the task done. Any other status (or a timeout)
schedules a retry; `429`/`503` with `Retry-After` slow the whole queue down.

## Development

```bash
dotnet test tests/Pushq.Tests                       # unit tests
PUSHQ_BIN=/path/to/pushq dotnet test tests/Pushq.Tests  # + integration tests against a real server
```
