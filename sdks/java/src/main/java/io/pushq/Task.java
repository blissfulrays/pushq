package io.pushq;

import com.fasterxml.jackson.annotation.JsonProperty;
import java.time.Instant;
import java.util.Map;

/** A task as returned by the server. */
public final class Task {

    @JsonProperty("id")
    private String id;

    @JsonProperty("queue")
    private String queue;

    @JsonProperty("state")
    private String state;

    @JsonProperty("url")
    private String url;

    @JsonProperty("method")
    private String method;

    @JsonProperty("headers")
    private Map<String, String> headers;

    @JsonProperty("body_base64")
    private String bodyBase64;

    @JsonProperty("schedule_time")
    private Instant scheduleTime;

    @JsonProperty("created_at")
    private Instant createdAt;

    @JsonProperty("dispatch_deadline_seconds")
    private Double dispatchDeadlineSeconds;

    @JsonProperty("retry")
    private RetryConfig retry;

    @JsonProperty("dispatch_count")
    private Integer dispatchCount;

    @JsonProperty("response_count")
    private Integer responseCount;

    @JsonProperty("first_attempt_at")
    private Instant firstAttemptAt;

    @JsonProperty("finished_at")
    private Instant finishedAt;

    @JsonProperty("last_attempt")
    private LastAttempt lastAttempt;

    public Task() {
    }

    public String getId() {
        return id;
    }

    public String getQueue() {
        return queue;
    }

    /** One of {@code pending}, {@code running}, {@code succeeded}, {@code failed}. */
    public String getState() {
        return state;
    }

    public String getUrl() {
        return url;
    }

    public String getMethod() {
        return method;
    }

    public Map<String, String> getHeaders() {
        return headers;
    }

    /** Raw body (base64); only populated with {@code view=full}. */
    public String getBodyBase64() {
        return bodyBase64;
    }

    public Instant getScheduleTime() {
        return scheduleTime;
    }

    public Instant getCreatedAt() {
        return createdAt;
    }

    public Double getDispatchDeadlineSeconds() {
        return dispatchDeadlineSeconds;
    }

    public RetryConfig getRetry() {
        return retry;
    }

    public Integer getDispatchCount() {
        return dispatchCount;
    }

    public Integer getResponseCount() {
        return responseCount;
    }

    public Instant getFirstAttemptAt() {
        return firstAttemptAt;
    }

    public Instant getFinishedAt() {
        return finishedAt;
    }

    public LastAttempt getLastAttempt() {
        return lastAttempt;
    }

    @Override
    public String toString() {
        return "Task{id=" + id + ", queue=" + queue + ", state=" + state + "}";
    }

    /** Summary of the most recent delivery attempt. */
    public static final class LastAttempt {

        @JsonProperty("at")
        private Instant at;

        @JsonProperty("status")
        private Integer status;

        @JsonProperty("error")
        private String error;

        public LastAttempt() {
        }

        public Instant getAt() {
            return at;
        }

        /** HTTP status of the attempt, if a response was received. */
        public Integer getStatus() {
            return status;
        }

        public String getError() {
            return error;
        }
    }
}
