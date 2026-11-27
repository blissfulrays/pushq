package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeWorkOS implements the slice of the WorkOS API pushq's SaaS mode uses,
// in memory: AuthKit code exchange, organizations, memberships, invitations.
type fakeWorkOS struct {
	*httptest.Server
	apiKey string

	mu          sync.Mutex
	seq         int
	users       map[string]fwUser   // id -> user
	codes       map[string]string   // auth code -> user id
	orgs        map[string]string   // id -> name
	memberships map[string]fwMember // id -> membership
	invitations map[string]fwInvite // id -> invitation
}

type fwUser struct {
	ID, Email, First string
	Verified         bool
}

type fwMember struct{ ID, UserID, OrgID, Role string }

type fwInvite struct{ ID, Email, OrgID, State, Role string }

func newFakeWorkOS(t *testing.T) *fakeWorkOS {
	f := &fakeWorkOS{apiKey: "sk_test_fake", users: map[string]fwUser{}, codes: map[string]string{},
		orgs: map[string]string{}, memberships: map[string]fwMember{}, invitations: map[string]fwInvite{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeWorkOS) id(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s_%04d", prefix, f.seq)
}

// addUser registers a WorkOS user and returns a one-time sign-in code for them.
func (f *fakeWorkOS) addUser(email, first string) (userID, code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := fwUser{ID: f.id("user"), Email: email, First: first, Verified: true}
	f.users[u.ID] = u
	return u.ID, f.newCodeLocked(u.ID)
}

func (f *fakeWorkOS) newCode(userID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.newCodeLocked(userID)
}

func (f *fakeWorkOS) newCodeLocked(userID string) string {
	c := f.id("code")
	f.codes[c] = userID
	return c
}

// acceptInvite simulates the invitee clicking WorkOS's email link.
func (f *fakeWorkOS) acceptInvite(email, userID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, inv := range f.invitations {
		if inv.Email == email && inv.State == "pending" {
			inv.State = "accepted"
			f.invitations[id] = inv
			m := fwMember{ID: f.id("om"), UserID: userID, OrgID: inv.OrgID, Role: firstNonEmpty(inv.Role, "member")}
			f.memberships[m.ID] = m
		}
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func (f *fakeWorkOS) userJSON(u fwUser) map[string]any {
	return map[string]any{"id": u.ID, "email": u.Email, "email_verified": u.Verified, "first_name": u.First, "last_name": ""}
}

func (f *fakeWorkOS) memberJSON(m fwMember) map[string]any {
	return map[string]any{"id": m.ID, "user_id": m.UserID, "organization_id": m.OrgID, "status": "active",
		"role": map[string]any{"slug": m.Role}}
}

func (f *fakeWorkOS) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	str := func(k string) string { s, _ := body[k].(string); return s }
	path := r.URL.Path

	if path == "/user_management/authenticate" {
		if str("client_secret") != f.apiKey {
			reply(401, map[string]any{"error": "invalid_client"})
			return
		}
		uid, ok := f.codes[str("code")]
		if !ok {
			reply(400, map[string]any{"error": "invalid_grant", "error_description": "bad code"})
			return
		}
		delete(f.codes, str("code"))
		reply(200, map[string]any{"user": f.userJSON(f.users[uid]), "access_token": "at", "refresh_token": "rt"})
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.apiKey {
		reply(401, map[string]any{"message": "unauthorized"})
		return
	}
	switch {
	case r.Method == "POST" && path == "/organizations":
		id := f.id("org")
		f.orgs[id] = str("name")
		reply(201, map[string]any{"id": id, "name": str("name")})
	case strings.HasPrefix(path, "/organizations/"):
		id := strings.TrimPrefix(path, "/organizations/")
		if _, ok := f.orgs[id]; !ok {
			reply(404, map[string]any{"message": "not found"})
			return
		}
		if r.Method == "PUT" {
			f.orgs[id] = str("name")
		}
		reply(200, map[string]any{"id": id, "name": f.orgs[id]})
	case strings.HasPrefix(path, "/user_management/users/"):
		u, ok := f.users[strings.TrimPrefix(path, "/user_management/users/")]
		if !ok {
			reply(404, map[string]any{"message": "not found"})
			return
		}
		reply(200, f.userJSON(u))
	case path == "/user_management/organization_memberships" && r.Method == "GET":
		q := r.URL.Query()
		data := []any{}
		for _, m := range f.memberships {
			if (q.Get("user_id") == "" || q.Get("user_id") == m.UserID) && (q.Get("organization_id") == "" || q.Get("organization_id") == m.OrgID) {
				data = append(data, f.memberJSON(m))
			}
		}
		reply(200, map[string]any{"data": data, "list_metadata": map[string]any{}})
	case path == "/user_management/organization_memberships" && r.Method == "POST":
		m := fwMember{ID: f.id("om"), UserID: str("user_id"), OrgID: str("organization_id"), Role: firstNonEmpty(str("role_slug"), "member")}
		f.memberships[m.ID] = m
		reply(201, f.memberJSON(m))
	case strings.HasPrefix(path, "/user_management/organization_memberships/"):
		id := strings.TrimPrefix(path, "/user_management/organization_memberships/")
		m, ok := f.memberships[id]
		if !ok {
			reply(404, map[string]any{"message": "not found"})
			return
		}
		if r.Method == "DELETE" {
			delete(f.memberships, id)
			w.WriteHeader(202)
			return
		}
		m.Role = str("role_slug")
		f.memberships[id] = m
		reply(200, f.memberJSON(m))
	case path == "/user_management/invitations" && r.Method == "POST":
		inv := fwInvite{ID: f.id("invitation"), Email: str("email"), OrgID: str("organization_id"), State: "pending", Role: str("role_slug")}
		f.invitations[inv.ID] = inv
		reply(201, map[string]any{"id": inv.ID, "email": inv.Email, "state": inv.State, "organization_id": inv.OrgID,
			"expires_at": time.Now().Add(7 * 24 * time.Hour).Format(time.RFC3339), "created_at": time.Now().Format(time.RFC3339)})
	case path == "/user_management/invitations" && r.Method == "GET":
		data := []any{}
		for _, inv := range f.invitations {
			if inv.OrgID == r.URL.Query().Get("organization_id") {
				data = append(data, map[string]any{"id": inv.ID, "email": inv.Email, "state": inv.State, "organization_id": inv.OrgID,
					"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339), "created_at": time.Now().Format(time.RFC3339)})
			}
		}
		reply(200, map[string]any{"data": data})
	case strings.HasSuffix(path, "/revoke"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/user_management/invitations/"), "/revoke")
		inv := f.invitations[id]
		inv.State = "revoked"
		f.invitations[id] = inv
		reply(200, map[string]any{"id": id, "state": "revoked"})
	default:
		reply(404, map[string]any{"message": "no route " + r.Method + " " + path})
	}
}
