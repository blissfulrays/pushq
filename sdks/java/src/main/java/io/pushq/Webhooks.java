package io.pushq;

import java.nio.charset.StandardCharsets;
import java.security.InvalidKeyException;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Clock;
import java.util.Base64;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

/**
 * Standard Webhooks signing and verification for pushq deliveries.
 *
 * <p>Queues configured with a {@code target.signing_secret} sign every
 * delivery with the headers {@code webhook-id}, {@code webhook-timestamp}
 * (unix seconds) and {@code webhook-signature} (a space-separated list of
 * {@code v1,<base64>} entries). Verify them in your handler:
 *
 * <pre>{@code
 * Webhooks.verifySignature(secret, msgId, timestamp, rawBody, signatureHeader, 300);
 * }</pre>
 *
 * <p>The signature is HMAC-SHA256 over {@code msgId + "." + timestamp + "." + payload},
 * keyed by the base64-decoded secret after stripping the {@code whsec_} prefix
 * (secrets without the prefix are used as raw UTF-8 bytes).
 */
public final class Webhooks {

    private static final String HMAC_SHA256 = "HmacSHA256";
    private static final String VERSION_PREFIX = "v1,";

    private Webhooks() {
    }

    /**
     * Computes the Standard Webhooks signature for the given message.
     *
     * @return the signature entry in header form: {@code v1,<base64>}
     */
    public static String sign(String secret, String msgId, long timestampSeconds, byte[] payload) {
        byte[] mac = hmac(secretBytes(secret), signedContent(msgId, timestampSeconds, payload));
        return VERSION_PREFIX + Base64.getEncoder().encodeToString(mac);
    }

    /**
     * Verifies a webhook delivery against the system clock.
     *
     * @param secret            the queue's signing secret ({@code whsec_...} or raw)
     * @param msgId             the {@code webhook-id} header value
     * @param timestampSeconds  the {@code webhook-timestamp} header value (unix seconds)
     * @param payload           the raw, unmodified request body bytes
     * @param signatureHeader   the {@code webhook-signature} header value
     *                          (space-separated {@code v1,<base64>} entries)
     * @param toleranceSeconds  max allowed clock skew between the timestamp and now
     * @throws WebhookVerificationException if the timestamp is outside the
     *                                      tolerance or no signature matches
     */
    public static void verifySignature(String secret, String msgId, long timestampSeconds,
                                       byte[] payload, String signatureHeader, long toleranceSeconds) {
        verifySignature(secret, msgId, timestampSeconds, payload, signatureHeader, toleranceSeconds,
                Clock.systemUTC());
    }

    /**
     * Same as {@link #verifySignature(String, String, long, byte[], String, long)}
     * but with an injectable {@link Clock} (useful in tests).
     */
    public static void verifySignature(String secret, String msgId, long timestampSeconds,
                                       byte[] payload, String signatureHeader, long toleranceSeconds,
                                       Clock clock) {
        long nowSeconds = Math.floorDiv(clock.millis(), 1000L);
        if (Math.abs(nowSeconds - timestampSeconds) > toleranceSeconds) {
            throw new WebhookVerificationException(
                    "webhook timestamp " + timestampSeconds + " outside tolerance of "
                            + toleranceSeconds + "s (now: " + nowSeconds + ")");
        }
        if (signatureHeader == null || signatureHeader.trim().isEmpty()) {
            throw new WebhookVerificationException("missing webhook signature header");
        }

        byte[] expected = hmac(secretBytes(secret), signedContent(msgId, timestampSeconds, payload));

        for (String entry : signatureHeader.trim().split("\\s+")) {
            if (!entry.startsWith(VERSION_PREFIX)) {
                continue; // unknown scheme version; ignore
            }
            byte[] candidate;
            try {
                candidate = Base64.getDecoder().decode(entry.substring(VERSION_PREFIX.length()));
            } catch (IllegalArgumentException malformed) {
                continue; // not valid base64; ignore this entry
            }
            if (MessageDigest.isEqual(expected, candidate)) {
                return; // verified
            }
        }
        throw new WebhookVerificationException("no matching webhook signature found");
    }

    private static byte[] signedContent(String msgId, long timestampSeconds, byte[] payload) {
        byte[] prefix = (msgId + "." + timestampSeconds + ".").getBytes(StandardCharsets.UTF_8);
        byte[] content = new byte[prefix.length + (payload == null ? 0 : payload.length)];
        System.arraycopy(prefix, 0, content, 0, prefix.length);
        if (payload != null) {
            System.arraycopy(payload, 0, content, prefix.length, payload.length);
        }
        return content;
    }

    private static byte[] secretBytes(String secret) {
        if (secret == null || secret.isEmpty()) {
            throw new WebhookVerificationException("webhook secret is empty");
        }
        if (secret.startsWith("whsec_")) {
            try {
                return Base64.getDecoder().decode(secret.substring("whsec_".length()));
            } catch (IllegalArgumentException e) {
                throw new WebhookVerificationException("webhook secret is not valid base64", e);
            }
        }
        return secret.getBytes(StandardCharsets.UTF_8);
    }

    private static byte[] hmac(byte[] key, byte[] content) {
        try {
            Mac mac = Mac.getInstance(HMAC_SHA256);
            mac.init(new SecretKeySpec(key, HMAC_SHA256));
            return mac.doFinal(content);
        } catch (NoSuchAlgorithmException | InvalidKeyException e) {
            throw new WebhookVerificationException("unable to compute HMAC-SHA256: " + e.getMessage(), e);
        }
    }
}
