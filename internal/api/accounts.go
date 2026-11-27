package api

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/time/rate"

	"github.com/blissfulrays/pushq/internal/accounts"
	"github.com/blissfulrays/pushq/internal/auth"
)

// Dashboard accounts: first-time setup, login sessions, team members,
// invites and dashboard-managed API keys.

// --- wire types ---

type userDTO struct {
	ID          int64      `json:"id"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Role        string     `json:"role"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

func toUserDTO(u accounts.User) userDTO {
	return userDTO{ID: u.ID, Email: u.Email, Name: u.Name, Role: string(u.Role), CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt}
}

type inviteDTO struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Email     string    `json:"email"`
	Role      string    `json:"role,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func toInviteDTO(i accounts.Invite) inviteDTO {
	return inviteDTO{ID: i.ID, Kind: i.Kind, Email: i.Email, Role: string(i.Role), CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt}
}

type keyDTO struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Role       string     `json:"role"`
	CreatedBy  string     `json:"created_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

func toKeyDTO(k accounts.APIKey) keyDTO {
	return keyDTO{ID: k.ID, Name: k.Name, Prefix: k.Prefix, Role: string(k.Role), CreatedBy: k.CreatedByEmail,
		CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt}
}

type permsDTO struct {
	Read        bool `json:"read"`
	List        bool `json:"list"`
	Write       bool `json:"write"`
	DeleteQueue bool `json:"delete_queue"`
	Manage      bool `json:"manage"`
}

type sessionResponse struct {
	// Mode is "dev" (auth disabled), "open" (no credentials needed until
	// the first account exists) or "locked".
	Mode string `json:"mode"`
	// SetupRequired: no dashboard account exists yet.
	SetupRequired bool `json:"setup_required"`
	// SetupRequiresKey: config-file API keys exist, so creating the first
	// account needs an admin key (the server is already protected).
	SetupRequiresKey bool     `json:"setup_requires_key"`
	Authenticated    bool     `json:"authenticated"`
	Kind             string   `json:"kind,omitempty"`
	Role             string   `json:"role,omitempty"`
	User             *userDTO `json:"user,omitempty"`
	KeyName          string   `json:"key_name,omitempty"`
	Permissions      permsDTO `json:"permissions"`
	// SSOProviders lists single sign-on options for the sign-in page ("workos").
	SSOProviders []string `json:"sso_providers"`

	// SaaS mode: the team this session works in, the user's teams, and
	// whether they still need to create or pick one.
	Team      *teamDTO  `json:"team,omitempty"`
	Teams     []teamDTO `json:"teams,omitempty"`
	NeedsTeam bool      `json:"needs_team"`
}

// invitePath is the dashboard route that accepts invite and reset links. The
// token rides in the fragment so it never reaches server logs or Referer
// headers.
func invitePath(token string) string { return "/invite#token=" + token }

// Response envelopes. Named types (not ad-hoc maps) so the generated OpenAPI
// spec describes exactly what is sent.
type userEnvelope struct {
	User userDTO `json:"user"`
}

type usersList struct {
	Users []userDTO `json:"users"`
}

type resetLinkCreated struct {
	Link inviteDTO `json:"link"`
	// Path is the single-use link, relative to the server ("/invite#token=…").
	Path string `json:"path" doc:"Single-use link relative to the server, e.g. /invite#token=…"`
}

type invitesList struct {
	Invites []inviteDTO `json:"invites"`
}

type inviteCreated struct {
	Invite inviteDTO `json:"invite"`
	// Path is set on self-hosted servers: a link for you to send.
	Path string `json:"path,omitempty" doc:"Self-hosted: the invite link to send (relative to the server)."`
	// Emailed is set in SaaS mode, where WorkOS emails the invitation.
	Emailed bool `json:"emailed,omitempty" doc:"SaaS mode: the invitation was emailed."`
}

type keysList struct {
	Keys       []keyDTO `json:"keys"`
	ConfigKeys int      `json:"config_keys" doc:"How many more keys are defined in the server config file."`
}

type keyCreated struct {
	Key    keyDTO `json:"key"`
	Secret string `json:"secret" doc:"The API key itself. Shown only once; store it now."`
}

// --- cookies & rate limiting ---

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: auth.SessionCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: auth.SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteStrictMode,
	})
}

