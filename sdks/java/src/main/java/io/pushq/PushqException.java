package io.pushq;

/**
 * Thrown when the pushq server returns a non-2xx response, or when the request
 * cannot be completed at all (network failure, interruption).
 *
 * <p>For HTTP errors, {@link #getStatus()} is the HTTP status and
 * {@link #getCode()} / {@link #getDetail()} are parsed from the RFC 9457
 * {@code application/problem+json} body ({@code code} is one of the server's
 * stable machine codes such as {@code not_found}, {@code already_exists},
 * {@code invalid_argument}, ...). Non-JSON error bodies are tolerated: the raw
 * body becomes {@link #getDetail()} and {@link #getCode()} is {@code null}.
 *
 * <p>For transport-level failures {@link #getStatus()} is {@code 0} and
 * {@link #getCode()} is {@code null}.
 */
public class PushqException extends RuntimeException {

    private static final long serialVersionUID = 1L;

    private final int status;
    private final String code;
    private final String detail;

    public PushqException(int status, String code, String detail) {
        super(buildMessage(status, code, detail));
        this.status = status;
        this.code = code;
        this.detail = detail;
    }

    public PushqException(String message, Throwable cause) {
        super(message, cause);
        this.status = 0;
        this.code = null;
        this.detail = message;
    }

    /** HTTP status code, or {@code 0} for transport-level failures. */
    public int getStatus() {
        return status;
    }

    /** Stable machine error code from the problem+json body, or {@code null}. */
    public String getCode() {
        return code;
    }

    /** Human-readable detail from the problem+json body (or raw body), or {@code null}. */
    public String getDetail() {
        return detail;
    }

    private static String buildMessage(int status, String code, String detail) {
        StringBuilder sb = new StringBuilder("pushq API error (HTTP ").append(status).append(')');
        if (code != null && !code.isEmpty()) {
            sb.append(" [").append(code).append(']');
        }
        if (detail != null && !detail.isEmpty()) {
            sb.append(": ").append(detail);
        }
        return sb.toString();
    }
}
