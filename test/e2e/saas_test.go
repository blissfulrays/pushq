package e2e

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blissfulrays/pushq/internal/accounts"
	"github.com/blissfulrays/pushq/internal/api"
	"github.com/blissfulrays/pushq/internal/auth"
	"github.com/blissfulrays/pushq/internal/config"
	"github.com/blissfulrays/pushq/internal/dispatch"
	"github.com/blissfulrays/pushq/internal/metrics"
	"github.com/blissfulrays/pushq/internal/store/sqlite"
	"github.com/blissfulrays/pushq/internal/workos"
)

type saasEnv struct {
	base     string
	fake     *fakeWorkOS
	receiver *receiver
}

// newSaaSServer runs pushq in SaaS mode (WorkOS configured) with a real
// dispatcher, against a fake WorkOS and a recording webhook receiver.
func newSaaSServer(t *testing.T) *saasEnv {
	t.Helper()
	st, err := sqlite.Open("sqlite:" + filepath.Join(t.TempDir(), "saas.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	disp := dispatch.New(st, dispatch.Options{
		GlobalConcurrency: 16, PerQueueClaimCap: 10, ReconcileInterval: 200 * time.Millisecond,
		SweepInterval: 100 * time.Millisecond, CleanupInterval: time.Second, LeaseGrace: 200 * time.Millisecond,
		DrainTimeout: 2 * time.Second, RetentionSucceeded: time.Hour, RetentionFailed: time.Hour, DedupWindow: time.Hour,
	}, logger, metrics.New())

	fake := newFakeWorkOS(t)
	w, r := st.SQLHandles()
	acc := accounts.New(w, r, st.Dialect())
	authn := auth.New([]config.APIKey{{Key: "operator-key", Role: config.RoleAdmin}}, acc, false)
	authn.EnableSaaS()
	srv := &api.Server{
		Store: st, Accounts: acc, Auth: authn, Log: logger, Limits: config.Default().Limits, Now: time.Now,
		WorkOS: &workos.Client{ClientID: "client_test", APIKey: fake.apiKey, BaseURL: fake.URL},
	}
	ts := httptest.NewServer(srv.Router())
	ctx, cancel := context.WithCancel(context.Background())
	disp.Start(ctx)
	rc := newReceiver()
	t.Cleanup(func() {
		cancel()
		disp.Stop()
		ts.Close()
		rc.Close()
		st.Close()
	})
	return &saasEnv{base: ts.URL, fake: fake, receiver: rc}
}

// signIn runs the browser flow: /v1/auth/workos/login, then WorkOS's redirect
// back to the callback with a code. Returns a cookie-carrying client.
func (e *saasEnv) signIn(t *testing.T, code string) *client {
	t.Helper()
	c := newClient(t, e.base)
	c.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := c.http.Get(e.base + "/v1/auth/workos/login?provider=github&next=/queues")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || resp.StatusCode != http.StatusFound || !strings.HasPrefix(loc.String(), e.fake.URL+"/user_management/authorize") {
		t.Fatalf("login redirect: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if loc.Query().Get("client_id") != "client_test" || loc.Query().Get("provider") != "GitHubOAuth" {
		t.Fatalf("authorize URL: %s", loc)
	}

	cb := e.base + "/v1/auth/workos/callback?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(loc.Query().Get("state"))
	resp, err = c.http.Get(cb)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Location"); resp.StatusCode != http.StatusFound || got != "/queues" {
		t.Fatalf("callback: %d -> %q", resp.StatusCode, got)
	}
	c.http.CheckRedirect = nil
	return c
}

