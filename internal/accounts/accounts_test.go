package accounts_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blissfulrays/pushq/internal/accounts"
	"github.com/blissfulrays/pushq/internal/store"
	"github.com/blissfulrays/pushq/internal/store/postgres"
	"github.com/blissfulrays/pushq/internal/store/sqlite"
)

func init() { accounts.SetPasswordIterationsForTesting(1000) }

var now = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

type backend struct {
	name string
	open func(t *testing.T) *accounts.Store
}

func backends() []backend {
	return []backend{
		{"sqlite", func(t *testing.T) *accounts.Store {
			st, err := sqlite.Open("sqlite:" + filepath.Join(t.TempDir(), "a.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.Close() })
			if err := st.Migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			w, r := st.SQLHandles()
			return accounts.New(w, r, st.Dialect())
		}},
		{"postgres", func(t *testing.T) *accounts.Store {
			url := os.Getenv("PUSHQ_TEST_PG_URL")
			if url == "" {
				t.Skip("PUSHQ_TEST_PG_URL not set")
			}
			// Own schema, so this can run in parallel with the store's
			// Postgres tests that reset the default schema.
			if err := createSchema(url, "pushq_accounts_test"); err != nil {
				t.Fatal(err)
			}
			sep := "?"
			if strings.Contains(url, "?") {
				sep = "&"
			}
			st, err := postgres.Open(url + sep + "search_path=pushq_accounts_test")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.Close() })
			w, r := st.SQLHandles()
			if _, err := w.Exec(`DROP TABLE IF EXISTS apps, team_invites, team_members, teams, api_keys, invites, sessions, users, task_attempts, tasks, queues, schema_migrations CASCADE`); err != nil {
				t.Fatal(err)
			}
			if err := st.Migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			return accounts.New(w, r, st.Dialect())
		}},
	}
}

func createSchema(url, name string) error {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`CREATE SCHEMA IF NOT EXISTS ` + name)
	return err
}

func each(t *testing.T, f func(t *testing.T, a *accounts.Store)) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) { f(t, b.open(t)) })
	}
}

var ctx = context.Background()

func TestSetupAndLogin(t *testing.T) {
	each(t, func(t *testing.T, a *accounts.Store) {
		if has, _ := a.HasUsers(ctx); has {
			t.Fatal("fresh store has users")
		}
		if _, err := a.Setup(ctx, "Admin@Example.com", "Ada", "short", now); err == nil {
			t.Fatal("weak password accepted")
		}
		u, err := a.Setup(ctx, " Admin@Example.com ", "Ada", "correct horse", now)
		if err != nil {
			t.Fatal(err)
		}
		if u.Email != "admin@example.com" || u.Role != accounts.RoleAdmin {
			t.Fatalf("setup user = %+v", u)
		}
		if _, err := a.Setup(ctx, "x@example.com", "", "correct horse", now); !errors.Is(err, accounts.ErrSetupDone) {
			t.Fatalf("second setup: %v", err)
		}
		if has, _ := a.HasUsers(ctx); !has {
			t.Fatal("HasUsers false after setup")
		}

		if _, err := a.Authenticate(ctx, "admin@example.com", "wrong password", now); !errors.Is(err, accounts.ErrBadCredentials) {
			t.Fatalf("wrong password: %v", err)
		}
		if _, err := a.Authenticate(ctx, "nobody@example.com", "correct horse", now); !errors.Is(err, accounts.ErrBadCredentials) {
			t.Fatalf("unknown email: %v", err)
		}
		if _, err := a.Authenticate(ctx, "ADMIN@example.com", "correct horse", now); err != nil {
			t.Fatalf("login (case-insensitive email): %v", err)
		}
	})
}

