package io.pushq;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.time.Instant;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/** Client tests against a JDK built-in {@link HttpServer} stub. */
class PushqClientTest {

    /** What the stub captured about the last request. */
    static final class Recorded {
        volatile String method;
        volatile URI uri;
        volatile String authorization;
        volatile String contentType;
        volatile byte[] body = new byte[0];
    }

    /** Canned response the stub should return. */
    static final class Canned {
        final int status;
        final String contentType;
        final String body;

        Canned(int status, String contentType, String body) {
            this.status = status;
            this.contentType = contentType;
            this.body = body;
        }
    }

    private static HttpServer server;
    private static Pushq client;
    private static final Recorded recorded = new Recorded();
    private static volatile Canned canned;
    private static final ObjectMapper mapper = new ObjectMapper();

    private static final String QUEUE_JSON = "{"
            + "\"name\":\"emails\",\"state\":\"running\","
            + "\"rate_limits\":{\"max_dispatches_per_second\":50.0,\"max_burst_size\":10,\"max_concurrent_dispatches\":5},"
            + "\"retry\":{\"max_attempts\":5,\"max_retry_duration_seconds\":0,\"min_backoff_seconds\":0.5,"
            + "\"max_backoff_seconds\":3600,\"max_doublings\":16},"
            + "\"target\":{\"base_url\":\"https://api.example.com\",\"headers\":{\"X-Auth\":\"t\"},\"has_signing_secret\":true},"
            + "\"dead_letter_queue\":\"dead\","
            + "\"created_at\":\"2026-08-02T10:00:00Z\",\"updated_at\":\"2026-08-02T11:30:00Z\"}";

    private static final String TASK_JSON = "{"
            + "\"id\":\"t-1\",\"queue\":\"q1\",\"state\":\"pending\","
            + "\"url\":\"https://api.example.com/hooks/x\",\"method\":\"POST\","
            + "\"schedule_time\":\"2026-08-02T12:00:01.500Z\",\"created_at\":\"2026-08-02T12:00:00Z\","
            + "\"dispatch_deadline_seconds\":600,\"dispatch_count\":0,\"response_count\":0}";

    @BeforeAll
    static void startServer() throws IOException {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext("/", PushqClientTest::handle);
        server.start();
        client = Pushq.builder()
                .baseUrl("http://127.0.0.1:" + server.getAddress().getPort())
                .apiKey("test-key")
                .timeout(Duration.ofSeconds(5))
                .build();
    }

    @AfterAll
    static void stopServer() {
        server.stop(0);
    }

    @BeforeEach
    void reset() {
        canned = new Canned(200, "application/json", "{}");
        recorded.method = null;
        recorded.uri = null;
        recorded.authorization = null;
        recorded.contentType = null;
        recorded.body = new byte[0];
    }

    private static void handle(HttpExchange exchange) throws IOException {
        recorded.method = exchange.getRequestMethod();
        recorded.uri = exchange.getRequestURI();
        recorded.authorization = exchange.getRequestHeaders().getFirst("Authorization");
        recorded.contentType = exchange.getRequestHeaders().getFirst("Content-Type");
        recorded.body = exchange.getRequestBody().readAllBytes();

        Canned c = canned;
        byte[] respBody = c.body == null ? new byte[0] : c.body.getBytes(StandardCharsets.UTF_8);
        if (c.contentType != null) {
            exchange.getResponseHeaders().set("Content-Type", c.contentType);
        }
        if (respBody.length == 0) {
            exchange.sendResponseHeaders(c.status, -1);
        } else {
            exchange.sendResponseHeaders(c.status, respBody.length);
            try (OutputStream os = exchange.getResponseBody()) {
                os.write(respBody);
            }
        }
        exchange.close();
    }

    private JsonNode requestJson() throws IOException {
        return mapper.readTree(recorded.body);
    }

    // ---------------------------------------------------------------- queues

