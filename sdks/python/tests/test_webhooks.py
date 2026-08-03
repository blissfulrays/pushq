import pytest

from pushq import WebhookVerificationError, sign, verify_signature

SECRET = "whsec_dGVzdC1zZWNyZXQ="
ID = "orders/job-1"
TS = 1_700_000_000
PAYLOAD = b'{"hello": "world"}'


def headers_for(payload: bytes, at: int = TS) -> dict:
    return {
        "Webhook-Id": ID,
        "Webhook-Timestamp": str(at),
        "Webhook-Signature": sign(SECRET, ID, at, payload),
    }


def test_round_trip():
    got = verify_signature(SECRET, headers_for(PAYLOAD), PAYLOAD, now=TS)
    assert got == ID


def test_tampered_payload_fails():
    with pytest.raises(WebhookVerificationError, match="no matching signature"):
        verify_signature(SECRET, headers_for(PAYLOAD), PAYLOAD + b"x", now=TS)


def test_wrong_secret_fails():
    with pytest.raises(WebhookVerificationError):
        verify_signature("whsec_d3Jvbmc=", headers_for(PAYLOAD), PAYLOAD, now=TS)


def test_stale_timestamp_fails():
    with pytest.raises(WebhookVerificationError, match="tolerance"):
        verify_signature(SECRET, headers_for(PAYLOAD), PAYLOAD, now=TS + 3600)


def test_multiple_signatures_one_valid():
    h = headers_for(PAYLOAD)
    h["Webhook-Signature"] = "v1,AAAA " + h["Webhook-Signature"]
    assert verify_signature(SECRET, h, PAYLOAD, now=TS) == ID


def test_raw_secret():
    h = {
        "webhook-id": ID,
        "webhook-timestamp": str(TS),
        "webhook-signature": sign("raw-secret", ID, TS, PAYLOAD),
    }
    assert verify_signature("raw-secret", h, PAYLOAD, now=TS) == ID


def test_missing_headers():
    with pytest.raises(WebhookVerificationError, match="missing"):
        verify_signature(SECRET, {}, PAYLOAD, now=TS)
