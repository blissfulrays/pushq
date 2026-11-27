package accounts

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// Passwords are hashed with PBKDF2-HMAC-SHA256 (stdlib, FIPS-approved) at the
// OWASP-recommended work factor. Encoded as
// "pbkdf2-sha256$<iterations>$<salt b64>$<hash b64>" so the factor can be
// raised later without invalidating existing hashes.
// pbkdf2Iterations applies to new hashes; verification uses the count stored
// in each hash.
var pbkdf2Iterations = 600_000

// SetPasswordIterationsForTesting lowers the hashing work factor so tests that
// create many accounts stay fast. Never call it outside tests.
func SetPasswordIterationsForTesting(n int) { pbkdf2Iterations = n }

const (
	saltLen = 16
	hashLen = 32

	MinPasswordLen = 8
	MaxPasswordLen = 256
)

var b64 = base64.RawStdEncoding

func hashPassword(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, pbkdf2Iterations, hashLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Iterations, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func verifyPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return false
	}
	salt, err1 := b64.DecodeString(parts[2])
	want, err2 := b64.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is verified against when the email is unknown, so a login
// attempt takes the same time whether or not the account exists.
var dummyHash = sync.OnceValue(func() string {
	h, err := hashPassword("pushq-timing-equalizer")
	if err != nil {
		panic(err)
	}
	return h
})

func validatePassword(pw string) error {
	n := utf8.RuneCountInString(pw)
	if n < MinPasswordLen {
		return invalid("password must be at least %d characters", MinPasswordLen)
	}
	if len(pw) > MaxPasswordLen {
		return invalid("password must be at most %d bytes", MaxPasswordLen)
	}
	return nil
}