func TestSaaSTeamIsolation(t *testing.T) {
	e := newSaaSServer(t)
	anon := newClient(t, e.base)

	// SaaS mode: nothing is open, local password flows are gone.
	s := anon.must("GET", "/v1/auth/session", nil, 200)
	if s["mode"] != "saas" || s["authenticated"] != false || s["setup_required"] != false {
		t.Fatalf("anonymous session = %v", s)
	}
	anon.must("PUT", "/v1/queues/x", map[string]any{}, 401)
	anon.must("POST", "/v1/auth/setup", map[string]any{"email": "a@x.io", "password": "password1"}, 404)
	anon.must("POST", "/v1/auth/login", map[string]any{"email": "a@x.io", "password": "password1"}, 404)

	// Forged callbacks are refused (no/mismatched state).
	_, code := e.fake.addUser("mallory@evil.test", "Mal")
	noFollow := newClient(t, e.base)
	noFollow.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, _ := noFollow.http.Get(e.base + "/v1/auth/workos/callback?code=" + code + "&state=forged")
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login?error=") {
		t.Fatalf("forged callback -> %q", loc)
	}
	resp.Body.Close()

	// Alice signs up: no team yet, so no access to queues until she makes one.
	aliceID, code := e.fake.addUser("alice@acme.test", "Alice")
	alice := e.signIn(t, code)
	if s := alice.must("GET", "/v1/auth/session", nil, 200); s["needs_team"] != true || s["kind"] != "user" {
		t.Fatalf("new user session = %v", s)
	}
	alice.must("GET", "/v1/queues", nil, 403)
	acme := alice.must("POST", "/v1/teams", map[string]any{"name": "Acme"}, 201)
	if acme["role"] != "admin" {
		t.Fatalf("creator role = %v", acme["role"])
	}
	if s := alice.must("GET", "/v1/auth/session", nil, 200); s["needs_team"] != false || s["team"].(map[string]any)["name"] != "Acme" {
		t.Fatalf("session after creating team = %v", s)
	}

	// Bob signs up and makes his own team.
	_, code = e.fake.addUser("bob@globex.test", "Bob")
	bob := e.signIn(t, code)
	bob.must("POST", "/v1/teams", map[string]any{"name": "Globex"}, 201)

	// Both create "emails": same name, separate queues.
	for _, c := range []*client{alice, bob} {
		q := c.must("PUT", "/v1/queues/emails", map[string]any{"target": map[string]any{"base_url": e.receiver.URL}}, 201)
		if q["name"] != "emails" {
			t.Fatalf("queue name in response = %v", q["name"])
		}
	}
	alice.must("PUT", "/v1/queues/"+url.PathEscape("user/ig"), map[string]any{}, 201)
	alice.must("POST", "/v1/queues/emails/tasks", map[string]any{"task_id": "a1", "path": "/hook", "delay_seconds": 3600}, 201)

	// Each sees only their own.
	for c, want := range map[*client]int{alice: 2, bob: 1} {
		list := c.must("GET", "/v1/queues", nil, 200)["queues"].([]any)
		if len(list) != want {
			t.Fatalf("listing shows %d queues, want %d: %v", len(list), want, list)
		}
		for _, q := range list {
			if n := q.(map[string]any)["name"].(string); strings.Contains(n, ":") {
				t.Fatalf("internal namespace leaked in name %q", n)
			}
		}
	}
	bob.must("GET", "/v1/queues/emails/tasks/a1", nil, 404)
	if n := len(bob.must("GET", "/v1/queues/emails/tasks", nil, 200)["tasks"].([]any)); n != 0 {
		t.Fatalf("bob sees %d of alice's tasks", n)
	}
	bob.must("GET", "/v1/queues/"+url.PathEscape("user/ig"), nil, 404)
	if n := len(bob.must("GET", "/v1/queues?name_prefix=user/", nil, 200)["queues"].([]any)); n != 0 {
		t.Fatal("bob's folder listing shows alice's queue")
	}
	// Error messages name the queue as the caller knows it.
	_, nf := bob.do("GET", "/v1/queues/missing", nil)
	if d := nf["detail"].(string); strings.Contains(d, ":") {
		t.Fatalf("404 detail leaks namespace: %q", d)
	}

	// Dead-letter queues resolve inside the team, and show by their short name.
	alice.must("PUT", "/v1/queues/emails-dlq", map[string]any{}, 201)
	p := alice.must("PATCH", "/v1/queues/emails", map[string]any{"dead_letter_queue": "emails-dlq"}, 200)
	if p["dead_letter_queue"] != "emails-dlq" {
		t.Fatalf("dlq in response = %v", p["dead_letter_queue"])
	}
	bob.must("PATCH", "/v1/queues/emails", map[string]any{"dead_letter_queue": "emails-dlq"}, 200) // his own (missing) DLQ name, no reach into Acme

	// Receivers see the short queue name, never the namespace.
	bob.must("POST", "/v1/queues/emails/tasks", map[string]any{"task_id": "b1", "path": "/bob-hook"}, 201)
	waitFor(t, 5*time.Second, "delivery", func() bool { return e.receiver.count("/bob-hook") >= 1 })
	if q := e.receiver.requests()[0].Headers.Get("X-Pushq-Queue"); q != "emails" {
		t.Fatalf("X-Pushq-Queue = %q", q)
	}

	// API keys are per team.
	key := alice.must("POST", "/v1/api-keys", map[string]any{"name": "acme-app", "role": "writer"}, 201)
	app := newClient(t, e.base)
	app.noCSRF, app.bearer = true, key["secret"].(string)
	app.must("PUT", "/v1/queues/from-key", map[string]any{}, 201)
	alice.must("GET", "/v1/queues/from-key", nil, 200)
	bob.must("GET", "/v1/queues/from-key", nil, 404)
	if n := len(bob.must("GET", "/v1/api-keys", nil, 200)["keys"].([]any)); n != 0 {
		t.Fatal("bob sees acme's API keys")
	}
	keyID := jsonNum(alice.must("GET", "/v1/api-keys", nil, 200)["keys"].([]any)[0].(map[string]any)["id"].(float64))
	bob.must("DELETE", "/v1/api-keys/"+keyID, nil, 404)

	// The operator's config-file key sees across teams.
	op := newClient(t, e.base)
	op.noCSRF, op.bearer = true, "operator-key"
	if n := len(op.must("GET", "/v1/queues", nil, 200)["queues"].([]any)); n < 4 {
		t.Fatalf("operator sees %d queues", n)
	}

	// Invites: WorkOS emails them; the role is pushq's.
	alice.must("POST", "/v1/invites", map[string]any{"email": "carol@acme.test", "role": "viewer"}, 201)
	invs := alice.must("GET", "/v1/invites", nil, 200)["invites"].([]any)
	if len(invs) != 1 || invs[0].(map[string]any)["role"] != "viewer" {
		t.Fatalf("pending invites = %v", invs)
	}
	carolID, code := e.fake.addUser("carol@acme.test", "Carol")
	e.fake.acceptInvite("carol@acme.test", carolID)
	carol := e.signIn(t, code)
	cs := carol.must("GET", "/v1/auth/session", nil, 200)
	if cs["team"].(map[string]any)["name"] != "Acme" || cs["role"] != "viewer" {
		t.Fatalf("invited user's session = %v", cs)
	}
	carol.must("GET", "/v1/queues/emails/tasks", nil, 200)
	carol.must("PUT", "/v1/queues/new", map[string]any{}, 403)
	carol.must("GET", "/v1/users", nil, 403)
	if n := len(alice.must("GET", "/v1/invites", nil, 200)["invites"].([]any)); n != 0 {
		t.Fatal("accepted invite still pending")
	}

	// Carol also makes her own team and switches between the two.
	carol.must("POST", "/v1/teams", map[string]any{"name": "Initech"}, 201)
	carol.must("GET", "/v1/queues/emails", nil, 404) // Initech has no "emails"
	teams := carol.must("GET", "/v1/teams", nil, 200)["teams"].([]any)
	if len(teams) != 2 {
		t.Fatalf("carol's teams = %v", teams)
	}
	var acmeID string
	for _, tm := range teams {
		if tm.(map[string]any)["name"] == "Acme" {
			acmeID = jsonNum(tm.(map[string]any)["id"].(float64))
		}
	}
	carol.must("POST", "/v1/teams/"+acmeID+":switch", nil, 204)
	carol.must("GET", "/v1/queues/emails", nil, 200)
	bob.must("POST", "/v1/teams/"+acmeID+":switch", nil, 404) // not his team

	// Team admin manages members; WorkOS stays in step.
	members := alice.must("GET", "/v1/users", nil, 200)["users"].([]any)
	var carolLocal, aliceLocal string
	for _, m := range members {
		mm := m.(map[string]any)
		switch mm["email"] {
		case "carol@acme.test":
			carolLocal = jsonNum(mm["id"].(float64))
		case "alice@acme.test":
			aliceLocal = jsonNum(mm["id"].(float64))
		}
	}
	if len(members) != 2 || carolLocal == "" {
		t.Fatalf("acme members = %v", members)
	}
	alice.must("PATCH", "/v1/users/"+carolLocal, map[string]any{"role": "member"}, 200)
	carol.must("PUT", "/v1/queues/carol-made", map[string]any{}, 201)
	alice.must("PATCH", "/v1/users/"+aliceLocal, map[string]any{"role": "viewer"}, 400) // last admin
	alice.must("DELETE", "/v1/users/"+carolLocal, nil, 204)
	carol.must("GET", "/v1/queues/emails", nil, 403) // removed: no access to Acme anymore
	e.fake.mu.Lock()
	for _, m := range e.fake.memberships {
		if m.UserID == carolID && m.OrgID != "" && e.fake.orgs[m.OrgID] == "Acme" {
			t.Error("carol's WorkOS membership in Acme was not removed")
		}
	}
	e.fake.mu.Unlock()
	_ = aliceID

	// Self-hosted-only endpoints are gone in SaaS mode.
	alice.must("POST", "/v1/me/password", map[string]any{"current_password": "x", "new_password": "yyyyyyyy"}, 404)
	alice.must("POST", "/v1/users/"+aliceLocal+"/reset-link", nil, 404)
}