// limiterMap rate-limits password-checking endpoints per client IP (10
// attempts, refilling one every 6s). Behind a reverse proxy every client
// shares the proxy's address, which still caps brute-force attempts.
type limiterMap struct {
	mu sync.Mutex
	m  map[string]*rate.Limiter
}

func (l *limiterMap) allow(r *http.Request) bool {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil || len(l.m) > 10_000 {
		l.m = map[string]*rate.Limiter{}
	}
	lim, ok := l.m[ip]
	if !ok {
		lim = rate.NewLimiter(rate.Every(6*time.Second), 10)
		l.m[ip] = lim
	}
	return lim.Allow()
}

func (s *Server) rateLimited(w http.ResponseWriter, r *http.Request) bool {
	if s.loginLimiter.allow(r) {
		return false
	}
	w.Header().Set("Retry-After", "6")
	writeProblem(w, r, http.StatusTooManyRequests, CodeRateLimited, "too many attempts; try again in a few seconds")
	return true
}

func (s *Server) accountsOr404(w http.ResponseWriter, r *http.Request) bool {
	if s.Accounts == nil {
		writeProblem(w, r, http.StatusNotFound, CodeNotFound, "dashboard accounts are not enabled")
		return false
	}
	return true
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errf("invalid id")
	}
	return id, nil
}

func createdBy(p auth.Principal) *int64 {
	if p.User != nil {
		return &p.User.ID
	}
	return nil
}

// --- sign-in flow ---

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	hasUsers, err := s.Auth.HasUsers(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := sessionResponse{
		Mode:          "locked",
		SetupRequired: s.Accounts != nil && !s.Auth.Dev() && !hasUsers,
		Authenticated: p.Kind != auth.KindNone,
		Kind:          string(p.Kind),
		Role:          p.Role,
		KeyName:       p.KeyName,
		Permissions: permsDTO{
			Read: p.Can(auth.PermRead), List: p.Can(auth.PermList), Write: p.Can(auth.PermWrite),
			DeleteQueue: p.Can(auth.PermDeleteQueue), Manage: p.Can(auth.PermManage),
		},
	}
	out.SetupRequiresKey = out.SetupRequired && s.Auth.HasConfigKeys()
	out.SSOProviders = []string{}
	if s.saas() {
		out.Mode = "saas"
		out.SetupRequired, out.SetupRequiresKey = false, false
		out.SSOProviders = append(out.SSOProviders, "workos")
		if p.Team != nil {
			out.Team = &teamDTO{ID: p.Team.ID, Name: p.Team.Name, Role: p.Role}
		}
		if p.User != nil {
			teams, err := s.Accounts.UserTeams(r.Context(), p.User.ID)
			if err != nil {
				s.writeErr(w, r, err)
				return
			}
			for _, t := range teams {
				out.Teams = append(out.Teams, teamDTO{ID: t.Team.ID, Name: t.Team.Name, Role: string(t.Role)})
			}
			out.NeedsTeam = p.Team == nil
		}
	}
	switch {
	case s.Auth.Dev():
		out.Mode = "dev"
	case p.Kind == auth.KindOpen:
		out.Mode = "open"
	}
	if p.User != nil {
		u := toUserDTO(*p.User)
		out.User = &u
	}
	writeJSON(w, http.StatusOK, out)
}

type mePatchBody struct {
	Name *string `json:"name"`
}

type passwordChangeBody struct {
	Current string `json:"current_password"`
	New     string `json:"new_password" doc:"At least 8 characters."`
}

type userPatchBody struct {
	Name *string        `json:"name" doc:"Self-hosted only; in SaaS mode names come from WorkOS."`
	Role *accounts.Role `json:"role" enum:"admin,member,viewer"`
}

