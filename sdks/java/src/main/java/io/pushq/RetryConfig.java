package io.pushq;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;

/**
 * GCP Cloud Tasks-style retry configuration. All fields are optional; only the
 * fields you set are serialized, so a partial {@code RetryConfig} on a task
 * overrides just those fields of the queue's retry config.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public final class RetryConfig {

    @JsonProperty("max_attempts")
    private Integer maxAttempts;

    @JsonProperty("max_retry_duration_seconds")
    private Double maxRetryDurationSeconds;

    @JsonProperty("min_backoff_seconds")
    private Double minBackoffSeconds;

    @JsonProperty("max_backoff_seconds")
    private Double maxBackoffSeconds;

    @JsonProperty("max_doublings")
    private Integer maxDoublings;

    public RetryConfig() {
    }

    private RetryConfig(Builder b) {
        this.maxAttempts = b.maxAttempts;
        this.maxRetryDurationSeconds = b.maxRetryDurationSeconds;
        this.minBackoffSeconds = b.minBackoffSeconds;
        this.maxBackoffSeconds = b.maxBackoffSeconds;
        this.maxDoublings = b.maxDoublings;
    }

    public static Builder builder() {
        return new Builder();
    }

    /** Attempts including the first; -1 = unlimited. */
    public Integer getMaxAttempts() {
        return maxAttempts;
    }

    /** Max retry window measured from the first attempt; 0 = unlimited. */
    public Double getMaxRetryDurationSeconds() {
        return maxRetryDurationSeconds;
    }

    public Double getMinBackoffSeconds() {
        return minBackoffSeconds;
    }

    public Double getMaxBackoffSeconds() {
        return maxBackoffSeconds;
    }

    public Integer getMaxDoublings() {
        return maxDoublings;
    }

    public static final class Builder {
        private Integer maxAttempts;
        private Double maxRetryDurationSeconds;
        private Double minBackoffSeconds;
        private Double maxBackoffSeconds;
        private Integer maxDoublings;

        private Builder() {
        }

        /** Attempts including the first; -1 = unlimited. */
        public Builder maxAttempts(int maxAttempts) {
            this.maxAttempts = maxAttempts;
            return this;
        }

        /** Retry window from the first attempt; 0 = unlimited. */
        public Builder maxRetryDurationSeconds(double maxRetryDurationSeconds) {
            this.maxRetryDurationSeconds = maxRetryDurationSeconds;
            return this;
        }

        public Builder minBackoffSeconds(double minBackoffSeconds) {
            this.minBackoffSeconds = minBackoffSeconds;
            return this;
        }

        public Builder maxBackoffSeconds(double maxBackoffSeconds) {
            this.maxBackoffSeconds = maxBackoffSeconds;
            return this;
        }

        /** Backoff doubles this many times, then grows linearly, capped at max backoff. */
        public Builder maxDoublings(int maxDoublings) {
            this.maxDoublings = maxDoublings;
            return this;
        }

        public RetryConfig build() {
            return new RetryConfig(this);
        }
    }
}