    @Test
    void upsertQueueSerializesConfigAndParsesResponse() throws IOException {
        canned = new Canned(201, "application/json", QUEUE_JSON);

        QueueConfig cfg = QueueConfig.builder()
                .rateLimits(RateLimits.builder()
                        .maxDispatchesPerSecond(50.0)
                        .maxBurstSize(10)
                        .maxConcurrentDispatches(5)
                        .build())
                .retry(RetryConfig.builder().maxAttempts(5).minBackoffSeconds(0.5).build())
                .target(Target.builder()
                        .baseUrl("https://api.example.com")
                        .header("X-Auth", "t")
                        .signingSecret("whsec_c2VjcmV0")
                        .build())
                .deadLetterQueue("dead")
                .build();

        Queue q = client.upsertQueue("emails", cfg);

        // Request line + headers.
        assertEquals("PUT", recorded.method);
        assertEquals("/v1/queues/emails", recorded.uri.getRawPath());
        assertEquals("Bearer test-key", recorded.authorization);
        assertEquals("application/json", recorded.contentType);

        // Body: snake_case field names, only the fields that were set.
        JsonNode body = requestJson();
        assertEquals(50.0, body.at("/rate_limits/max_dispatches_per_second").asDouble());
        assertEquals(10, body.at("/rate_limits/max_burst_size").asInt());
        assertEquals(5, body.at("/rate_limits/max_concurrent_dispatches").asInt());
        assertEquals(5, body.at("/retry/max_attempts").asInt());
        assertEquals(0.5, body.at("/retry/min_backoff_seconds").asDouble());
        assertFalse(body.get("retry").has("max_retry_duration_seconds"),
                "unset retry fields must not serialize");
        assertFalse(body.get("retry").has("max_backoff_seconds"));
        assertEquals("https://api.example.com", body.at("/target/base_url").asText());
        assertEquals("t", body.at("/target/headers/X-Auth").asText());
        assertEquals("whsec_c2VjcmV0", body.at("/target/signing_secret").asText());
        assertEquals("dead", body.get("dead_letter_queue").asText());
        assertFalse(body.has("name"), "upsert body must not carry the name (it is in the path)");
        assertFalse(body.has("state"), "upsert must never send a run state");

        // Response parsing.
        assertEquals("emails", q.getName());
        assertEquals("running", q.getState());
        assertEquals(10, q.getRateLimits().getMaxBurstSize());
        assertEquals(50.0, q.getRateLimits().getMaxDispatchesPerSecond());
        assertEquals(5, q.getRetry().getMaxAttempts());
        assertEquals("https://api.example.com", q.getTarget().getBaseUrl());
        assertEquals(Boolean.TRUE, q.getTarget().getHasSigningSecret());
        assertNull(q.getTarget().getSigningSecret(), "secret is never returned");
        assertEquals("dead", q.getDeadLetterQueue());
        assertEquals(Instant.parse("2026-08-02T10:00:00Z"), q.getCreatedAt());
        assertEquals(Instant.parse("2026-08-02T11:30:00Z"), q.getUpdatedAt());
    }

    @Test
    void createQueuePostsNamePlusConfig() throws IOException {
        canned = new Canned(201, "application/json", QUEUE_JSON);

        client.createQueue("emails", QueueConfig.builder()
                .rateLimits(RateLimits.builder().maxDispatchesPerSecond(2.0).build())
                .build());

        assertEquals("POST", recorded.method);
        assertEquals("/v1/queues", recorded.uri.getRawPath());
        JsonNode body = requestJson();
        assertEquals("emails", body.get("name").asText());
        assertEquals(2.0, body.at("/rate_limits/max_dispatches_per_second").asDouble());
    }

    @Test
    void patchQueueSendsOnlySetFields() throws IOException {
        canned = new Canned(200, "application/json", QUEUE_JSON);

        client.patchQueue("emails", QueuePatch.builder().state("paused").build());

        assertEquals("PATCH", recorded.method);
        assertEquals("/v1/queues/emails", recorded.uri.getRawPath());
        JsonNode body = requestJson();
        assertEquals("paused", body.get("state").asText());
        assertFalse(body.has("rate_limits"));
        assertFalse(body.has("retry"));
    }

