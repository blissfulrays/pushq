# pushq Java SDK

Official Java client for [pushq](https://github.com/blissfulrays/pushq), a self-hosted
HTTP push task queue (an open-source alternative to GCP Cloud Tasks). Tasks are
delivered to your endpoints as HTTP requests with per-queue rate limits,
concurrency caps, and Cloud Tasks-compatible retry semantics.

- Java 11+, synchronous, thread-safe.
- One runtime dependency (Jackson); HTTP via the JDK's `java.net.http.HttpClient`.
- Errors surface as unchecked `PushqException` with the server's RFC 9457
  problem+json `code` and `detail`.
- `Webhooks` implements Standard Webhooks signing and verification for
  deliveries signed with a queue `signing_secret`.

## Install

```xml
<dependency>
  <groupId>io.github.blissfulrays</groupId>
  <artifactId>pushq</artifactId>
  <version>1.0.0-SNAPSHOT</version>
</dependency>
```

## Quickstart

### Create the client

```java
import io.pushq.*;
import java.time.Duration;

Pushq client = Pushq.builder()
    .apiKey(System.getenv("PUSHQ_API_KEY"))
    // Defaults to the hosted API (https://api.pushqueue.dev). Self-hosting?
    // .baseUrl("http://localhost:8080")
    // and omit the key against an unauthenticated dev server.
    .timeout(Duration.ofSeconds(10))
    .build();
```

### Upsert a queue with rate limits

`upsertQueue` is idempotent (`PUT`): it creates the queue or replaces its
configuration, and never changes the queue's run state.

```java
Queue queue = client.upsertQueue("emails", QueueConfig.builder()
    .rateLimits(RateLimits.builder()
        .maxDispatchesPerSecond(50)
        .maxConcurrentDispatches(10)
        .build())
    .retry(RetryConfig.builder()
        .maxAttempts(8)
        .minBackoffSeconds(1)
        .maxBackoffSeconds(300)
        .build())
    .target(Target.builder()
        .baseUrl("https://api.example.com")          // resolves tasks that use .path(...)
        .header("Authorization", "Bearer worker-token")
        .signingSecret("whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw") // sign deliveries
        .build())
    .deadLetterQueue("emails-dead")
    .build());

System.out.println(queue.getName() + " is " + queue.getState());
```

### Enqueue a task with a delay and a task ID

A client-chosen `taskId` deduplicates: a second create with the same ID gets a
409 `already_exists`.

```java
Task task = client.createTask("emails", TaskCreate.builder()
    .taskId("welcome-user-9182")            // dedup key
    .path("/hooks/send-welcome")            // resolved against the queue's target.base_url
    .jsonBody(Map.of("user_id", 9182))      // sent as json_body with Content-Type: application/json
    .delaySeconds(30)                       // or .scheduleTime(Instant)
    .retry(RetryConfig.builder().maxAttempts(5).build()) // per-task override
    .build());

System.out.println("enqueued " + task.getId() + ", runs at " + task.getScheduleTime());
```

Batch enqueue (up to 500, non-atomic, per-item results):

```java
List<BatchResult> results = client.createTasks("emails", List.of(
    TaskCreate.builder().taskId("a").path("/hooks/a").build(),
    TaskCreate.builder().taskId("b").path("/hooks/b").build()));
for (BatchResult r : results) {
    if (!r.isSuccess()) {
        System.err.println("item " + r.getIndex() + " failed: " + r.getError().getCode());
    }
}
```

### Verify a webhook signature

Deliveries from queues with a `signing_secret` carry the
[Standard Webhooks](https://www.standardwebhooks.com/) headers `webhook-id`,
`webhook-timestamp`, and `webhook-signature`. Verify against the **raw** request
body before parsing it.

Servlet:

```java
@Override
protected void doPost(HttpServletRequest req, HttpServletResponse resp) throws IOException {
    byte[] payload = req.getInputStream().readAllBytes();
    try {
        Webhooks.verifySignature(
            System.getenv("PUSHQ_SIGNING_SECRET"),           // whsec_...
            req.getHeader("webhook-id"),
            Long.parseLong(req.getHeader("webhook-timestamp")),
            payload,
            req.getHeader("webhook-signature"),
            300);                                            // tolerance in seconds
    } catch (WebhookVerificationException e) {
        resp.sendError(401);
        return;
    }
    // trusted: handle the task delivery, then 2xx to ack
    resp.setStatus(200);
}
```

Spring:

```java
@PostMapping("/hooks/send-welcome")
public ResponseEntity<Void> handle(@RequestBody byte[] payload,
                                   @RequestHeader("webhook-id") String id,
                                   @RequestHeader("webhook-timestamp") long ts,
                                   @RequestHeader("webhook-signature") String sig) {
    try {
        Webhooks.verifySignature(secret, id, ts, payload, sig, 300);
    } catch (WebhookVerificationException e) {
        return ResponseEntity.status(HttpStatus.UNAUTHORIZED).build();
    }
    // process payload...
    return ResponseEntity.ok().build();
}
```

`Webhooks.sign(secret, msgId, timestampSeconds, payload)` produces the matching
`v1,<base64>` entry if you need to generate signatures (e.g. in tests).

## Error handling

Every non-2xx response throws `PushqException` (a `RuntimeException`):

```java
try {
    client.createTask("emails", TaskCreate.builder().taskId("welcome-user-9182").path("/x").build());
} catch (PushqException e) {
    if ("already_exists".equals(e.getCode())) {
        // duplicate task id -- already enqueued, safe to ignore
    } else {
        System.err.println("HTTP " + e.getStatus() + " " + e.getCode() + ": " + e.getDetail());
        throw e;
    }
}
```

Codes: `already_exists`, `not_found`, `failed_precondition`, `invalid_argument`,
`unauthenticated`, `permission_denied`, `payload_too_large`, `internal`.
Transport failures throw `PushqException` with `getStatus() == 0`.

## More operations

```java
client.getQueue("emails");
client.patchQueue("emails", QueuePatch.builder().state("paused").build());
client.pauseQueue("emails");                    // POST /v1/queues/emails:pause
client.resumeQueue("emails");
client.purgeQueue("emails");                    // deletes all tasks created up to now
client.queueStats("emails", "welcome-");        // stats scoped to a task-id prefix
client.deleteQueue("emails");                   // admin

QueuePage qp = client.listQueues(ListQueuesParams.builder().namePrefix("em").build());

TaskPage tp = client.listTasks("emails", ListTasksParams.builder()
    .state("failed").pageSize(100).build());
client.getTaskFull("emails", "welcome-user-9182"); // includes body_base64
client.runTask("emails", "welcome-user-9182");     // force immediate dispatch
client.listAttempts("emails", "welcome-user-9182");
client.deleteTask("emails", "welcome-user-9182");
```

## Building from source

```sh
mvn test     # unit tests (no network; stub HTTP server)
mvn package  # builds target/pushq-1.0.0-SNAPSHOT.jar
```
