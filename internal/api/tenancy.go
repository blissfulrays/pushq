package api

import (
	"net/http"
	"strings"

	"github.com/blissfulrays/pushq/internal/api/types"
	"github.com/blissfulrays/pushq/internal/store"
)

// Team isolation (SaaS mode).
//
// A team's queues are stored as "<ns>:<name>". Queue names can't contain ":",
// so a name a client sends can never reach outside its own namespace. These
// helpers are the only place names cross that boundary: inName on the way
// in, the *Out helpers on the way out. Self-hosted callers and the operator's
// config-file keys have no namespace, so their names pass through unchanged.

// teamPrefix is "<ns>:" for a team-scoped caller, "" otherwise.
func teamPrefix(r *http.Request) string {
	if t := principal(r).Team; t != nil {
		return t.NS + ":"
	}
	return ""
}

// inName maps a client-supplied queue name to its stored name.
func inName(r *http.Request, name string) string {
	if name == "" {
		return ""
	}
	return teamPrefix(r) + name
}

// outName maps a stored queue name back to what the caller knows it as.
func outName(r *http.Request, name string) string {
	return strings.TrimPrefix(name, teamPrefix(r))
}

// scrub removes the caller's namespace from text bound for the client (error
// details quote queue names).
func scrub(r *http.Request, s string) string {
	if p := teamPrefix(r); p != "" {
		return strings.ReplaceAll(s, p, "")
	}
	return s
}

func queueOut(r *http.Request, q store.Queue) types.QueueResponse {
	resp := queueToResponse(q)
	resp.Name = outName(r, resp.Name)
	resp.DeadLetterQueue = outName(r, resp.DeadLetterQueue)
	return resp
}

func taskOut(r *http.Request, t store.Task) types.TaskResponse {
	resp := taskToResponse(t)
	resp.Queue = outName(r, resp.Queue)
	return resp
}

// mapUpsert namespaces a validated queue config's names.
func mapUpsert(r *http.Request, u *store.QueueUpsert) {
	u.Name = inName(r, u.Name)
	u.DeadLetterQueue = inName(r, u.DeadLetterQueue)
}

// validateStored validates a stored config by its client-facing names
// (validation rejects the ":" the namespace adds).
func validateStored(r *http.Request) func(store.QueueUpsert) error {
	return func(u store.QueueUpsert) error {
		u.Name = outName(r, u.Name)
		u.DeadLetterQueue = outName(r, u.DeadLetterQueue)
		return validateUpsert(u)
	}
}