func TestConcurrentSetupCreatesOneAdmin(t *testing.T) {
	each(t, func(t *testing.T, a *accounts.Store) {
		var wg sync.WaitGroup
		var mu sync.Mutex
		ok := 0
		for i := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := a.Setup(ctx, "u"+string(rune('a'+i))+"@example.com", "", "correct horse", now)
				if err == nil {
					mu.Lock()
					ok++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		users, _ := a.ListUsers(ctx)
		if ok != 1 || len(users) != 1 {
			t.Fatalf("successful setups = %d, users = %d; want 1, 1", ok, len(users))
		}
	})
}

func TestSessions(t *testing.T) {
	each(t, func(t *testing.T, a *accounts.Store) {
		u, _ := a.Setup(ctx, "a@example.com", "", "correct horse", now)
		tok, exp, err := a.CreateSession(ctx, u.ID, now)
		if err != nil || !exp.Equal(now.Add(accounts.SessionTTL)) {
			t.Fatalf("create session: %v %v", exp, err)
		}
		got, renewed, err := a.SessionUser(ctx, tok, now.Add(time.Hour))
		if err != nil || got.ID != u.ID || renewed != nil {
			t.Fatalf("session lookup: %+v %v %v", got, renewed, err)
		}
		// Used late in its life, the session slides forward.
		late := now.Add(accounts.SessionTTL - time.Hour)
		if _, renewed, err := a.SessionUser(ctx, tok, late); err != nil || renewed == nil {
			t.Fatalf("expected renewal: %v %v", renewed, err)
		}
		if _, _, err := a.SessionUser(ctx, tok, late.Add(accounts.SessionTTL+time.Second)); !errors.Is(err, accounts.ErrBadCredentials) {
			t.Fatalf("expired session accepted: %v", err)
		}
		if _, _, err := a.SessionUser(ctx, "pqs_bogus", now); !errors.Is(err, accounts.ErrBadCredentials) {
			t.Fatalf("bogus token: %v", err)
		}
		tok2, _, _ := a.CreateSession(ctx, u.ID, now)
		if err := a.DeleteSession(ctx, tok2); err != nil {
			t.Fatal(err)
		}
		if _, _, err := a.SessionUser(ctx, tok2, now); err == nil {
			t.Fatal("logged-out session still valid")
		}
	})
}

func TestInvitesAndRoles(t *testing.T) {
	each(t, func(t *testing.T, a *accounts.Store) {
		admin, _ := a.Setup(ctx, "admin@example.com", "", "correct horse", now)

		if _, _, err := a.CreateInvite(ctx, "admin@example.com", accounts.RoleMember, &admin.ID, now); !errors.Is(err, store.ErrAlreadyExists) {
			t.Fatalf("invite existing member: %v", err)
		}
		if _, _, err := a.CreateInvite(ctx, "x@example.com", "owner", &admin.ID, now); err == nil {
			t.Fatal("bad role accepted")
		}
		old, _, _ := a.CreateInvite(ctx, "bob@example.com", accounts.RoleViewer, &admin.ID, now)
		tok, inv, err := a.CreateInvite(ctx, "bob@example.com", accounts.RoleMember, &admin.ID, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.LookupInvite(ctx, old, now); err == nil {
			t.Fatal("re-invite should replace the old link")
		}
		pending, _ := a.ListInvites(ctx, now)
		if len(pending) != 1 || pending[0].ID != inv.ID || pending[0].Role != accounts.RoleMember {
			t.Fatalf("pending = %+v", pending)
		}
		if _, err := a.LookupInvite(ctx, tok, now.Add(accounts.InviteTTL+time.Second)); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("expired invite: %v", err)
		}

		bob, err := a.AcceptInvite(ctx, tok, "Bob", "bob password", now)
		if err != nil || bob.Role != accounts.RoleMember || bob.Email != "bob@example.com" {
			t.Fatalf("accept: %+v %v", bob, err)
		}
		if _, err := a.AcceptInvite(ctx, tok, "Bob", "bob password", now); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("invite reused: %v", err)
		}

		// Last-admin protection.
		member := accounts.RoleMember
		if _, err := a.UpdateUser(ctx, admin.ID, nil, &member, now); !errors.Is(err, accounts.ErrLastAdmin) {
			t.Fatalf("demote last admin: %v", err)
		}
		if err := a.DeleteUser(ctx, admin.ID); !errors.Is(err, accounts.ErrLastAdmin) {
			t.Fatalf("delete last admin: %v", err)
		}
		adminRole := accounts.RoleAdmin
		if _, err := a.UpdateUser(ctx, bob.ID, nil, &adminRole, now); err != nil {
			t.Fatal(err)
		}
		if _, err := a.UpdateUser(ctx, admin.ID, nil, &member, now); err != nil {
			t.Fatalf("demote with another admin present: %v", err)
		}

		// Deleting a user ends their sessions.
		s, _, _ := a.CreateSession(ctx, admin.ID, now)
		if err := a.DeleteUser(ctx, admin.ID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := a.SessionUser(ctx, s, now); err == nil {
			t.Fatal("deleted user's session still valid")
		}
	})
}

func TestPasswordResetAndChange(t *testing.T) {
	each(t, func(t *testing.T, a *accounts.Store) {
		u, _ := a.Setup(ctx, "a@example.com", "", "first password", now)
		keep, _, _ := a.CreateSession(ctx, u.ID, now)
		other, _, _ := a.CreateSession(ctx, u.ID, now)

		if err := a.ChangePassword(ctx, u.ID, "wrong", "second password", keep, now); err == nil {
			t.Fatal("wrong current password accepted")
		}
		if err := a.ChangePassword(ctx, u.ID, "first password", "second password", keep, now); err != nil {
			t.Fatal(err)
		}
		if _, _, err := a.SessionUser(ctx, keep, now); err != nil {
			t.Fatal("current session should survive a password change")
		}
		if _, _, err := a.SessionUser(ctx, other, now); err == nil {
			t.Fatal("other sessions should end on password change")
		}

		tok, inv, err := a.CreateResetLink(ctx, u.ID, nil, now)
		if err != nil || inv.Kind != "reset" {
			t.Fatalf("reset link: %+v %v", inv, err)
		}
		if _, err := a.AcceptInvite(ctx, tok, "", "third password", now); err != nil {
			t.Fatal(err)
		}
		if _, _, err := a.SessionUser(ctx, keep, now); err == nil {
			t.Fatal("reset should sign the user out everywhere")
		}
		if _, err := a.Authenticate(ctx, "a@example.com", "third password", now); err != nil {
			t.Fatalf("login with reset password: %v", err)
		}
	})
}

func TestAPIKeys(t *testing.T) {
	each(t, func(t *testing.T, a *accounts.Store) {
		u, _ := a.Setup(ctx, "a@example.com", "", "correct horse", now)
		key, k, err := a.CreateAPIKey(ctx, 0, 0, "billing service", accounts.KeyWriter, &u.ID, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(key) < 30 || key[:3] != "pq_" || k.Prefix != key[:11] {
			t.Fatalf("key %q prefix %q", key, k.Prefix)
		}
		got, ok, err := a.LookupAPIKey(ctx, key, now)
		if err != nil || !ok || got.Role != accounts.KeyWriter {
			t.Fatalf("lookup: %+v %v %v", got, ok, err)
		}
		if _, ok, _ := a.LookupAPIKey(ctx, key+"x", now); ok {
			t.Fatal("wrong key accepted")
		}
		list, _ := a.ListAPIKeys(ctx, 0)
		if len(list) != 1 || list[0].CreatedByEmail != "a@example.com" || list[0].LastUsedAt == nil {
			t.Fatalf("list = %+v", list)
		}
		if err := a.DeleteAPIKey(ctx, 0, k.ID); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := a.LookupAPIKey(ctx, key, now); ok {
			t.Fatal("revoked key still works")
		}
	})
}
