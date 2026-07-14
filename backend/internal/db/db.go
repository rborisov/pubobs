package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/001_init.sql
var schema string

func Open(dsn string) (*sql.DB, error) {
	// In-memory databases (used by tests) are per-connection — a second
	// pooled connection would see a fresh, empty database — so they must
	// stick to a single connection, same as before this function grew a
	// real connection pool for on-disk databases.
	isMemory := dsn == ":memory:" || strings.HasPrefix(dsn, "file::memory:")

	openDSN := dsn
	if !isMemory {
		// modernc.org/sqlite needs its own `_pragma=name(value)` DSN syntax
		// for settings that are per-connection (busy_timeout, foreign_keys);
		// a plain `db.Exec("PRAGMA ...")` only configures whichever single
		// connection happens to run it, which stopped being reliable once
		// the pool below can open more than one connection.
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		openDSN = dsn + sep + "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	}

	db, err := sql.Open("sqlite", openDSN)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if isMemory {
		db.SetMaxOpenConns(1)
		if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
			db.Close()
			return nil, fmt.Errorf("enable foreign keys: %w", err)
		}
	} else {
		// WAL is persisted in the database file itself (unlike the
		// per-connection pragmas above), so setting it once here is enough.
		// It lets readers proceed concurrently with the single writer,
		// instead of every request in the process serializing behind one
		// connection as MaxOpenConns(1) used to force — previously, one
		// slow request (e.g. a large sync) could make unrelated reads
		// (e.g. the repo list powering the dashboard) queue behind it for
		// a long time.
		if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
			db.Close()
			return nil, fmt.Errorf("enable WAL mode: %w", err)
		}
		db.SetMaxOpenConns(4)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	// Add is_banned to existing installs (new installs have it from schema).
	if _, err := db.Exec(`ALTER TABLE users ADD COLUMN is_banned INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			db.Close()
			return nil, fmt.Errorf("migrate users.is_banned: %w", err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE repos ADD COLUMN allow_guest INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			db.Close()
			return nil, fmt.Errorf("migrate repos.allow_guest: %w", err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			db.Close()
			return nil, fmt.Errorf("migrate users.is_admin: %w", err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE group_members ADD COLUMN role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('member', 'admin'))`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			db.Close()
			return nil, fmt.Errorf("migrate group_members.role: %w", err)
		}
	}
	for _, alter := range []string{
		`ALTER TABLE repos ADD COLUMN storage_destination_id TEXT REFERENCES storage_destinations(id)`,
		`ALTER TABLE repos ADD COLUMN migration_status TEXT NOT NULL DEFAULT 'idle'`,
		`ALTER TABLE repos ADD COLUMN migration_total INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE repos ADD COLUMN migration_done INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(alter); err != nil {
			if !strings.Contains(err.Error(), "duplicate column name") {
				db.Close()
				return nil, fmt.Errorf("migrate repos storage columns: %w", err)
			}
		}
	}
	// encryption_key/shared_publicly are lazily populated per note (see
	// store.GetOrCreateNoteKey / SetNoteShared) rather than backfilled here —
	// existing rows start with an empty key and shared_publicly=false, so
	// nothing becomes publicly link-accessible just from running this
	// migration.
	for _, alter := range []string{
		`ALTER TABLE notes ADD COLUMN encryption_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE notes ADD COLUMN shared_publicly INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(alter); err != nil {
			if !strings.Contains(err.Error(), "duplicate column name") {
				db.Close()
				return nil, fmt.Errorf("migrate notes sharing columns: %w", err)
			}
		}
	}
	for _, alter := range []string{
		`ALTER TABLE repos ADD COLUMN owner_user_id TEXT`,
		`ALTER TABLE repos ADD COLUMN strict_credentials INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(alter); err != nil {
			if !strings.Contains(err.Error(), "duplicate column name") {
				db.Close()
				return nil, fmt.Errorf("migrate repos ownership columns: %w", err)
			}
		}
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS repo_user_credentials (
			repo_id         TEXT NOT NULL,
			user_id         TEXT NOT NULL,
			encrypted_creds TEXT NOT NULL,
			git_name        TEXT NOT NULL DEFAULT '',
			git_email       TEXT NOT NULL DEFAULT '',
			verify_status   TEXT NOT NULL DEFAULT 'unverified',
			verify_error    TEXT NOT NULL DEFAULT '',
			verified_at     TIMESTAMP,
			updated_at      TIMESTAMP NOT NULL,
			PRIMARY KEY (repo_id, user_id)
		)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate repo_user_credentials: %w", err)
	}
	return db, nil
}
