// Package auth implements static bearer API keys with two roles. Keys are
// compared as SHA-256 digests in constant time.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/blissfulrays/pushq/internal/config"
)

type Role int

const (
	RoleNone Role = iota
	RoleWriter
	RoleAdmin
)

type Auth struct {
	keys []keyEntry
}

type keyEntry struct {
	hash [32]byte
	role Role
}

func New(keys []config.APIKey) *Auth {
	a := &Auth{}
	for _, k := range keys {
		role := RoleWriter
		if k.Role == config.RoleAdmin {
			role = RoleAdmin
		}
		a.keys = append(a.keys, keyEntry{hash: sha256.Sum256([]byte(k.Key)), role: role})
	}
	return a
}

// Open reports whether no keys are configured (auth disabled).
func (a *Auth) Open() bool { return len(a.keys) == 0 }

// Authenticate resolves the request's role. With no keys configured every
// request is admin.
func (a *Auth) Authenticate(r *http.Request) Role {
	if a.Open() {
		return RoleAdmin
	}
	h := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || token == "" {
		return RoleNone
	}
	sum := sha256.Sum256([]byte(token))
	role := RoleNone
	// Scan every key regardless of match so timing does not leak which (if
	// any) entry matched.
	for _, k := range a.keys {
		if subtle.ConstantTimeCompare(sum[:], k.hash[:]) == 1 {
			role = k.role
		}
	}
	return role
}
