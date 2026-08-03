package io.pushq;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import java.util.LinkedHashMap;
import java.util.Map;

/**
 * Queue-level dispatch target configuration.
 *
 * <p>When writing (create/upsert/patch), {@link Builder#signingSecret} sets the
 * Standard Webhooks secret ({@code whsec_...}); on upsert, omitting it keeps
 * the stored secret and {@code ""} clears it. The secret is never returned:
 * responses carry {@link #getHasSigningSecret()} instead.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public final class Target {

    @JsonProperty("base_url")
    private String baseUrl;

    @JsonProperty("method")
    private String method;

    @JsonProperty("headers")
    private Map<String, String> headers;

    /** Write-only: never present in responses. */
    @JsonProperty("signing_secret")
    private String signingSecret;

    /** Read-only: only present in responses. */
    @JsonProperty("has_signing_secret")
    private Boolean hasSigningSecret;

    public Target() {
    }

    private Target(Builder b) {
        this.baseUrl = b.baseUrl;
        this.method = b.method;
        this.headers = b.headers;
        this.signingSecret = b.signingSecret;
    }

    public static Builder builder() {
        return new Builder();
    }

    /** Absolute http(s) URL; resolves tasks that use a relative {@code path}. */
    public String getBaseUrl() {
        return baseUrl;
    }

    /** When set, overrides every task's method. */
    public String getMethod() {
        return method;
    }

    /** Merged over task headers at dispatch (queue wins). */
    public Map<String, String> getHeaders() {
        return headers;
    }

    /** The signing secret sent in a request; always {@code null} in responses. */
    public String getSigningSecret() {
        return signingSecret;
    }

    /** Response-only: whether the queue has a signing secret configured. */
    public Boolean getHasSigningSecret() {
        return hasSigningSecret;
    }

    public static final class Builder {
        private String baseUrl;
        private String method;
        private Map<String, String> headers;
        private String signingSecret;

        private Builder() {
        }

        public Builder baseUrl(String baseUrl) {
            this.baseUrl = baseUrl;
            return this;
        }

        public Builder method(String method) {
            this.method = method;
            return this;
        }

        /** Replaces all target headers. */
        public Builder headers(Map<String, String> headers) {
            this.headers = headers == null ? null : new LinkedHashMap<>(headers);
            return this;
        }

        /** Adds a single target header. */
        public Builder header(String name, String value) {
            if (this.headers == null) {
                this.headers = new LinkedHashMap<>();
            }
            this.headers.put(name, value);
            return this;
        }

        /** Standard Webhooks secret ({@code whsec_...}); {@code ""} clears it on upsert. */
        public Builder signingSecret(String signingSecret) {
            this.signingSecret = signingSecret;
            return this;
        }

        public Target build() {
            return new Target(this);
        }
    }
}