    @Test
    void listQueuesAndStatsEncodeQueryParams() {
        canned = new Canned(200, "application/json",
                "{\"queues\":[" + QUEUE_JSON + "],\"next_page_token\":\"tok2\"}");
        QueuePage page = client.listQueues(ListQueuesParams.builder()
                .namePrefix("em")
                .pageSize(50)
                .pageToken("tok1")
                .build());
        assertEquals("/v1/queues", recorded.uri.getRawPath());
        assertEquals("name_prefix=em&page_size=50&page_token=tok1", recorded.uri.getRawQuery());
        assertEquals(1, page.getQueues().size());
        assertEquals("emails", page.getQueues().get(0).getName());
        assertEquals("tok2", page.getNextPageToken());

        canned = new Canned(200, "application/json",
                "{\"state\":\"running\",\"pending\":3,\"scheduled_future\":7,"
                        + "\"dispatched_last_1m\":12,\"errors_last_1m\":1,"
                        + "\"oldest_due_at\":\"2026-08-02T09:00:00Z\",\"oldest_due_age_seconds\":4.5}");
        QueueStats stats = client.queueStats("emails", "ab c");
        assertEquals("/v1/queues/emails/stats", recorded.uri.getRawPath());
        assertEquals("id_prefix=ab%20c", recorded.uri.getRawQuery(), "query values must be percent-encoded");
        assertEquals(3L, stats.getPending());
        assertEquals(7L, stats.getScheduledFuture());
        assertEquals(12L, stats.getDispatchedLast1m());
        assertEquals(Instant.parse("2026-08-02T09:00:00Z"), stats.getOldestDueAt());
        assertEquals(4.5, stats.getOldestDueAgeSeconds());
    }

    @Test
    void deleteQueueAccepts204NoBody() {
        canned = new Canned(204, null, null);
        assertDoesNotThrow(() -> client.deleteQueue("emails"));
        assertEquals("DELETE", recorded.method);
        assertEquals("/v1/queues/emails", recorded.uri.getRawPath());
    }

    // -------------------------------------------------------- colon endpoints

    @Test
    void pauseResumePurgeHitRawColonPaths() {
        canned = new Canned(200, "application/json", QUEUE_JSON);
        Queue q = client.pauseQueue("q1");
        assertEquals("POST", recorded.method);
        assertEquals("/v1/queues/q1:pause", recorded.uri.getRawPath(),
                "colon must not be percent-encoded");
        assertFalse(recorded.uri.toString().contains("%3A"));
        assertEquals("emails", q.getName());

        client.resumeQueue("q1");
        assertEquals("/v1/queues/q1:resume", recorded.uri.getRawPath());

        canned = new Canned(202, "application/json", QUEUE_JSON); // purge returns 202
        assertDoesNotThrow(() -> client.purgeQueue("q1"));
        assertEquals("/v1/queues/q1:purge", recorded.uri.getRawPath());
    }

    @Test
    void batchAndRunHitRawColonPaths() {
        canned = new Canned(200, "application/json",
                "{\"results\":[{\"index\":0,\"task\":" + TASK_JSON + "},"
                        + "{\"index\":1,\"error\":{\"code\":\"already_exists\",\"detail\":\"duplicate id\"}}]}");
        List<BatchResult> results = client.createTasks("q1", Arrays.asList(
                TaskCreate.builder().url("https://a.example/1").build(),
                TaskCreate.builder().url("https://a.example/2").taskId("dup").build()));

        assertEquals("POST", recorded.method);
        assertEquals("/v1/queues/q1/tasks:batch", recorded.uri.getRawPath());
        assertFalse(recorded.uri.toString().contains("%3A"));
        assertEquals(2, results.size());
        assertTrue(results.get(0).isSuccess());
        assertEquals("t-1", results.get(0).getTask().getId());
        assertFalse(results.get(1).isSuccess());
        assertEquals("already_exists", results.get(1).getError().getCode());
        assertEquals("duplicate id", results.get(1).getError().getDetail());

        canned = new Canned(200, "application/json", TASK_JSON);
        Task run = client.runTask("q1", "t-1");
        assertEquals("POST", recorded.method);
        assertEquals("/v1/queues/q1/tasks/t-1:run", recorded.uri.getRawPath());
        assertFalse(recorded.uri.toString().contains("%3A"));
        assertEquals("t-1", run.getId());
    }