// A second sign-in lands in the team used last.
func TestSaaSRemembersLastTeam(t *testing.T) {
	e := newSaaSServer(t)
	uid, code := e.fake.addUser("dana@x.test", "Dana")
	d := e.signIn(t, code)
	d.must("POST", "/v1/teams", map[string]any{"name": "One"}, 201)
	two := d.must("POST", "/v1/teams", map[string]any{"name": "Two"}, 201)
	_ = two
	d2 := e.signIn(t, e.fake.newCode(uid))
	if s := d2.must("GET", "/v1/auth/session", nil, 200); s["team"].(map[string]any)["name"] != "Two" {
		t.Fatalf("second sign-in team = %v", s["team"])
	}
}

// Passwordless sign-in on pushq's own page: an email code signs people up and in.
func TestSaaSEmailCodeSignIn(t *testing.T) {
	e := newSaaSServer(t)
	c := newClient(t, e.base)

	// A brand-new email: WorkOS gets an (unverified) user and emails a code.
	c.must("POST", "/v1/auth/email-code", map[string]any{"email": "  New.Person@Example.test "}, 200)
	if u, ok := e.fake.userByEmail("new.person@example.test"); !ok || u.Verified {
		t.Fatalf("expected an unverified WorkOS user, got %+v %v", u, ok)
	}
	code := e.fake.emailedCode("new.person@example.test")
	if len(code) != 6 {
		t.Fatalf("no code emailed: %q", code)
	}

	// A wrong code is refused without signing in.
	c.must("POST", "/v1/auth/email-code:verify", map[string]any{"email": "new.person@example.test", "code": "000000"}, 401)
	c.must("GET", "/v1/queues", nil, 401)

	// The right code signs in, verifies the email, and needs a team first.
	out := c.must("POST", "/v1/auth/email-code:verify", map[string]any{"email": "New.Person@example.test", "code": code}, 200)
	if out["user"].(map[string]any)["email"] != "new.person@example.test" {
		t.Fatalf("signed-in user = %v", out)
	}
	if u, _ := e.fake.userByEmail("new.person@example.test"); !u.Verified {
		t.Fatal("email should be marked verified after a correct code")
	}
	if s := c.must("GET", "/v1/auth/session", nil, 200); s["authenticated"] != true || s["needs_team"] != true {
		t.Fatalf("session = %v", s)
	}
	c.must("POST", "/v1/teams", map[string]any{"name": "Code Co"}, 201)
	c.must("PUT", "/v1/queues/jobs", map[string]any{}, 201)

	// The code is single-use; an existing user signs in again with a new one.
	again := newClient(t, e.base)
	again.must("POST", "/v1/auth/email-code:verify", map[string]any{"email": "new.person@example.test", "code": code}, 401)
	again.must("POST", "/v1/auth/email-code", map[string]any{"email": "new.person@example.test"}, 200)
	again.must("POST", "/v1/auth/email-code:verify", map[string]any{"email": "new.person@example.test", "code": e.fake.emailedCode("new.person@example.test")}, 200)
	again.must("GET", "/v1/queues/jobs", nil, 200) // back in their team

	// Requests that set a cookie need the CSRF header; junk emails are rejected.
	bad := newClient(t, e.base)
	bad.must("POST", "/v1/auth/email-code", map[string]any{"email": "not-an-email"}, 400)
	bad.noCSRF = true
	bad.must("POST", "/v1/auth/email-code", map[string]any{"email": "x@example.test"}, 403)

	// Only known OAuth providers are offered.
	nf := newClient(t, e.base)
	nf.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, _ := nf.http.Get(e.base + "/v1/auth/workos/login?provider=authkit")
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login?error=") {
		t.Fatalf("unknown provider -> %q", loc)
	}
	resp.Body.Close()
}

