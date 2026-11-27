package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/blissfulrays/pushq/internal/accounts"
	"github.com/blissfulrays/pushq/internal/auth"
	"github.com/blissfulrays/pushq/internal/workos"
)

// SaaS mode: teams are WorkOS Organizations. WorkOS owns who belongs to which
// organization (and sends invite emails); pushq keeps the roles and mirrors
// memberships locally, re-syncing at sign-in and whenever the team page loads.

type teamDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

func (s *Server) saas() bool { return s.Auth.SaaS() && s.WorkOS != nil && s.Accounts != nil }

// selfHostedOnly rejects endpoints that WorkOS replaces in SaaS mode.
func (s *Server) selfHostedOnly(w http.ResponseWriter, r *http.Request) bool {
	if s.saas() {
		writeProblem(w, r, http.StatusNotFound, CodeNotFound, "not available here: sign-in and passwords are managed by WorkOS")
		return false
	}
	return true
}

// WorkOS AuthKit environments come with "admin" and "member" roles; pushq's
// viewer is a member in WorkOS terms (pushq enforces the difference).
func workosRole(r accounts.Role) string {
	if r == accounts.RoleAdmin {
		return "admin"
	}
	return "member"
}

func roleFromWorkOS(slug string) accounts.Role {
	if slug == "admin" {
		return accounts.RoleAdmin
	}
	return accounts.RoleMember
}

func teamID(p auth.Principal) int64 {
	if p.Team != nil {
		return p.Team.ID
	}
	return 0
}

// workosUser returns the WorkOS user ID of the signed-in user.
func (s *Server) workosUser(r *http.Request) (string, error) {
	p := principal(r)
	if p.User == nil {
		return "", errf("sign in to the dashboard first")
	}
	id, err := s.Accounts.WorkOSUserID(r.Context(), p.User.ID)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", errf("this account isn't linked to WorkOS")
	}
	return id, nil
}

// syncUserTeams mirrors a user's WorkOS organization memberships as local
// team memberships and returns the team to start in (0 = none yet).
func (s *Server) syncUserTeams(ctx context.Context, userID int64, workosUserID, preferOrgID string) (int64, error) {
	ms, err := s.WorkOS.ListMemberships(ctx, workosUserID, "")
	if err != nil {
		return 0, err
	}
	now := s.Now()
	var keep []int64
	var preferred int64
	for _, m := range ms {
		t, err := s.teamForOrg(ctx, m.OrganizationID)
		if err != nil {
			return 0, err
		}
		if _, err := s.Accounts.AddMemberIfMissing(ctx, t.ID, userID, roleFromWorkOS(m.Role.Slug), m.ID, now); err != nil {
			return 0, err
		}
		keep = append(keep, t.ID)
		if m.OrganizationID == preferOrgID {
			preferred = t.ID
		}
	}
	if err := s.Accounts.RemoveMembershipsExcept(ctx, userID, keep); err != nil {
		return 0, err
	}
	if last, err := s.Accounts.LastTeam(ctx, userID); err == nil && last > 0 {
		return last, nil
	}
	if preferred > 0 {
		return preferred, nil
	}
	if len(keep) > 0 {
		return keep[0], nil
	}
	return 0, nil
}

func (s *Server) teamForOrg(ctx context.Context, orgID string) (accounts.Team, error) {
	name := ""
	if o, err := s.WorkOS.GetOrganization(ctx, orgID); err == nil {
		name = o.Name
	}
	return s.Accounts.EnsureTeamForOrg(ctx, orgID, name, s.Now())
}

// --- teams ---

