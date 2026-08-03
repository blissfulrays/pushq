package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RunMigrations applies numbered NNNN_*.sql files from dir in order, tracking
// applied versions in schema_migrations. Each migration runs in its own
// transaction.
func RunMigrations(ctx context.Context, db *sql.DB, dir fs.FS) error {
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at BIGINT NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	type mig struct {
		version int
		name    string
	}
	var migs []mig
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		numStr, _, ok := strings.Cut(name, "_")
		if !ok {
			return fmt.Errorf("migration %q: name must be NNNN_description.sql", name)
		}
		v, err := strconv.Atoi(numStr)
		if err != nil {
			return fmt.Errorf("migration %q: bad version prefix: %w", name, err)
		}
		migs = append(migs, mig{version: v, name: name})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })

	applied := map[int]bool{}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, m := range migs {
		if applied[m.version] {
			continue
		}
		sqlBytes, err := fs.ReadFile(dir, m.name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", m.name, err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
		// Literal values so the same statement works on both SQLite ("?")
		// and pgx ("$1") without a placeholder abstraction.
		record := fmt.Sprintf(`INSERT INTO schema_migrations (version, applied_at) VALUES (%d, %d)`,
			m.version, time.Now().UnixMilli())
		if _, err := tx.ExecContext(ctx, record); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", m.name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", m.name, err)
		}
	}
	return nil
}
