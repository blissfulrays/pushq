package accounts_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blissfulrays/pushq/internal/accounts"
)

func wid(id, email string) accounts.ExternalIdentity {
	return accounts.ExternalIdentity{WorkOSUserID: id, Email: email, EmailVerified: true, Name: "Pat Example"}
}

func TestUpsertWorkOSUser(t *testing.T) {
	each(t, func(t *testing.T, a *accounts.Store) {
		u, err := a.UpsertWorkOSUser(ctx, wid("user_1", "Pat@Example.com"), now)
		if err != nil || u.Email != "pat@example.com" || u.Name != "Pat Example" || u.LastLoginAt == nil {
			t.Fatalf("create: %+v %v", u, err)
		}
		again, err := a.UpsertWorkOSUser(ctx, wid("user_1", "pat@example.com"), now.Add(time.Hour))
		if err != nil || again.ID != u.ID {
			t.Fatalf("same WorkOS user should map to the same account: %+v %v", again, err)
		}
		if _, err := a.UpsertWorkOSUser(ctx, wid("user_2", "pat@example.com"), now); !errors.Is(err, accounts.ErrIdentityConflict) {
			t.Fatalf("second WorkOS identity for the same email: %v", err)
		}
		unv := wid("user_3", "new@example.com")
		unv.EmailVerified = false
		if _, err := a.UpsertWorkOSUser(ctx, unv, now); err == nil {
			t.Fatal("unverified email created an account")
		}
		if _, err := a.Authenticate(ctx, "pat@example.com", "!", now); !errors.Is(err, accounts.ErrBadCredentials) {
			t.Fatalf("WorkOS accounts must have no usable password: %v", err)
		}
	})
}

func TestTeams(t *testing.T) {
	each(t, func(t *testing.T, a *accounts.Store) {
		alice, _ := a.UpsertWorkOSUser(ctx, wid("user_a", "alice@x.test"), now)
		bob, _ := a.UpsertWorkOSUser(ctx, wid("user_b", "bob@x.test"), now)

		acme, err := a.CreateTeam(ctx, "Acme", "org_acme", alice.ID, "om_a", now)
		if err != nil || acme.NS == "" || strings.Contains(acme.NS, ":") {
			t.Fatalf("create team: %+v %v", acme, err)
		}
		other, _ := a.CreateTeam(ctx, "Other", "org_other", bob.ID, "om_b", now)
		if other.NS == acme.NS {
			t.Fatal("teams must get distinct namespaces")
		}
		if got, _ := a.EnsureTeamForOrg(ctx, "org_acme", "ignored", now); got.ID != acme.ID {
			t.Fatal("EnsureTeamForOrg should find the existing team")
		}

		// A pending pushq invite decides the role when the member arrives.
		if _, err := a.AddTeamInvite(ctx, acme.ID, "bob@x.test", accounts.RoleViewer, "inv_1", now.Add(time.Hour), now); err != nil {
			t.Fatal(err)
		}
		role, err := a.AddMemberIfMissing(ctx, acme.ID, bob.ID, accounts.RoleMember, "om_b2", now)
		if err != nil || role != accounts.RoleViewer {
			t.Fatalf("invited member role = %v %v", role, err)
		}
		if inv, _ := a.TeamInviteRoles(ctx, acme.ID); len(inv) != 0 {
			t.Fatalf("invite not consumed: %v", inv)
		}
		if role, _ := a.AddMemberIfMissing(ctx, acme.ID, bob.ID, accounts.RoleAdmin, "om_b2", now); role != accounts.RoleViewer {
			t.Fatal("re-sync must keep the existing role")
		}
		teams, _ := a.UserTeams(ctx, bob.ID)
		if len(teams) != 2 {
			t.Fatalf("bob's teams = %+v", teams)
		}

		// Sessions carry a team; switching requires membership.
		tok, _, _ := a.CreateTeamSession(ctx, bob.ID, other.ID, now)
		if _, team, _, _ := a.Session(ctx, tok, now); team != other.ID {
			t.Fatalf("session team = %d", team)
		}
		if err := a.SetSessionTeam(ctx, tok, bob.ID, acme.ID); err != nil {
			t.Fatal(err)
		}
		if last, _ := a.LastTeam(ctx, bob.ID); last != acme.ID {
			t.Fatalf("last team = %d", last)
		}
		stranger, _ := a.UpsertWorkOSUser(ctx, wid("user_c", "c@x.test"), now)
		ctok, _, _ := a.CreateTeamSession(ctx, stranger.ID, 0, now)
		if err := a.SetSessionTeam(ctx, ctok, stranger.ID, acme.ID); !errors.Is(err, accounts.ErrNotMember) {
			t.Fatalf("switching into someone else's team: %v", err)
		}

		// Last-admin protection is per team.
		if _, err := a.SetMemberRole(ctx, acme.ID, alice.ID, accounts.RoleMember); !errors.Is(err, accounts.ErrLastAdmin) {
			t.Fatalf("demote last admin: %v", err)
		}
		if mid, err := a.SetMemberRole(ctx, acme.ID, bob.ID, accounts.RoleAdmin); err != nil || mid != "om_b2" {
			t.Fatalf("promote: %q %v", mid, err)
		}
		if _, err := a.SetMemberRole(ctx, acme.ID, alice.ID, accounts.RoleMember); err != nil {
			t.Fatalf("demote with another admin: %v", err)
		}

		// Removing a member drops their session's access to the team.
		if _, err := a.SetMemberRole(ctx, acme.ID, alice.ID, accounts.RoleAdmin); err != nil {
			t.Fatal(err)
		}
		if mid, err := a.RemoveMember(ctx, acme.ID, bob.ID); err != nil || mid != "om_b2" {
			t.Fatalf("remove: %q %v", mid, err)
		}
		if _, team, _, _ := a.Session(ctx, tok, now); team != 0 {
			t.Fatalf("removed member's session still on team %d", team)
		}
		if _, err := a.MemberRole(ctx, acme.ID, bob.ID); !errors.Is(err, accounts.ErrNotMember) {
			t.Fatal("removed member still has a role")
		}

		// Sync drops memberships that WorkOS no longer has.
		_, _ = a.AddMemberIfMissing(ctx, acme.ID, bob.ID, accounts.RoleMember, "om_b3", now)
		if err := a.RemoveMembersExcept(ctx, acme.ID, []string{"om_a"}); err != nil {
			t.Fatal(err)
		}
		if ms, _ := a.ListMembers(ctx, acme.ID); len(ms) != 1 || ms[0].ID != alice.ID {
			t.Fatalf("members after sync = %+v", ms)
		}

		// API keys belong to one team.
		_, k, _ := a.CreateAPIKey(ctx, acme.ID, "acme-app", accounts.KeyWriter, &alice.ID, now)
		if keys, _ := a.ListAPIKeys(ctx, other.ID); len(keys) != 0 {
			t.Fatal("other team sees acme's key")
		}
		if err := a.DeleteAPIKey(ctx, other.ID, k.ID); err == nil {
			t.Fatal("other team deleted acme's key")
		}
		if keys, _ := a.ListAPIKeys(ctx, acme.ID); len(keys) != 1 || keys[0].TeamID != acme.ID {
			t.Fatalf("acme keys = %+v", keys)
		}
	})
}
