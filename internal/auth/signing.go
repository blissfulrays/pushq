package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Standard Webhooks (https://www.standardwebhooks.com) signing: signature is
// HMAC-SHA256 over "{msg_id}.{timestamp}.{payload}" keyed by the decoded
// secret. Wire format matches the spec so existing standard-webhooks
// libraries verify pushq deliveries out of the box.

const SecretPrefix = "whsec_"

// signingKey returns the raw HMAC key for a secret. "whsec_"-prefixed secrets
// are base64-decoded per the spec; anything else is used as raw bytes.
func signingKey(secret string) ([]byte, error) {
	if enc, ok := strings.CutPrefix(secret, SecretPrefix); ok {
		key, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return nil, fmt.Errorf("invalid whsec_ secret: %w", err)
		}
		return key, nil
	}
	return []byte(secret), nil
}

// Sign produces the webhook-signature header value ("v1,<base64>").
func Sign(secret, msgID string, ts time.Time, payload []byte) (string, error) {
	key, err := signingKey(secret)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "%s.%d.", msgID, ts.Unix())
	mac.Write(payload)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// SignedHeaders returns the three Standard Webhooks headers for a delivery.
func SignedHeaders(secret, msgID string, ts time.Time, payload []byte) (map[string]string, error) {
	sig, err := Sign(secret, msgID, ts, payload)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"webhook-id":        msgID,
		"webhook-timestamp": strconv.FormatInt(ts.Unix(), 10),
		"webhook-signature": sig,
	}, nil
}

// VerifySignature checks a webhook-signature header (space-separated list of
// "v1,<sig>" entries) in constant time. Used by tests and by receiver-side
// tooling.
func VerifySignature(secret, msgID string, ts time.Time, payload []byte, header string, tolerance time.Duration, now time.Time) error {
	if tolerance > 0 {
		diff := now.Sub(ts)
		if diff < 0 {
			diff = -diff
		}
		if diff > tolerance {
			return fmt.Errorf("timestamp outside tolerance")
		}
	}
	expected, err := Sign(secret, msgID, ts, payload)
	if err != nil {
		return err
	}
	_, expSig, _ := strings.Cut(expected, ",")
	for _, part := range strings.Fields(header) {
		version, sig, ok := strings.Cut(part, ",")
		if !ok || version != "v1" {
			continue
		}
		if hmac.Equal([]byte(sig), []byte(expSig)) {
			return nil
		}
	}
	return fmt.Errorf("no matching signature")
}
