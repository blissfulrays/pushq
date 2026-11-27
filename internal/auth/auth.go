// Package auth resolves who is making a request and what they may do.
//
// Callers are one of:
//   - an API key, from the config file (compared as SHA-256 digests in
//     constant time) or created in the dashboard (stored in the database);
//   - a dashboard user, via the session cookie set at login;
//   - anyone, while the server is "open": dev mode, or no config keys and no
//     dashboard accounts yet. Creating the first account closes it.
//
// In SaaS mode (WorkOS configured) the server is never open. Dashboard users
// and dashboard-created API keys act inside one team (Principal.Team), with
// the role they hold there; config-file keys are the operator's and are not
// team-scoped.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/blissfulrays/pushq/internal/accounts"
	"github.com/blissfulrays/pushq/internal/config"
)

// SessionCookie holds the dashboard login session.
const SessionCookie = "pushq_session"

// Perm is a set of capabilities.
type Perm uint16

const (
	PermRead        Perm = 1 << iota // view a queue, its stats and tasks by name
	PermList                         // list every queue; scrape /metrics
	PermWrite                        // create/modify queues; enqueue, run, delete tasks; pause/resume/purge
	PermDeleteQueue                  // delete queues
	PermManage                       // team members, invites, API keys

	PermAll = PermRead | PermList | PermWrite | PermDeleteQueue | PermManage
)

// PermsForUser maps a dashboard role to its capabilities.
func PermsForUser(r accounts.Role) Perm {
	switch r {
	case accounts.RoleAdmin:
		return PermAll
	case accounts.RoleMember:
		return PermRead | PermList | PermWrite | PermDeleteQueue
	case accounts.RoleViewer:
		return PermRead | PermList
	}
	return 0
}

// PermsForKey maps an API key role to its capabilities. Writer keys keep
// their original scope: work with queues they know by name, no listing.
func PermsForKey(r accounts.KeyRole) Perm {
	switch r {
	case accounts.KeyAdmin:
		return PermAll
	case accounts.KeyWriter:
		return PermRead | PermWrite
	}
	return 0
}

type Kind string

const (
	KindNone Kind = ""
	KindOpen Kind = "open"
	KindKey  Kind = "api_key"
	KindUser Kind = "user"
)

// Principal is the authenticated caller.
type Principal struct {
	Kind    Kind
	Perms   Perm
	Role    string // user role, key role, or "admin" when open
	User    *accounts.User
	KeyName string
	// SessionToken is set when authenticated by the session cookie.
	SessionToken string
	// Team is the team this caller acts in (SaaS mode). Nil for self-hosted
	// callers and for the operator's config-file keys; a SaaS user with no
	// team yet has Team == nil and no queue permissions.
	Team *accounts.Team
}

func (p Principal) Can(x Perm) bool { return p.Perms&x == x }

type Auth struct {
	keys     []keyEntry
	accounts *accounts.Store
	dev      bool
	saas     bool
	now      func() time.Time
}

// EnableSaaS switches to multi-team mode (WorkOS sign-in, team-scoped access).
func (a *Auth) EnableSaaS() { a.saas = true }

// SaaS reports whether multi-team mode is on.
func (a *Auth) SaaS() bool { return a.saas }

type keyEntry struct {
	hash [32]byte
	role accounts.KeyRole
}

// New builds the authenticator. acc may be nil (no dashboard accounts).
// dev disables authentication entirely.
func New(keys []config.APIKey, acc *accounts.Store, dev bool) *Auth {
	a := &Auth{accounts: acc, dev: dev, now: time.Now}
	for _, k := range keys {
		role := accounts.KeyWriter
		if k.Role == config.RoleAdmin {
			role = accounts.KeyAdmin
		}
		a.keys = append(a.keys, keyEntry{hash: sha256.Sum256([]byte(k.Key)), role: role})
	}
	return a
}

func (a *Auth) Dev() bool                 { return a.dev }
func (a *Auth) HasConfigKeys() bool       { return len(a.keys) > 0 }
func (a *Auth) ConfigKeyCount() int       { return len(a.keys) }
func (a *Auth) Accounts() *accounts.Store { return a.accounts }

