package io.pushq;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;

/**
 * Per-queue dispatch rate limits. All fields are optional; unset fields take
 * server defaults.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public final class RateLimits {

    @JsonProperty("max_dispatches_per_second")
    private Double maxDispatchesPerSecond;

    @JsonProperty("max_burst_size")
    private Integer maxBurstSize;

    @JsonProperty("max_concurrent_dispatches")
    private Integer maxConcurrentDispatches;

    public RateLimits() {
    }

    private RateLimits(Builder b) {
        this.maxDispatchesPerSecond = b.maxDispatchesPerSecond;
        this.maxBurstSize = b.maxBurstSize;
        this.maxConcurrentDispatches = b.maxConcurrentDispatches;
    }

    public static Builder builder() {
        return new Builder();
    }

    public Double getMaxDispatchesPerSecond() {
        return maxDispatchesPerSecond;
    }

    /** In responses this is the resolved effective burst. */
    public Integer getMaxBurstSize() {
        return maxBurstSize;
    }

    public Integer getMaxConcurrentDispatches() {
        return maxConcurrentDispatches;
    }

    public static final class Builder {
        private Double maxDispatchesPerSecond;
        private Integer maxBurstSize;
        private Integer maxConcurrentDispatches;

        private Builder() {
        }

        public Builder maxDispatchesPerSecond(double maxDispatchesPerSecond) {
            this.maxDispatchesPerSecond = maxDispatchesPerSecond;
            return this;
        }

        /** 0 (or unset) = auto: clamp(ceil(rate), 1, 500). */
        public Builder maxBurstSize(int maxBurstSize) {
            this.maxBurstSize = maxBurstSize;
            return this;
        }

        public Builder maxConcurrentDispatches(int maxConcurrentDispatches) {
            this.maxConcurrentDispatches = maxConcurrentDispatches;
            return this;
        }

        public RateLimits build() {
            return new RateLimits(this);
        }
    }
}
