package io.pushq;

import com.fasterxml.jackson.annotation.JsonProperty;
import java.time.Instant;

/** One delivery attempt of a task. */
public final class Attempt {

    @JsonProperty("number")
    private Integer number;

    @JsonProperty("dispatch_time")
    private Instant dispatchTime;

    @JsonProperty("response_time")
    private Instant responseTime;

    @JsonProperty("response_status")
    private Integer responseStatus;

    @JsonProperty("error_kind")
    private String errorKind;

    @JsonProperty("error_snippet")
    private String errorSnippet;

    @JsonProperty("duration_ms")
    private Long durationMs;

    public Attempt() {
    }

    public Integer getNumber() {
        return number;
    }

    public Instant getDispatchTime() {
        return dispatchTime;
    }

    public Instant getResponseTime() {
        return responseTime;
    }

    public Integer getResponseStatus() {
        return responseStatus;
    }

    /**
     * One of {@code timeout}, {@code connect}, {@code dns}, {@code tls},
     * {@code non_2xx}, {@code lease_lost}, {@code config}; {@code null} on success.
     */
    public String getErrorKind() {
        return errorKind;
    }

    public String getErrorSnippet() {
        return errorSnippet;
    }

    public Long getDurationMs() {
        return durationMs;
    }
}
