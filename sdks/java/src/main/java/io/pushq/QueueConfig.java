package io.pushq;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;

/**
 * Desired queue configuration for {@link Pushq#createQueue} and
 * {@link Pushq#upsertQueue}. Unset fields take server defaults (and, on
 * upsert, an omitted {@code target.signing_secret} keeps the stored secret).
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public final class QueueConfig {

    @JsonProperty("rate_limits")
    private RateLimits rateLimits;

    @JsonProperty("retry")
    private RetryConfig retry;

    @JsonProperty("target")
    private Target target;

    @JsonProperty("dead_letter_queue")
    private String deadLetterQueue;

    public QueueConfig() {
    }

    private QueueConfig(Builder b) {
        this.rateLimits = b.rateLimits;
        this.retry = b.retry;
        this.target = b.target;
        this.deadLetterQueue = b.deadLetterQueue;
    }

    public static Builder builder() {
        return new Builder();
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
        private RateLimits rateLimits;
        private RetryConfig retry;
        private Target target;
        private String deadLetterQueue;

        private Builder() {
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

        /** Queue that receives a copy of permanently failed tasks. */
        public Builder deadLetterQueue(String deadLetterQueue) {
            this.deadLetterQueue = deadLetterQueue;
            return this;
        }

        public QueueConfig build() {
            return new QueueConfig(this);
        }
    }
}