func (s *Server) handleTeamsList(w http.ResponseWriter, r *http.Request) {
	if !s.saasOr404(w, r) {
		return
	}
	teams, err := s.Accounts.UserTeams(r.Context(), principal(r).User.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := make([]teamDTO, len(teams))
	for i, t := range teams {
		out[i] = teamDTO{ID: t.Team.ID, Name: t.Team.Name, Role: string(t.Role)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"teams": out})
}

// handleTeamCreate creates a WorkOS Organization with the caller as admin,
// mirrors it as a team, and switches the session to it.
func (s *Server) handleTeamCreate(w http.ResponseWriter, r *http.Request) {
	if !s.saasOr404(w, r) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := s.decodeJSON(w, r, &body, 16<<10); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if l := len([]rune(body.Name)); l == 0 || l > 100 {
		s.writeErr(w, r, errf("team name must be 1-100 characters"))
		return
	}
	p := principal(r)
	wuser, err := s.workosUser(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	org, err := s.WorkOS.CreateOrganization(r.Context(), body.Name)
	if err != nil {
		s.workosErr(w, r, "create the team", err)
		return
	}
	m, err := s.createMembership(r.Context(), wuser, org.ID, accounts.RoleAdmin)
	if err != nil {
		s.workosErr(w, r, "add you to the team", err)
		return
	}
	t, err := s.Accounts.CreateTeam(r.Context(), org.Name, org.ID, p.User.ID, m.ID, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := s.Accounts.SetSessionTeam(r.Context(), p.SessionToken, p.User.ID, t.ID); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.Log.Info("team created", "team", t.Name, "workos_org", org.ID, "by", p.User.Email)
	writeJSON(w, http.StatusCreated, teamDTO{ID: t.ID, Name: t.Name, Role: string(accounts.RoleAdmin)})
}

// createMembership adds a WorkOS membership, falling back to the
// environment's default role if the role slug doesn't exist there.
func (s *Server) createMembership(ctx context.Context, workosUserID, orgID string, role accounts.Role) (workos.Membership, error) {
	m, err := s.WorkOS.CreateMembership(ctx, workosUserID, orgID, workosRole(role))
	var we *workos.Error
	if errors.As(err, &we) && (we.Status == http.StatusUnprocessableEntity || we.Status == http.StatusBadRequest) && role == accounts.RoleAdmin {
		s.Log.Warn("workos rejected role slug; using the default role", "role", workosRole(role), "err", err)
		return s.WorkOS.CreateMembership(ctx, workosUserID, orgID, "")
	}
	return m, err
}

func (s *Server) handleTeamSwitch(w http.ResponseWriter, r *http.Request) {
	if !s.saasOr404(w, r) {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.writeErr(w, r, errf("invalid team id"))
		return
	}
	p := principal(r)
	if err := s.Accounts.SetSessionTeam(r.Context(), p.SessionToken, p.User.ID, id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTeamRename(w http.ResponseWriter, r *http.Request) {
	if !s.saasOr404(w, r) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := s.decodeJSON(w, r, &body, 16<<10); err != nil {
		s.writeErr(w, r, err)
		return
	}
	team := principal(r).Team
	t, err := s.Accounts.RenameTeam(r.Context(), team.ID, body.Name, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if team.WorkOSOrgID != "" {
		if _, err := s.WorkOS.UpdateOrganization(r.Context(), team.WorkOSOrgID, t.Name); err != nil {
			s.Log.Warn("rename workos organization", "err", err)
		}
	}
	writeJSON(w, http.StatusOK, teamDTO{ID: t.ID, Name: t.Name, Role: principal(r).Role})
}

func (s *Server) saasOr404(w http.ResponseWriter, r *http.Request) bool {
	if !s.saas() {
		writeProblem(w, r, http.StatusNotFound, CodeNotFound, "teams are only available when WorkOS is configured")
		return false
	}
	return true
}

func (s *Server) workosErr(w http.ResponseWriter, r *http.Request, what string, err error) {
	s.Log.Warn("workos call failed", "action", what, "err", err)
	var we *workos.Error
	if errors.As(err, &we) && we.Status < 500 && we.Description != "" {
		writeProblem(w, r, http.StatusBadRequest, CodeFailedPrecondition, "couldn't "+what+": "+we.Description)
		return
	}
	writeProblem(w, r, http.StatusBadGateway, CodeInternal, "couldn't "+what+": WorkOS is unavailable, try again")
}

// --- members of the current team ---

// syncTeamMembers pulls the team's memberships from WorkOS (people who
// accepted invites, or were removed there) before listing them.
func (s *Server) syncTeamMembers(ctx context.Context, team *accounts.Team) error {
	if team.WorkOSOrgID == "" {
		return nil
	}
	ms, err := s.WorkOS.ListMemberships(ctx, "", team.WorkOSOrgID)
	if err != nil {
		return err
	}
	now := s.Now()
	var ids []string
	for _, m := range ms {
		ids = append(ids, m.ID)
		uid, ok, err := s.Accounts.UserIDByWorkOS(ctx, m.UserID)
		if err != nil {
			return err
		}
		if !ok {
			wu, err := s.WorkOS.GetUser(ctx, m.UserID)
			if err != nil {
				return err
			}
			u, err := s.Accounts.UpsertWorkOSUser(ctx, accounts.ExternalIdentity{
				WorkOSUserID: wu.ID, Email: wu.Email, EmailVerified: wu.EmailVerified, Name: wu.Name(),
			}, now)
			if err != nil {
				s.Log.Warn("skipping WorkOS member", "workos_user", m.UserID, "err", err)
				continue
			}
			uid = u.ID
		}
		if _, err := s.Accounts.AddMemberIfMissing(ctx, team.ID, uid, roleFromWorkOS(m.Role.Slug), m.ID, now); err != nil {
			return err
		}
	}
	return s.Accounts.RemoveMembersExcept(ctx, team.ID, ids)
}

func (s *Server) saasUserList(w http.ResponseWriter, r *http.Request) {
	team := principal(r).Team
	if err := s.syncTeamMembers(r.Context(), team); err != nil {
		s.Log.Warn("sync team members", "team", team.ID, "err", err) // still show what we have
	}
	members, err := s.Accounts.ListMembers(r.Context(), team.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := make([]userDTO, len(members))
	for i, m := range members {
		out[i] = toUserDTO(m.User)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *Server) saasUserPatch(w http.ResponseWriter, r *http.Request, id int64, role *accounts.Role) {
	team := principal(r).Team
	if role == nil {
		s.writeErr(w, r, errf("only the role can be changed here; names come from WorkOS"))
		return
	}
	mid, err := s.Accounts.SetMemberRole(r.Context(), team.ID, id, *role)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if mid != "" {
		if err := s.WorkOS.UpdateMembershipRole(r.Context(), mid, workosRole(*role)); err != nil {
			s.Log.Warn("update workos membership role", "err", err) // pushq's role is authoritative
		}
	}
	for _, m := range mustMembers(s, r, team.ID) {
		if m.ID == id {
			writeJSON(w, http.StatusOK, toUserDTO(m.User))
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func mustMembers(s *Server, r *http.Request, teamID int64) []accounts.Member {
	ms, _ := s.Accounts.ListMembers(r.Context(), teamID)
	return ms
}

func (s *Server) saasUserDelete(w http.ResponseWriter, r *http.Request, id int64) {
	team := principal(r).Team
	mid, err := s.Accounts.RemoveMember(r.Context(), team.ID, id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if mid != "" {
		if err := s.WorkOS.DeleteMembership(r.Context(), mid); err != nil && !workos.IsNotFound(err) {
			s.Log.Error("delete workos membership (removed locally)", "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- invites for the current team (WorkOS emails them) ---

func (s *Server) saasInviteList(w http.ResponseWriter, r *http.Request) {
	team := principal(r).Team
	local, err := s.Accounts.TeamInviteRoles(r.Context(), team.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := []inviteDTO{}
	if team.WorkOSOrgID != "" {
		invs, err := s.WorkOS.ListInvitations(r.Context(), team.WorkOSOrgID)
		if err != nil {
			s.workosErr(w, r, "load invitations", err)
			return
		}
		for _, inv := range invs {
			l, ok := local[inv.ID]
			if inv.State != "pending" {
				if ok && inv.State != "accepted" { // accepted ones are consumed when the member syncs
					_ = s.Accounts.DeleteTeamInvite(r.Context(), team.ID, inv.ID)
				}
				continue
			}
			role := accounts.RoleMember
			if ok {
				role = l.Role
			}
			out = append(out, inviteDTO{ID: l.ID, Kind: "invite", Email: inv.Email, Role: string(role),
				CreatedAt: inv.CreatedAt, ExpiresAt: inv.ExpiresAt})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": out})
}

func (s *Server) saasInviteCreate(w http.ResponseWriter, r *http.Request, email string, role accounts.Role) {
	p := principal(r)
	if !role.Valid() {
		s.writeErr(w, r, errf("role must be admin, member or viewer"))
		return
	}
	inviter, err := s.workosUser(r)
	if err != nil {
		inviter = "" // e.g. an admin API key inviting
	}
	inv, err := s.WorkOS.SendInvitation(r.Context(), email, p.Team.WorkOSOrgID, inviter, workosRole(role))
	var we *workos.Error
	if errors.As(err, &we) && (we.Status == http.StatusUnprocessableEntity || we.Status == http.StatusBadRequest) && workosRole(role) != "member" {
		inv, err = s.WorkOS.SendInvitation(r.Context(), email, p.Team.WorkOSOrgID, inviter, "")
	}
	if err != nil {
		s.workosErr(w, r, "send the invitation", err)
		return
	}
	expires := inv.ExpiresAt
	if expires.IsZero() {
		expires = s.Now().Add(7 * 24 * time.Hour)
	}
	ti, err := s.Accounts.AddTeamInvite(r.Context(), p.Team.ID, inv.Email, role, inv.ID, expires, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	// No link to hand out: WorkOS emails the invitation.
	writeJSON(w, http.StatusCreated, map[string]any{"invite": inviteDTO{ID: ti.ID, Kind: "invite", Email: ti.Email,
		Role: string(ti.Role), CreatedAt: ti.CreatedAt, ExpiresAt: ti.ExpiresAt}, "emailed": true})
}

func (s *Server) saasInviteDelete(w http.ResponseWriter, r *http.Request, id int64) {
	team := principal(r).Team
	local, err := s.Accounts.TeamInviteRoles(r.Context(), team.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	for wid, inv := range local {
		if inv.ID != id {
			continue
		}
		if err := s.WorkOS.RevokeInvitation(r.Context(), wid); err != nil && !workos.IsNotFound(err) {
			s.workosErr(w, r, "revoke the invitation", err)
			return
		}
		if err := s.Accounts.DeleteTeamInvite(r.Context(), team.ID, wid); err != nil {
			s.writeErr(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeProblem(w, r, http.StatusNotFound, CodeNotFound, "invite not found")
}
