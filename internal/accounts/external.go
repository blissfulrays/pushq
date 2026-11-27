package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blissfulrays/pushq/internal/store"
)

// ExternalIdentity is a person authenticated by an identity provider (WorkOS).
type ExternalIdentity struct {
	WorkOSUserID  string
	Email         string
	EmailVerified bool
	Name          string
}

// noPassword marks accounts that sign in through WorkOS. It never verifies.
const noPassword = "!"

// ErrIdentityConflict means the email already belongs to an account linked
// to a different WorkOS user.
var ErrIdentityConflict = fmt.Errorf("this email is already linked to a different WorkOS account: %w", store.ErrAlreadyExists)

// UpsertWorkOSUser finds or creates the pushq user for a WorkOS user (SaaS
// mode). It matches by WorkOS user ID, then links an existing account with the
// same verified email, otherwise creates one. Users carry no global role in
// SaaS mode: what they may do comes from their team memberships.
func (s *Store) UpsertWorkOSUser(ctx context.Context, id ExternalIdentity, now time.Time) (User, error) {
	if id.WorkOSUserID == "" {
		return User{}, invalid("missing WorkOS user id")
	}
	name, err := normalizeName(id.Name)
	if err != nil {
		name = ""
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()

	finish := func(userID int64) (User, error) {
		if _, err := tx.ExecContext(ctx, s.q(`UPDATE users SET last_login_at=? WHERE id=?`), ms(now), userID); err != nil {
			return User{}, err
		}
		u, err := scanUser(tx.QueryRowContext(ctx, s.q(`SELECT `+userCols+` FROM users WHERE id=?`), userID))
		if err != nil {
			return User{}, err
		}
		if err := tx.Commit(); err != nil {
			return User{}, err
		}
		s.hasUsers.Store(true)
		return u, nil
	}

	var userID int64
	err = tx.QueryRowContext(ctx, s.q(`SELECT id FROM users WHERE workos_user_id=?`), id.WorkOSUserID).Scan(&userID)
	if err == nil {
		if name != "" {
			// Keep names in step with WorkOS (people edit them there).
			if _, err := tx.ExecContext(ctx, s.q(`UPDATE users SET name=? WHERE id=? AND name=''`), name, userID); err != nil {
				return User{}, err
			}
		}
		return finish(userID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return User{}, err
	}

	// Linking or creating by email requires WorkOS to have verified it, so
	// nobody can take over an account with an address they don't own.
	if !id.EmailVerified {
		return User{}, invalid("your email address isn't verified yet; verify it and try again")
	}
	email, err := normalizeEmail(id.Email)
	if err != nil {
		return User{}, err
	}
	var linked sql.NullString
	err = tx.QueryRowContext(ctx, s.q(`SELECT id, workos_user_id FROM users WHERE email=?`), email).Scan(&userID, &linked)
	switch {
	case err == nil:
		if linked.Valid && linked.String != id.WorkOSUserID {
			return User{}, ErrIdentityConflict
		}
		if _, err := tx.ExecContext(ctx, s.q(`UPDATE users SET workos_user_id=?, updated_at=? WHERE id=?`),
			id.WorkOSUserID, ms(now), userID); err != nil {
			return User{}, err
		}
		return finish(userID)
	case !errors.Is(err, sql.ErrNoRows):
		return User{}, err
	}

	// The users.role column is NOT NULL but unused in SaaS mode.
	u, err := s.insertUser(ctx, tx, email, name, noPassword, RoleViewer, now)
	if err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE users SET workos_user_id=? WHERE id=?`), id.WorkOSUserID, u.ID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return User{}, ErrIdentityConflict
		}
		return User{}, err
	}
	return finish(u.ID)
}

// WorkOSUserID returns the WorkOS user ID linked to a user ("" if none).
func (s *Store) WorkOSUserID(ctx context.Context, userID int64) (string, error) {
	var id sql.NullString
	err := s.r.QueryRowContext(ctx, s.q(`SELECT workos_user_id FROM users WHERE id=?`), userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("user: %w", store.ErrNotFound)
	}
	return id.String, err
}

// UserIDByWorkOS returns the local user for a WorkOS user ID.
func (s *Store) UserIDByWorkOS(ctx context.Context, workosUserID string) (int64, bool, error) {
	var id int64
	err := s.r.QueryRowContext(ctx, s.q(`SELECT id FROM users WHERE workos_user_id=?`), workosUserID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}
