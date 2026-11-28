package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blissfulrays/pushq/internal/accounts"
	"github.com/blissfulrays/pushq/internal/api"
	"github.com/blissfulrays/pushq/internal/auth"
	"github.com/blissfulrays/pushq/internal/config"
	"github.com/blissfulrays/pushq/internal/store/sqlite"
)

func init() { accounts.SetPasswordIterationsForTesting(1000) }

// client is a browser-like API client: cookie jar + the CSRF header the
// dashboard sends.
type client struct {
	t      *testing.T
	base   string
	http   *http.Client
	bearer string
	noCSRF bool
	app    string // X-PushQ-App
}

func newAccountsServer(t *testing.T, keys []config.APIKey) string {
	t.Helper()
	st, err := sqlite.Open("sqlite:" + filepath.Join(t.TempDir(), "acc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	w, r := st.SQLHandles()
	acc := accounts.New(w, r, st.Dialect())
	srv := &api.Server{
		Store: st, Accounts: acc, Auth: auth.New(keys, acc, false),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Limits: config.Default().Limits, Now: time.Now,
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)
	return ts.URL
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, http: &http.Client{Jar: jar}}
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if !c.noCSRF {
		req.Header.Set("X-Requested-With", "pushq")
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if c.app != "" {
		req.Header.Set(api.AppHeader, c.app)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (c *client) must(method, path string, body any, want int) map[string]any {
	c.t.Helper()
	got, out := c.do(method, path, body)
	if got != want {
		c.t.Fatalf("%s %s = %d (want %d): %v", method, path, got, want, out)
	}
	return out
}

// tokenFrom extracts the secret from a returned "/invite#token=..." path.
func tokenFrom(t *testing.T, out map[string]any) string {
	t.Helper()
	path, _ := out["path"].(string)
	_, tok, ok := strings.Cut(path, "#token=")
	if !ok {
		t.Fatalf("no token in path %q", path)
	}
	return tok
}

func TestAccountsFlow(t *testing.T) {
	base := newAccountsServer(t, nil)
	anon := newClient(t, base)

	// Fresh server: open, setup required, API usable without credentials.
	s := anon.must("GET", "/v1/auth/session", nil, 200)
	if s["mode"] != "open" || s["setup_required"] != true {
		t.Fatalf("fresh session = %v", s)
	}
	anon.must("PUT", "/v1/queues/q1", map[string]any{}, 201)

	// Setup needs the CSRF header (it sets a cookie).
	anon.noCSRF = true
	anon.must("POST", "/v1/auth/setup", map[string]any{"email": "a@example.com", "password": "admin password"}, 403)
	anon.noCSRF = false

	admin := newClient(t, base)
	admin.must("POST", "/v1/auth/setup", map[string]any{"email": "a@example.com", "name": "Ada", "password": "admin password"}, 201)
	admin.must("POST", "/v1/auth/setup", map[string]any{"email": "b@example.com", "password": "other password"}, 400)

	// Now locked: anonymous requests are refused.
	if s := anon.must("GET", "/v1/auth/session", nil, 200); s["mode"] != "locked" || s["setup_required"] != false || s["authenticated"] != false {
		t.Fatalf("session after setup = %v", s)
	}
	anon.must("GET", "/v1/queues/q1", nil, 401)
	anon.must("PUT", "/v1/queues/q2", map[string]any{}, 401)

	// The admin's cookie works; cookie-authenticated writes need the CSRF header.
	if s := admin.must("GET", "/v1/auth/session", nil, 200); s["role"] != "admin" || s["kind"] != "user" {
		t.Fatalf("admin session = %v", s)
	}
	admin.must("GET", "/v1/queues", nil, 200)
	admin.noCSRF = true
	admin.must("POST", "/v1/queues/q1:pause", nil, 403)
	admin.noCSRF = false
	admin.must("POST", "/v1/queues/q1:pause", nil, 200)

	// Login: wrong password, then right.
	other := newClient(t, base)
	other.must("POST", "/v1/auth/login", map[string]any{"email": "a@example.com", "password": "nope nope"}, 401)
	other.must("POST", "/v1/auth/login", map[string]any{"email": "A@example.com", "password": "admin password"}, 200)
	other.must("GET", "/v1/queues", nil, 200)
	other.must("POST", "/v1/auth/logout", nil, 204)
	other.must("GET", "/v1/queues", nil, 401)

	// Invite a viewer; they accept and are signed in, read-only.
	inv := admin.must("POST", "/v1/invites", map[string]any{"email": "v@example.com", "role": "viewer"}, 201)
	tok := tokenFrom(t, inv)
	if got := anon.must("GET", "/v1/auth/invites/"+tok, nil, 200); got["email"] != "v@example.com" || got["role"] != "viewer" {
		t.Fatalf("invite lookup = %v", got)
	}
	viewer := newClient(t, base)
	viewer.must("POST", "/v1/auth/invites/"+tok+":accept", map[string]any{"name": "Vic", "password": "viewer password"}, 200)
	viewer.must("POST", "/v1/auth/invites/"+tok+":accept", map[string]any{"password": "viewer password"}, 404)
	viewer.must("GET", "/v1/queues", nil, 200)
	viewer.must("GET", "/v1/queues/q1/tasks", nil, 200)
	viewer.must("POST", "/v1/queues/q1:resume", nil, 403)
	viewer.must("GET", "/v1/users", nil, 403)

	// Promote to member: can manage queues, not the team.
	users := admin.must("GET", "/v1/users", nil, 200)["users"].([]any)
	var viewerID float64
	for _, u := range users {
		if m := u.(map[string]any); m["email"] == "v@example.com" {
			viewerID = m["id"].(float64)
		}
	}
	vid := "/v1/users/" + jsonNum(viewerID)
	admin.must("PATCH", vid, map[string]any{"role": "member"}, 200)
	viewer.must("POST", "/v1/queues/q1:resume", nil, 200)
	viewer.must("DELETE", "/v1/queues/q1", nil, 204)
	viewer.must("POST", "/v1/invites", map[string]any{"email": "x@example.com", "role": "admin"}, 403)

	// The last admin can't be demoted or removed.
	var adminID float64
	for _, u := range users {
		if m := u.(map[string]any); m["email"] == "a@example.com" {
			adminID = m["id"].(float64)
		}
	}
	admin.must("PATCH", "/v1/users/"+jsonNum(adminID), map[string]any{"role": "viewer"}, 400)

	// Password reset link signs the user out and sets a new password.
	reset := admin.must("POST", vid+"/reset-link", nil, 201)
	newClient(t, base).must("POST", "/v1/auth/invites/"+tokenFrom(t, reset)+":accept", map[string]any{"password": "new viewer password"}, 200)
	viewer.must("GET", "/v1/queues", nil, 401)

	// Removing a member ends their access.
	admin.must("DELETE", vid, nil, 204)

	// API keys created in the dashboard work as bearer tokens.
	created := admin.must("POST", "/v1/api-keys", map[string]any{"name": "billing", "role": "writer"}, 201)
	secret := created["secret"].(string)
	app := newClient(t, base)
	app.noCSRF = true // plain API client
	app.bearer = secret
	app.must("PUT", "/v1/queues/billing", map[string]any{}, 201)
	app.must("POST", "/v1/queues/billing/tasks", map[string]any{"url": "http://127.0.0.1:9/x", "delay_seconds": 60}, 201)
	app.must("GET", "/v1/queues", nil, 403) // writer keys can't list
	keys := admin.must("GET", "/v1/api-keys", nil, 200)["keys"].([]any)
	keyID := jsonNum(keys[0].(map[string]any)["id"].(float64))
	admin.must("DELETE", "/v1/api-keys/"+keyID, nil, 204)
	app.must("PUT", "/v1/queues/billing", map[string]any{}, 401)

	// Own account: rename, change password.
	admin.must("PATCH", "/v1/me", map[string]any{"name": "Ada L."}, 200)
	admin.must("POST", "/v1/me/password", map[string]any{"current_password": "wrong", "new_password": "brand new pass"}, 400)
	admin.must("POST", "/v1/me/password", map[string]any{"current_password": "admin password", "new_password": "brand new pass"}, 204)
	admin.must("GET", "/v1/queues", nil, 200) // current session survives
}

func jsonNum(f float64) string { b, _ := json.Marshal(int64(f)); return string(b) }

// A server already protected by config-file API keys must not be claimable
// by whoever reaches the dashboard first.
func TestSetupRequiresAdminKeyWhenKeysConfigured(t *testing.T) {
	base := newAccountsServer(t, []config.APIKey{
		{Key: "admin-key", Role: config.RoleAdmin},
		{Key: "writer-key", Role: config.RoleWriter},
	})
	c := newClient(t, base)
	if s := c.must("GET", "/v1/auth/session", nil, 200); s["setup_required"] != true || s["setup_requires_key"] != true || s["mode"] != "locked" {
		t.Fatalf("session = %v", s)
	}
	body := map[string]any{"email": "a@example.com", "password": "admin password"}
	c.must("POST", "/v1/auth/setup", body, 403)
	body["api_key"] = "writer-key"
	c.must("POST", "/v1/auth/setup", body, 403)
	body["api_key"] = "admin-key"
	c.must("POST", "/v1/auth/setup", body, 201)
	c.must("GET", "/v1/queues", nil, 200)
}

func TestLoginRateLimited(t *testing.T) {
	base := newAccountsServer(t, nil)
	c := newClient(t, base)
	c.must("POST", "/v1/auth/setup", map[string]any{"email": "a@example.com", "password": "admin password"}, 201)
	limited := false
	for range 15 {
		if got, _ := c.do("POST", "/v1/auth/login", map[string]any{"email": "a@example.com", "password": "guess guess"}); got == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("15 rapid failed logins were never rate limited")
	}
}

// The running server serves its generated OpenAPI spec and Swagger UI.
func TestOpenAPIAndDocsServed(t *testing.T) {
	base := newAccountsServer(t, nil)
	resp, err := http.Get(base + "/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&spec); err != nil || resp.StatusCode != 200 {
		t.Fatalf("/openapi.json: %d %v", resp.StatusCode, err)
	}
	resp.Body.Close()
	if _, ok := spec["paths"].(map[string]any)["/v1/queues/{queue}:pause"]; !ok {
		t.Fatal("spec lacks /v1/queues/{queue}:pause")
	}

	resp, err = http.Get(base + "/docs")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(page), "SwaggerUIBundle") || !strings.Contains(string(page), "/openapi.json") {
		t.Fatalf("/docs: %d", resp.StatusCode)
	}
}
