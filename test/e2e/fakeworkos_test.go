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

	mu               sync.Mutex
	seq              int
	users            map[string]fwUser   // id -> user
	codes            map[string]string   // auth code -> user id
	orgs             map[string]string   // id -> name
	memberships      map[string]fwMember // id -> membership
	invitations      map[string]fwInvite // id -> invitation
	magicCodes       map[string]string   // email -> current emailed code
	skipVerification bool                // environment doesn\'t require verified emails
	verifyCodes      map[string]string   // pending token -> user id
	verifyCode       map[string]string   // pending token -> emailed verification code
}

type fwUser struct {
	ID, Email, First string
	Verified         bool
}

type fwMember struct{ ID, UserID, OrgID, Role string }

type fwInvite struct{ ID, Email, OrgID, State, Role string }

func newFakeWorkOS(t *testing.T) *fakeWorkOS {
	f := &fakeWorkOS{apiKey: "sk_test_fake", users: map[string]fwUser{}, codes: map[string]string{},
		orgs: map[string]string{}, memberships: map[string]fwMember{}, invitations: map[string]fwInvite{},
		magicCodes: map[string]string{}, verifyCodes: map[string]string{}, verifyCode: map[string]string{}}
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

// addUnverifiedUser registers a WorkOS user whose email isn't verified (e.g.
// from GitHub), so authenticating requires email verification first.
func (f *fakeWorkOS) addUnverifiedUser(email string) (userID, code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := fwUser{ID: f.id("user"), Email: email, First: "Gita", Verified: false}
	f.users[u.ID] = u
	return u.ID, f.newCodeLocked(u.ID)
}

// verificationCode returns the email-verification code "emailed" for the
// most recent paused sign-in.
func (f *fakeWorkOS) verificationCode() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	last := ""
	for _, c := range f.verifyCode {
		last = c
	}
	return last
}

// emailedCode returns the code WorkOS "emailed" to an address.
func (f *fakeWorkOS) emailedCode(email string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.magicCodes[email]
}

func (f *fakeWorkOS) userByEmail(email string) (fwUser, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.Email == email {
			return u, true
		}
	}
	return fwUser{}, false
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
		if str("grant_type") == "urn:workos:oauth:grant-type:email-verification:code" {
			tok := str("pending_authentication_token")
			uid, ok := f.verifyCodes[tok]
			if !ok || f.verifyCode[tok] != str("code") {
				reply(400, map[string]any{"error": "invalid_grant", "error_description": "Invalid code"})
				return
			}
			delete(f.verifyCodes, tok)
			delete(f.verifyCode, tok)
			u := f.users[uid]
			u.Verified = true
			f.users[uid] = u
			reply(200, map[string]any{"user": f.userJSON(u)})
			return
		}
		if str("grant_type") == "urn:workos:oauth:grant-type:magic-auth:code" {
			email := str("email")
			if want, ok := f.magicCodes[email]; !ok || want != str("code") {
				reply(400, map[string]any{"error": "invalid_grant", "error_description": "Invalid or expired code"})
				return
			}
			delete(f.magicCodes, email)
			for _, u := range f.users {
				if u.Email == email {
					reply(200, map[string]any{"user": f.userJSON(u)})
					return
				}
			}
			reply(400, map[string]any{"error": "invalid_grant"})
			return
		}
		uid, ok := f.codes[str("code")]
		if !ok {
			reply(400, map[string]any{"error": "invalid_grant", "error_description": "bad code"})
			return
		}
		delete(f.codes, str("code"))
		if u := f.users[uid]; !u.Verified && !f.skipVerification {
			// Like a WorkOS environment that requires verified emails.
			tok := f.id("pending")
			f.verifyCodes[tok] = uid
			f.verifyCode[tok] = fmt.Sprintf("%06d", 200000+f.seq)
			reply(403, map[string]any{"code": "email_verification_required",
				"message":                      "Email ownership must be verified before authentication.",
				"pending_authentication_token": tok, "email": u.Email, "email_verification_id": f.id("ev")})
			return
		}
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
	case path == "/user_management/users" && r.Method == "GET":
		data := []any{}
		for _, u := range f.users {
			if u.Email == r.URL.Query().Get("email") {
				data = append(data, f.userJSON(u))
			}
		}
		reply(200, map[string]any{"data": data})
	case path == "/user_management/users" && r.Method == "POST":
		u := fwUser{ID: f.id("user"), Email: strings.ToLower(str("email")), Verified: body["email_verified"] == true}
		f.users[u.ID] = u
		reply(201, f.userJSON(u))
	case path == "/user_management/magic_auth" && r.Method == "POST":
		code := fmt.Sprintf("%06d", 100000+f.seq)
		f.seq++
		f.magicCodes[str("email")] = code
		reply(201, map[string]any{"id": f.id("magic_auth"), "email": str("email")})
	case strings.HasPrefix(path, "/user_management/users/"):
		id := strings.TrimPrefix(path, "/user_management/users/")
		u, ok := f.users[id]
		if !ok {
			reply(404, map[string]any{"message": "not found"})
			return
		}
		if r.Method == "PUT" {
			if v, ok := body["email_verified"].(bool); ok {
				u.Verified = v
				f.users[id] = u
			}
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