type inviteCreateBody struct {
	Email string        `json:"email"`
	Role  accounts.Role `json:"role" enum:"admin,member,viewer"`
}

type keyCreateBody struct {
	Name string           `json:"name" doc:"What uses the key, e.g. billing-service."`
	Role accounts.KeyRole `json:"role" enum:"admin,writer"`
}

type credentialsBody struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	APIKey   string `json:"api_key"`
}

func (s *Server) signIn(w http.ResponseWriter, r *http.Request, u accounts.User, status int) {
	token, expires, err := s.Accounts.CreateSession(r.Context(), u.ID, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	setSessionCookie(w, r, token, expires)
	writeJSON(w, status, userEnvelope{User: toUserDTO(u)})
}

// handleSetup creates the first admin account. Allowed only while no account
// exists; if config-file API keys protect the server, an admin key is
// required too (otherwise anyone reaching the dashboard could claim it).
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !s.accountsOr404(w, r) || !s.selfHostedOnly(w, r) || s.rateLimited(w, r) {
		return
	}
	var body credentialsBody
	if err := s.decodeJSON(w, r, &body, 16<<10); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if s.Auth.HasConfigKeys() {
		key := body.APIKey
		if key == "" {
			key, _ = strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if s.Auth.ConfigKeyRole(strings.TrimSpace(key)) != accounts.KeyAdmin {
			writeProblem(w, r, http.StatusForbidden, CodePermissionDenied,
				"this server is protected by API keys; enter an admin API key from its config to create the first account")
			return
		}
	}
	u, err := s.Accounts.Setup(r.Context(), body.Email, body.Name, body.Password, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.Log.Info("first dashboard account created; the API now requires a login session or an API key", "email", u.Email)
	s.signIn(w, r, u, http.StatusCreated)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.accountsOr404(w, r) || !s.selfHostedOnly(w, r) || s.rateLimited(w, r) {
		return
	}
	var body credentialsBody
	if err := s.decodeJSON(w, r, &body, 16<<10); err != nil {
		s.writeErr(w, r, err)
		return
	}
	u, err := s.Accounts.Authenticate(r.Context(), body.Email, body.Password, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.signIn(w, r, u, http.StatusOK)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.SessionCookie); err == nil && c.Value != "" && s.Accounts != nil {
		if err := s.Accounts.DeleteSession(r.Context(), c.Value); err != nil {
			s.writeErr(w, r, err)
			return
		}
	}
	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleInviteGet(w http.ResponseWriter, r *http.Request) {
	if !s.accountsOr404(w, r) || !s.selfHostedOnly(w, r) {
		return
	}
	inv, err := s.Accounts.LookupInvite(r.Context(), chi.URLParam(r, "token"), s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toInviteDTO(inv))
}

func (s *Server) handleInviteAccept(w http.ResponseWriter, r *http.Request) {
	if !s.accountsOr404(w, r) || !s.selfHostedOnly(w, r) || s.rateLimited(w, r) {
		return
	}
	var body credentialsBody
	if err := s.decodeJSON(w, r, &body, 16<<10); err != nil {
		s.writeErr(w, r, err)
		return
	}
	u, err := s.Accounts.AcceptInvite(r.Context(), chi.URLParam(r, "token"), body.Name, body.Password, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.signIn(w, r, u, http.StatusOK)
}

// --- own account ---

func (s *Server) handleMePatch(w http.ResponseWriter, r *http.Request) {
	var body mePatchBody
	if err := s.decodeJSON(w, r, &body, 16<<10); err != nil {
		s.writeErr(w, r, err)
		return
	}
	u, err := s.Accounts.UpdateUser(r.Context(), principal(r).User.ID, body.Name, nil, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserDTO(u))
}

func (s *Server) handleMePassword(w http.ResponseWriter, r *http.Request) {
	if !s.selfHostedOnly(w, r) || s.rateLimited(w, r) {
		return
	}
	var body passwordChangeBody
	if err := s.decodeJSON(w, r, &body, 16<<10); err != nil {
		s.writeErr(w, r, err)
		return
	}
	p := principal(r)
	if err := s.Accounts.ChangePassword(r.Context(), p.User.ID, body.Current, body.New, p.SessionToken, s.Now()); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- team ---

func (s *Server) handleUserList(w http.ResponseWriter, r *http.Request) {
	if s.saas() {
		s.saasUserList(w, r)
		return
	}
	users, err := s.Accounts.ListUsers(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := make([]userDTO, len(users))
	for i, u := range users {
		out[i] = toUserDTO(u)
	}
	writeJSON(w, http.StatusOK, usersList{Users: out})
}

func (s *Server) handleUserPatch(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var body userPatchBody
	if err := s.decodeJSON(w, r, &body, 16<<10); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if s.saas() {
		s.saasUserPatch(w, r, id, body.Role)
		return
	}
	u, err := s.Accounts.UpdateUser(r.Context(), id, body.Name, body.Role, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserDTO(u))
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if s.saas() {
		s.saasUserDelete(w, r, id)
		return
	}
	if err := s.Accounts.DeleteUser(r.Context(), id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUserResetLink(w http.ResponseWriter, r *http.Request) {
	if !s.selfHostedOnly(w, r) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	token, inv, err := s.Accounts.CreateResetLink(r.Context(), id, createdBy(principal(r)), s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, resetLinkCreated{Link: toInviteDTO(inv), Path: invitePath(token)})
}

func (s *Server) handleInviteList(w http.ResponseWriter, r *http.Request) {
	if s.saas() {
		s.saasInviteList(w, r)
		return
	}
	invs, err := s.Accounts.ListInvites(r.Context(), s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := make([]inviteDTO, len(invs))
	for i, inv := range invs {
		out[i] = toInviteDTO(inv)
	}
	writeJSON(w, http.StatusOK, invitesList{Invites: out})
}

func (s *Server) handleInviteCreate(w http.ResponseWriter, r *http.Request) {
	var body inviteCreateBody
	if err := s.decodeJSON(w, r, &body, 16<<10); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if s.saas() {
		s.saasInviteCreate(w, r, body.Email, body.Role)
		return
	}
	token, inv, err := s.Accounts.CreateInvite(r.Context(), body.Email, body.Role, createdBy(principal(r)), s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, inviteCreated{Invite: toInviteDTO(inv), Path: invitePath(token)})
}

func (s *Server) handleInviteDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if s.saas() {
		s.saasInviteDelete(w, r, id)
		return
	}
	if err := s.Accounts.DeleteInvite(r.Context(), id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- API keys ---

func (s *Server) handleKeyList(w http.ResponseWriter, r *http.Request) {
	keys, err := s.Accounts.ListAPIKeys(r.Context(), teamID(principal(r)))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := make([]keyDTO, len(keys))
	for i, k := range keys {
		out[i] = toKeyDTO(k)
	}
	cfgKeys := s.Auth.ConfigKeyCount()
	if s.saas() {
		cfgKeys = 0 // the operator's keys aren't the team's business
	}
	writeJSON(w, http.StatusOK, keysList{Keys: out, ConfigKeys: cfgKeys})
}

func (s *Server) handleKeyCreate(w http.ResponseWriter, r *http.Request) {
	var body keyCreateBody
	if err := s.decodeJSON(w, r, &body, 16<<10); err != nil {
		s.writeErr(w, r, err)
		return
	}
	secret, k, err := s.Accounts.CreateAPIKey(r.Context(), teamID(principal(r)), body.Name, body.Role, createdBy(principal(r)), s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if p := principal(r); p.User != nil {
		k.CreatedByEmail = p.User.Email
	}
	writeJSON(w, http.StatusCreated, keyCreated{Key: toKeyDTO(k), Secret: secret})
}

func (s *Server) handleKeyDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := s.Accounts.DeleteAPIKey(r.Context(), teamID(principal(r)), id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
