// Package accounts stores dashboard users, login sessions, team invites and
// dashboard-managed API keys. It runs on the same database as the queue store
// (SQLite or Postgres) through database/sql; the schema lives in each store's
// 0002_accounts.sql migration and uses unix-millisecond timestamps so a
// single implementation serves both dialects.
package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blissfulrays/pushq/internal/store"
)

type Role string

const (
	RoleAdmin  Role = "admin"  // everything, including team and API keys
	RoleMember Role = "member" // manage queues and tasks
	RoleViewer Role = "viewer" // read-only
)

func (r Role) Valid() bool { return r == RoleAdmin || r == RoleMember || r == RoleViewer }

// KeyRole is an API key's role; same meaning as config-file api_keys.
type KeyRole string

const (
	KeyAdmin  KeyRole = "admin"
	KeyWriter KeyRole = "writer"
)

func (r KeyRole) Valid() bool { return r == KeyAdmin || r == KeyWriter }

const (
	SessionTTL     = 30 * 24 * time.Hour
	sessionRenewAt = SessionTTL / 2 // extend once less than this remains
	InviteTTL      = 7 * 24 * time.Hour
	ResetTTL       = 24 * time.Hour
	keyTouchEvery  = time.Minute
)

var (
	// ErrBadCredentials is returned for an unknown email or wrong password
	// (deliberately indistinguishable).
	ErrBadCredentials = errors.New("invalid email or password")
	ErrSetupDone      = fmt.Errorf("setup has already been completed: %w", store.ErrFailedPrecondition)
	ErrLastAdmin      = fmt.Errorf("the team must keep at least one admin: %w", store.ErrFailedPrecondition)
)

// InvalidError is a validation failure; the message is safe to show users.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &InvalidError{Msg: fmt.Sprintf(format, args...)}
}

