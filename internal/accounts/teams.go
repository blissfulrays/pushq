package accounts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blissfulrays/pushq/internal/store"
)

// Teams exist only in SaaS mode, where each is a WorkOS Organization. A
// team's queues live in its namespace NS: stored queue names are "NS:name".

type Team struct {
	ID          int64
	NS          string
	Name        string
	WorkOSOrgID string
	CreatedAt   time.Time
}

// TeamMembership is one team a user belongs to, with their role there.
type TeamMembership struct {
	Team Team
	Role Role
}

// Member is a user as seen from one team: User.Role is their team role.
type Member struct {
	User
	WorkOSMembershipID string
}

type TeamInvite struct {
	ID                 int64
	TeamID             int64
	Email              string
	Role               Role
	WorkOSInvitationID string
	CreatedAt          time.Time
	ExpiresAt          time.Time
}

var ErrNotMember = fmt.Errorf("not a member of this team: %w", store.ErrNotFound)

const nsAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// newNS returns a random 12-character namespace (62 bits; never reused).
func newNS() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = nsAlphabet[int(b[i])%len(nsAlphabet)]
	}
	return string(b)
}

func normalizeTeamName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 {
		return "", invalid("team name must be 1-100 characters")
	}
	return name, nil
}

const teamCols = `t.id, t.ns, t.name, COALESCE(t.workos_org_id, ''), t.created_at`

func scanTeam(r scanner) (Team, error) {
	var t Team
	var created int64
	err := r.Scan(&t.ID, &t.NS, &t.Name, &t.WorkOSOrgID, &created)
	t.CreatedAt = fromMS(created)
	return t, err
}

// CreateTeam records a team (already created as a WorkOS Organization) and,
// when ownerID > 0, makes that user its admin.
func (s *Store) CreateTeam(ctx context.Context, name, workosOrgID string, ownerID int64, ownerMembershipID string, now time.Time) (Team, error) {
	name, err := normalizeTeamName(name)
	if err != nil {
		return Team{}, err
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Team{}, err
	}
	defer tx.Rollback()
	t, err := s.insertTeam(ctx, tx, name, workosOrgID, now)
	if err != nil {
		return Team{}, err
	}
	if ownerID > 0 {
		if err := s.insertMember(ctx, tx, t.ID, ownerID, RoleAdmin, ownerMembershipID, now); err != nil {
			return Team{}, err
		}
	}
	return t, tx.Commit()
}

func (s *Store) insertTeam(ctx context.Context, tx *sql.Tx, name, workosOrgID string, now time.Time) (Team, error) {
	var org any
	if workosOrgID != "" {
		org = workosOrgID
	}
	ns := newNS()
	var id int64
	if err := tx.QueryRowContext(ctx, s.q(`INSERT INTO teams (ns, name, workos_org_id, created_at, updated_at)
		VALUES (?,?,?,?,?) RETURNING id`), ns, name, org, ms(now), ms(now)).Scan(&id); err != nil {
		return Team{}, err
	}
	if err := s.insertDefaultApp(ctx, tx, id, ns, now); err != nil {
		return Team{}, err
	}
	return Team{ID: id, NS: ns, Name: name, WorkOSOrgID: workosOrgID, CreatedAt: fromMS(ms(now))}, nil
}

func (s *Store) insertMember(ctx context.Context, tx *sql.Tx, teamID, userID int64, role Role, membershipID string, now time.Time) error {
	var mid any
	if membershipID != "" {
		mid = membershipID
	}
	_, err := tx.ExecContext(ctx, s.q(`INSERT INTO team_members (team_id, user_id, role, workos_membership_id, created_at)
		VALUES (?,?,?,?,?)`), teamID, userID, string(role), mid, ms(now))
	return err
}

