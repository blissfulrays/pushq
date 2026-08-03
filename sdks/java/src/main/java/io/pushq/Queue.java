package io.pushq;

import com.fasterxml.jackson.annotation.JsonProperty;
import java.time.Instant;

/** A queue as returned by the server. */
public final class Queue {

    @JsonProperty("name")
    private String name;

    @JsonProperty("state")
    private String state;

    @JsonProperty("rate_limits")
    private RateLimits rateLimits;

    @JsonProperty("retry")
    private RetryConfig retry;

    @JsonProperty("target")
    private Target target;

    @JsonProperty("dead_letter_queue")
    private String deadLetterQueue;

    @JsonProperty("purged_at")
    private Instant purgedAt;

    @JsonProperty("created_at")
    private Instant createdAt;

    @JsonProperty("updated_at")
    private Instant updatedAt;

    public Queue() {
    }

    public String getName() {
        return name;
    }

    /** One of {@code running}, {@code paused}, {@code disabled}. */
    public String getState() {
        return state;
    }

    public RateLimits getRateLimits() {
        return rateLimits;
    }

    public RetryConfig getRetry() {
        return retry;
    }

    public Target getTarget() {
        return target;
    }

    public String getDeadLetterQueue() {
        return deadLetterQueue;
    }

    public Instant getPurgedAt() {
        return purgedAt;
    }

    public Instant getCreatedAt() {
        return createdAt;
    }

    public Instant getUpdatedAt() {
        return updatedAt;
    }

    @Override
    public String toString() {
        return "Queue{name=" + name + ", state=" + state + "}";
    }
}