type User struct {
	ID          int64
	Email       string
	Name        string
	Role        Role
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

type Invite struct {
	ID        int64
	Kind      string // "invite" (new member) or "reset" (password reset)
	Email     string
	Role      Role // invites only
	UserID    *int64
	CreatedAt time.Time
	ExpiresAt time.Time
}

type APIKey struct {
	ID             int64
	TeamID         int64 // 0 = not team-scoped (self-hosted)
	Name           string
	Prefix         string // first characters of the key, for recognition
	Role           KeyRole
	CreatedByEmail string
	CreatedAt      time.Time
	LastUsedAt     *time.Time
}

type Store struct {
	w, r *sql.DB
	pg   bool

	hasUsers atomic.Bool // sticky once true: the last admin can't be removed

	mu       sync.Mutex
	keyTouch map[int64]time.Time // API key id -> last last_used_at write
}

// New wraps the store's database handles. write must be the handle used for
// all writes (SQLite's single writer connection).
func New(write, read *sql.DB, dialect string) *Store {
	return &Store{w: write, r: read, pg: dialect == "postgres", keyTouch: map[int64]time.Time{}}
}

// q rewrites ? placeholders to $n for Postgres.
func (s *Store) q(query string) string {
	if !s.pg {
		return query
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}

func ms(t time.Time) int64     { return t.UnixMilli() }
func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }
func optMS(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromMS(v.Int64)
	return &t
}

func newSecret(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

func digest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func normalizeEmail(e string) (string, error) {
	e = strings.ToLower(strings.TrimSpace(e))
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || len(e) > 254 {
		return "", invalid("enter a valid email address")
	}
	return e, nil
}

func normalizeName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if len([]rune(n)) > 100 {
		return "", invalid("name must be at most 100 characters")
	}
	return n, nil
}

// --- users & setup ---

// HasUsers reports whether any account exists (i.e. first-time setup is done).
func (s *Store) HasUsers(ctx context.Context) (bool, error) {
	if s.hasUsers.Load() {
		return true, nil
	}
	var one int
	err := s.r.QueryRowContext(ctx, `SELECT 1 FROM users LIMIT 1`).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.hasUsers.Store(true)
	return true, nil
}

// Setup creates the first admin. It fails with ErrSetupDone once any user
// exists, so concurrent attempts produce exactly one admin.
func (s *Store) Setup(ctx context.Context, email, name, password string, now time.Time) (User, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	if name, err = normalizeName(name); err != nil {
		return User{}, err
	}
	if err := validatePassword(password); err != nil {
		return User{}, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	if s.pg {
		// Serialize concurrent setups (SQLite's single writer already does).
		if _, err := tx.ExecContext(ctx, `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return User{}, err
		}
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return User{}, err
	}
	if n > 0 {
		return User{}, ErrSetupDone
	}
	u, err := s.insertUser(ctx, tx, email, name, hash, RoleAdmin, now)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	s.hasUsers.Store(true)
	return u, nil
}

func (s *Store) insertUser(ctx context.Context, tx *sql.Tx, email, name, hash string, role Role, now time.Time) (User, error) {
	var exists int
	err := tx.QueryRowContext(ctx, s.q(`SELECT 1 FROM users WHERE email=?`), email).Scan(&exists)
	if err == nil {
		return User{}, fmt.Errorf("a user with email %s already exists: %w", email, store.ErrAlreadyExists)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return User{}, err
	}
	var id int64
	// New accounts are signed in straight away (setup, invite acceptance).
	err = tx.QueryRowContext(ctx, s.q(`INSERT INTO users (email, name, password_hash, role, created_at, updated_at, last_login_at)
		VALUES (?,?,?,?,?,?,?) RETURNING id`), email, name, hash, string(role), ms(now), ms(now), ms(now)).Scan(&id)
	if err != nil {
		return User{}, err
	}
	t := fromMS(ms(now))
	return User{ID: id, Email: email, Name: name, Role: role, CreatedAt: t, LastLoginAt: &t}, nil
}

const userCols = `id, email, name, role, created_at, last_login_at`

type scanner interface{ Scan(...any) error }

func scanUser(r scanner) (User, error) {
	var u User
	var created int64
	var lastLogin sql.NullInt64
	if err := r.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &created, &lastLogin); err != nil {
		return u, err
	}
	u.CreatedAt = fromMS(created)
	u.LastLoginAt = optMS(lastLogin)
	return u, nil
}

// Authenticate checks an email/password pair.
func (s *Store) Authenticate(ctx context.Context, email, password string, now time.Time) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var hash string
	var u User
	var created int64
	var lastLogin sql.NullInt64
	err := s.r.QueryRowContext(ctx, s.q(`SELECT `+userCols+`, password_hash FROM users WHERE email=?`), email).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, &created, &lastLogin, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		verifyPassword(dummyHash(), password) // equalize timing
		return User{}, ErrBadCredentials
	}
	if err != nil {
		return User{}, err
	}
	if !verifyPassword(hash, password) {
		return User{}, ErrBadCredentials
	}
	u.CreatedAt = fromMS(created)
	if _, err := s.w.ExecContext(ctx, s.q(`UPDATE users SET last_login_at=? WHERE id=?`), ms(now), u.ID); err != nil {
		return User{}, err
	}
	t := fromMS(ms(now))
	u.LastLoginAt = &t
	return u, nil
}

func (s *Store) GetUser(ctx context.Context, id int64) (User, error) {
	u, err := scanUser(s.r.QueryRowContext(ctx, s.q(`SELECT `+userCols+` FROM users WHERE id=?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return u, fmt.Errorf("user: %w", store.ErrNotFound)
	}
	return u, err
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// lockAdmins returns how many admins exist, locking their rows on Postgres
// so concurrent demotions/removals can't leave the team without an admin.
func (s *Store) lockAdmins(ctx context.Context, tx *sql.Tx) (int, error) {
	q := `SELECT id FROM users WHERE role='admin'`
	if s.pg {
		q += ` FOR UPDATE`
	}
	rows, err := tx.QueryContext(ctx, q)
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

// UpdateUser changes a user's name and/or role.
func (s *Store) UpdateUser(ctx context.Context, id int64, name *string, role *Role, now time.Time) (User, error) {
	if role != nil && !role.Valid() {
		return User{}, invalid("role must be admin, member or viewer")
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	admins, err := s.lockAdmins(ctx, tx)
	if err != nil {
		return User{}, err
	}
	u, err := scanUser(tx.QueryRowContext(ctx, s.q(`SELECT `+userCols+` FROM users WHERE id=?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return u, fmt.Errorf("user: %w", store.ErrNotFound)
	}
	if err != nil {
		return u, err
	}
	if name != nil {
		if u.Name, err = normalizeName(*name); err != nil {
			return u, err
		}
	}
	if role != nil {
		if u.Role == RoleAdmin && *role != RoleAdmin && admins <= 1 {
			return u, ErrLastAdmin
		}
		u.Role = *role
	}
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE users SET name=?, role=?, updated_at=? WHERE id=?`),
		u.Name, string(u.Role), ms(now), id); err != nil {
		return u, err
	}
	return u, tx.Commit()
}

// DeleteUser removes a user and all their sessions.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	admins, err := s.lockAdmins(ctx, tx)
	if err != nil {
		return err
	}
	var role Role
	err = tx.QueryRowContext(ctx, s.q(`SELECT role FROM users WHERE id=?`), id).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("user: %w", store.ErrNotFound)
	}
	if err != nil {
		return err
	}
	if role == RoleAdmin && admins <= 1 {
		return ErrLastAdmin
	}
	// Sessions and pending reset links cascade with the user.
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM users WHERE id=?`), id); err != nil {
		return err
	}
	return tx.Commit()
}

// ChangePassword sets a new password after checking the current one, and
// signs the user out everywhere except keepSessionToken.
func (s *Store) ChangePassword(ctx context.Context, id int64, current, next, keepSessionToken string, now time.Time) error {
	if err := validatePassword(next); err != nil {
		return err
	}
	var hash string
	err := s.r.QueryRowContext(ctx, s.q(`SELECT password_hash FROM users WHERE id=?`), id).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("user: %w", store.ErrNotFound)
	}
	if err != nil {
		return err
	}
	if !verifyPassword(hash, current) {
		return invalid("current password is incorrect")
	}
	newHash, err := hashPassword(next)
	if err != nil {
		return err
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE users SET password_hash=?, updated_at=? WHERE id=?`), newHash, ms(now), id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM sessions WHERE user_id=? AND token_hash != ?`),
		id, digest(keepSessionToken)); err != nil {
		return err
	}
	return tx.Commit()
}

// --- sessions ---

// CreateSession signs a user in and returns the secret cookie value.
func (s *Store) CreateSession(ctx context.Context, userID int64, now time.Time) (token string, expires time.Time, err error) {
	return s.CreateTeamSession(ctx, userID, 0, now)
}

// CreateTeamSession signs a user in with a current team (SaaS mode; 0 = none).
func (s *Store) CreateTeamSession(ctx context.Context, userID, teamID int64, now time.Time) (token string, expires time.Time, err error) {
	token = newSecret("pqs_", 32)
	expires = now.Add(SessionTTL)
	// Opportunistically drop expired sessions; the table stays small.
	if _, err := s.w.ExecContext(ctx, s.q(`DELETE FROM sessions WHERE expires_at <= ?`), ms(now)); err != nil {
		return "", time.Time{}, err
	}
	var team any
	if teamID > 0 {
		team = teamID
	}
	_, err = s.w.ExecContext(ctx, s.q(`INSERT INTO sessions (token_hash, user_id, created_at, expires_at, team_id) VALUES (?,?,?,?,?)`),
		digest(token), userID, ms(now), ms(expires), team)
	return token, expires, err
}

// SessionUser resolves a session cookie to its user. Sessions slide: one used
// in the second half of its lifetime is extended. renewed is the new expiry
// when that happened (so the cookie can be refreshed too).
func (s *Store) SessionUser(ctx context.Context, token string, now time.Time) (u User, renewed *time.Time, err error) {
	u, _, renewed, err = s.Session(ctx, token, now)
	return u, renewed, err
}

// Session is SessionUser plus the session's current team (0 = none).
func (s *Store) Session(ctx context.Context, token string, now time.Time) (u User, teamID int64, renewed *time.Time, err error) {
	var sid, expMS int64
	var created int64
	var lastLogin, team sql.NullInt64
	err = s.r.QueryRowContext(ctx, s.q(`SELECT s.id, s.expires_at, s.team_id, u.id, u.email, u.name, u.role, u.created_at, u.last_login_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash=? AND s.expires_at > ?`), digest(token), ms(now)).
		Scan(&sid, &expMS, &team, &u.ID, &u.Email, &u.Name, &u.Role, &created, &lastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, 0, nil, ErrBadCredentials
	}
	if err != nil {
		return User{}, 0, nil, err
	}
	teamID = team.Int64
	u.CreatedAt = fromMS(created)
	u.LastLoginAt = optMS(lastLogin)
	if fromMS(expMS).Sub(now) < sessionRenewAt {
		exp := now.Add(SessionTTL)
		if _, err := s.w.ExecContext(ctx, s.q(`UPDATE sessions SET expires_at=? WHERE id=?`), ms(exp), sid); err != nil {
			return User{}, 0, nil, err
		}
		renewed = &exp
	}
	return u, teamID, renewed, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.w.ExecContext(ctx, s.q(`DELETE FROM sessions WHERE token_hash=?`), digest(token))
	return err
}

// --- invites & password resets ---

const inviteCols = `id, kind, email, role, user_id, created_at, expires_at`

func scanInvite(r scanner) (Invite, error) {
	var inv Invite
	var role sql.NullString
	var userID sql.NullInt64
	var created, expires int64
	if err := r.Scan(&inv.ID, &inv.Kind, &inv.Email, &role, &userID, &created, &expires); err != nil {
		return inv, err
	}
	inv.Role = Role(role.String)
	if userID.Valid {
		inv.UserID = &userID.Int64
	}
	inv.CreatedAt, inv.ExpiresAt = fromMS(created), fromMS(expires)
	return inv, nil
}

func (s *Store) insertInvite(ctx context.Context, tx *sql.Tx, kind, email string, role *Role, userID, createdBy *int64, now time.Time, ttl time.Duration) (string, Invite, error) {
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM invites WHERE expires_at <= ?`), ms(now)); err != nil {
		return "", Invite{}, err
	}
	token := newSecret("pqi_", 32)
	var roleArg any
	if role != nil {
		roleArg = string(*role)
	}
	var id int64
	err := tx.QueryRowContext(ctx, s.q(`INSERT INTO invites (token_hash, kind, email, role, user_id, created_by, created_at, expires_at)
		VALUES (?,?,?,?,?,?,?,?) RETURNING id`),
		digest(token), kind, email, roleArg, userID, createdBy, ms(now), ms(now.Add(ttl))).Scan(&id)
	if err != nil {
		return "", Invite{}, err
	}
	inv := Invite{ID: id, Kind: kind, Email: email, UserID: userID,
		CreatedAt: fromMS(ms(now)), ExpiresAt: fromMS(ms(now.Add(ttl)))}
	if role != nil {
		inv.Role = *role
	}
	return token, inv, nil
}

// CreateInvite makes a single-use link for a new team member. Re-inviting an
// email replaces its previous pending invite. createdBy is nil when an admin
// API key (not a user) issues it.
func (s *Store) CreateInvite(ctx context.Context, email string, role Role, createdBy *int64, now time.Time) (string, Invite, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return "", Invite{}, err
	}
	if !role.Valid() {
		return "", Invite{}, invalid("role must be admin, member or viewer")
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return "", Invite{}, err
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRowContext(ctx, s.q(`SELECT 1 FROM users WHERE email=?`), email).Scan(&exists)
	if err == nil {
		return "", Invite{}, fmt.Errorf("%s is already a team member: %w", email, store.ErrAlreadyExists)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", Invite{}, err
	}
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM invites WHERE kind='invite' AND email=?`), email); err != nil {
		return "", Invite{}, err
	}
	token, inv, err := s.insertInvite(ctx, tx, "invite", email, &role, nil, createdBy, now, InviteTTL)
	if err != nil {
		return "", Invite{}, err
	}
	return token, inv, tx.Commit()
}

// CreateResetLink makes a single-use password-reset link for a user,
// replacing any earlier one. createdBy is nil when issued from the CLI.
func (s *Store) CreateResetLink(ctx context.Context, userID int64, createdBy *int64, now time.Time) (string, Invite, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return "", Invite{}, err
	}
	defer tx.Rollback()
	var email string
	err = tx.QueryRowContext(ctx, s.q(`SELECT email FROM users WHERE id=?`), userID).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", Invite{}, fmt.Errorf("user: %w", store.ErrNotFound)
	}
	if err != nil {
		return "", Invite{}, err
	}
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM invites WHERE kind='reset' AND user_id=?`), userID); err != nil {
		return "", Invite{}, err
	}
	token, inv, err := s.insertInvite(ctx, tx, "reset", email, nil, &userID, createdBy, now, ResetTTL)
	if err != nil {
		return "", Invite{}, err
	}
	return token, inv, tx.Commit()
}

