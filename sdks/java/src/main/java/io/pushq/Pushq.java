package io.pushq;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.io.IOException;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.Objects;

/**
 * Client for the pushq HTTP API.
 *
 * <pre>{@code
 * Pushq client = Pushq.builder()
 *     .apiKey("my-key")                 // hosted API by default
 *     .baseUrl("http://localhost:8080") // or your self-hosted server
 *     .timeout(Duration.ofSeconds(10))
 *     .build();
 * }</pre>
 *
 * <p>All methods are synchronous and thread-safe. Non-2xx responses raise
 * {@link PushqException} (unchecked) carrying the HTTP status and the RFC 9457
 * problem+json {@code code}/{@code detail}.
 */
public final class Pushq {

    static final String VERSION = "0.1.0-SNAPSHOT";

    /** The hosted pushq API, used when no base URL is set. */
    public static final String DEFAULT_BASE_URL = "https://api.pushqueue.dev";

    private final String baseUrl;
    private final String apiKey;
    private final Duration timeout;
    private final HttpClient http;

    private Pushq(Builder b) {
        String url = b.baseUrl == null || b.baseUrl.trim().isEmpty() ? DEFAULT_BASE_URL : b.baseUrl.trim();
        while (url.endsWith("/")) {
            url = url.substring(0, url.length() - 1);
        }
        this.baseUrl = url;
        this.apiKey = b.apiKey;
        this.timeout = b.timeout;
        this.http = HttpClient.newBuilder()
                .connectTimeout(b.timeout)
                .followRedirects(HttpClient.Redirect.NEVER)
                .build();
    }

    public static Builder builder() {
        return new Builder();
    }

    /** The server this client talks to, without a trailing slash. */
    public String getBaseUrl() {
        return baseUrl;
    }

    // ------------------------------------------------------------------ queues

    /**
     * Create or replace a queue's configuration (idempotent).
     * {@code PUT /v1/queues/{name}}. Never changes the queue's run state.
     */
    public Queue upsertQueue(String name, QueueConfig config) {
        return sendJson("PUT", queuePath(name), config == null ? new QueueConfig() : config, Queue.class);
    }

    /**
     * Create a queue; fails with {@code already_exists} (409) if it exists.
     * {@code POST /v1/queues}.
     */
    public Queue createQueue(String name, QueueConfig config) {
        Objects.requireNonNull(name, "name is required");
        ObjectNode body = config == null
                ? Json.MAPPER.createObjectNode()
                : (ObjectNode) Json.MAPPER.valueToTree(config);
        body.put("name", name);
        return sendJson("POST", "/v1/queues", body, Queue.class);
    }

    /** {@code GET /v1/queues/{name}}. */
    public Queue getQueue(String name) {
        return send("GET", queuePath(name), Queue.class);
    }

    /** Update only the provided fields (can also set state). {@code PATCH /v1/queues/{name}}. */
    public Queue patchQueue(String name, QueuePatch patch) {
        return sendJson("PATCH", queuePath(name), patch == null ? new QueuePatch() : patch, Queue.class);
    }

    /** List queues (admin). {@code GET /v1/queues}. */
    public QueuePage listQueues(ListQueuesParams params) {
        StringBuilder query = new StringBuilder();
        if (params != null) {
            addQuery(query, "name_prefix", params.getNamePrefix());
            addQuery(query, "page_size", params.getPageSize());
            addQuery(query, "page_token", params.getPageToken());
        }
        return send("GET", "/v1/queues" + query, QueuePage.class);
    }

    /** List queues with default parameters. */
    public QueuePage listQueues() {
        return listQueues(null);
    }

    /** Delete a queue and all its tasks (admin). {@code DELETE /v1/queues/{name}}. */
    public void deleteQueue(String name) {
        sendNoResult("DELETE", queuePath(name));
    }

    /** Stop dispatching; task creation still allowed. {@code POST /v1/queues/{name}:pause}. */
    public Queue pauseQueue(String name) {
        return sendEmptyPost(queuePath(name) + ":pause", Queue.class);
    }

    /** Resume dispatching. {@code POST /v1/queues/{name}:resume}. */
    public Queue resumeQueue(String name) {
        return sendEmptyPost(queuePath(name) + ":resume", Queue.class);
    }

    /**
     * Delete all tasks created up to now (exact watermark, includes in-flight).
     * {@code POST /v1/queues/{name}:purge}.
     */
    public Queue purgeQueue(String name) {
        return sendEmptyPost(queuePath(name) + ":purge", Queue.class);
    }

    /** Live counts and recent dispatch/error rates. {@code GET /v1/queues/{name}/stats}. */
    public QueueStats queueStats(String name) {
        return queueStats(name, null);
    }