// WorkOS can pause a GitHub sign-in until the email is verified: pushq's own
// page asks for the emailed code and then finishes the sign-in.
func TestSaaSEmailVerificationRequired(t *testing.T) {
	e := newSaaSServer(t)
	_, code := e.fake.addUnverifiedUser("gita@example.test")

	c := newClient(t, e.base)
	c.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, _ := c.http.Get(e.base + "/v1/auth/workos/login?provider=github&next=/queues")
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	resp, _ = c.http.Get(e.base + "/v1/auth/workos/callback?code=" + code + "&state=" + url.QueryEscape(loc.Query().Get("state")))
	resp.Body.Close()
	if got := resp.Header.Get("Location"); got != "/login?verify=1" {
		t.Fatalf("callback -> %q, want the verify step", got)
	}
	c.http.CheckRedirect = nil

	if p := c.must("GET", "/v1/auth/email-verification", nil, 200); p["pending"] != true || p["email"] != "gita@example.test" {
		t.Fatalf("pending verification = %v", p)
	}
	c.must("GET", "/v1/queues", nil, 401) // not signed in yet
	c.must("POST", "/v1/auth/email-verification", map[string]any{"code": "000000"}, 401)

	out := c.must("POST", "/v1/auth/email-verification", map[string]any{"code": e.fake.verificationCode()}, 200)
	if out["next"] != "/queues" || out["user"].(map[string]any)["email"] != "gita@example.test" {
		t.Fatalf("verify result = %v", out)
	}
	if s := c.must("GET", "/v1/auth/session", nil, 200); s["authenticated"] != true || s["needs_team"] != true {
		t.Fatalf("session after verification = %v", s)
	}
	// The pending sign-in is single use.
	c.must("POST", "/v1/auth/email-verification", map[string]any{"code": "123456"}, 400)
	newClient(t, e.base).must("POST", "/v1/auth/email-verification", map[string]any{"code": "123456"}, 400)
}

// With WorkOS's email verification turned off, a GitHub sign-in whose email
// WorkOS hasn't verified goes straight in (no code step).
func TestSaaSGitHubWithoutEmailVerification(t *testing.T) {
	e := newSaaSServer(t)
	e.fake.skipVerification = true
	_, code := e.fake.addUnverifiedUser("gh@example.test")
	c := e.signIn(t, code) // fails the test unless the callback lands on /queues
	if s := c.must("GET", "/v1/auth/session", nil, 200); s["authenticated"] != true || s["user"].(map[string]any)["email"] != "gh@example.test" {
		t.Fatalf("session = %v", s)
	}
	c.must("POST", "/v1/teams", map[string]any{"name": "GH"}, 201)
}