// HasUsers reports whether first-time setup has been completed.
func (a *Auth) HasUsers(ctx context.Context) (bool, error) {
	if a.accounts == nil {
		return false, nil
	}
	return a.accounts.HasUsers(ctx)
}

// IsOpen reports whether requests need no credentials.
func (a *Auth) IsOpen(ctx context.Context) (bool, error) {
	if a.dev {
		return true, nil
	}
	if a.saas {
		return false, nil
	}
	if a.HasConfigKeys() {
		return false, nil
	}
	has, err := a.HasUsers(ctx)
	return !has, err
}

// ConfigKeyRole returns the role of a config-file key ("" if it isn't one).
// Every entry is compared so timing doesn't reveal which matched.
func (a *Auth) ConfigKeyRole(token string) accounts.KeyRole {
	sum := sha256.Sum256([]byte(token))
	var role accounts.KeyRole
	for _, k := range a.keys {
		if subtle.ConstantTimeCompare(sum[:], k.hash[:]) == 1 {
			role = k.role
		}
	}
	return role
}

func bearer(r *http.Request) string {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return strings.TrimSpace(token)
}

// Authenticate resolves the caller. A zero Principal (KindNone) means no
// valid credentials. renewed is set when the session cookie's expiry slid
// forward and the cookie should be re-issued.
func (a *Auth) Authenticate(r *http.Request) (p Principal, renewed *time.Time, err error) {
	ctx := r.Context()
	open, err := a.IsOpen(ctx)
	if err != nil {
		return Principal{}, nil, err
	}
	if open {
		return Principal{Kind: KindOpen, Perms: PermAll, Role: "admin"}, nil, nil
	}

	if r.Header.Get("Authorization") != "" {
		token := bearer(r)
		if token == "" {
			return Principal{}, nil, nil
		}
		if role := a.ConfigKeyRole(token); role != "" {
			return Principal{Kind: KindKey, Perms: PermsForKey(role), Role: string(role), KeyName: "config file"}, nil, nil
		}
		if a.accounts != nil {
			k, ok, err := a.accounts.LookupAPIKey(ctx, token, a.now())
			if err != nil {
				return Principal{}, nil, err
			}
			if ok {
				p := Principal{Kind: KindKey, Perms: PermsForKey(k.Role), Role: string(k.Role), KeyName: k.Name}
				if k.TeamID > 0 {
					t, err := a.accounts.GetTeam(ctx, k.TeamID)
					if err != nil {
						return Principal{}, nil, err
					}
					p.Team = &t
				} else if a.saas {
					// An unscoped dashboard key can't exist in SaaS mode; refuse rather than grant operator access.
					return Principal{}, nil, nil
				}
				return p, nil, nil
			}
		}
		return Principal{}, nil, nil
	}

	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" && a.accounts != nil {
		u, teamID, renewed, err := a.accounts.Session(ctx, c.Value, a.now())
		if err == accounts.ErrBadCredentials {
			return Principal{}, nil, nil
		}
		if err != nil {
			return Principal{}, nil, err
		}
		p := Principal{Kind: KindUser, Perms: PermsForUser(u.Role), Role: string(u.Role), User: &u, SessionToken: c.Value}
		if a.saas {
			// Permissions come from the role in the current team; no team = none.
			p.Perms, p.Role = 0, ""
			if teamID > 0 {
				role, err := a.accounts.MemberRole(ctx, teamID, u.ID)
				switch {
				case err == nil:
					t, err := a.accounts.GetTeam(ctx, teamID)
					if err != nil {
						return Principal{}, nil, err
					}
					p.Team, p.Role, p.Perms = &t, string(role), PermsForUser(role)
					p.User.Role = role
				case errors.Is(err, accounts.ErrNotMember):
					// Removed from the team since this session picked it.
				default:
					return Principal{}, nil, err
				}
			}
		}
		return p, renewed, nil
	}
	return Principal{}, nil, nil
}