// UserByEmail looks a user up by email (CLI password recovery).
func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	u, err := scanUser(s.r.QueryRowContext(ctx, s.q(`SELECT `+userCols+` FROM users WHERE email=?`),
		strings.ToLower(strings.TrimSpace(email))))
	if errors.Is(err, sql.ErrNoRows) {
		return u, fmt.Errorf("no user with email %s: %w", email, store.ErrNotFound)
	}
	return u, err
}

// ListInvites returns pending (unexpired) member invites.
func (s *Store) ListInvites(ctx context.Context, now time.Time) ([]Invite, error) {
	rows, err := s.r.QueryContext(ctx, s.q(`SELECT `+inviteCols+` FROM invites
		WHERE kind='invite' AND expires_at > ? ORDER BY created_at DESC`), ms(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invite{}
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (s *Store) DeleteInvite(ctx context.Context, id int64) error {
	res, err := s.w.ExecContext(ctx, s.q(`DELETE FROM invites WHERE id=? AND kind='invite'`), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("invite: %w", store.ErrNotFound)
	}
	return nil
}

var errBadLink = fmt.Errorf("this link is invalid or has expired: %w", store.ErrNotFound)

// LookupInvite resolves an invite or reset token without consuming it.
func (s *Store) LookupInvite(ctx context.Context, token string, now time.Time) (Invite, error) {
	inv, err := scanInvite(s.r.QueryRowContext(ctx, s.q(`SELECT `+inviteCols+` FROM invites
		WHERE token_hash=? AND expires_at > ?`), digest(token), ms(now)))
	if errors.Is(err, sql.ErrNoRows) {
		return inv, errBadLink
	}
	return inv, err
}

// AcceptInvite consumes an invite (creating the account) or a reset link
// (setting a new password and signing the user out everywhere).
func (s *Store) AcceptInvite(ctx context.Context, token, name, password string, now time.Time) (User, error) {
	if err := validatePassword(password); err != nil {
		return User{}, err
	}
	name, err := normalizeName(name)
	if err != nil {
		return User{}, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	// Delete-and-return consumes the token atomically: a link works once.
	inv, err := scanInvite(tx.QueryRowContext(ctx, s.q(`DELETE FROM invites
		WHERE token_hash=? AND expires_at > ? RETURNING `+inviteCols), digest(token), ms(now)))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, errBadLink
	}
	if err != nil {
		return User{}, err
	}
	var u User
	switch inv.Kind {
	case "invite":
		if u, err = s.insertUser(ctx, tx, inv.Email, name, hash, inv.Role, now); err != nil {
			return User{}, err
		}
	case "reset":
		if inv.UserID == nil {
			return User{}, errBadLink
		}
		if _, err := tx.ExecContext(ctx, s.q(`UPDATE users SET password_hash=?, updated_at=?, last_login_at=? WHERE id=?`),
			hash, ms(now), ms(now), *inv.UserID); err != nil {
			return User{}, err
		}
		if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM sessions WHERE user_id=?`), *inv.UserID); err != nil {
			return User{}, err
		}
		if u, err = scanUser(tx.QueryRowContext(ctx, s.q(`SELECT `+userCols+` FROM users WHERE id=?`), *inv.UserID)); err != nil {
			return User{}, err
		}
	default:
		return User{}, errBadLink
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	s.hasUsers.Store(true)
	return u, nil
}

// --- API keys ---

// CreateAPIKey returns the plaintext key; only its digest is stored, so it
// can never be shown again.
func (s *Store) CreateAPIKey(ctx context.Context, teamID int64, name string, role KeyRole, createdBy *int64, now time.Time) (string, APIKey, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 {
		return "", APIKey{}, invalid("key name must be 1-100 characters")
	}
	if !role.Valid() {
		return "", APIKey{}, invalid("key role must be admin or writer")
	}
	key := newSecret("pq_", 24)
	prefix := key[:11]
	var id int64
	var team any
	if teamID > 0 {
		team = teamID
	}
	err := s.w.QueryRowContext(ctx, s.q(`INSERT INTO api_keys (name, prefix, key_hash, role, created_by, created_at, team_id)
		VALUES (?,?,?,?,?,?,?) RETURNING id`), name, prefix, digest(key), string(role), createdBy, ms(now), team).Scan(&id)
	if err != nil {
		return "", APIKey{}, err
	}
	return key, APIKey{ID: id, TeamID: teamID, Name: name, Prefix: prefix, Role: role, CreatedAt: fromMS(ms(now))}, nil
}

// ListAPIKeys lists one team's keys (teamID 0 = the self-hosted, unscoped keys).
func (s *Store) ListAPIKeys(ctx context.Context, teamID int64) ([]APIKey, error) {
	rows, err := s.r.QueryContext(ctx, s.q(`SELECT k.id, k.name, k.prefix, k.role, COALESCE(u.email, ''), k.created_at, k.last_used_at
		FROM api_keys k LEFT JOIN users u ON u.id = k.created_by
		WHERE COALESCE(k.team_id, 0)=? ORDER BY k.created_at DESC`), teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		var k APIKey
		var created int64
		var used sql.NullInt64
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.Role, &k.CreatedByEmail, &created, &used); err != nil {
			return nil, err
		}
		k.TeamID = teamID
		k.CreatedAt, k.LastUsedAt = fromMS(created), optMS(used)
		out = append(out, k)
	}
	return out, rows.Err()
}

// DeleteAPIKey revokes a key; it must belong to teamID (0 = self-hosted).
func (s *Store) DeleteAPIKey(ctx context.Context, teamID, id int64) error {
	res, err := s.w.ExecContext(ctx, s.q(`DELETE FROM api_keys WHERE id=? AND COALESCE(team_id, 0)=?`), id, teamID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("api key: %w", store.ErrNotFound)
	}
	return nil
}

// LookupAPIKey resolves a bearer token against dashboard-managed keys.
// Revocation takes effect on the next request.
func (s *Store) LookupAPIKey(ctx context.Context, key string, now time.Time) (APIKey, bool, error) {
	var k APIKey
	var created int64
	var used sql.NullInt64
	err := s.r.QueryRowContext(ctx, s.q(`SELECT id, COALESCE(team_id, 0), name, prefix, role, created_at, last_used_at
		FROM api_keys WHERE key_hash=?`), digest(key)).
		Scan(&k.ID, &k.TeamID, &k.Name, &k.Prefix, &k.Role, &created, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return APIKey{}, false, nil
	}
	if err != nil {
		return APIKey{}, false, err
	}
	k.CreatedAt, k.LastUsedAt = fromMS(created), optMS(used)

	// Record usage at most once a minute per key per process, so a busy
	// key doesn't turn every API call into a write.
	s.mu.Lock()
	touch := now.Sub(s.keyTouch[k.ID]) >= keyTouchEvery
	if touch {
		s.keyTouch[k.ID] = now
	}
	s.mu.Unlock()
	if touch {
		if _, err := s.w.ExecContext(ctx, s.q(`UPDATE api_keys SET last_used_at=? WHERE id=?`), ms(now), k.ID); err != nil {
			return APIKey{}, false, err
		}
	}
	return k, true, nil
}
