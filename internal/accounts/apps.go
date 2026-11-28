package accounts

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blissfulrays/pushq/internal/store"
)

// Applications group queues. Every team (SaaS) or server (self-hosted,
// teamID 0) has a Default application plus any others its admins create.
// Each application is a queue namespace: stored queue names are "NS:name",
// or just "name" for the self-hosted Default application (NS "").
//
// The Default application reuses the namespace queues had before
// applications existed, so nothing has to be renamed.

// DefaultAppName is the name of the application every team starts with.
const DefaultAppName = "Default"

type App struct {
	ID        int64
	TeamID    int64 // 0 = self-hosted
	NS        string
	Name      string
	Icon      string // data: URL of a small image; "" = initials
	Default   bool
	CreatedAt time.Time
}

// ErrDefaultApp is returned when deleting a team's Default application.
var ErrDefaultApp = fmt.Errorf("the default application can't be deleted: %w", store.ErrFailedPrecondition)

const appCols = `a.id, COALESCE(a.team_id, 0), a.ns, a.name, COALESCE(a.icon, ''), a.is_default, a.created_at`

func scanApp(r scanner) (App, error) {
	var a App
	var def int
	var created int64
	err := r.Scan(&a.ID, &a.TeamID, &a.NS, &a.Name, &a.Icon, &def, &created)
	a.Default, a.CreatedAt = def == 1, fromMS(created)
	return a, err
}

func normalizeAppName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 60 {
		return "", invalid("application name must be 1-60 characters")
	}
	return name, nil
}

// MaxAppIconBytes caps an icon's data: URL (the console sends ~128px images).
const MaxAppIconBytes = 64 << 10

var appIconTypes = []string{"image/png", "image/jpeg", "image/webp", "image/gif"}

// normalizeAppIcon accepts "" (no icon) or a base64 data: URL of a PNG,
// JPEG, WebP or GIF image. SVG is refused: it can carry script.
func normalizeAppIcon(icon string) (string, error) {
	icon = strings.TrimSpace(icon)
	if icon == "" {
		return "", nil
	}
	if len(icon) > MaxAppIconBytes {
		return "", invalid("application icon must be at most %d KB", MaxAppIconBytes>>10)
	}
	for _, t := range appIconTypes {
		if data, ok := strings.CutPrefix(icon, "data:"+t+";base64,"); ok {
			if _, err := base64.StdEncoding.DecodeString(data); err != nil || data == "" {
				return "", invalid("application icon is not valid base64")
			}
			return icon, nil
		}
	}
	return "", invalid("application icon must be a PNG, JPEG, WebP or GIF data: URL")
}

func nullIcon(icon string) any {
	if icon == "" {
		return nil
	}
	return icon
}

