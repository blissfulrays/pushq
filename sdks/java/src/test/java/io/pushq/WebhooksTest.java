package io.pushq;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.Base64;
import org.junit.jupiter.api.Test;

class WebhooksTest {

    private static final String SECRET =
            "whsec_" + Base64.getEncoder().encodeToString("a-very-secret-key".getBytes(StandardCharsets.UTF_8));
    private static final byte[] PAYLOAD = "{\"task_id\":\"t-1\",\"n\":42}".getBytes(StandardCharsets.UTF_8);

    private static long nowSeconds() {
        return System.currentTimeMillis() / 1000L;
    }

    @Test
    void signVerifyRoundTrip() {
        long ts = nowSeconds();
        String sig = Webhooks.sign(SECRET, "msg_1", ts, PAYLOAD);
        assertTrue(sig.startsWith("v1,"), "signature must be a v1,<base64> entry");
        assertDoesNotThrow(() -> Webhooks.verifySignature(SECRET, "msg_1", ts, PAYLOAD, sig, 300));
    }

    @Test
    void rawSecretWithoutPrefixRoundTrips() {
        long ts = nowSeconds();
        String sig = Webhooks.sign("plain-raw-secret", "msg_raw", ts, PAYLOAD);
        assertDoesNotThrow(() -> Webhooks.verifySignature("plain-raw-secret", "msg_raw", ts, PAYLOAD, sig, 300));
    }

    @Test
    void tamperedPayloadFails() {
        long ts = nowSeconds();
        String sig = Webhooks.sign(SECRET, "msg_1", ts, PAYLOAD);
        byte[] tampered = "{\"task_id\":\"t-1\",\"n\":43}".getBytes(StandardCharsets.UTF_8);
        assertThrows(WebhookVerificationException.class,
                () -> Webhooks.verifySignature(SECRET, "msg_1", ts, tampered, sig, 300));
    }

    @Test
    void tamperedMsgIdFails() {
        long ts = nowSeconds();
        String sig = Webhooks.sign(SECRET, "msg_1", ts, PAYLOAD);
        assertThrows(WebhookVerificationException.class,
                () -> Webhooks.verifySignature(SECRET, "msg_2", ts, PAYLOAD, sig, 300));
    }

    @Test
    void wrongSecretFails() {
        long ts = nowSeconds();
        String sig = Webhooks.sign(SECRET, "msg_1", ts, PAYLOAD);
        String otherSecret =
                "whsec_" + Base64.getEncoder().encodeToString("another-key".getBytes(StandardCharsets.UTF_8));
        assertThrows(WebhookVerificationException.class,
                () -> Webhooks.verifySignature(otherSecret, "msg_1", ts, PAYLOAD, sig, 300));
    }

    @Test
    void timestampOutsideToleranceFails() {
        long ts = 1_700_000_000L;
        String sig = Webhooks.sign(SECRET, "msg_1", ts, PAYLOAD);

        // 301 seconds after the timestamp with 300s tolerance: rejected.
        Clock late = Clock.fixed(Instant.ofEpochSecond(ts + 301), ZoneOffset.UTC);
        assertThrows(WebhookVerificationException.class,
                () -> Webhooks.verifySignature(SECRET, "msg_1", ts, PAYLOAD, sig, 300, late));

        // Timestamps from the future are equally rejected.
        Clock early = Clock.fixed(Instant.ofEpochSecond(ts - 301), ZoneOffset.UTC);
        assertThrows(WebhookVerificationException.class,
                () -> Webhooks.verifySignature(SECRET, "msg_1", ts, PAYLOAD, sig, 300, early));

        // Control: inside the tolerance the same delivery verifies.
        Clock onTime = Clock.fixed(Instant.ofEpochSecond(ts + 299), ZoneOffset.UTC);
        assertDoesNotThrow(
                () -> Webhooks.verifySignature(SECRET, "msg_1", ts, PAYLOAD, sig, 300, onTime));
    }

    @Test
    void multipleSpaceSeparatedSignaturesOneMatchPasses() {
        long ts = nowSeconds();
        String good = Webhooks.sign(SECRET, "msg_1", ts, PAYLOAD);
        String bogus = "v1," + Base64.getEncoder().encodeToString("garbage-sig".getBytes(StandardCharsets.UTF_8));
        String unknownVersion = "v2,AAAA";
        String header = bogus + " " + unknownVersion + " " + good;
        assertDoesNotThrow(() -> Webhooks.verifySignature(SECRET, "msg_1", ts, PAYLOAD, header, 300));
    }

    @Test
    void noMatchingSignatureAmongManyFails() {
        long ts = nowSeconds();
        String bogus1 = "v1," + Base64.getEncoder().encodeToString("garbage-1".getBytes(StandardCharsets.UTF_8));
        String bogus2 = "v1,not-base64!!";
        assertThrows(WebhookVerificationException.class,
                () -> Webhooks.verifySignature(SECRET, "msg_1", ts, PAYLOAD, bogus1 + " " + bogus2, 300));
    }
}
