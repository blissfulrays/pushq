package io.pushq;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import java.time.Instant;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.Map;

/**
 * A task to enqueue. Build with {@link #builder()}:
 *
 * <pre>{@code
 * TaskCreate task = TaskCreate.builder()
 *     .taskId("order-1234")                 // optional, for dedup
 *     .path("/hooks/charge")                // or .url("https://...")
 *     .jsonBody(Map.of("order", 1234))      // or .body(byte[])
 *     .delaySeconds(30)                     // or .scheduleTime(Instant)
 *     .retry(RetryConfig.builder().maxAttempts(5).build())
 *     .build();
 * }</pre>
 *
 * <p>Only fields you set are serialized. Mutually exclusive pairs
 * ({@code url}/{@code path}, {@code body}/{@code jsonBody},
 * {@code scheduleTime}/{@code delaySeconds}) are validated at
 * {@link Builder#build()}.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public final class TaskCreate {

    @JsonProperty("task_id")
    private final String taskId;

    @JsonProperty("url")
    private final String url;

    @JsonProperty("path")
    private final String path;

    @JsonProperty("method")
    private final String method;

    @JsonProperty("headers")
    private final Map<String, String> headers;

    @JsonProperty("body_base64")
    private final String bodyBase64;

    @JsonProperty("json_body")
    private final Object jsonBody;

    @JsonProperty("schedule_time")
    private final Instant scheduleTime;

    @JsonProperty("delay_seconds")
    private final Double delaySeconds;

    @JsonProperty("dispatch_deadline_seconds")
    private final Double dispatchDeadlineSeconds;

    @JsonProperty("retry")
    private final RetryConfig retry;

    private TaskCreate(Builder b) {
        this.taskId = b.taskId;
        this.url = b.url;
        this.path = b.path;
        this.method = b.method;
        this.headers = b.headers;
        this.bodyBase64 = b.bodyBase64;
        this.jsonBody = b.jsonBody;
        this.scheduleTime = b.scheduleTime;
        this.delaySeconds = b.delaySeconds;
        this.dispatchDeadlineSeconds = b.dispatchDeadlineSeconds;
        this.retry = b.retry;
    }

    public static Builder builder() {
        return new Builder();
    }

    public String getTaskId() {
        return taskId;
    }

    public String getUrl() {
        return url;
    }

    public String getPath() {
        return path;
    }

    public String getMethod() {
        return method;
    }

    public Map<String, String> getHeaders() {
        return headers;
    }

    public String getBodyBase64() {
        return bodyBase64;
    }

    public Object getJsonBody() {
        return jsonBody;
    }

    public Instant getScheduleTime() {
        return scheduleTime;
    }

    public Double getDelaySeconds() {
        return delaySeconds;
    }

    public Double getDispatchDeadlineSeconds() {
        return dispatchDeadlineSeconds;
    }

    public RetryConfig getRetry() {
        return retry;
    }

    public static final class Builder {
        private String taskId;
        private String url;
        private String path;
        private String method;
        private Map<String, String> headers;
        private String bodyBase64;
        private Object jsonBody;
        private boolean hasJsonBody;
        private Instant scheduleTime;
        private Double delaySeconds;
        private Double dispatchDeadlineSeconds;
        private RetryConfig retry;

        private Builder() {
        }

        /**
         * Client-chosen ID for deduplication; the server answers 409
         * {@code already_exists} on collision. Omitted = server-generated UUIDv7.
         */
        public Builder taskId(String taskId) {
            this.taskId = taskId;
            return this;
        }

        /** Absolute http(s) target URL. Exclusive with {@link #path}. */
        public Builder url(String url) {
            this.url = url;
            return this;
        }

        /** Path resolved against the queue's {@code target.base_url}. Exclusive with {@link #url}. */
        public Builder path(String path) {
            this.path = path;
            return this;
        }

        /** HTTP method (GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS). Default POST. */
        public Builder method(String method) {
            this.method = method;
            return this;
        }

        /** Replaces all task headers. */
        public Builder headers(Map<String, String> headers) {
            this.headers = headers == null ? null : new LinkedHashMap<>(headers);
            return this;
        }

        /** Adds a single task header. */
        public Builder header(String name, String value) {
            if (this.headers == null) {
                this.headers = new LinkedHashMap<>();
            }
            this.headers.put(name, value);
            return this;
        }

        /**
         * Raw request body (sent base64-encoded as {@code body_base64}).
         * Exclusive with {@link #jsonBody}. POST/PUT/PATCH only.
         */
        public Builder body(byte[] body) {
            this.bodyBase64 = body == null ? null : Base64.getEncoder().encodeToString(body);
            return this;
        }

        /**
         * Arbitrary object serialized with Jackson and sent as {@code json_body}
         * (delivered with {@code Content-Type: application/json}).
         * Exclusive with {@link #body}.
         */
        public Builder jsonBody(Object jsonBody) {
            this.jsonBody = jsonBody;
            this.hasJsonBody = true;
            return this;
        }

        /** Absolute dispatch time. Exclusive with {@link #delaySeconds}. Default = now. */
        public Builder scheduleTime(Instant scheduleTime) {
            this.scheduleTime = scheduleTime;
            return this;
        }

        /** Dispatch delay from now, in seconds. Exclusive with {@link #scheduleTime}. */
        public Builder delaySeconds(double delaySeconds) {
            this.delaySeconds = delaySeconds;
            return this;
        }

        /** Per-attempt HTTP timeout, 15-1800 seconds (default 600). */
        public Builder dispatchDeadlineSeconds(double dispatchDeadlineSeconds) {
            this.dispatchDeadlineSeconds = dispatchDeadlineSeconds;
            return this;
        }

        /** Per-task retry override (any subset of fields) of the queue's retry config. */
        public Builder retry(RetryConfig retry) {
            this.retry = retry;
            return this;
        }

        public TaskCreate build() {
            if (url != null && path != null) {
                throw new IllegalStateException("TaskCreate: set either url or path, not both");
            }
            if (bodyBase64 != null && hasJsonBody) {
                throw new IllegalStateException("TaskCreate: set either body or jsonBody, not both");
            }
            if (scheduleTime != null && delaySeconds != null) {
                throw new IllegalStateException("TaskCreate: set either scheduleTime or delaySeconds, not both");
            }
            return new TaskCreate(this);
        }
    }
}