    @Test
    void batchRequestBodyWrapsTasksArray() throws IOException {
        canned = new Canned(200, "application/json", "{\"results\":[]}");
        client.createTasks("q1", List.of(TaskCreate.builder().path("/a").build()));
        JsonNode body = requestJson();
        assertTrue(body.get("tasks").isArray());
        assertEquals(1, body.get("tasks").size());
        assertEquals("/a", body.at("/tasks/0/path").asText());
    }

    // ------------------------------------------------------------------ tasks

    @Test
    void createTaskWithJsonBodySendsJsonBodyField() throws IOException {
        canned = new Canned(201, "application/json", TASK_JSON);

        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("a", 1);
        payload.put("b", "two");
        Task t = client.createTask("q1", TaskCreate.builder()
                .taskId("t-1")
                .path("/hooks/x")
                .header("X-Trace", "abc")
                .jsonBody(payload)
                .delaySeconds(1.5)
                .build());

        assertEquals("POST", recorded.method);
        assertEquals("/v1/queues/q1/tasks", recorded.uri.getRawPath());

        JsonNode body = requestJson();
        assertTrue(body.get("json_body").isObject(), "json_body must be an embedded JSON object");
        assertEquals(mapper.readTree("{\"a\":1,\"b\":\"two\"}"), body.get("json_body"));
        assertEquals("t-1", body.get("task_id").asText());
        assertEquals("/hooks/x", body.get("path").asText());
        assertEquals("abc", body.at("/headers/X-Trace").asText());
        assertEquals(1.5, body.get("delay_seconds").asDouble());
        assertFalse(body.has("body_base64"), "jsonBody and body_base64 are exclusive");
        assertFalse(body.has("url"));
        assertFalse(body.has("schedule_time"));

        // Response parsing.
        assertEquals("t-1", t.getId());
        assertEquals("pending", t.getState());
        assertEquals(Instant.parse("2026-08-02T12:00:01.500Z"), t.getScheduleTime());
        assertEquals(600.0, t.getDispatchDeadlineSeconds());
    }

    @Test
    void createTaskWithRawBodyAndScheduleTime() throws IOException {
        canned = new Canned(201, "application/json", TASK_JSON);

        client.createTask("q1", TaskCreate.builder()
                .url("https://api.example.com/x")
                .method("PUT")
                .body("hello".getBytes(StandardCharsets.UTF_8))
                .scheduleTime(Instant.parse("2030-01-01T00:00:00Z"))
                .dispatchDeadlineSeconds(120)
                .retry(RetryConfig.builder().maxAttempts(3).build())
                .build());

        JsonNode body = requestJson();
        assertEquals("aGVsbG8=", body.get("body_base64").asText());
        assertEquals("2030-01-01T00:00:00Z", body.get("schedule_time").asText(),
                "Instant must serialize as an RFC 3339 string");
        assertEquals("PUT", body.get("method").asText());
        assertEquals(120.0, body.get("dispatch_deadline_seconds").asDouble());
        assertEquals(3, body.at("/retry/max_attempts").asInt());
        assertFalse(body.has("json_body"));
        assertFalse(body.has("delay_seconds"));
        assertFalse(body.has("task_id"));
    }

