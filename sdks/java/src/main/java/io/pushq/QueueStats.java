package io.pushq;

import com.fasterxml.jackson.annotation.JsonProperty;
import java.time.Instant;

/** Live queue counters and recent dispatch/error rates. */
public final class QueueStats {

    @JsonProperty("state")
    private String state;

    @JsonProperty("pending")
    private Long pending;

    @JsonProperty("running")
    private Long running;

    @JsonProperty("succeeded")
    private Long succeeded;

    @JsonProperty("failed")
    private Long failed;

    @JsonProperty("scheduled_future")
    private Long scheduledFuture;

    @JsonProperty("oldest_due_at")
    private Instant oldestDueAt;

    @JsonProperty("oldest_due_age_seconds")
    private Double oldestDueAgeSeconds;

    @JsonProperty("dispatched_last_1m")
    private Long dispatchedLast1m;

    @JsonProperty("errors_last_1m")
    private Long errorsLast1m;

    @JsonProperty("dispatched_last_1h")
    private Long dispatchedLast1h;

    @JsonProperty("errors_last_1h")
    private Long errorsLast1h;

    @JsonProperty("last_purge")
    private Instant lastPurge;

    public QueueStats() {
    }

    public String getState() {
        return state;
    }

    /** Tasks due now or overdue. */
    public Long getPending() {
        return pending;
    }

    public Long getRunning() {
        return running;
    }

    /** Succeeded tasks within the retention window. */
    public Long getSucceeded() {
        return succeeded;
    }

    /** Failed tasks within the retention window. */
    public Long getFailed() {
        return failed;
    }

    public Long getScheduledFuture() {
        return scheduledFuture;
    }

    public Instant getOldestDueAt() {
        return oldestDueAt;
    }

    public Double getOldestDueAgeSeconds() {
        return oldestDueAgeSeconds;
    }

    public Long getDispatchedLast1m() {
        return dispatchedLast1m;
    }

    public Long getErrorsLast1m() {
        return errorsLast1m;
    }

    public Long getDispatchedLast1h() {
        return dispatchedLast1h;
    }

    public Long getErrorsLast1h() {
        return errorsLast1h;
    }

    public Instant getLastPurge() {
        return lastPurge;
    }
}