// EnsureTeamForOrg returns the team for a WorkOS Organization, creating it
// (with no members) the first time pushq sees that organization.
func (s *Store) EnsureTeamForOrg(ctx context.Context, orgID, orgName string, now time.Time) (Team, error) {
	t, err := scanTeam(s.w.QueryRowContext(ctx, s.q(`SELECT `+teamCols+` FROM teams t WHERE t.workos_org_id=?`), orgID))
	if err == nil {
		return t, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Team{}, err
	}
	name, nerr := normalizeTeamName(orgName)
	if nerr != nil {
		name = "Team"
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Team{}, err
	}
	defer tx.Rollback()
	t, err = s.insertTeam(ctx, tx, name, orgID, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			// Created concurrently by another sign-in.
			return scanTeam(s.w.QueryRowContext(ctx, s.q(`SELECT `+teamCols+` FROM teams t WHERE t.workos_org_id=?`), orgID))
		}
		return Team{}, err
	}
	return t, tx.Commit()
}

func (s *Store) GetTeam(ctx context.Context, id int64) (Team, error) {
	t, err := scanTeam(s.r.QueryRowContext(ctx, s.q(`SELECT `+teamCols+` FROM teams t WHERE t.id=?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, fmt.Errorf("team: %w", store.ErrNotFound)
	}
	return t, err
}

func (s *Store) RenameTeam(ctx context.Context, id int64, name string, now time.Time) (Team, error) {
	name, err := normalizeTeamName(name)
	if err != nil {
		return Team{}, err
	}
	if _, err := s.w.ExecContext(ctx, s.q(`UPDATE teams SET name=?, updated_at=? WHERE id=?`), name, ms(now), id); err != nil {
		return Team{}, err
	}
	return s.GetTeam(ctx, id)
}

// UserTeams lists a user's teams, oldest membership first.
func (s *Store) UserTeams(ctx context.Context, userID int64) ([]TeamMembership, error) {
	rows, err := s.r.QueryContext(ctx, s.q(`SELECT `+teamCols+`, m.role FROM team_members m
		JOIN teams t ON t.id = m.team_id WHERE m.user_id=? ORDER BY m.created_at, t.id`), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TeamMembership{}
	for rows.Next() {
		var tm TeamMembership
		var created int64
		if err := rows.Scan(&tm.Team.ID, &tm.Team.NS, &tm.Team.Name, &tm.Team.WorkOSOrgID, &created, &tm.Role); err != nil {
			return nil, err
		}
		tm.Team.CreatedAt = fromMS(created)
		out = append(out, tm)
	}
	return out, rows.Err()
}

// MemberRole returns a user's role in a team, or ErrNotMember.
func (s *Store) MemberRole(ctx context.Context, teamID, userID int64) (Role, error) {
	var role Role
	err := s.r.QueryRowContext(ctx, s.q(`SELECT role FROM team_members WHERE team_id=? AND user_id=?`), teamID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotMember
	}
	return role, err
}

// AddMemberIfMissing adds a user to a team with role (keeping the existing
// role if they're already a member) and records the WorkOS membership ID.
// A pending pushq invite for their email decides the role and is consumed.
func (s *Store) AddMemberIfMissing(ctx context.Context, teamID, userID int64, role Role, membershipID string, now time.Time) (Role, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var existing Role
	err = tx.QueryRowContext(ctx, s.q(`SELECT role FROM team_members WHERE team_id=? AND user_id=?`), teamID, userID).Scan(&existing)
	if err == nil {
		if membershipID != "" {
			if _, err := tx.ExecContext(ctx, s.q(`UPDATE team_members SET workos_membership_id=? WHERE team_id=? AND user_id=?`),
				membershipID, teamID, userID); err != nil {
				return "", err
			}
		}
		return existing, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var email string
	if err := tx.QueryRowContext(ctx, s.q(`SELECT email FROM users WHERE id=?`), userID).Scan(&email); err != nil {
		return "", err
	}
	var invRole Role
	err = tx.QueryRowContext(ctx, s.q(`SELECT role FROM team_invites WHERE team_id=? AND email=? ORDER BY created_at DESC LIMIT 1`),
		teamID, email).Scan(&invRole)
	switch {
	case err == nil && invRole.Valid():
		role = invRole
		if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM team_invites WHERE team_id=? AND email=?`), teamID, email); err != nil {
			return "", err
		}
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return "", err
	}
	if !role.Valid() {
		role = RoleMember
	}
	if err := s.insertMember(ctx, tx, teamID, userID, role, membershipID, now); err != nil {
		return "", err
	}
	return role, tx.Commit()
}