    @Test
    void taskCreateBuilderRejectsExclusivePairs() {
        assertThrows(IllegalStateException.class, () -> TaskCreate.builder()
                .url("https://a.example").path("/p").build());
        assertThrows(IllegalStateException.class, () -> TaskCreate.builder()
                .body(new byte[]{1}).jsonBody(Map.of("a", 1)).build());
        assertThrows(IllegalStateException.class, () -> TaskCreate.builder()
                .scheduleTime(Instant.now()).delaySeconds(5).build());
    }

    @Test
    void getTaskViewsListTasksAndAttempts() {
        canned = new Canned(200, "application/json", TASK_JSON);
        client.getTask("q1", "t-1");
        assertEquals("/v1/queues/q1/tasks/t-1", recorded.uri.getRawPath());
        assertNull(recorded.uri.getRawQuery());

        client.getTaskFull("q1", "t-1");
        assertEquals("view=full", recorded.uri.getRawQuery());

        canned = new Canned(200, "application/json",
                "{\"tasks\":[" + TASK_JSON + "],\"next_page_token\":\"\"}");
        TaskPage page = client.listTasks("q1", ListTasksParams.builder()
                .state("pending").idPrefix("t-").view("full").pageSize(10).pageToken("tk").build());
        assertEquals("/v1/queues/q1/tasks", recorded.uri.getRawPath());
        assertEquals("state=pending&id_prefix=t-&view=full&page_size=10&page_token=tk",
                recorded.uri.getRawQuery());
        assertEquals(1, page.getTasks().size());

        canned = new Canned(200, "application/json",
                "{\"attempts\":[{\"number\":2,\"dispatch_time\":\"2026-08-02T12:00:05Z\","
                        + "\"response_status\":503,\"error_kind\":\"non_2xx\","
                        + "\"error_snippet\":\"upstream busy\",\"duration_ms\":41}]}");
        List<Attempt> attempts = client.listAttempts("q1", "t-1");
        assertEquals("/v1/queues/q1/tasks/t-1/attempts", recorded.uri.getRawPath());
        assertEquals(1, attempts.size());
        assertEquals(2, attempts.get(0).getNumber());
        assertEquals(503, attempts.get(0).getResponseStatus());
        assertEquals("non_2xx", attempts.get(0).getErrorKind());
        assertEquals(41L, attempts.get(0).getDurationMs());

        canned = new Canned(204, null, null);
        assertDoesNotThrow(() -> client.deleteTask("q1", "t-1"));
        assertEquals("DELETE", recorded.method);
    }

    // ----------------------------------------------------------------- errors

    @Test
    void problemJsonBecomesPushqException() {
        canned = new Canned(409, "application/problem+json",
                "{\"type\":\"about:blank\",\"title\":\"Conflict\",\"status\":409,"
                        + "\"code\":\"already_exists\",\"detail\":\"task 't-1' already exists\"}");

        PushqException e = assertThrows(PushqException.class,
                () -> client.createTask("q1", TaskCreate.builder().taskId("t-1").path("/x").build()));

        assertEquals(409, e.getStatus());
        assertEquals("already_exists", e.getCode());
        assertEquals("task 't-1' already exists", e.getDetail());
        assertTrue(e.getMessage().contains("already_exists"));
        assertTrue(e.getMessage().contains("409"));
    }

    @Test
    void notFoundProblemOnGetQueue() {
        canned = new Canned(404, "application/problem+json",
                "{\"status\":404,\"code\":\"not_found\",\"detail\":\"queue 'nope' not found\"}");
        PushqException e = assertThrows(PushqException.class, () -> client.getQueue("nope"));
        assertEquals(404, e.getStatus());
        assertEquals("not_found", e.getCode());
    }

    @Test
    void nonJsonErrorBodyIsTolerated() {
        canned = new Canned(500, "text/plain", "boom: something broke");
        PushqException e = assertThrows(PushqException.class, () -> client.getQueue("q1"));
        assertEquals(500, e.getStatus());
        assertNull(e.getCode());
        assertEquals("boom: something broke", e.getDetail());
    }
}
