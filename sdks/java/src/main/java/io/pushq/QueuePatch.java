package io.pushq;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;

/**
 * Partial queue update for {@link Pushq#patchQueue}: only the fields you set
 * are sent. Unlike upsert, a patch can also change the queue's run state
 * ({@code running}, {@code paused}, {@code disabled}).
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public final class QueuePatch {

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

    public QueuePatch() {
    }

    private QueuePatch(Builder b) {
        this.state = b.state;
        this.rateLimits = b.rateLimits;
        this.retry = b.retry;
        this.target = b.target;
        this.deadLetterQueue = b.deadLetterQueue;
    }

    public static Builder builder() {
        return new Builder();
    }

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

    public static final class Builder {
        private String state;
        private RateLimits rateLimits;
        private RetryConfig retry;
        private Target target;
        private String deadLetterQueue;

        private Builder() {
        }

        /** One of {@code running}, {@code paused}, {@code disabled}. */
        public Builder state(String state) {
            this.state = state;
            return this;
        }

        public Builder rateLimits(RateLimits rateLimits) {
            this.rateLimits = rateLimits;
            return this;
        }

        public Builder retry(RetryConfig retry) {
            this.retry = retry;
            return this;
        }

        public Builder target(Target target) {
            this.target = target;
            return this;
        }

        public Builder deadLetterQueue(String deadLetterQueue) {
            this.deadLetterQueue = deadLetterQueue;
            return this;
        }

        public QueuePatch build() {
            return new QueuePatch(this);
        }
    }
}
