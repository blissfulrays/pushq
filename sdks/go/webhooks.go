package pushq

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrVerification is wrapped by all VerifySignature failures.
var ErrVerification = errors.New("pushq webhook verification failed")

// VerifySignature verifies a pushq delivery (Standard Webhooks scheme:
// HMAC-SHA256 over "id.timestamp.payload"). Returns the webhook id
// ("queue/task_id" — stable across retries, usable as an idempotency key).
// tolerance <= 0 disables timestamp checking.
func VerifySignature(secret string, headers http.Header, payload []byte, tolerance time.Duration) (string, error) {
	return verifyAt(secret, headers, payload, tolerance, time.Now())
}

func verifyAt(secret string, headers http.Header, payload []byte, tolerance time.Duration, now time.Time) (string, error) {
	id := headers.Get("webhook-id")
	tsRaw := headers.Get("webhook-timestamp")
	sigHeader := headers.Get("webhook-signature")
	if id == "" || tsRaw == "" || sigHeader == "" {
		return "", fmt.Errorf("%w: missing webhook-id/-timestamp/-signature headers", ErrVerification)
	}
	ts, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil {
		return "", fmt.Errorf("%w: invalid timestamp", ErrVerification)
	}
	if tolerance > 0 {
		diff := now.Sub(time.Unix(ts, 0))
		if diff < 0 {
			diff = -diff
		}
		if diff > tolerance {
			return "", fmt.Errorf("%w: timestamp outside tolerance", ErrVerification)
		}
	}

	expected, err := Sign(secret, id, ts, payload)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrVerification, err)
	}
	_, expSig, _ := strings.Cut(expected, ",")
	for _, part := range strings.Fields(sigHeader) {
		version, sig, ok := strings.Cut(part, ",")
		if !ok || version != "v1" {
			continue
		}
		if hmac.Equal([]byte(sig), []byte(expSig)) {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: no matching signature", ErrVerification)
}

// Sign computes a "v1,<base64>" signature (tooling/tests).
func Sign(secret, msgID string, timestamp int64, payload []byte) (string, error) {
	key, err := signingKey(secret)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "%s.%d.", msgID, timestamp)
	mac.Write(payload)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

func signingKey(secret string) ([]byte, error) {
	if enc, ok := strings.CutPrefix(secret, "whsec_"); ok {
		key, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return nil, fmt.Errorf("invalid whsec_ secret: %w", err)
		}
		return key, nil
	}
	return []byte(secret), nil
}
