package io.pushq;

/**
 * Thrown by {@link Webhooks#verifySignature} when a webhook delivery cannot be
 * authenticated: the timestamp is outside the allowed tolerance, no signature
 * in the header matches, or the inputs are malformed.
 */
public class WebhookVerificationException extends RuntimeException {

    private static final long serialVersionUID = 1L;

    public WebhookVerificationException(String message) {
        super(message);
    }

    public WebhookVerificationException(String message, Throwable cause) {
        super(message, cause);
    }
}