    /** Queue stats scoped to task IDs starting with {@code idPrefix}. */
    public QueueStats queueStats(String name, String idPrefix) {
        StringBuilder query = new StringBuilder();
        addQuery(query, "id_prefix", idPrefix);
        return send("GET", queuePath(name) + "/stats" + query, QueueStats.class);
    }

    // ------------------------------------------------------------------- tasks

    /** Enqueue a task. {@code POST /v1/queues/{queue}/tasks}. */
    public Task createTask(String queue, TaskCreate task) {
        Objects.requireNonNull(task, "task is required");
        return sendJson("POST", queuePath(queue) + "/tasks", task, Task.class);
    }

    /**
     * Enqueue up to 500 tasks in one call (non-atomic; per-item results in
     * request order). {@code POST /v1/queues/{queue}/tasks:batch}.
     */
    public List<BatchResult> createTasks(String queue, List<TaskCreate> tasks) {
        Objects.requireNonNull(tasks, "tasks is required");
        JsonNode root = sendJson("POST", queuePath(queue) + "/tasks:batch",
                Collections.singletonMap("tasks", tasks), JsonNode.class);
        JsonNode results = root == null ? null : root.get("results");
        if (results == null || results.isNull()) {
            return Collections.emptyList();
        }
        return Json.MAPPER.convertValue(results, new TypeReference<List<BatchResult>>() {
        });
    }

    /** Get a task (basic view). {@code GET /v1/queues/{queue}/tasks/{id}}. */
    public Task getTask(String queue, String taskId) {
        return send("GET", taskPath(queue, taskId), Task.class);
    }

    /** Get a task with {@code view=full} (includes {@code body_base64}). */
    public Task getTaskFull(String queue, String taskId) {
        return send("GET", taskPath(queue, taskId) + "?view=full", Task.class);
    }

    /** List tasks, newest first. {@code GET /v1/queues/{queue}/tasks}. */
    public TaskPage listTasks(String queue, ListTasksParams params) {
        StringBuilder query = new StringBuilder();
        if (params != null) {
            addQuery(query, "state", params.getState());
            addQuery(query, "id_prefix", params.getIdPrefix());
            addQuery(query, "view", params.getView());
            addQuery(query, "page_size", params.getPageSize());
            addQuery(query, "page_token", params.getPageToken());
        }
        return send("GET", queuePath(queue) + "/tasks" + query, TaskPage.class);
    }

    /** List tasks with default parameters. */
    public TaskPage listTasks(String queue) {
        return listTasks(queue, null);
    }

    /**
     * Delete a task; its ID stays reserved for the dedup window.
     * {@code DELETE /v1/queues/{queue}/tasks/{id}}.
     */
    public void deleteTask(String queue, String taskId) {
        sendNoResult("DELETE", taskPath(queue, taskId));
    }

    /**
     * Force immediate dispatch, bypassing rate limits and PAUSED state.
     * {@code POST /v1/queues/{queue}/tasks/{id}:run}.
     */
    public Task runTask(String queue, String taskId) {
        return sendEmptyPost(taskPath(queue, taskId) + ":run", Task.class);
    }

    /** Attempt history, newest first. {@code GET /v1/queues/{queue}/tasks/{id}/attempts}. */
    public List<Attempt> listAttempts(String queue, String taskId) {
        JsonNode root = send("GET", taskPath(queue, taskId) + "/attempts", JsonNode.class);
        JsonNode attempts = root == null ? null : root.get("attempts");
        if (attempts == null || attempts.isNull()) {
            return Collections.emptyList();
        }
        return Json.MAPPER.convertValue(attempts, new TypeReference<List<Attempt>>() {
        });
    }

    // ---------------------------------------------------------------- plumbing

    private String queuePath(String name) {
        Objects.requireNonNull(name, "queue name is required");
        return "/v1/queues/" + encodeSegment(name);
    }

    private String taskPath(String queue, String taskId) {
        Objects.requireNonNull(taskId, "task id is required");
        return queuePath(queue) + "/tasks/" + encodeSegment(taskId);
    }

    private <T> T send(String method, String pathAndQuery, Class<T> responseType) {
        HttpRequest req = requestBuilder(pathAndQuery)
                .method(method, HttpRequest.BodyPublishers.noBody())
                .build();
        return execute(req, responseType);
    }

    private <T> T sendEmptyPost(String pathAndQuery, Class<T> responseType) {
        HttpRequest req = requestBuilder(pathAndQuery)
                .POST(HttpRequest.BodyPublishers.noBody())
                .build();
        return execute(req, responseType);
    }

    private void sendNoResult(String method, String pathAndQuery) {
        HttpRequest req = requestBuilder(pathAndQuery)
                .method(method, HttpRequest.BodyPublishers.noBody())
                .build();
        HttpResponse<byte[]> resp = doSend(req);
        checkOk(resp);
    }