// RemoveMembershipsExcept drops a user's local memberships in teams not
// listed (they left or were removed in WorkOS).
func (s *Store) RemoveMembershipsExcept(ctx context.Context, userID int64, keepTeamIDs []int64) error {
	keep := map[int64]bool{}
	for _, id := range keepTeamIDs {
		keep[id] = true
	}
	teams, err := s.UserTeams(ctx, userID)
	if err != nil {
		return err
	}
	for _, tm := range teams {
		if !keep[tm.Team.ID] {
			if _, err := s.w.ExecContext(ctx, s.q(`DELETE FROM team_members WHERE team_id=? AND user_id=?`), tm.Team.ID, userID); err != nil {
				return err
			}
		}
	}
	return nil
}

// RemoveMembersExcept drops a team's local members whose WorkOS membership
// is gone, given the WorkOS membership IDs that still exist.
func (s *Store) RemoveMembersExcept(ctx context.Context, teamID int64, keepMembershipIDs []string) error {
	members, err := s.ListMembers(ctx, teamID)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, id := range keepMembershipIDs {
		keep[id] = true
	}
	for _, m := range members {
		if m.WorkOSMembershipID != "" && !keep[m.WorkOSMembershipID] {
			if _, err := s.w.ExecContext(ctx, s.q(`DELETE FROM team_members WHERE team_id=? AND user_id=?`), teamID, m.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// ListMembers lists a team's members; User.Role is their team role.
func (s *Store) ListMembers(ctx context.Context, teamID int64) ([]Member, error) {
	rows, err := s.r.QueryContext(ctx, s.q(`SELECT u.id, u.email, u.name, m.role, u.created_at, u.last_login_at,
		COALESCE(m.workos_membership_id, '') FROM team_members m JOIN users u ON u.id = m.user_id
		WHERE m.team_id=? ORDER BY u.email`), teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		var created int64
		var last sql.NullInt64
		if err := rows.Scan(&m.ID, &m.Email, &m.Name, &m.Role, &created, &last, &m.WorkOSMembershipID); err != nil {
			return nil, err
		}
		m.CreatedAt, m.LastLoginAt = fromMS(created), optMS(last)
		out = append(out, m)
	}
	return out, rows.Err()
}

// lockTeamAdmins counts a team's admins, locking them on Postgres so
// concurrent changes can't leave the team without one.
func (s *Store) lockTeamAdmins(ctx context.Context, tx *sql.Tx, teamID int64) (int, error) {
	q := `SELECT user_id FROM team_members WHERE team_id=? AND role='admin'`
	if s.pg {
		q += ` FOR UPDATE`
	}
	rows, err := tx.QueryContext(ctx, s.q(q), teamID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	return n, rows.Err()
}

// SetMemberRole changes a member's role and returns their WorkOS membership ID.
func (s *Store) SetMemberRole(ctx context.Context, teamID, userID int64, role Role) (string, error) {
	if !role.Valid() {
		return "", invalid("role must be admin, member or viewer")
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	admins, err := s.lockTeamAdmins(ctx, tx, teamID)
	if err != nil {
		return "", err
	}
	var cur Role
	var mid sql.NullString
	err = tx.QueryRowContext(ctx, s.q(`SELECT role, workos_membership_id FROM team_members WHERE team_id=? AND user_id=?`),
		teamID, userID).Scan(&cur, &mid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotMember
	}
	if err != nil {
		return "", err
	}
	if cur == RoleAdmin && role != RoleAdmin && admins <= 1 {
		return "", ErrLastAdmin
	}
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE team_members SET role=? WHERE team_id=? AND user_id=?`), string(role), teamID, userID); err != nil {
		return "", err
	}
	return mid.String, tx.Commit()
}

// RemoveMember removes a member and returns their WorkOS membership ID.
func (s *Store) RemoveMember(ctx context.Context, teamID, userID int64) (string, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	admins, err := s.lockTeamAdmins(ctx, tx, teamID)
	if err != nil {
		return "", err
	}
	var cur Role
	var mid sql.NullString
	err = tx.QueryRowContext(ctx, s.q(`SELECT role, workos_membership_id FROM team_members WHERE team_id=? AND user_id=?`),
		teamID, userID).Scan(&cur, &mid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotMember
	}
	if err != nil {
		return "", err
	}
	if cur == RoleAdmin && admins <= 1 {
		return "", ErrLastAdmin
	}
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM team_members WHERE team_id=? AND user_id=?`), teamID, userID); err != nil {
		return "", err
	}
	// Their sessions on this team lose access at once.
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE sessions SET team_id=NULL WHERE team_id=? AND user_id=?`), teamID, userID); err != nil {
		return "", err
	}
	return mid.String, tx.Commit()
}

// --- invites (WorkOS sends the email; pushq remembers the role to grant) ---

func (s *Store) AddTeamInvite(ctx context.Context, teamID int64, email string, role Role, workosInvitationID string, expires, now time.Time) (TeamInvite, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return TeamInvite{}, err
	}
	if !role.Valid() {
		return TeamInvite{}, invalid("role must be admin, member or viewer")
	}
	var id int64
	err = s.w.QueryRowContext(ctx, s.q(`INSERT INTO team_invites (team_id, email, role, workos_invitation_id, created_at, expires_at)
		VALUES (?,?,?,?,?,?) RETURNING id`), teamID, email, string(role), workosInvitationID, ms(now), ms(expires)).Scan(&id)
	if err != nil {
		return TeamInvite{}, err
	}
	return TeamInvite{ID: id, TeamID: teamID, Email: email, Role: role, WorkOSInvitationID: workosInvitationID,
		CreatedAt: fromMS(ms(now)), ExpiresAt: fromMS(ms(expires))}, nil
}

// TeamInviteRoles maps WorkOS invitation IDs to the roles pushq will grant.
func (s *Store) TeamInviteRoles(ctx context.Context, teamID int64) (map[string]TeamInvite, error) {
	rows, err := s.r.QueryContext(ctx, s.q(`SELECT id, email, role, COALESCE(workos_invitation_id,''), created_at, expires_at
		FROM team_invites WHERE team_id=?`), teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]TeamInvite{}
	for rows.Next() {
		var inv TeamInvite
		var created, expires int64
		if err := rows.Scan(&inv.ID, &inv.Email, &inv.Role, &inv.WorkOSInvitationID, &created, &expires); err != nil {
			return nil, err
		}
		inv.TeamID, inv.CreatedAt, inv.ExpiresAt = teamID, fromMS(created), fromMS(expires)
		out[inv.WorkOSInvitationID] = inv
	}
	return out, rows.Err()
}

func (s *Store) DeleteTeamInvite(ctx context.Context, teamID int64, workosInvitationID string) error {
	_, err := s.w.ExecContext(ctx, s.q(`DELETE FROM team_invites WHERE team_id=? AND workos_invitation_id=?`), teamID, workosInvitationID)
	return err
}

// --- current team on a session ---

// SetSessionTeam switches a session to a team the user belongs to.
func (s *Store) SetSessionTeam(ctx context.Context, token string, userID, teamID int64) error {
	if _, err := s.MemberRole(ctx, teamID, userID); err != nil {
		return err
	}
	if _, err := s.w.ExecContext(ctx, s.q(`UPDATE sessions SET team_id=? WHERE token_hash=? AND user_id=?`),
		teamID, digest(token), userID); err != nil {
		return err
	}
	_, err := s.w.ExecContext(ctx, s.q(`UPDATE users SET last_team_id=? WHERE id=?`), teamID, userID)
	return err
}

// LastTeam returns the team a user last worked in (0 if none or no longer a member).
func (s *Store) LastTeam(ctx context.Context, userID int64) (int64, error) {
	var id sql.NullInt64
	err := s.r.QueryRowContext(ctx, s.q(`SELECT u.last_team_id FROM users u
		JOIN team_members m ON m.team_id = u.last_team_id AND m.user_id = u.id WHERE u.id=?`), userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id.Int64, err
}