func nullTeam(teamID int64) any {
	if teamID > 0 {
		return teamID
	}
	return nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

// insertDefaultApp records a team's Default application, which owns the
// team's own namespace.
func (s *Store) insertDefaultApp(ctx context.Context, tx *sql.Tx, teamID int64, ns string, now time.Time) error {
	_, err := tx.ExecContext(ctx, s.q(`INSERT INTO apps (team_id, ns, name, is_default, created_at, updated_at)
		VALUES (?,?,?,1,?,?)`), nullTeam(teamID), ns, DefaultAppName, ms(now), ms(now))
	return err
}

// ListApps lists a team's applications, Default first, then by name.
func (s *Store) ListApps(ctx context.Context, teamID int64) ([]App, error) {
	if _, err := s.DefaultApp(ctx, teamID); err != nil {
		return nil, err
	}
	rows, err := s.r.QueryContext(ctx, s.q(`SELECT `+appCols+` FROM apps a WHERE COALESCE(a.team_id, 0)=?
		ORDER BY a.is_default DESC, lower(a.name), a.id`), teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []App{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetApp returns one of a team's applications.
func (s *Store) GetApp(ctx context.Context, teamID, id int64) (App, error) {
	a, err := scanApp(s.r.QueryRowContext(ctx, s.q(`SELECT `+appCols+` FROM apps a
		WHERE a.id=? AND COALESCE(a.team_id, 0)=?`), id, teamID))
	if errors.Is(err, sql.ErrNoRows) {
		return App{}, fmt.Errorf("application: %w", store.ErrNotFound)
	}
	return a, err
}

// DefaultApp returns a team's Default application, creating it if missing
// (the migration and team creation normally already have).
func (s *Store) DefaultApp(ctx context.Context, teamID int64) (App, error) {
	q := s.q(`SELECT ` + appCols + ` FROM apps a WHERE COALESCE(a.team_id, 0)=? AND a.is_default=1`)
	a, err := scanApp(s.r.QueryRowContext(ctx, q, teamID))
	if !errors.Is(err, sql.ErrNoRows) {
		return a, err
	}
	ns := ""
	if teamID > 0 {
		t, err := s.GetTeam(ctx, teamID)
		if err != nil {
			return App{}, err
		}
		ns = t.NS
	}
	now := ms(time.Now())
	_, err = s.w.ExecContext(ctx, s.q(`INSERT INTO apps (team_id, ns, name, is_default, created_at, updated_at)
		VALUES (?,?,?,1,?,?)`), nullTeam(teamID), ns, DefaultAppName, now, now)
	if err != nil && !isUnique(err) { // unique: created concurrently
		return App{}, err
	}
	// Read from the write handle: a replica may not have the new row yet.
	return scanApp(s.w.QueryRowContext(ctx, q, teamID))
}

// CreateApp adds an application with a fresh namespace.
func (s *Store) CreateApp(ctx context.Context, teamID int64, name, icon string, now time.Time) (App, error) {
	name, err := normalizeAppName(name)
	if err != nil {
		return App{}, err
	}
	if icon, err = normalizeAppIcon(icon); err != nil {
		return App{}, err
	}
	if _, err := s.DefaultApp(ctx, teamID); err != nil {
		return App{}, err
	}
	ns := newNS()
	var id int64
	err = s.w.QueryRowContext(ctx, s.q(`INSERT INTO apps (team_id, ns, name, icon, is_default, created_at, updated_at)
		VALUES (?,?,?,?,0,?,?) RETURNING id`), nullTeam(teamID), ns, name, nullIcon(icon), ms(now), ms(now)).Scan(&id)
	if isUnique(err) {
		return App{}, fmt.Errorf("an application named %q already exists: %w", name, store.ErrAlreadyExists)
	}
	if err != nil {
		return App{}, err
	}
	return App{ID: id, TeamID: teamID, NS: ns, Name: name, Icon: icon, CreatedAt: fromMS(ms(now))}, nil
}

// UpdateApp renames one of a team's applications (the Default one too)
// and/or changes its icon ("" removes it). Nil fields are left alone.
func (s *Store) UpdateApp(ctx context.Context, teamID, id int64, name, icon *string, now time.Time) (App, error) {
	sets, args := []string{"updated_at=?"}, []any{ms(now)}
	var newName string
	if name != nil {
		n, err := normalizeAppName(*name)
		if err != nil {
			return App{}, err
		}
		newName = n
		sets, args = append(sets, "name=?"), append(args, n)
	}
	if icon != nil {
		ic, err := normalizeAppIcon(*icon)
		if err != nil {
			return App{}, err
		}
		sets, args = append(sets, "icon=?"), append(args, nullIcon(ic))
	}
	args = append(args, id, teamID)
	res, err := s.w.ExecContext(ctx, s.q(`UPDATE apps SET `+strings.Join(sets, ", ")+` WHERE id=? AND COALESCE(team_id, 0)=?`), args...)
	if isUnique(err) {
		return App{}, fmt.Errorf("an application named %q already exists: %w", newName, store.ErrAlreadyExists)
	}
	if err != nil {
		return App{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return App{}, fmt.Errorf("application: %w", store.ErrNotFound)
	}
	return scanApp(s.w.QueryRowContext(ctx, s.q(`SELECT `+appCols+` FROM apps a WHERE a.id=?`), id))
}

// DeleteApp removes an application and the API keys scoped to it. The
// caller must make sure it has no queues left; the Default application
// can't be deleted.
func (s *Store) DeleteApp(ctx context.Context, teamID, id int64) error {
	a, err := s.GetApp(ctx, teamID, id)
	if err != nil {
		return err
	}
	if a.Default {
		return ErrDefaultApp
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Explicit, so it doesn't depend on SQLite's foreign_keys pragma.
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM api_keys WHERE app_id=?`), id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM apps WHERE id=? AND is_default=0`), id); err != nil {
		return err
	}
	return tx.Commit()
}