    private <T> T sendJson(String method, String pathAndQuery, Object body, Class<T> responseType) {
        byte[] json;
        try {
            json = Json.MAPPER.writeValueAsBytes(body);
        } catch (IOException e) {
            throw new PushqException("failed to serialize request body: " + e.getMessage(), e);
        }
        HttpRequest req = requestBuilder(pathAndQuery)
                .header("Content-Type", "application/json")
                .method(method, HttpRequest.BodyPublishers.ofByteArray(json))
                .build();
        return execute(req, responseType);
    }

    private HttpRequest.Builder requestBuilder(String pathAndQuery) {
        // Build the URI from the raw string so path colons (":pause", ":run",
        // "tasks:batch", ...) are never percent-encoded.
        HttpRequest.Builder rb = HttpRequest.newBuilder()
                .uri(URI.create(baseUrl + pathAndQuery))
                .timeout(timeout)
                .header("Accept", "application/json")
                .header("User-Agent", "pushq-java/" + VERSION);
        if (apiKey != null && !apiKey.isEmpty()) {
            rb.header("Authorization", "Bearer " + apiKey);
        }
        return rb;
    }

    private <T> T execute(HttpRequest req, Class<T> responseType) {
        HttpResponse<byte[]> resp = doSend(req);
        checkOk(resp);
        byte[] body = resp.body();
        if (body == null || body.length == 0) {
            return null;
        }
        try {
            return Json.MAPPER.readValue(body, responseType);
        } catch (IOException e) {
            throw new PushqException("failed to parse response body: " + e.getMessage(), e);
        }
    }

    private HttpResponse<byte[]> doSend(HttpRequest req) {
        try {
            return http.send(req, HttpResponse.BodyHandlers.ofByteArray());
        } catch (IOException e) {
            throw new PushqException("request to " + req.uri() + " failed: " + e.getMessage(), e);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new PushqException("request to " + req.uri() + " interrupted", e);
        }
    }

    private static void checkOk(HttpResponse<byte[]> resp) {
        int status = resp.statusCode();
        if (status / 100 == 2) {
            return;
        }
        String code = null;
        String detail = null;
        String raw = resp.body() == null ? "" : new String(resp.body(), StandardCharsets.UTF_8);
        try {
            JsonNode node = Json.MAPPER.readTree(raw);
            if (node != null && node.isObject()) {
                if (node.hasNonNull("code")) {
                    code = node.get("code").asText();
                }
                if (node.hasNonNull("detail")) {
                    detail = node.get("detail").asText();
                } else if (node.hasNonNull("title")) {
                    detail = node.get("title").asText();
                }
            }
        } catch (IOException ignored) {
            // Not JSON; fall through to the raw body.
        }
        if (detail == null && !raw.isEmpty()) {
            detail = raw.length() > 1000 ? raw.substring(0, 1000) : raw;
        }
        throw new PushqException(status, code, detail);
    }

    private static void addQuery(StringBuilder query, String name, Object value) {
        if (value == null) {
            return;
        }
        query.append(query.length() == 0 ? '?' : '&')
                .append(name)
                .append('=')
                .append(URLEncoder.encode(String.valueOf(value), StandardCharsets.UTF_8).replace("+", "%20"));
    }

    /**
     * Percent-encodes a single path segment (RFC 3986 unreserved characters are
     * kept). Queue names and task IDs are alphanumeric plus {@code -_} so this
     * is normally a no-op, but it keeps malformed input from corrupting the URL.
     */
    private static String encodeSegment(String segment) {
        StringBuilder sb = new StringBuilder(segment.length());
        byte[] bytes = segment.getBytes(StandardCharsets.UTF_8);
        for (byte b : bytes) {
            char c = (char) (b & 0xFF);
            if ((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
                    || c == '-' || c == '_' || c == '.' || c == '~') {
                sb.append(c);
            } else {
                sb.append('%').append(String.format("%02X", b & 0xFF));
            }
        }
        return sb.toString();
    }

    /** Builder for {@link Pushq}. */
    public static final class Builder {
        private String baseUrl;
        private String apiKey;
        private Duration timeout = Duration.ofSeconds(30);

        private Builder() {
        }

        /**
         * Server base URL, e.g. {@code http://localhost:8080} when self-hosting.
         * Defaults to the hosted API, {@link Pushq#DEFAULT_BASE_URL}.
         */
        public Builder baseUrl(String baseUrl) {
            this.baseUrl = baseUrl;
            return this;
        }

        /** API key sent as {@code Authorization: Bearer <key>}. Omit for dev mode. */
        public Builder apiKey(String apiKey) {
            this.apiKey = apiKey;
            return this;
        }

        /** Connect and per-request timeout (default 30s). */
        public Builder timeout(Duration timeout) {
            this.timeout = Objects.requireNonNull(timeout, "timeout");
            return this;
        }

        public Pushq build() {
            return new Pushq(this);
        }
    }
}
