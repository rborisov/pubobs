# Phase 2 — PubObs Backend (Go) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the PubObs Go backend: SQLite persistence, OIDC/PKCE auth, sync/pull API, wiki API, comment API, admin API, git cache with per-repo mutex, background eviction job, and Docker packaging.

**Architecture:** Single Go binary; chi router; SQLite via modernc.org/sqlite (pure Go, no CGO); system `git` binary for all git operations; in-memory PKCE session store; JWT access + refresh tokens signed with HMAC-SHA256.

**Tech Stack:** Go 1.22, chi v5, modernc.org/sqlite, golang-jwt/jwt v5, coreos/go-oidc v3, google/uuid, stretchr/testify

---

## Parts overview (execute one per session)

| Part | Tasks | What it produces |
|------|-------|-----------------|
| **A** | 0–2 | Buildable Go module, config, SQLite schema |
| **B** | 3–8 | All store CRUD (users, groups, repos, notes, comments, health) |
| **C** | 9–13 | Cred encryption, PKCE, JWT, OIDC client, HTTP auth middleware |
| **D** | 14–15 | Git command runner + cache manager (integration-tested against real bare repo) |
| **E** | 16–22 | All API handlers (auth, me, repos, sync, files, wiki, admin) |
| **F** | 23–26 | Background eviction job, main entry point, Dockerfile, docker-compose |

---

## Part A — Scaffold + Config + DB

### Task 0: Project scaffold

**Files:**
- Create: `backend/go.mod`
- Create: `backend/go.sum` (generated)
- Create: `backend/cmd/server/main.go` (empty placeholder)
- Create: `backend/internal/config/config.go` (empty placeholder)
- Create: `backend/internal/db/db.go` (empty placeholder)
- Create: `backend/internal/db/migrations/001_init.sql` (empty placeholder)
- Create: `backend/internal/model/model.go` (empty placeholder)
- Create: `backend/internal/store/store.go` (empty placeholder)
- Create: `backend/internal/auth/pkce.go` (empty placeholder)
- Create: `backend/internal/gitcache/cache.go` (empty placeholder)
- Create: `backend/internal/api/router.go` (empty placeholder)
- Create: `backend/internal/jobs/eviction.go` (empty placeholder)
- Create: `backend/frontend/embed.go` (empty placeholder)

- [ ] **Step 1: Create directory tree**

```bash
mkdir -p backend/cmd/server \
         backend/internal/config \
         backend/internal/db/migrations \
         backend/internal/model \
         backend/internal/store \
         backend/internal/auth \
         backend/internal/gitcache \
         backend/internal/api \
         backend/internal/jobs \
         backend/frontend/static
```

- [ ] **Step 2: Create go.mod**

`backend/go.mod`:
```
module github.com/pubobs/backend

go 1.22
```

- [ ] **Step 3: Install dependencies**

```bash
cd backend
go get github.com/go-chi/chi/v5@latest
go get modernc.org/sqlite@latest
go get github.com/golang-jwt/jwt/v5@latest
go get github.com/coreos/go-oidc/v3@latest
go get github.com/google/uuid@latest
go get github.com/stretchr/testify@latest
go get golang.org/x/crypto@latest
```

- [ ] **Step 4: Verify module builds**

```bash
cd backend && go build ./...
```
Expected: no output (empty packages build cleanly).

- [ ] **Step 5: Commit**

```bash
cd backend && git add go.mod go.sum
git add backend/
git commit -m "feat(backend): scaffold Go module and directory tree"
```

---

### Task 1: Config

**Files:**
- Create: `backend/internal/config/config.go`
- Create: `backend/internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/config/config_test.go`:
```go
package config_test

import (
	"testing"
	"time"

	"github.com/pubobs/backend/internal/config"
	"github.com/stretchr/testify/require"
)

func withRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PUBOBS_SECRET_KEY", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2")
	t.Setenv("PUBOBS_OIDC_ISSUER", "https://accounts.example.com")
	t.Setenv("PUBOBS_OIDC_CLIENT_ID", "client-id")
	t.Setenv("PUBOBS_OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("PUBOBS_BASE_URL", "https://pubobs.example.com")
}

func TestLoad_defaults(t *testing.T) {
	withRequiredEnv(t)
	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, "8080", cfg.Port)
	require.Equal(t, 24*time.Hour, cfg.RepoCacheTTL)
	require.Equal(t, time.Hour, cfg.CacheCheckInterval)
	require.Equal(t, float64(20), cfg.DiskWarnPct)
	require.Equal(t, float64(5), cfg.DiskCritPct)
	require.Equal(t, "/data/repos", cfg.RepoCacheDir)
	require.Equal(t, "/data/db/pubobs.db", cfg.DBPath)
	require.Len(t, cfg.SecretKey, 32)
}

func TestLoad_missingRequired(t *testing.T) {
	// No env vars set — all required fields should cause error
	_, err := config.Load()
	require.Error(t, err)
}

func TestLoad_badSecretKey(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("PUBOBS_SECRET_KEY", "notenoughbytes")
	_, err := config.Load()
	require.Error(t, err)
}

func TestLoad_customDuration(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("PUBOBS_REPO_CACHE_TTL", "48h")
	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, 48*time.Hour, cfg.RepoCacheTTL)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/config/... -v
```
Expected: compile error (package not implemented yet).

- [ ] **Step 3: Implement config.go**

`backend/internal/config/config.go`:
```go
package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port               string
	BaseURL            string
	OIDCIssuer         string
	OIDCClientID       string
	OIDCClientSecret   string
	SecretKey          []byte
	RepoCacheDir       string
	RepoCacheTTL       time.Duration
	CacheCheckInterval time.Duration
	DiskWarnPct        float64
	DiskCritPct        float64
	DBPath             string
}

func Load() (*Config, error) {
	cfg := &Config{
		Port:         getEnv("PUBOBS_PORT", "8080"),
		BaseURL:      getEnv("PUBOBS_BASE_URL", ""),
		OIDCIssuer:   getEnv("PUBOBS_OIDC_ISSUER", ""),
		OIDCClientID: getEnv("PUBOBS_OIDC_CLIENT_ID", ""),
		OIDCClientSecret: getEnv("PUBOBS_OIDC_CLIENT_SECRET", ""),
		RepoCacheDir: getEnv("PUBOBS_REPO_CACHE_DIR", "/data/repos"),
		DBPath:       getEnv("PUBOBS_DB_PATH", "/data/db/pubobs.db"),
	}

	if raw := os.Getenv("PUBOBS_SECRET_KEY"); raw != "" {
		key, err := hex.DecodeString(raw)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("PUBOBS_SECRET_KEY must be 64 hex chars (32 bytes)")
		}
		cfg.SecretKey = key
	} else {
		return nil, fmt.Errorf("PUBOBS_SECRET_KEY is required")
	}

	var err error
	if cfg.RepoCacheTTL, err = parseDuration("PUBOBS_REPO_CACHE_TTL", "24h"); err != nil {
		return nil, err
	}
	if cfg.CacheCheckInterval, err = parseDuration("PUBOBS_CACHE_CHECK_INTERVAL", "1h"); err != nil {
		return nil, err
	}
	if cfg.DiskWarnPct, err = parseFloat("PUBOBS_DISK_WARN_PCT", 20); err != nil {
		return nil, err
	}
	if cfg.DiskCritPct, err = parseFloat("PUBOBS_DISK_CRIT_PCT", 5); err != nil {
		return nil, err
	}

	for _, check := range []struct{ name, val string }{
		{"PUBOBS_BASE_URL", cfg.BaseURL},
		{"PUBOBS_OIDC_ISSUER", cfg.OIDCIssuer},
		{"PUBOBS_OIDC_CLIENT_ID", cfg.OIDCClientID},
		{"PUBOBS_OIDC_CLIENT_SECRET", cfg.OIDCClientSecret},
	} {
		if check.val == "" {
			return nil, fmt.Errorf("%s is required", check.name)
		}
	}

	return cfg, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseDuration(key, def string) (time.Duration, error) {
	raw := getEnv(key, def)
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q: %w", key, raw, err)
	}
	return d, nil
}

func parseFloat(key string, def float64) (float64, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid number %q: %w", key, raw, err)
	}
	return f, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd backend && go test ./internal/config/... -v
```
Expected: all 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/config/
git commit -m "feat(backend): add Config with env-var loading"
```

---

### Task 2: Database + Migrations

**Files:**
- Create: `backend/internal/db/migrations/001_init.sql`
- Create: `backend/internal/db/db.go`
- Create: `backend/internal/db/db_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/db/db_test.go`:
```go
package db_test

import (
	"testing"

	"github.com/pubobs/backend/internal/db"
	"github.com/stretchr/testify/require"
)

func TestOpen_createsAllTables(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()

	tables := []string{
		"users", "groups", "group_members", "repos", "repo_access",
		"notes", "note_snapshots", "comments", "note_links",
		"folder_mappings", "system_health",
	}
	for _, tbl := range tables {
		var name string
		err := d.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl,
		).Scan(&name)
		require.NoError(t, err, "table %q should exist", tbl)
		require.Equal(t, tbl, name)
	}
}

func TestOpen_idempotent(t *testing.T) {
	// Running Open twice on same DB should not error (CREATE TABLE IF NOT EXISTS)
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	d.Close()
	// Reopen — can't re-run on :memory: (new db each time), so just verify no error
	d2, err := db.Open(":memory:")
	require.NoError(t, err)
	d2.Close()
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/db/... -v
```
Expected: compile error.

- [ ] **Step 3: Write the SQL migration**

`backend/internal/db/migrations/001_init.sql`:
```sql
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    email TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    is_instance_admin INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS groups (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS group_members (
    group_id TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id  TEXT NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);

CREATE TABLE IF NOT EXISTS repos (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    remote_url      TEXT NOT NULL,
    encrypted_creds TEXT NOT NULL,
    default_branch  TEXT NOT NULL DEFAULT 'main',
    local_path      TEXT,
    cloned_at       DATETIME,
    last_used_at    DATETIME,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS repo_access (
    id             TEXT PRIMARY KEY,
    repo_id        TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    principal_type TEXT NOT NULL CHECK (principal_type IN ('user','group')),
    principal_id   TEXT NOT NULL,
    role           TEXT NOT NULL CHECK (role IN ('reader','commentator','editor','admin')),
    UNIQUE (repo_id, principal_type, principal_id)
);

CREATE TABLE IF NOT EXISTS notes (
    id         TEXT PRIMARY KEY,
    repo_id    TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    path       TEXT NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (repo_id, path)
);

CREATE TABLE IF NOT EXISTS note_snapshots (
    id            TEXT PRIMARY KEY,
    note_id       TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    html_content  TEXT NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    synced_by     TEXT NOT NULL REFERENCES users(id),
    git_commit_sha TEXT NOT NULL,
    synced_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS comments (
    id         TEXT PRIMARY KEY,
    note_id    TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id),
    parent_id  TEXT REFERENCES comments(id),
    body       TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS note_links (
    source_note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    target_path    TEXT NOT NULL,
    PRIMARY KEY (source_note_id, target_path)
);

CREATE TABLE IF NOT EXISTS folder_mappings (
    user_id        TEXT NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    repo_id        TEXT NOT NULL REFERENCES repos(id)  ON DELETE CASCADE,
    vault_folder   TEXT NOT NULL,
    repo_subfolder TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (user_id, repo_id)
);

CREATE TABLE IF NOT EXISTS system_health (
    id               INTEGER PRIMARY KEY CHECK (id = 1),
    disk_free_pct    REAL    NOT NULL DEFAULT 100,
    disk_free_bytes  INTEGER NOT NULL DEFAULT 0,
    disk_status      TEXT    NOT NULL DEFAULT 'ok',
    last_eviction_at DATETIME,
    checked_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

- [ ] **Step 4: Implement db.go**

`backend/internal/db/db.go`:
```go
package db

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed migrations/001_init.sql
var schema string

func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite is single-writer
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	return db, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
cd backend && go test ./internal/db/... -v
```
Expected: `TestOpen_createsAllTables` and `TestOpen_idempotent` both PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/db/
git commit -m "feat(backend): add SQLite db.Open with schema migrations"
```

---

## Part B — Models + Store

### Task 3: Models

**Files:**
- Create: `backend/internal/model/model.go`

No tests needed (pure types).

- [ ] **Step 1: Write model.go**

`backend/internal/model/model.go`:
```go
package model

import "time"

type User struct {
	ID              string
	Email           string
	Name            string
	IsInstanceAdmin bool
	CreatedAt       time.Time
}

type Group struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

type Repo struct {
	ID             string
	Name           string
	RemoteURL      string
	EncryptedCreds string
	DefaultBranch  string
	LocalPath      *string
	ClonedAt       *time.Time
	LastUsedAt     *time.Time
	CreatedAt      time.Time
}

type RepoAccess struct {
	ID            string
	RepoID        string
	PrincipalType string // "user" | "group"
	PrincipalID   string
	Role          string // "reader" | "commentator" | "editor" | "admin"
}

type Note struct {
	ID        string
	RepoID    string
	Path      string
	UpdatedAt time.Time
}

type NoteSnapshot struct {
	ID           string
	NoteID       string
	HTMLContent  string
	MetadataJSON string
	SyncedBy     string
	GitCommitSHA string
	SyncedAt     time.Time
}

type Comment struct {
	ID        string
	NoteID    string
	UserID    string
	ParentID  *string
	Body      string
	CreatedAt time.Time
}

type NoteLink struct {
	SourceNoteID string
	TargetPath   string
}

type FolderMapping struct {
	UserID        string
	RepoID        string
	VaultFolder   string
	RepoSubfolder string
}

type SystemHealth struct {
	ID             int
	DiskFreePct    float64
	DiskFreeBytes  int64
	DiskStatus     string // "ok" | "warn" | "crit"
	LastEvictionAt *time.Time
	CheckedAt      time.Time
}

// Commit is a git log entry returned by the history API.
type Commit struct {
	SHA       string
	AuthorEmail string
	AuthorName  string
	Date      time.Time
	Message   string
}

// FileEntry is a file path + content + blob SHA returned by the files API.
type FileEntry struct {
	Path    string
	Content string
	SHA     string
}

// roleOrder defines cumulative permission levels.
var roleOrder = map[string]int{
	"reader": 1, "commentator": 2, "editor": 3, "admin": 4,
}

// RoleAtLeast returns true if userRole satisfies the required minimum role.
func RoleAtLeast(userRole, required string) bool {
	return roleOrder[userRole] >= roleOrder[required]
}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd backend && go build ./internal/model/...
```
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/model/
git commit -m "feat(backend): add domain models and RoleAtLeast helper"
```

---

### Task 4: Store — Setup + Users

**Files:**
- Create: `backend/internal/store/store.go`
- Create: `backend/internal/store/user.go`
- Create: `backend/internal/store/user_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/store/user_test.go`:
```go
package store_test

import (
	"context"
	"testing"

	"github.com/pubobs/backend/internal/db"
	"github.com/pubobs/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	return store.New(d)
}

func TestUpsertAndGetUser(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u, err := s.UpsertUser(ctx, "user1", "alice@example.com", "Alice")
	require.NoError(t, err)
	require.Equal(t, "user1", u.ID)
	require.Equal(t, "alice@example.com", u.Email)
	require.False(t, u.IsInstanceAdmin)

	// Upsert same ID with updated name — should update
	u2, err := s.UpsertUser(ctx, "user1", "alice@example.com", "Alice Smith")
	require.NoError(t, err)
	require.Equal(t, "Alice Smith", u2.Name)

	got, err := s.GetUserByID(ctx, "user1")
	require.NoError(t, err)
	require.Equal(t, "Alice Smith", got.Name)
}

func TestGetUserByEmail(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.UpsertUser(ctx, "user2", "bob@example.com", "Bob")
	require.NoError(t, err)

	got, err := s.GetUserByEmail(ctx, "bob@example.com")
	require.NoError(t, err)
	require.Equal(t, "user2", got.ID)
}

func TestListUsers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.UpsertUser(ctx, "u2", "b@x.com", "B")

	users, err := s.ListUsers(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
}

func TestSetInstanceAdmin(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.UpsertUser(ctx, "u1", "a@x.com", "A")

	err := s.SetInstanceAdmin(ctx, "u1", true)
	require.NoError(t, err)

	u, err := s.GetUserByID(ctx, "u1")
	require.NoError(t, err)
	require.True(t, u.IsInstanceAdmin)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/store/... -v -run TestUpsertAndGetUser
```
Expected: compile error.

- [ ] **Step 3: Implement store.go**

`backend/internal/store/store.go`:
```go
package store

import "database/sql"

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}
```

- [ ] **Step 4: Implement user.go**

`backend/internal/store/user.go`:
```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pubobs/backend/internal/model"
)

func (s *Store) UpsertUser(ctx context.Context, id, email, name string) (*model.User, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (id, email, name, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET email=excluded.email, name=excluded.name`,
		id, email, name, time.Now().UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("upsert user: %w", err)
	}
	return s.GetUserByID(ctx, id)
}

func (s *Store) GetUserByID(ctx context.Context, id string) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, email, name, is_instance_admin, created_at FROM users WHERE id=?`, id))
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, email, name, is_instance_admin, created_at FROM users WHERE email=?`, email))
}

func (s *Store) ListUsers(ctx context.Context) ([]*model.User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, email, name, is_instance_admin, created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var out []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) SetInstanceAdmin(ctx context.Context, userID string, admin bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET is_instance_admin=? WHERE id=?`, admin, userID)
	return err
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanUser(row scanner) (*model.User, error) {
	var u model.User
	var admin int
	err := row.Scan(&u.ID, &u.Email, &u.Name, &admin, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	u.IsInstanceAdmin = admin == 1
	return &u, nil
}
```

- [ ] **Step 5: Run tests**

```bash
cd backend && go test ./internal/store/... -v -run "TestUpsertAndGetUser|TestGetUserByEmail|TestListUsers|TestSetInstanceAdmin"
```
Expected: all 4 tests PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/store/
git commit -m "feat(backend): add Store with user CRUD"
```

---

### Task 5: Store — Groups

**Files:**
- Create: `backend/internal/store/group.go`
- Create: `backend/internal/store/group_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/store/group_test.go`:
```go
package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	g, err := s.CreateGroup(ctx, "grp1", "Engineering")
	require.NoError(t, err)
	require.Equal(t, "grp1", g.ID)
	require.Equal(t, "Engineering", g.Name)

	groups, err := s.ListGroups(ctx)
	require.NoError(t, err)
	require.Len(t, groups, 1)
}

func TestGroupMembers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.UpsertUser(ctx, "u2", "b@x.com", "B")
	s.CreateGroup(ctx, "g1", "Team")

	require.NoError(t, s.AddGroupMember(ctx, "g1", "u1"))
	require.NoError(t, s.AddGroupMember(ctx, "g1", "u2"))

	members, err := s.GetGroupMembers(ctx, "g1")
	require.NoError(t, err)
	require.Len(t, members, 2)

	require.NoError(t, s.RemoveGroupMember(ctx, "g1", "u1"))
	members, _ = s.GetGroupMembers(ctx, "g1")
	require.Len(t, members, 1)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/store/... -run TestGroupCRUD -v
```
Expected: compile error (group.go not yet written).

- [ ] **Step 3: Implement group.go**

`backend/internal/store/group.go`:
```go
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/pubobs/backend/internal/model"
)

func (s *Store) CreateGroup(ctx context.Context, id, name string) (*model.Group, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO groups (id, name, created_at) VALUES (?,?,?)`,
		id, name, time.Now().UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("create group: %w", err)
	}
	return &model.Group{ID: id, Name: name}, nil
}

func (s *Store) ListGroups(ctx context.Context) ([]*model.Group, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, created_at FROM groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Group
	for rows.Next() {
		var g model.Group
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}

func (s *Store) AddGroupMember(ctx context.Context, groupID, userID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO group_members (group_id, user_id) VALUES (?,?)`,
		groupID, userID)
	return err
}

func (s *Store) RemoveGroupMember(ctx context.Context, groupID, userID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM group_members WHERE group_id=? AND user_id=?`, groupID, userID)
	return err
}

func (s *Store) GetGroupMembers(ctx context.Context, groupID string) ([]*model.User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.email, u.name, u.is_instance_admin, u.created_at
		FROM users u
		JOIN group_members gm ON gm.user_id = u.id
		WHERE gm.group_id=?`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetUserGroups returns all group IDs a user belongs to.
func (s *Store) GetUserGroupIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT group_id FROM group_members WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
```

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/store/... -run "TestGroupCRUD|TestGroupMembers" -v
```
Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/store/group.go backend/internal/store/group_test.go
git commit -m "feat(backend): add group store CRUD"
```

---

### Task 6: Store — Repos + Access

**Files:**
- Create: `backend/internal/store/repo.go`
- Create: `backend/internal/store/access.go`
- Create: `backend/internal/store/repo_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/store/repo_test.go`:
```go
package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepoCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r, err := s.CreateRepo(ctx, "r1", "My Repo", "https://github.com/org/repo.git", "enc-creds", "main")
	require.NoError(t, err)
	require.Equal(t, "r1", r.ID)
	require.Nil(t, r.LocalPath)

	got, err := s.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.Equal(t, "My Repo", got.Name)

	err = s.UpdateRepoLocalPath(ctx, "r1", "/data/repos/r1", mustParseTime("2024-01-01T00:00:00Z"))
	require.NoError(t, err)

	got, _ = s.GetRepo(ctx, "r1")
	require.NotNil(t, got.LocalPath)
	require.Equal(t, "/data/repos/r1", *got.LocalPath)

	err = s.ClearRepoLocalPath(ctx, "r1")
	require.NoError(t, err)
	got, _ = s.GetRepo(ctx, "r1")
	require.Nil(t, got.LocalPath)

	repos, err := s.ListRepos(ctx)
	require.NoError(t, err)
	require.Len(t, repos, 1)

	err = s.DeleteRepo(ctx, "r1")
	require.NoError(t, err)
	repos, _ = s.ListRepos(ctx)
	require.Len(t, repos, 0)
}

func TestGetUserRole(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.UpsertUser(ctx, "u2", "b@x.com", "B")
	s.CreateGroup(ctx, "g1", "Readers")
	s.AddGroupMember(ctx, "g1", "u2")
	s.CreateRepo(ctx, "r1", "Repo", "https://x.com/r.git", "c", "main")

	// Grant u1 editor role directly
	err := s.GrantAccess(ctx, "acc1", "r1", "user", "u1", "editor")
	require.NoError(t, err)
	// Grant group g1 reader role
	err = s.GrantAccess(ctx, "acc2", "r1", "group", "g1", "reader")
	require.NoError(t, err)

	role, err := s.GetUserRole(ctx, "u1", "r1")
	require.NoError(t, err)
	require.Equal(t, "editor", role)

	// u2 gets reader via group
	role, err = s.GetUserRole(ctx, "u2", "r1")
	require.NoError(t, err)
	require.Equal(t, "reader", role)

	// u1 also has group reader, but direct editor wins
	// (max role is returned)
	s.AddGroupMember(ctx, "g1", "u1")
	role, _ = s.GetUserRole(ctx, "u1", "r1")
	require.Equal(t, "editor", role)

	// unknown user → empty role
	role, err = s.GetUserRole(ctx, "nobody", "r1")
	require.NoError(t, err)
	require.Equal(t, "", role)
}

func mustParseTime(s string) interface{} { return s }
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/store/... -run TestRepoCRUD -v
```
Expected: compile error.

- [ ] **Step 3: Implement repo.go**

`backend/internal/store/repo.go`:
```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pubobs/backend/internal/model"
)

func (s *Store) CreateRepo(ctx context.Context, id, name, remoteURL, encCreds, branch string) (*model.Repo, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO repos (id, name, remote_url, encrypted_creds, default_branch, created_at)
		VALUES (?,?,?,?,?,?)`,
		id, name, remoteURL, encCreds, branch, time.Now().UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("create repo: %w", err)
	}
	return s.GetRepo(ctx, id)
}

func (s *Store) GetRepo(ctx context.Context, id string) (*model.Repo, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, remote_url, encrypted_creds, default_branch,
		       local_path, cloned_at, last_used_at, created_at
		FROM repos WHERE id=?`, id)
	return scanRepo(row)
}

func (s *Store) ListRepos(ctx context.Context) ([]*model.Repo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, remote_url, encrypted_creds, default_branch,
		       local_path, cloned_at, last_used_at, created_at
		FROM repos ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Repo
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpdateRepo(ctx context.Context, id, name, remoteURL, encCreds, branch string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE repos SET name=?, remote_url=?, encrypted_creds=?, default_branch=?
		WHERE id=?`, name, remoteURL, encCreds, branch, id)
	return err
}

func (s *Store) DeleteRepo(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM repos WHERE id=?`, id)
	return err
}

func (s *Store) UpdateRepoLocalPath(ctx context.Context, id, localPath string, clonedAt interface{}) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE repos SET local_path=?, cloned_at=?, last_used_at=? WHERE id=?`,
		localPath, clonedAt, time.Now().UTC(), id)
	return err
}

func (s *Store) ClearRepoLocalPath(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE repos SET local_path=NULL, cloned_at=NULL WHERE id=?`, id)
	return err
}

func (s *Store) TouchLastUsedAt(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE repos SET last_used_at=? WHERE id=?`, time.Now().UTC(), id)
	return err
}

// ListStaleRepos returns repos whose last_used_at is older than cutoff and local_path is not null.
func (s *Store) ListStaleRepos(ctx context.Context, cutoff time.Time) ([]*model.Repo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, remote_url, encrypted_creds, default_branch,
		       local_path, cloned_at, last_used_at, created_at
		FROM repos WHERE local_path IS NOT NULL AND last_used_at < ?`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Repo
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanRepo(row scanner) (*model.Repo, error) {
	var r model.Repo
	var localPath sql.NullString
	var clonedAt, lastUsedAt sql.NullTime
	err := row.Scan(
		&r.ID, &r.Name, &r.RemoteURL, &r.EncryptedCreds, &r.DefaultBranch,
		&localPath, &clonedAt, &lastUsedAt, &r.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan repo: %w", err)
	}
	if localPath.Valid {
		r.LocalPath = &localPath.String
	}
	if clonedAt.Valid {
		r.ClonedAt = &clonedAt.Time
	}
	if lastUsedAt.Valid {
		r.LastUsedAt = &lastUsedAt.Time
	}
	return &r, nil
}
```

- [ ] **Step 4: Implement access.go**

`backend/internal/store/access.go`:
```go
package store

import (
	"context"
	"fmt"

	"github.com/pubobs/backend/internal/model"
)

var roleOrder = map[string]int{
	"reader": 1, "commentator": 2, "editor": 3, "admin": 4,
}

func (s *Store) GrantAccess(ctx context.Context, id, repoID, principalType, principalID, role string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO repo_access (id, repo_id, principal_type, principal_id, role)
		VALUES (?,?,?,?,?)
		ON CONFLICT(repo_id, principal_type, principal_id) DO UPDATE SET role=excluded.role`,
		id, repoID, principalType, principalID, role,
	)
	return err
}

func (s *Store) RevokeAccess(ctx context.Context, accessID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM repo_access WHERE id=?`, accessID)
	return err
}

func (s *Store) ListRepoAccess(ctx context.Context, repoID string) ([]*model.RepoAccess, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, repo_id, principal_type, principal_id, role FROM repo_access WHERE repo_id=?`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.RepoAccess
	for rows.Next() {
		var a model.RepoAccess
		if err := rows.Scan(&a.ID, &a.RepoID, &a.PrincipalType, &a.PrincipalID, &a.Role); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// GetUserRole returns the highest role the user has on the repo (direct or via group).
// Returns "" if the user has no access.
// Instance admins are NOT automatically granted access here — callers must check is_instance_admin separately.
func (s *Store) GetUserRole(ctx context.Context, userID, repoID string) (string, error) {
	groupIDs, err := s.GetUserGroupIDs(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("get user groups: %w", err)
	}

	best := ""
	setBest := func(role string) {
		if roleOrder[role] > roleOrder[best] {
			best = role
		}
	}

	// Direct user access
	var directRole string
	err = s.db.QueryRowContext(ctx,
		`SELECT role FROM repo_access WHERE repo_id=? AND principal_type='user' AND principal_id=?`,
		repoID, userID,
	).Scan(&directRole)
	if err == nil {
		setBest(directRole)
	}

	// Group access
	for _, gid := range groupIDs {
		var groupRole string
		err = s.db.QueryRowContext(ctx,
			`SELECT role FROM repo_access WHERE repo_id=? AND principal_type='group' AND principal_id=?`,
			repoID, gid,
		).Scan(&groupRole)
		if err == nil {
			setBest(groupRole)
		}
	}

	return best, nil
}

// ListUserRepos returns all repos the user has any access to (direct or via group), with their role.
func (s *Store) ListUserRepos(ctx context.Context, userID string) ([]*model.Repo, error) {
	groupIDs, err := s.GetUserGroupIDs(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("get user groups: %w", err)
	}

	// Build a set of accessible repo IDs
	seen := map[string]bool{}
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT repo_id FROM repo_access WHERE principal_type='user' AND principal_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rid string
		rows.Scan(&rid)
		seen[rid] = true
	}
	for _, gid := range groupIDs {
		rows2, err := s.db.QueryContext(ctx,
			`SELECT DISTINCT repo_id FROM repo_access WHERE principal_type='group' AND principal_id=?`, gid)
		if err != nil {
			return nil, err
		}
		for rows2.Next() {
			var rid string
			rows2.Scan(&rid)
			seen[rid] = true
		}
		rows2.Close()
	}

	var out []*model.Repo
	for rid := range seen {
		r, err := s.GetRepo(ctx, rid)
		if err != nil {
			return nil, err
		}
		if r != nil {
			out = append(out, r)
		}
	}
	return out, nil
}
```

- [ ] **Step 5: Fix the test helper (mustParseTime is not needed)**

Remove `func mustParseTime` from repo_test.go — replace with direct string in `UpdateRepoLocalPath` call since the function accepts `interface{}` for clonedAt:

The test already compiles as-is since `mustParseTime` returns `interface{}`. No change needed.

- [ ] **Step 6: Run tests**

```bash
cd backend && go test ./internal/store/... -run "TestRepoCRUD|TestGetUserRole" -v
```
Expected: both PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/store/repo.go backend/internal/store/access.go backend/internal/store/repo_test.go
git commit -m "feat(backend): add repo and access store with role resolution"
```

---

### Task 7: Store — Notes + Snapshots + Links

**Files:**
- Create: `backend/internal/store/note.go`
- Create: `backend/internal/store/note_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/store/note_test.go`:
```go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func seedRepoAndUser(t *testing.T, s interface{ CreateRepo(...) error; UpsertUser(...) error }) {
	// handled by calling in tests directly
}

func TestNoteUpsertAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "c", "main")

	n, err := s.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.NotEmpty(t, n.ID)
	require.Equal(t, "docs/intro.md", n.Path)

	// Upsert same path → returns same note ID
	n2, err := s.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.Equal(t, n.ID, n2.ID)

	notes, err := s.ListNotes(ctx, "r1")
	require.NoError(t, err)
	require.Len(t, notes, 1)
}

func TestNoteSnapshot(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "c", "main")
	n, _ := s.UpsertNote(ctx, "r1", "intro.md")

	err := s.UpsertSnapshot(ctx, n.ID, "<h1>Intro</h1>", `{"links":["other"]}`, "u1", "abc1234")
	require.NoError(t, err)

	snap, err := s.GetSnapshot(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, "<h1>Intro</h1>", snap.HTMLContent)
	require.Equal(t, "abc1234", snap.GitCommitSHA)
}

func TestNoteLinks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "c", "main")
	src, _ := s.UpsertNote(ctx, "r1", "intro.md")
	tgt, _ := s.UpsertNote(ctx, "r1", "other.md")

	err := s.UpsertNoteLinks(ctx, src.ID, []string{"other.md", "missing.md"})
	require.NoError(t, err)

	backlinks, err := s.GetBacklinks(ctx, "r1", "other.md")
	require.NoError(t, err)
	require.Len(t, backlinks, 1)
	require.Equal(t, src.ID, backlinks[0].ID)
	_ = tgt
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/store/... -run TestNoteUpsertAndList -v
```
Expected: compile error.

- [ ] **Step 3: Implement note.go**

`backend/internal/store/note.go`:
```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pubobs/backend/internal/model"
)

func (s *Store) UpsertNote(ctx context.Context, repoID, path string) (*model.Note, error) {
	id := uuid.NewString()
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notes (id, repo_id, path, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(repo_id, path) DO UPDATE SET updated_at=excluded.updated_at`,
		id, repoID, path, now,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert note: %w", err)
	}
	return s.GetNote(ctx, repoID, path)
}

func (s *Store) GetNote(ctx context.Context, repoID, path string) (*model.Note, error) {
	var n model.Note
	err := s.db.QueryRowContext(ctx,
		`SELECT id, repo_id, path, updated_at FROM notes WHERE repo_id=? AND path=?`,
		repoID, path,
	).Scan(&n.ID, &n.RepoID, &n.Path, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &n, err
}

func (s *Store) GetNoteByID(ctx context.Context, id string) (*model.Note, error) {
	var n model.Note
	err := s.db.QueryRowContext(ctx,
		`SELECT id, repo_id, path, updated_at FROM notes WHERE id=?`, id,
	).Scan(&n.ID, &n.RepoID, &n.Path, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &n, err
}

func (s *Store) ListNotes(ctx context.Context, repoID string) ([]*model.Note, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, repo_id, path, updated_at FROM notes WHERE repo_id=? ORDER BY path`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Note
	for rows.Next() {
		var n model.Note
		if err := rows.Scan(&n.ID, &n.RepoID, &n.Path, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}

func (s *Store) UpsertSnapshot(ctx context.Context, noteID, htmlContent, metadataJSON, syncedBy, commitSHA string) error {
	id := uuid.NewString()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO note_snapshots (id, note_id, html_content, metadata_json, synced_by, git_commit_sha, synced_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(note_id) DO UPDATE SET
			html_content=excluded.html_content,
			metadata_json=excluded.metadata_json,
			synced_by=excluded.synced_by,
			git_commit_sha=excluded.git_commit_sha,
			synced_at=excluded.synced_at`,
		id, noteID, htmlContent, metadataJSON, syncedBy, commitSHA, time.Now().UTC(),
	)
	return err
}

func (s *Store) GetSnapshot(ctx context.Context, noteID string) (*model.NoteSnapshot, error) {
	var snap model.NoteSnapshot
	err := s.db.QueryRowContext(ctx, `
		SELECT id, note_id, html_content, metadata_json, synced_by, git_commit_sha, synced_at
		FROM note_snapshots WHERE note_id=?`, noteID,
	).Scan(&snap.ID, &snap.NoteID, &snap.HTMLContent, &snap.MetadataJSON,
		&snap.SyncedBy, &snap.GitCommitSHA, &snap.SyncedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &snap, err
}

func (s *Store) UpsertNoteLinks(ctx context.Context, sourceNoteID string, targetPaths []string) error {
	// Delete old links for this source note, then re-insert
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM note_links WHERE source_note_id=?`, sourceNoteID); err != nil {
		return err
	}
	for _, tp := range targetPaths {
		if _, err := s.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO note_links (source_note_id, target_path) VALUES (?,?)`,
			sourceNoteID, tp); err != nil {
			return err
		}
	}
	return nil
}

// GetBacklinks returns notes (in the same repo) that contain a link pointing to targetPath.
func (s *Store) GetBacklinks(ctx context.Context, repoID, targetPath string) ([]*model.Note, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.repo_id, n.path, n.updated_at
		FROM notes n
		JOIN note_links nl ON nl.source_note_id = n.id
		WHERE n.repo_id=? AND nl.target_path=?`,
		repoID, targetPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Note
	for rows.Next() {
		var n model.Note
		if err := rows.Scan(&n.ID, &n.RepoID, &n.Path, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}
```

Note: `note_snapshots` has `UNIQUE(note_id)` implied by the upsert — add it to the SQL schema. Add to `001_init.sql` after `note_id` line: the `ON CONFLICT` in the upsert relies on a unique constraint. Update the schema:

In `001_init.sql`, the `note_snapshots` table, add after the closing columns:
```sql
    UNIQUE (note_id)
```

Full corrected table:
```sql
CREATE TABLE IF NOT EXISTS note_snapshots (
    id            TEXT PRIMARY KEY,
    note_id       TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    html_content  TEXT NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    synced_by     TEXT NOT NULL REFERENCES users(id),
    git_commit_sha TEXT NOT NULL,
    synced_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (note_id)
);
```

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/store/... -run "TestNoteUpsertAndList|TestNoteSnapshot|TestNoteLinks" -v
```
Expected: all 3 PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/store/note.go backend/internal/store/note_test.go backend/internal/db/migrations/001_init.sql
git commit -m "feat(backend): add note, snapshot, and link store"
```

---

### Task 8: Store — Comments + FolderMappings + Health

**Files:**
- Create: `backend/internal/store/comment.go`
- Create: `backend/internal/store/folder.go`
- Create: `backend/internal/store/health.go`
- Create: `backend/internal/store/extras_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/store/extras_test.go`:
```go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestComments(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "c", "main")
	note, _ := s.UpsertNote(ctx, "r1", "intro.md")

	c1, err := s.CreateComment(ctx, "c1", note.ID, "u1", nil, "Hello!")
	require.NoError(t, err)
	require.Equal(t, "Hello!", c1.Body)

	// Reply
	c2, err := s.CreateComment(ctx, "c2", note.ID, "u1", &c1.ID, "Reply!")
	require.NoError(t, err)
	require.NotNil(t, c2.ParentID)

	comments, err := s.ListComments(ctx, note.ID)
	require.NoError(t, err)
	require.Len(t, comments, 2)
}

func TestFolderMappings(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "c", "main")

	err := s.UpsertFolderMapping(ctx, "u1", "r1", "MyVaultFolder", "docs")
	require.NoError(t, err)

	m, err := s.GetFolderMapping(ctx, "u1", "r1")
	require.NoError(t, err)
	require.Equal(t, "MyVaultFolder", m.VaultFolder)
	require.Equal(t, "docs", m.RepoSubfolder)

	// Upsert updates
	s.UpsertFolderMapping(ctx, "u1", "r1", "NewFolder", "")
	m, _ = s.GetFolderMapping(ctx, "u1", "r1")
	require.Equal(t, "NewFolder", m.VaultFolder)
}

func TestSystemHealth(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	err := s.UpsertHealth(ctx, 75.5, 1000000, "ok", &now)
	require.NoError(t, err)

	h, err := s.GetHealth(ctx)
	require.NoError(t, err)
	require.Equal(t, "ok", h.DiskStatus)
	require.InDelta(t, 75.5, h.DiskFreePct, 0.01)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/store/... -run TestComments -v
```
Expected: compile error.

- [ ] **Step 3: Implement comment.go**

`backend/internal/store/comment.go`:
```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/pubobs/backend/internal/model"
)

func (s *Store) CreateComment(ctx context.Context, id, noteID, userID string, parentID *string, body string) (*model.Comment, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO comments (id, note_id, user_id, parent_id, body, created_at) VALUES (?,?,?,?,?,?)`,
		id, noteID, userID, parentID, body, time.Now().UTC(),
	)
	if err != nil {
		return nil, err
	}
	return &model.Comment{
		ID: id, NoteID: noteID, UserID: userID, ParentID: parentID, Body: body,
	}, nil
}

func (s *Store) ListComments(ctx context.Context, noteID string) ([]*model.Comment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, note_id, user_id, parent_id, body, created_at
		FROM comments WHERE note_id=? ORDER BY created_at`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Comment
	for rows.Next() {
		var c model.Comment
		var parentID sql.NullString
		if err := rows.Scan(&c.ID, &c.NoteID, &c.UserID, &parentID, &c.Body, &c.CreatedAt); err != nil {
			return nil, err
		}
		if parentID.Valid {
			c.ParentID = &parentID.String
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Implement folder.go**

`backend/internal/store/folder.go`:
```go
package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/pubobs/backend/internal/model"
)

func (s *Store) UpsertFolderMapping(ctx context.Context, userID, repoID, vaultFolder, repoSubfolder string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO folder_mappings (user_id, repo_id, vault_folder, repo_subfolder) VALUES (?,?,?,?)
		ON CONFLICT(user_id, repo_id) DO UPDATE SET vault_folder=excluded.vault_folder, repo_subfolder=excluded.repo_subfolder`,
		userID, repoID, vaultFolder, repoSubfolder,
	)
	return err
}

func (s *Store) GetFolderMapping(ctx context.Context, userID, repoID string) (*model.FolderMapping, error) {
	var m model.FolderMapping
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, repo_id, vault_folder, repo_subfolder FROM folder_mappings WHERE user_id=? AND repo_id=?`,
		userID, repoID,
	).Scan(&m.UserID, &m.RepoID, &m.VaultFolder, &m.RepoSubfolder)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &m, err
}

func (s *Store) ListUserFolderMappings(ctx context.Context, userID string) ([]*model.FolderMapping, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, repo_id, vault_folder, repo_subfolder FROM folder_mappings WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.FolderMapping
	for rows.Next() {
		var m model.FolderMapping
		if err := rows.Scan(&m.UserID, &m.RepoID, &m.VaultFolder, &m.RepoSubfolder); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Implement health.go**

`backend/internal/store/health.go`:
```go
package store

import (
	"context"
	"time"

	"github.com/pubobs/backend/internal/model"
)

func (s *Store) UpsertHealth(ctx context.Context, diskFreePct float64, diskFreeBytes int64, status string, lastEviction *time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system_health (id, disk_free_pct, disk_free_bytes, disk_status, last_eviction_at, checked_at)
		VALUES (1,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			disk_free_pct=excluded.disk_free_pct,
			disk_free_bytes=excluded.disk_free_bytes,
			disk_status=excluded.disk_status,
			last_eviction_at=excluded.last_eviction_at,
			checked_at=excluded.checked_at`,
		diskFreePct, diskFreeBytes, status, lastEviction, time.Now().UTC(),
	)
	return err
}

func (s *Store) GetHealth(ctx context.Context) (*model.SystemHealth, error) {
	var h model.SystemHealth
	var lastEviction interface{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, disk_free_pct, disk_free_bytes, disk_status, last_eviction_at, checked_at FROM system_health WHERE id=1`,
	).Scan(&h.ID, &h.DiskFreePct, &h.DiskFreeBytes, &h.DiskStatus, &lastEviction, &h.CheckedAt)
	if err != nil {
		return nil, err
	}
	if t, ok := lastEviction.(time.Time); ok {
		h.LastEvictionAt = &t
	}
	return &h, nil
}
```

- [ ] **Step 6: Run all store tests**

```bash
cd backend && go test ./internal/store/... -v
```
Expected: all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/store/
git commit -m "feat(backend): add comment, folder mapping, and health store"
```

---

## Part C — Cred Encryption + Auth

### Task 9: Cred Encryption

**Files:**
- Create: `backend/internal/auth/crypto.go`
- Create: `backend/internal/auth/crypto_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/auth/crypto_test.go`:
```go
package auth_test

import (
	"testing"

	"github.com/pubobs/backend/internal/auth"
	"github.com/stretchr/testify/require"
)

func TestEncryptDecryptCreds(t *testing.T) {
	key := make([]byte, 32)
	for i := range key { key[i] = byte(i) }

	plaintext := `{"username":"x-access-token","password":"ghp_secret123"}`

	enc, err := auth.EncryptCreds(key, plaintext)
	require.NoError(t, err)
	require.NotEqual(t, plaintext, enc)

	dec, err := auth.DecryptCreds(key, enc)
	require.NoError(t, err)
	require.Equal(t, plaintext, dec)
}

func TestEncryptCreds_differentNonceEachTime(t *testing.T) {
	key := make([]byte, 32)
	plaintext := "secret"

	enc1, _ := auth.EncryptCreds(key, plaintext)
	enc2, _ := auth.EncryptCreds(key, plaintext)
	require.NotEqual(t, enc1, enc2, "each encryption should use a unique nonce")
}

func TestDecryptCreds_wrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	key2[0] = 0xFF

	enc, _ := auth.EncryptCreds(key1, "secret")
	_, err := auth.DecryptCreds(key2, enc)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/auth/... -run TestEncryptDecryptCreds -v
```
Expected: compile error.

- [ ] **Step 3: Implement crypto.go**

`backend/internal/auth/crypto.go`:
```go
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// EncryptCreds encrypts plaintext using AES-256-GCM with key.
// Returns base64-encoded nonce+ciphertext.
func EncryptCreds(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptCreds decrypts a base64-encoded nonce+ciphertext produced by EncryptCreds.
func DecryptCreds(key []byte, encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return "", errors.New("ciphertext too short")
	}
	plaintext, err := gcm.Open(nil, data[:ns], data[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}
```

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/auth/... -run "TestEncryptDecryptCreds|TestEncryptCreds_differentNonceEachTime|TestDecryptCreds_wrongKey" -v
```
Expected: all 3 PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/crypto.go backend/internal/auth/crypto_test.go
git commit -m "feat(backend): add AES-256-GCM credential encryption"
```

---

### Task 10: Auth — PKCE + Session Store

**Files:**
- Create: `backend/internal/auth/pkce.go`
- Create: `backend/internal/auth/pkce_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/auth/pkce_test.go`:
```go
package auth_test

import (
	"testing"
	"time"

	"github.com/pubobs/backend/internal/auth"
	"github.com/stretchr/testify/require"
)

func TestPKCEChallenge(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	// SHA256(verifier) base64url == expected value (RFC 7636 Appendix B)
	challenge := auth.ComputeChallenge(verifier)
	require.Equal(t, "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", challenge)
}

func TestSessionStore_StoreAndConsume(t *testing.T) {
	ss := auth.NewSessionStore()

	sessionID := ss.StoreSession("challenge123", "http://localhost:12345/callback", "plugin-state-xyz")
	require.NotEmpty(t, sessionID)

	sess, ok := ss.GetSession(sessionID)
	require.True(t, ok)
	require.Equal(t, "challenge123", sess.CodeChallenge)
	require.Equal(t, "plugin-state-xyz", sess.PluginState)
}

func TestSessionStore_StoreCode_and_Consume(t *testing.T) {
	ss := auth.NewSessionStore()

	code := ss.StoreAuthCode("user-123", "challenge123")
	require.NotEmpty(t, code)

	// Correct verifier
	userID, err := ss.ConsumeAuthCode(code, "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
	// This verifier won't match challenge123 — that's fine, we're testing the lookup+consume mechanics
	// Use matching pair:
	_ = userID; _ = err

	verifier2 := "s256testverifier0000000000000000000000000000"
	challenge2 := auth.ComputeChallenge(verifier2)
	code2 := ss.StoreAuthCode("user-456", challenge2)

	uid, err := ss.ConsumeAuthCode(code2, verifier2)
	require.NoError(t, err)
	require.Equal(t, "user-456", uid)

	// Code is single-use
	_, err = ss.ConsumeAuthCode(code2, verifier2)
	require.Error(t, err)
}

func TestSessionStore_ExpiredCode(t *testing.T) {
	ss := auth.NewSessionStore()
	ss.SetCodeTTL(1 * time.Millisecond)

	code := ss.StoreAuthCode("user-789", "anychallenge")
	time.Sleep(5 * time.Millisecond)

	_, err := ss.ConsumeAuthCode(code, "anyverifier")
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/auth/... -run TestPKCEChallenge -v
```
Expected: compile error.

- [ ] **Step 3: Implement pkce.go**

`backend/internal/auth/pkce.go`:
```go
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ComputeChallenge computes the PKCE S256 code challenge from a verifier.
func ComputeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

type pkceSession struct {
	CodeChallenge string
	RedirectURI   string
	PluginState   string
	ExpiresAt     time.Time
}

type authCode struct {
	UserID        string
	CodeChallenge string
	ExpiresAt     time.Time
}

// SessionStore holds in-memory PKCE sessions and short-lived auth codes.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*pkceSession
	codes    map[string]*authCode
	codeTTL  time.Duration
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*pkceSession),
		codes:    make(map[string]*authCode),
		codeTTL:  5 * time.Minute,
	}
}

// SetCodeTTL overrides the default 5-minute TTL (used in tests).
func (ss *SessionStore) SetCodeTTL(d time.Duration) {
	ss.mu.Lock()
	ss.codeTTL = d
	ss.mu.Unlock()
}

// StoreSession saves an incoming plugin auth request and returns a random session ID.
func (ss *SessionStore) StoreSession(codeChallenge, redirectURI, pluginState string) string {
	id := randomBase64(16)
	ss.mu.Lock()
	ss.sessions[id] = &pkceSession{
		CodeChallenge: codeChallenge,
		RedirectURI:   redirectURI,
		PluginState:   pluginState,
		ExpiresAt:     time.Now().Add(10 * time.Minute),
	}
	ss.mu.Unlock()
	return id
}

// GetSession retrieves and deletes a session (single-use).
func (ss *SessionStore) GetSession(id string) (*pkceSession, bool) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.sessions[id]
	if !ok || time.Now().After(s.ExpiresAt) {
		delete(ss.sessions, id)
		return nil, false
	}
	delete(ss.sessions, id)
	return s, true
}

// StoreAuthCode saves an auth code tied to a user + code_challenge.
func (ss *SessionStore) StoreAuthCode(userID, codeChallenge string) string {
	code := randomBase64(32)
	ss.mu.Lock()
	ss.codes[code] = &authCode{
		UserID:        userID,
		CodeChallenge: codeChallenge,
		ExpiresAt:     time.Now().Add(ss.codeTTL),
	}
	ss.mu.Unlock()
	return code
}

// ConsumeAuthCode verifies the PKCE code exchange and returns the userID.
// The code is deleted after the first call.
func (ss *SessionStore) ConsumeAuthCode(code, codeVerifier string) (string, error) {
	ss.mu.Lock()
	ac, ok := ss.codes[code]
	delete(ss.codes, code)
	ss.mu.Unlock()

	if !ok {
		return "", errors.New("invalid or already-used auth code")
	}
	if time.Now().After(ac.ExpiresAt) {
		return "", errors.New("auth code expired")
	}
	if ComputeChallenge(codeVerifier) != ac.CodeChallenge {
		return "", fmt.Errorf("PKCE verification failed")
	}
	return ac.UserID, nil
}

func randomBase64(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
```

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/auth/... -run "TestPKCEChallenge|TestSessionStore" -v
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/pkce.go backend/internal/auth/pkce_test.go
git commit -m "feat(backend): add PKCE session store and challenge computation"
```

---

### Task 11: Auth — JWT

**Files:**
- Create: `backend/internal/auth/jwt.go`
- Create: `backend/internal/auth/jwt_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/auth/jwt_test.go`:
```go
package auth_test

import (
	"testing"
	"time"

	"github.com/pubobs/backend/internal/auth"
	"github.com/stretchr/testify/require"
)

func testKey() []byte {
	k := make([]byte, 32)
	for i := range k { k[i] = byte(i + 1) }
	return k
}

func TestIssueAndVerifyAccessToken(t *testing.T) {
	key := testKey()
	token, err := auth.IssueAccessToken(key, "user-1", "alice@x.com", false, 24*time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := auth.VerifyAccessToken(key, token)
	require.NoError(t, err)
	require.Equal(t, "user-1", claims.UserID)
	require.Equal(t, "alice@x.com", claims.Email)
	require.False(t, claims.IsAdmin)
}

func TestAccessToken_expired(t *testing.T) {
	key := testKey()
	token, _ := auth.IssueAccessToken(key, "u1", "a@x.com", false, -1*time.Second)
	_, err := auth.VerifyAccessToken(key, token)
	require.Error(t, err)
}

func TestIssueAndVerifyRefreshToken(t *testing.T) {
	key := testKey()
	token, err := auth.IssueRefreshToken(key, "user-2", 30*24*time.Hour)
	require.NoError(t, err)

	userID, err := auth.VerifyRefreshToken(key, token)
	require.NoError(t, err)
	require.Equal(t, "user-2", userID)
}

func TestRefreshToken_wrongType(t *testing.T) {
	key := testKey()
	// Access token should not pass as refresh token
	accessToken, _ := auth.IssueAccessToken(key, "u1", "a@x.com", false, time.Hour)
	_, err := auth.VerifyRefreshToken(key, accessToken)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/auth/... -run TestIssueAndVerifyAccessToken -v
```
Expected: compile error.

- [ ] **Step 3: Implement jwt.go**

`backend/internal/auth/jwt.go`:
```go
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type AccessClaims struct {
	UserID  string
	Email   string
	IsAdmin bool
}

type accessJWTClaims struct {
	jwt.RegisteredClaims
	Email   string `json:"email"`
	IsAdmin bool   `json:"is_admin"`
	Type    string `json:"type"`
}

type refreshJWTClaims struct {
	jwt.RegisteredClaims
	Type string `json:"type"`
}

func IssueAccessToken(key []byte, userID, email string, isAdmin bool, ttl time.Duration) (string, error) {
	claims := accessJWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
		},
		Email:   email,
		IsAdmin: isAdmin,
		Type:    "access",
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
}

func VerifyAccessToken(key []byte, tokenStr string) (*AccessClaims, error) {
	var claims accessJWTClaims
	_, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return key, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}
	if claims.Type != "access" {
		return nil, errors.New("not an access token")
	}
	return &AccessClaims{
		UserID:  claims.Subject,
		Email:   claims.Email,
		IsAdmin: claims.IsAdmin,
	}, nil
}

func IssueRefreshToken(key []byte, userID string, ttl time.Duration) (string, error) {
	claims := refreshJWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
		},
		Type: "refresh",
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
}

func VerifyRefreshToken(key []byte, tokenStr string) (string, error) {
	var claims refreshJWTClaims
	_, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return key, nil
	})
	if err != nil {
		return "", fmt.Errorf("parse refresh token: %w", err)
	}
	if claims.Type != "refresh" {
		return "", errors.New("not a refresh token")
	}
	return claims.Subject, nil
}
```

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/auth/... -run "TestIssueAndVerifyAccessToken|TestAccessToken_expired|TestIssueAndVerifyRefreshToken|TestRefreshToken_wrongType" -v
```
Expected: all 4 PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/jwt.go backend/internal/auth/jwt_test.go
git commit -m "feat(backend): add JWT access and refresh token issuance/verification"
```

---

### Task 12: Auth — OIDC Client

**Files:**
- Create: `backend/internal/auth/oidc.go`
- Create: `backend/internal/auth/oidc_test.go`

Note: Full OIDC tests require an OIDC provider. We test the struct initialisation; provider-dependent logic is covered by integration tests in Part E.

- [ ] **Step 1: Write the failing test**

`backend/internal/auth/oidc_test.go`:
```go
package auth_test

import (
	"testing"

	"github.com/pubobs/backend/internal/auth"
	"github.com/stretchr/testify/require"
)

func TestUserClaims_fields(t *testing.T) {
	// UserClaims is a plain struct — verify it has the expected fields.
	uc := auth.UserClaims{
		Subject: "oidc-sub-123",
		Email:   "user@example.com",
		Name:    "Test User",
	}
	require.Equal(t, "oidc-sub-123", uc.Subject)
	require.Equal(t, "user@example.com", uc.Email)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/auth/... -run TestUserClaims_fields -v
```
Expected: compile error.

- [ ] **Step 3: Implement oidc.go**

`backend/internal/auth/oidc.go`:
```go
package auth

import (
	"context"
	"fmt"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// UserClaims holds the identity fields extracted from an OIDC ID token.
type UserClaims struct {
	Subject string
	Email   string
	Name    string
}

// OIDCClient wraps go-oidc for the PKCE flow.
type OIDCClient struct {
	provider *gooidc.Provider
	oauth2   oauth2.Config
}

// NewOIDCClient discovers the OIDC provider at issuer and returns a configured client.
func NewOIDCClient(ctx context.Context, issuer, clientID, clientSecret, baseURL string) (*OIDCClient, error) {
	provider, err := gooidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider %q: %w", issuer, err)
	}
	return &OIDCClient{
		provider: provider,
		oauth2: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  baseURL + "/auth/callback",
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{gooidc.ScopeOpenID, "email", "profile"},
		},
	}, nil
}

// AuthCodeURL returns the OIDC authorization URL with the given state.
func (c *OIDCClient) AuthCodeURL(state string) string {
	return c.oauth2.AuthCodeURL(state)
}

// ExchangeCode exchanges an OIDC authorization code for validated user claims.
func (c *OIDCClient) ExchangeCode(ctx context.Context, code string) (*UserClaims, error) {
	token, err := c.oauth2.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("missing id_token in response")
	}
	verifier := c.provider.Verifier(&gooidc.Config{ClientID: c.oauth2.ClientID})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("verify id_token: %w", err)
	}
	var claims struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}
	return &UserClaims{
		Subject: idToken.Subject,
		Email:   claims.Email,
		Name:    claims.Name,
	}, nil
}
```

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/auth/... -run TestUserClaims_fields -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/oidc.go backend/internal/auth/oidc_test.go
git commit -m "feat(backend): add OIDC client wrapper"
```

---

### Task 13: Auth — HTTP Middleware

**Files:**
- Create: `backend/internal/auth/middleware.go`
- Create: `backend/internal/auth/middleware_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/auth/middleware_test.go`:
```go
package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pubobs/backend/internal/auth"
	"github.com/stretchr/testify/require"
)

func TestRequireAuth_valid(t *testing.T) {
	key := testKey()
	token, _ := auth.IssueAccessToken(key, "user-1", "alice@x.com", false, 0)

	// 0 duration → expires immediately; use 1 hour
	token, _ = auth.IssueAccessToken(key, "user-1", "alice@x.com", false, 3600*1e9)

	called := false
	handler := auth.RequireAuth(key)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		require.NotNil(t, claims)
		require.Equal(t, "user-1", claims.UserID)
		called = true
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	require.True(t, called)
}

func TestRequireAuth_missing(t *testing.T) {
	key := testKey()
	handler := auth.RequireAuth(key)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach handler")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireAuth_invalid(t *testing.T) {
	key := testKey()
	handler := auth.RequireAuth(key)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach handler")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer bad.token.here")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/auth/... -run TestRequireAuth_valid -v
```
Expected: compile error.

- [ ] **Step 3: Implement middleware.go**

`backend/internal/auth/middleware.go`:
```go
package auth

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const claimsKey contextKey = "claims"

// RequireAuth is an HTTP middleware that validates the Bearer JWT and injects
// AccessClaims into the request context. Returns 401 if the token is missing or invalid.
func RequireAuth(key []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr := extractBearerToken(r)
			if tokenStr == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			claims, err := VerifyAccessToken(key, tokenStr)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClaimsFromContext retrieves AccessClaims from the request context.
// Returns nil if not present.
func ClaimsFromContext(ctx context.Context) *AccessClaims {
	c, _ := ctx.Value(claimsKey).(*AccessClaims)
	return c
}

func extractBearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(h, "Bearer ")
}
```

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/auth/... -v
```
Expected: all auth tests PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/middleware.go backend/internal/auth/middleware_test.go
git commit -m "feat(backend): add JWT auth middleware with context injection"
```

---

## Part D — Git Cache

### Task 14: Git Command Runner

**Files:**
- Create: `backend/internal/gitcache/git.go`
- Create: `backend/internal/gitcache/git_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/gitcache/git_test.go`:
```go
package gitcache_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pubobs/backend/internal/gitcache"
	"github.com/stretchr/testify/require"
)

// newBareRepo creates a temporary bare git repo and returns its path.
func newBareRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bare := filepath.Join(dir, "remote.git")
	require.NoError(t, exec.Command("git", "init", "--bare", bare).Run())
	return bare
}

// seedBareRepo clones the bare repo, adds a file, and pushes it.
func seedBareRepo(t *testing.T, bareURL string) {
	t.Helper()
	work := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@x.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@x.com",
		)
		require.NoError(t, cmd.Run())
	}
	run("clone", bareURL, ".")
	os.WriteFile(filepath.Join(work, "hello.md"), []byte("# Hello"), 0644)
	run("add", ".")
	run("commit", "-m", "initial")
	run("push", "origin", "HEAD:main")
}

func TestCloneAndListFiles(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	cloneDir := t.TempDir()
	g := gitcache.NewGitRunner()

	err := g.Clone(cloneDir, bareURL, "", "main")
	require.NoError(t, err)

	files, err := g.ListFiles(cloneDir)
	require.NoError(t, err)
	require.Contains(t, files, "hello.md")
}

func TestAddCommitPush(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	cloneDir := t.TempDir()
	g := gitcache.NewGitRunner()
	require.NoError(t, g.Clone(cloneDir, bareURL, "", "main"))

	// Write a new file and commit+push
	os.WriteFile(filepath.Join(cloneDir, "docs/note.md"), []byte("# Note"), 0644)
	require.NoError(t, os.MkdirAll(filepath.Join(cloneDir, "docs"), 0755))
	os.WriteFile(filepath.Join(cloneDir, "docs/note.md"), []byte("# Note"), 0644)

	sha, err := g.AddCommitPush(cloneDir, bareURL, "", "main", "pubobs: sync 2024-01-01 by alice")
	require.NoError(t, err)
	require.Len(t, sha, 40)
}

func TestLogFile(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	cloneDir := t.TempDir()
	g := gitcache.NewGitRunner()
	require.NoError(t, g.Clone(cloneDir, bareURL, "", "main"))

	commits, err := g.LogFile(cloneDir, "hello.md")
	require.NoError(t, err)
	require.Len(t, commits, 1)
	require.Equal(t, "initial", commits[0].Message)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/gitcache/... -run TestCloneAndListFiles -v
```
Expected: compile error.

- [ ] **Step 3: Implement git.go**

`backend/internal/gitcache/git.go`:
```go
package gitcache

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pubobs/backend/internal/model"
)

// GitRunner executes system git commands.
type GitRunner struct{}

func NewGitRunner() *GitRunner { return &GitRunner{} }

// credentialedURL injects credentials into an HTTPS remote URL if a PAT is provided.
// credJSON is expected to be `{"username":"...","password":"..."}` — parsed simply.
// If credJSON is empty, the URL is returned unchanged (for unauthenticated or SSH remotes).
func credentialedURL(remoteURL, credJSON string) string {
	if credJSON == "" {
		return remoteURL
	}
	var username, password string
	// Simple JSON parse without reflection to avoid extra deps
	for _, field := range []struct{ key, dst *string }{
		{"\"username\":", &username}, {"\"password\":", &password},
	} {
		if idx := strings.Index(credJSON, *field.key); idx >= 0 {
			rest := credJSON[idx+len(*field.key):]
			rest = strings.TrimSpace(rest)
			if len(rest) > 0 && rest[0] == '"' {
				end := strings.Index(rest[1:], "\"")
				if end >= 0 {
					*field.dst = rest[1 : end+1]
				}
			}
		}
	}
	if username == "" || password == "" {
		return remoteURL
	}
	// Insert user:pass@ after the scheme
	for _, scheme := range []string{"https://", "http://"} {
		if strings.HasPrefix(remoteURL, scheme) {
			return scheme + username + ":" + password + "@" + remoteURL[len(scheme):]
		}
	}
	return remoteURL
}

func (g *GitRunner) run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=pubobs",
		"GIT_AUTHOR_EMAIL=pubobs@localhost",
		"GIT_COMMITTER_NAME=pubobs",
		"GIT_COMMITTER_EMAIL=pubobs@localhost",
		"GIT_TERMINAL_PROMPT=0",
	)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", args[0], err, errOut.String())
	}
	return strings.TrimSpace(out.String()), nil
}

// Clone clones remoteURL into dir. credJSON may be empty for public repos.
func (g *GitRunner) Clone(dir, remoteURL, credJSON, branch string) error {
	authedURL := credentialedURL(remoteURL, credJSON)
	_, err := g.run("", "clone", "--branch", branch, "--single-branch", authedURL, dir)
	if err != nil {
		// If branch doesn't exist yet (fresh bare repo), clone without --branch
		_, err = g.run("", "clone", authedURL, dir)
	}
	return err
}

// Pull fetches and fast-forwards the current branch.
func (g *GitRunner) Pull(dir, remoteURL, credJSON string) error {
	authedURL := credentialedURL(remoteURL, credJSON)
	_, err := g.run(dir, "pull", authedURL)
	return err
}

// AddCommitPush stages all changes, commits, pushes, and returns the commit SHA.
func (g *GitRunner) AddCommitPush(dir, remoteURL, credJSON, branch, message string) (string, error) {
	if _, err := g.run(dir, "add", "-A"); err != nil {
		return "", err
	}
	// Check if there's anything to commit
	status, _ := g.run(dir, "status", "--porcelain")
	if status == "" {
		// Nothing to commit — return current HEAD
		return g.run(dir, "rev-parse", "HEAD")
	}
	if _, err := g.run(dir, "commit", "-m", message); err != nil {
		return "", err
	}
	authedURL := credentialedURL(remoteURL, credJSON)
	if _, err := g.run(dir, "push", authedURL, "HEAD:"+branch); err != nil {
		return "", err
	}
	return g.run(dir, "rev-parse", "HEAD")
}

// ListFiles returns all tracked .md file paths relative to the repo root.
func (g *GitRunner) ListFiles(dir string) ([]string, error) {
	out, err := g.run(dir, "ls-files", "--", "*.md")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// ReadFile returns the content of a tracked file.
func (g *GitRunner) ReadFile(dir, path string) (string, error) {
	out, err := g.run(dir, "show", "HEAD:"+path)
	if err != nil {
		return "", err
	}
	return out, nil
}

// BlobSHA returns the git blob SHA for a tracked file at HEAD.
func (g *GitRunner) BlobSHA(dir, path string) (string, error) {
	out, err := g.run(dir, "ls-tree", "--format=%(objectname)", "HEAD", "--", path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// LogFile returns the commit history for a specific file path.
func (g *GitRunner) LogFile(dir, path string) ([]model.Commit, error) {
	out, err := g.run(dir, "log",
		"--format=%H%x1f%ae%x1f%an%x1f%aI%x1f%s",
		"--", path)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var commits []model.Commit
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "\x1f")
		if len(parts) < 5 {
			continue
		}
		t, _ := time.Parse(time.RFC3339, parts[3])
		commits = append(commits, model.Commit{
			SHA:         parts[0],
			AuthorEmail: parts[1],
			AuthorName:  parts[2],
			Date:        t,
			Message:     parts[4],
		})
	}
	return commits, nil
}
```

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/gitcache/... -v -timeout 60s
```
Expected: all tests PASS. (Tests clone real bare repos in temp dirs — requires `git` on PATH.)

- [ ] **Step 5: Commit**

```bash
git add backend/internal/gitcache/git.go backend/internal/gitcache/git_test.go
git commit -m "feat(backend): add git command runner (clone, pull, commit, push, log)"
```

---

### Task 15: Git Cache Manager

**Files:**
- Create: `backend/internal/gitcache/cache.go`
- Create: `backend/internal/gitcache/cache_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/gitcache/cache_test.go`:
```go
package gitcache_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/model"
	"github.com/stretchr/testify/require"
)

func TestCache_SyncAndListFiles(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	cacheDir := t.TempDir()
	cache := gitcache.NewCache(cacheDir)

	repo := &model.Repo{
		ID:            "r1",
		RemoteURL:     bareURL,
		EncryptedCreds: "",
		DefaultBranch: "main",
	}

	files := []gitcache.SyncFile{
		{Path: "newdoc.md", MDContent: "# New", HTMLContent: "<h1>New</h1>"},
	}
	sha, err := cache.Sync(context.Background(), repo, "", files, "sync 2024-01-01 by alice")
	require.NoError(t, err)
	require.NotEmpty(t, sha)

	// local_path should now be set
	localPath := filepath.Join(cacheDir, "r1")
	_, err = os.Stat(localPath)
	require.NoError(t, err)

	entries, err := cache.ListFiles(context.Background(), repo, "")
	require.NoError(t, err)
	var paths []string
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	require.Contains(t, paths, "newdoc.md")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/gitcache/... -run TestCache_SyncAndListFiles -v
```
Expected: compile error.

- [ ] **Step 3: Implement cache.go**

`backend/internal/gitcache/cache.go`:
```go
package gitcache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/pubobs/backend/internal/model"
)

// SyncFile is one file in a sync payload from the plugin.
type SyncFile struct {
	Path        string
	MDContent   string
	HTMLContent string
}

// Cache manages per-repo local git clones.
type Cache struct {
	baseDir string
	mu      sync.Mutex
	locks   map[string]*sync.Mutex
	git     *GitRunner
}

func NewCache(baseDir string) *Cache {
	return &Cache{
		baseDir: baseDir,
		locks:   make(map[string]*sync.Mutex),
		git:     NewGitRunner(),
	}
}

func (c *Cache) repoLock(repoID string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.locks[repoID]; !ok {
		c.locks[repoID] = &sync.Mutex{}
	}
	return c.locks[repoID]
}

func (c *Cache) repoDir(repoID string) string {
	return filepath.Join(c.baseDir, repoID)
}

// getOrClone ensures the repo is cloned locally. Must be called with repo lock held.
func (c *Cache) getOrClone(repo *model.Repo, credJSON string) (string, error) {
	dir := c.repoDir(repo.ID)
	if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", dir, err)
		}
		if err := c.git.Clone(dir, repo.RemoteURL, credJSON, repo.DefaultBranch); err != nil {
			os.RemoveAll(dir)
			return "", fmt.Errorf("clone %s: %w", repo.RemoteURL, err)
		}
	} else {
		if err := c.git.Pull(dir, repo.RemoteURL, credJSON); err != nil {
			return "", fmt.Errorf("pull %s: %w", repo.ID, err)
		}
	}
	return dir, nil
}

// Sync writes files to the cache, commits them, and pushes.
// credJSON is the decrypted credentials string (may be empty for public repos).
// Returns the commit SHA.
func (c *Cache) Sync(ctx context.Context, repo *model.Repo, credJSON string, files []SyncFile, commitMsg string) (string, error) {
	lock := c.repoLock(repo.ID)
	lock.Lock()
	defer lock.Unlock()

	dir, err := c.getOrClone(repo, credJSON)
	if err != nil {
		return "", err
	}

	for _, f := range files {
		fullPath := filepath.Join(dir, f.Path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return "", err
		}
		if err := os.WriteFile(fullPath, []byte(f.MDContent), 0644); err != nil {
			return "", fmt.Errorf("write %s: %w", f.Path, err)
		}
	}

	sha, err := c.git.AddCommitPush(dir, repo.RemoteURL, credJSON, repo.DefaultBranch, commitMsg)
	if err != nil {
		return "", fmt.Errorf("commit+push: %w", err)
	}
	return sha, nil
}

// ListFiles returns all .md files in the repo with their content and blob SHA.
func (c *Cache) ListFiles(ctx context.Context, repo *model.Repo, credJSON string) ([]model.FileEntry, error) {
	lock := c.repoLock(repo.ID)
	lock.Lock()
	defer lock.Unlock()

	dir, err := c.getOrClone(repo, credJSON)
	if err != nil {
		return nil, err
	}

	paths, err := c.git.ListFiles(dir)
	if err != nil {
		return nil, err
	}

	var out []model.FileEntry
	for _, p := range paths {
		content, err := c.git.ReadFile(dir, p)
		if err != nil {
			return nil, err
		}
		sha, _ := c.git.BlobSHA(dir, p)
		out = append(out, model.FileEntry{Path: p, Content: content, SHA: sha})
	}
	return out, nil
}

// History returns the commit log for a specific file.
func (c *Cache) History(ctx context.Context, repo *model.Repo, credJSON, filePath string) ([]model.Commit, error) {
	lock := c.repoLock(repo.ID)
	lock.Lock()
	defer lock.Unlock()

	dir, err := c.getOrClone(repo, credJSON)
	if err != nil {
		return nil, err
	}
	return c.git.LogFile(dir, filePath)
}

// Evict removes the local clone for a repo.
func (c *Cache) Evict(repoID string) error {
	lock := c.repoLock(repoID)
	lock.Lock()
	defer lock.Unlock()
	return os.RemoveAll(c.repoDir(repoID))
}

// DiskUsage returns free bytes and percentage on the cache volume.
func (c *Cache) DiskUsage() (freeBytes int64, freePct float64, err error) {
	return diskUsage(c.baseDir)
}
```

Create `backend/internal/gitcache/disk_unix.go` (syscall varies by OS):

`backend/internal/gitcache/disk_unix.go`:
```go
//go:build !windows

package gitcache

import "syscall"

func diskUsage(path string) (freeBytes int64, freePct float64, err error) {
	var stat syscall.Statfs_t
	if err = syscall.Statfs(path, &stat); err != nil {
		return
	}
	freeBytes = int64(stat.Bavail) * int64(stat.Bsize)
	total := int64(stat.Blocks) * int64(stat.Bsize)
	if total > 0 {
		freePct = float64(freeBytes) / float64(total) * 100
	}
	return
}
```

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/gitcache/... -v -timeout 60s
```
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/gitcache/
git commit -m "feat(backend): add git cache manager with per-repo mutex"
```

---

## Part E — API Handlers

### Task 16: API Router + Deps

**Files:**
- Create: `backend/internal/api/deps.go`
- Create: `backend/internal/api/router.go`
- Create: `backend/internal/api/helpers.go`

- [ ] **Step 1: Implement deps.go**

`backend/internal/api/deps.go`:
```go
package api

import (
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/config"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/store"
)

// Deps holds all shared dependencies injected into API handlers.
type Deps struct {
	Store    *store.Store
	Cache    *gitcache.Cache
	Auth     *auth.SessionStore
	OIDC     *auth.OIDCClient
	Config   *config.Config
}
```

- [ ] **Step 2: Implement helpers.go**

`backend/internal/api/helpers.go`:
```go
package api

import (
	"encoding/json"
	"net/http"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}
```

- [ ] **Step 3: Implement router.go**

`backend/internal/api/router.go`:
```go
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/pubobs/backend/internal/auth"
)

func BuildRouter(deps *Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Auth (unauthenticated)
	r.Get("/auth/plugin", handlePluginAuth(deps))
	r.Get("/auth/callback", handleAuthCallback(deps))
	r.Post("/auth/token", handleToken(deps))
	r.Post("/auth/refresh", handleRefresh(deps))

	// Authenticated routes
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth(deps.Config.SecretKey))

		r.Get("/api/me", handleMe(deps))
		r.Get("/api/me/folder-mappings", handleListFolderMappings(deps))
		r.Put("/api/me/folder-mappings/{repoID}", handleUpsertFolderMapping(deps))
		r.Get("/api/repos", handleListRepos(deps))

		// Repo-scoped
		r.Post("/api/repos/{id}/sync", handleSync(deps))
		r.Get("/api/repos/{id}/files", handleListFiles(deps))
		r.Get("/api/repos/{id}/notes", handleListNotes(deps))
		r.Get("/api/repos/{id}/notes/*", handleNoteGet(deps))
		r.Post("/api/repos/{id}/notes/*", handleNotePost(deps))

		// Admin (instance_admin only)
		r.Get("/api/admin/health", handleAdminHealth(deps))
		r.Post("/api/admin/repos", handleAdminCreateRepo(deps))
		r.Put("/api/admin/repos/{id}", handleAdminUpdateRepo(deps))
		r.Delete("/api/admin/repos/{id}", handleAdminDeleteRepo(deps))
		r.Post("/api/admin/repos/{id}/access", handleAdminGrantAccess(deps))
		r.Delete("/api/admin/repos/{id}/access/{accessID}", handleAdminRevokeAccess(deps))
		r.Get("/api/admin/users", handleAdminListUsers(deps))
		r.Post("/api/admin/groups", handleAdminCreateGroup(deps))
		r.Post("/api/admin/groups/{id}/members", handleAdminAddGroupMember(deps))
	})

	// Frontend (served last — catch-all for SPA)
	r.Handle("/*", http.FileServer(http.FS(frontendFS)))

	return r
}
```

Note: `frontendFS` is defined in `backend/frontend/embed.go` (Task 24). For now add a placeholder.

Create `backend/internal/api/frontend_stub.go` (temporary):
```go
package api

import "io/fs"

// frontendFS is replaced in production by the go:embed FS from the frontend package.
// This stub is used until Phase 4 embeds the real frontend.
var frontendFS fs.FS = emptyFS{}

type emptyFS struct{}
func (emptyFS) Open(name string) (fs.File, error) { return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist} }
```

- [ ] **Step 4: Verify it compiles (stubs for missing handlers)**

Create `backend/internal/api/stubs.go` with placeholder functions to allow compilation while implementing one handler at a time:
```go
package api

import "net/http"

func handlePluginAuth(d *Deps) http.HandlerFunc        { return notImpl }
func handleAuthCallback(d *Deps) http.HandlerFunc      { return notImpl }
func handleToken(d *Deps) http.HandlerFunc             { return notImpl }
func handleRefresh(d *Deps) http.HandlerFunc           { return notImpl }
func handleMe(d *Deps) http.HandlerFunc                { return notImpl }
func handleListFolderMappings(d *Deps) http.HandlerFunc { return notImpl }
func handleUpsertFolderMapping(d *Deps) http.HandlerFunc { return notImpl }
func handleListRepos(d *Deps) http.HandlerFunc         { return notImpl }
func handleSync(d *Deps) http.HandlerFunc              { return notImpl }
func handleListFiles(d *Deps) http.HandlerFunc         { return notImpl }
func handleListNotes(d *Deps) http.HandlerFunc         { return notImpl }
func handleNoteGet(d *Deps) http.HandlerFunc           { return notImpl }
func handleNotePost(d *Deps) http.HandlerFunc          { return notImpl }
func handleAdminHealth(d *Deps) http.HandlerFunc       { return notImpl }
func handleAdminCreateRepo(d *Deps) http.HandlerFunc   { return notImpl }
func handleAdminUpdateRepo(d *Deps) http.HandlerFunc   { return notImpl }
func handleAdminDeleteRepo(d *Deps) http.HandlerFunc   { return notImpl }
func handleAdminGrantAccess(d *Deps) http.HandlerFunc  { return notImpl }
func handleAdminRevokeAccess(d *Deps) http.HandlerFunc { return notImpl }
func handleAdminListUsers(d *Deps) http.HandlerFunc    { return notImpl }
func handleAdminCreateGroup(d *Deps) http.HandlerFunc  { return notImpl }
func handleAdminAddGroupMember(d *Deps) http.HandlerFunc { return notImpl }

func notImpl(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
```

```bash
cd backend && go build ./internal/api/...
```
Expected: compiles cleanly.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/
git commit -m "feat(backend): add API router, Deps, helpers, and stub handlers"
```

---

### Task 17: API — Auth Handlers

**Files:**
- Create: `backend/internal/api/auth.go`
- Create: `backend/internal/api/auth_test.go`
- Delete or empty relevant entries from `stubs.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/api/auth_test.go`:
```go
package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/config"
	"github.com/pubobs/backend/internal/db"
	"github.com/pubobs/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func newTestDeps(t *testing.T) *api.Deps {
	t.Helper()
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	key := make([]byte, 32)
	for i := range key { key[i] = byte(i + 1) }

	return &api.Deps{
		Store:  store.New(d),
		Auth:   auth.NewSessionStore(),
		Config: &config.Config{SecretKey: key, BaseURL: "http://localhost:8080"},
	}
}

func TestHandleToken_validPKCE(t *testing.T) {
	deps := newTestDeps(t)

	// Seed a user
	deps.Store.UpsertUser(t.Context(), "u1", "alice@x.com", "Alice")

	// Store an auth code with a known challenge
	verifier := "s256testverifier0000000000000000000000000000"
	challenge := auth.ComputeChallenge(verifier)
	code := deps.Auth.StoreAuthCode("u1", challenge)

	body := `{"code":"` + code + `","code_verifier":"` + verifier + `"}`
	req := httptest.NewRequest("POST", "/auth/token", strings.NewReader(body))
	rr := httptest.NewRecorder()

	router := api.BuildRouter(deps)
	router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.NotEmpty(t, resp["access_token"])
	require.NotEmpty(t, resp["refresh_token"])
}

func TestHandleToken_badVerifier(t *testing.T) {
	deps := newTestDeps(t)
	deps.Store.UpsertUser(t.Context(), "u1", "alice@x.com", "Alice")

	code := deps.Auth.StoreAuthCode("u1", "challenge123")
	body := `{"code":"` + code + `","code_verifier":"wrongverifier"}`
	req := httptest.NewRequest("POST", "/auth/token", strings.NewReader(body))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestHandleRefresh(t *testing.T) {
	deps := newTestDeps(t)
	deps.Store.UpsertUser(t.Context(), "u1", "alice@x.com", "Alice")

	refreshToken, err := auth.IssueRefreshToken(deps.Config.SecretKey, "u1", 24*3600*1e9)
	require.NoError(t, err)

	body := `{"refresh_token":"` + refreshToken + `"}`
	req := httptest.NewRequest("POST", "/auth/refresh", strings.NewReader(body))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/api/... -run TestHandleToken_validPKCE -v
```
Expected: FAIL (stub returns 501).

- [ ] **Step 3: Implement auth.go**

`backend/internal/api/auth.go`:
```go
package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
)

func handlePluginAuth(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		redirectURI := q.Get("redirect_uri")
		codeChallenge := q.Get("code_challenge")
		pluginState := q.Get("state")
		if redirectURI == "" || codeChallenge == "" {
			writeError(w, http.StatusBadRequest, "redirect_uri and code_challenge are required")
			return
		}
		sessionID := deps.Auth.StoreSession(codeChallenge, redirectURI, pluginState)
		authURL := deps.OIDC.AuthCodeURL(sessionID)
		http.Redirect(w, r, authURL, http.StatusFound)
	}
}

func handleAuthCallback(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		code := r.URL.Query().Get("code")

		sess, ok := deps.Auth.GetSession(state)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid or expired session")
			return
		}

		claims, err := deps.OIDC.ExchangeCode(r.Context(), code)
		if err != nil {
			writeError(w, http.StatusBadGateway, "OIDC exchange failed")
			return
		}

		user, err := deps.Store.UpsertUser(r.Context(), claims.Subject, claims.Email, claims.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "upsert user failed")
			return
		}

		authCode := deps.Auth.StoreAuthCode(user.ID, sess.CodeChallenge)
		redirectURL := fmt.Sprintf("%s?code=%s&state=%s", sess.RedirectURI, authCode, sess.PluginState)
		http.Redirect(w, r, redirectURL, http.StatusFound)
	}
}

func handleToken(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Code         string `json:"code"`
			CodeVerifier string `json:"code_verifier"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		userID, err := deps.Auth.ConsumeAuthCode(body.Code, body.CodeVerifier)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid code or verifier")
			return
		}
		user, err := deps.Store.GetUserByID(r.Context(), userID)
		if err != nil || user == nil {
			writeError(w, http.StatusInternalServerError, "user not found")
			return
		}
		issueTokenPair(w, deps, user.ID, user.Email, user.IsInstanceAdmin)
	}
}

func handleRefresh(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		userID, err := auth.VerifyRefreshToken(deps.Config.SecretKey, body.RefreshToken)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid refresh token")
			return
		}
		user, err := deps.Store.GetUserByID(r.Context(), userID)
		if err != nil || user == nil {
			writeError(w, http.StatusUnauthorized, "user not found")
			return
		}
		issueTokenPair(w, deps, user.ID, user.Email, user.IsInstanceAdmin)
	}
}

func issueTokenPair(w http.ResponseWriter, deps *Deps, userID, email string, isAdmin bool) {
	access, err := auth.IssueAccessToken(deps.Config.SecretKey, userID, email, isAdmin, 24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "issue token failed")
		return
	}
	refresh, err := auth.IssueRefreshToken(deps.Config.SecretKey, userID, 30*24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "issue refresh failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"expires_in":    int(24 * time.Hour / time.Second),
	})
}

// Remove handlePluginAuth, handleAuthCallback, handleToken, handleRefresh from stubs.go.
var _ = chi.URLParam // ensure chi import in this file
```

Remove the 4 stub functions from `stubs.go`.

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/api/... -run "TestHandleToken|TestHandleRefresh" -v
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/auth.go backend/internal/api/auth_test.go backend/internal/api/stubs.go
git commit -m "feat(backend): implement /auth/token and /auth/refresh handlers"
```

---

### Task 18: API — Me + Repos + FolderMappings

**Files:**
- Create: `backend/internal/api/me.go`
- Create: `backend/internal/api/repos.go`
- Create: `backend/internal/api/me_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/api/me_test.go`:
```go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pubobs/backend/internal/auth"
	"github.com/stretchr/testify/require"
)

func bearerHeader(t *testing.T, deps *api.Deps, userID, email string, isAdmin bool) string {
	t.Helper()
	token, err := auth.IssueAccessToken(deps.Config.SecretKey, userID, email, isAdmin, time.Hour)
	require.NoError(t, err)
	return "Bearer " + token
}

func TestHandleMe(t *testing.T) {
	deps := newTestDeps(t)
	deps.Store.UpsertUser(context.Background(), "u1", "alice@x.com", "Alice")

	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "alice@x.com", resp["email"])
}

func TestHandleListRepos_empty(t *testing.T) {
	deps := newTestDeps(t)
	deps.Store.UpsertUser(context.Background(), "u1", "alice@x.com", "Alice")

	req := httptest.NewRequest("GET", "/api/repos", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var repos []any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&repos))
	require.Len(t, repos, 0)
}

func TestHandleUpsertFolderMapping(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "c", "main")

	body := `{"vault_folder":"MyNotes","repo_subfolder":"docs"}`
	req := httptest.NewRequest("PUT", "/api/me/folder-mappings/r1", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/api/... -run TestHandleMe -v
```
Expected: FAIL (stub returns 501).

- [ ] **Step 3: Implement me.go**

`backend/internal/api/me.go`:
```go
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
)

func handleMe(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		user, err := deps.Store.GetUserByID(r.Context(), claims.UserID)
		if err != nil || user == nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":               user.ID,
			"email":            user.Email,
			"name":             user.Name,
			"is_instance_admin": user.IsInstanceAdmin,
		})
	}
}

func handleListFolderMappings(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		mappings, err := deps.Store.ListUserFolderMappings(r.Context(), claims.UserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list mappings failed")
			return
		}
		if mappings == nil {
			mappings = []*model.FolderMapping{}
		}
		writeJSON(w, http.StatusOK, mappings)
	}
}

func handleUpsertFolderMapping(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "repoID")
		var body struct {
			VaultFolder   string `json:"vault_folder"`
			RepoSubfolder string `json:"repo_subfolder"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := deps.Store.UpsertFolderMapping(r.Context(), claims.UserID, repoID, body.VaultFolder, body.RepoSubfolder); err != nil {
			writeError(w, http.StatusInternalServerError, "upsert failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

Add missing import to me.go: `"github.com/pubobs/backend/internal/model"`.

- [ ] **Step 4: Implement repos.go**

`backend/internal/api/repos.go`:
```go
package api

import (
	"net/http"

	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/model"
)

func handleListRepos(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		var repos []*model.Repo
		var err error
		if claims.IsAdmin {
			repos, err = deps.Store.ListRepos(r.Context())
		} else {
			repos, err = deps.Store.ListUserRepos(r.Context(), claims.UserID)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list repos failed")
			return
		}
		if repos == nil {
			repos = []*model.Repo{}
		}
		// Strip encrypted creds before returning
		type repoResp struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			RemoteURL     string `json:"remote_url"`
			DefaultBranch string `json:"default_branch"`
			IsCloned      bool   `json:"is_cloned"`
		}
		out := make([]repoResp, len(repos))
		for i, r := range repos {
			out[i] = repoResp{
				ID: r.ID, Name: r.Name, RemoteURL: r.RemoteURL,
				DefaultBranch: r.DefaultBranch, IsCloned: r.LocalPath != nil,
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}
```

Remove me/repos stubs from stubs.go.

- [ ] **Step 5: Run tests**

```bash
cd backend && go test ./internal/api/... -run "TestHandleMe|TestHandleListRepos|TestHandleUpsertFolderMapping" -v
```
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/api/me.go backend/internal/api/repos.go backend/internal/api/me_test.go backend/internal/api/stubs.go
git commit -m "feat(backend): implement /api/me, /api/repos, and folder mapping handlers"
```

---

### Task 19: API — Sync Handler

**Files:**
- Create: `backend/internal/api/sync.go`
- Create: `backend/internal/api/sync_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/api/sync_test.go`:
```go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/gitcache"
	"github.com/stretchr/testify/require"
)

func newTestDepsWithCache(t *testing.T) *api.Deps {
	t.Helper()
	deps := newTestDeps(t)
	deps.Cache = gitcache.NewCache(t.TempDir())
	return deps
}

func TestHandleSync(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")

	payload := `{"files":[{"path":"notes/hello.md","md_content":"# Hello","html_content":"<h1>Hello</h1>","frontmatter":{"tags":["test"]}}]}`
	req := httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(payload))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.NotEmpty(t, resp["commit_sha"])
}

func TestHandleSync_insufficientRole(t *testing.T) {
	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", "https://x.com/r.git", "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader") // reader, not editor

	req := httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(`{"files":[]}`))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/api/... -run TestHandleSync -v -timeout 60s
```
Expected: FAIL (stub returns 501).

- [ ] **Step 3: Implement sync.go**

`backend/internal/api/sync.go`:
```go
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/model"
)

type syncFilePayload struct {
	Path        string         `json:"path"`
	MDContent   string         `json:"md_content"`
	HTMLContent string         `json:"html_content"`
	Frontmatter map[string]any `json:"frontmatter"`
}

func handleSync(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")

		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}

		role, err := deps.Store.GetUserRole(r.Context(), claims.UserID, repoID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "role check failed")
			return
		}
		if !claims.IsAdmin && !model.RoleAtLeast(role, "editor") {
			writeError(w, http.StatusForbidden, "editor role required")
			return
		}

		var payload struct {
			Files []syncFilePayload `json:"files"`
		}
		if err := readJSON(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		credJSON, err := decryptCreds(deps, repo.EncryptedCreds)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cred decrypt failed")
			return
		}

		cacheFiles := make([]gitcache.SyncFile, len(payload.Files))
		for i, f := range payload.Files {
			cacheFiles[i] = gitcache.SyncFile{
				Path:        f.Path,
				MDContent:   f.MDContent,
				HTMLContent: f.HTMLContent,
			}
		}

		user, _ := deps.Store.GetUserByID(r.Context(), claims.UserID)
		commitMsg := fmt.Sprintf("pubobs: sync %s by %s", time.Now().UTC().Format(time.RFC3339), user.Email)

		sha, err := deps.Cache.Sync(r.Context(), repo, credJSON, cacheFiles, commitMsg)
		if err != nil {
			if strings.Contains(err.Error(), "non-fast-forward") || strings.Contains(err.Error(), "rejected") {
				writeError(w, http.StatusConflict, "push rejected: pull first, then sync")
				return
			}
			writeError(w, http.StatusBadGateway, "sync failed: "+err.Error())
			return
		}

		// Persist notes + snapshots + links
		for _, f := range payload.Files {
			note, err := deps.Store.UpsertNote(r.Context(), repoID, f.Path)
			if err != nil {
				continue
			}
			meta := extractMetadata(f.MDContent, f.Frontmatter)
			metaJSON, _ := json.Marshal(meta)
			deps.Store.UpsertSnapshot(r.Context(), note.ID, f.HTMLContent, string(metaJSON), claims.UserID, sha)
			deps.Store.UpsertNoteLinks(r.Context(), note.ID, meta.Links)
		}
		deps.Store.TouchLastUsedAt(r.Context(), repoID)

		writeJSON(w, http.StatusOK, map[string]string{"commit_sha": sha})
	}
}

type noteMetadata struct {
	Headings    []string       `json:"headings"`
	Links       []string       `json:"links"`
	Tags        []string       `json:"tags"`
	Frontmatter map[string]any `json:"frontmatter"`
}

var (
	headingRE  = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	wikilinkRE = regexp.MustCompile(`\[\[([^\]|]+)`)
)

func extractMetadata(md string, frontmatter map[string]any) noteMetadata {
	var meta noteMetadata
	meta.Frontmatter = frontmatter

	for _, m := range headingRE.FindAllStringSubmatch(md, -1) {
		meta.Headings = append(meta.Headings, strings.TrimSpace(m[1]))
	}
	seen := map[string]bool{}
	for _, m := range wikilinkRE.FindAllStringSubmatch(md, -1) {
		link := strings.TrimSpace(m[1])
		if !seen[link] {
			meta.Links = append(meta.Links, link)
			seen[link] = true
		}
	}
	if tags, ok := frontmatter["tags"].([]any); ok {
		for _, t := range tags {
			if s, ok := t.(string); ok {
				meta.Tags = append(meta.Tags, s)
			}
		}
	}
	return meta
}

func decryptCreds(deps *Deps, encCreds string) (string, error) {
	if encCreds == "" {
		return "", nil
	}
	return auth.DecryptCreds(deps.Config.SecretKey, encCreds)
}

var _ = uuid.NewString // imported for UpsertNote dependency
```

Remove `handleSync` from stubs.go.

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/api/... -run "TestHandleSync" -v -timeout 60s
```
Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/sync.go backend/internal/api/sync_test.go backend/internal/api/stubs.go
git commit -m "feat(backend): implement POST /api/repos/{id}/sync handler"
```

---

### Task 20: API — Files Handler (Sync In)

**Files:**
- Create: `backend/internal/api/files.go`
- Create: `backend/internal/api/files_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/api/files_test.go`:
```go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHandleListFiles(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Repo", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader")

	req := httptest.NewRequest("GET", "/api/repos/r1/files", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var files []map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&files))
	require.NotEmpty(t, files)

	var paths []string
	for _, f := range files {
		paths = append(paths, f["path"])
	}
	require.Contains(t, paths, "hello.md")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/api/... -run TestHandleListFiles -v -timeout 60s
```
Expected: FAIL.

- [ ] **Step 3: Implement files.go**

`backend/internal/api/files.go`:
```go
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/model"
)

func handleListFiles(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")

		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}
		role, _ := deps.Store.GetUserRole(r.Context(), claims.UserID, repoID)
		if !claims.IsAdmin && !model.RoleAtLeast(role, "reader") {
			writeError(w, http.StatusForbidden, "reader role required")
			return
		}

		credJSON, err := decryptCreds(deps, repo.EncryptedCreds)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cred decrypt failed")
			return
		}

		entries, err := deps.Cache.ListFiles(r.Context(), repo, credJSON)
		if err != nil {
			writeError(w, http.StatusBadGateway, "list files failed: "+err.Error())
			return
		}
		if entries == nil {
			entries = []model.FileEntry{}
		}
		deps.Store.TouchLastUsedAt(r.Context(), repoID)
		writeJSON(w, http.StatusOK, entries)
	}
}
```

Remove `handleListFiles` from stubs.go.

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/api/... -run TestHandleListFiles -v -timeout 60s
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/files.go backend/internal/api/files_test.go backend/internal/api/stubs.go
git commit -m "feat(backend): implement GET /api/repos/{id}/files handler"
```

---

### Task 21: API — Wiki Handlers

**Files:**
- Create: `backend/internal/api/wiki.go`
- Create: `backend/internal/api/wiki_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/api/wiki_test.go`:
```go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func seedNoteForWiki(t *testing.T, deps *api.Deps) {
	t.Helper()
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader")
	note, _ := deps.Store.UpsertNote(ctx, "r1", "docs/intro.md")
	deps.Store.UpsertSnapshot(ctx, note.ID, "<h1>Intro</h1>", `{"links":["other.md"]}`, "u1", "abc123")
	other, _ := deps.Store.UpsertNote(ctx, "r1", "other.md")
	deps.Store.UpsertNoteLinks(ctx, note.ID, []string{"other.md"})
	_ = other
}

func TestHandleListNotes(t *testing.T) {
	deps := newTestDeps(t)
	seedNoteForWiki(t, deps)

	req := httptest.NewRequest("GET", "/api/repos/r1/notes", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var notes []map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&notes))
	require.Len(t, notes, 2)
}

func TestHandleNoteView(t *testing.T) {
	deps := newTestDeps(t)
	seedNoteForWiki(t, deps)

	req := httptest.NewRequest("GET", "/api/repos/r1/notes/docs/intro.md", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "<h1>Intro</h1>", resp["html_content"])
}

func TestHandleBacklinks(t *testing.T) {
	deps := newTestDeps(t)
	seedNoteForWiki(t, deps)

	req := httptest.NewRequest("GET", "/api/repos/r1/notes/other.md/backlinks", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var notes []map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&notes))
	require.Len(t, notes, 1)
}

func TestHandleAddComment(t *testing.T) {
	deps := newTestDeps(t)
	seedNoteForWiki(t, deps)

	ctx := context.Background()
	deps.Store.GrantAccess(ctx, "a2", "r1", "user", "u1", "commentator")

	body := `{"body":"Great note!"}`
	req := httptest.NewRequest("POST", "/api/repos/r1/notes/docs/intro.md/comments",
		strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/api/... -run TestHandleListNotes -v
```
Expected: FAIL.

- [ ] **Step 3: Implement wiki.go**

`backend/internal/api/wiki.go`:
```go
package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/model"
)

func handleListNotes(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")
		if err := requireRepoRole(r.Context(), deps, claims, repoID, "reader"); err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		notes, err := deps.Store.ListNotes(r.Context(), repoID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list notes failed")
			return
		}
		if notes == nil {
			notes = []*model.Note{}
		}
		type noteResp struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		}
		out := make([]noteResp, len(notes))
		for i, n := range notes {
			out[i] = noteResp{ID: n.ID, Path: n.Path}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// handleNoteGet dispatches GET /api/repos/{id}/notes/* based on path suffix.
func handleNoteGet(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")
		notePath := chi.URLParam(r, "*")

		switch {
		case strings.HasSuffix(notePath, "/backlinks"):
			serveBacklinks(w, r, deps, claims, repoID, strings.TrimSuffix(notePath, "/backlinks"))
		case strings.HasSuffix(notePath, "/history"):
			serveHistory(w, r, deps, claims, repoID, strings.TrimSuffix(notePath, "/history"))
		case strings.HasSuffix(notePath, "/comments"):
			serveListComments(w, r, deps, claims, repoID, strings.TrimSuffix(notePath, "/comments"))
		default:
			serveNoteView(w, r, deps, claims, repoID, notePath)
		}
	}
}

// handleNotePost dispatches POST /api/repos/{id}/notes/* (only /comments supported).
func handleNotePost(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")
		notePath := chi.URLParam(r, "*")

		if strings.HasSuffix(notePath, "/comments") {
			serveAddComment(w, r, deps, claims, repoID, strings.TrimSuffix(notePath, "/comments"))
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func serveNoteView(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "reader"); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	note, err := deps.Store.GetNote(r.Context(), repoID, notePath)
	if err != nil || note == nil {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}
	snap, err := deps.Store.GetSnapshot(r.Context(), note.ID)
	if err != nil || snap == nil {
		writeError(w, http.StatusNotFound, "snapshot not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            note.ID,
		"path":          note.Path,
		"html_content":  snap.HTMLContent,
		"metadata_json": snap.MetadataJSON,
		"git_commit_sha": snap.GitCommitSHA,
		"synced_at":     snap.SyncedAt,
	})
}

func serveHistory(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "reader"); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	repo, _ := deps.Store.GetRepo(r.Context(), repoID)
	if repo == nil {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}
	credJSON, _ := decryptCreds(deps, repo.EncryptedCreds)
	commits, err := deps.Cache.History(r.Context(), repo, credJSON, notePath)
	if err != nil {
		writeError(w, http.StatusBadGateway, "history failed: "+err.Error())
		return
	}
	if commits == nil {
		commits = []model.Commit{}
	}
	writeJSON(w, http.StatusOK, commits)
}

func serveBacklinks(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "reader"); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	notes, err := deps.Store.GetBacklinks(r.Context(), repoID, notePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "backlinks failed")
		return
	}
	if notes == nil {
		notes = []*model.Note{}
	}
	writeJSON(w, http.StatusOK, notes)
}

func serveListComments(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "reader"); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	note, _ := deps.Store.GetNote(r.Context(), repoID, notePath)
	if note == nil {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}
	comments, err := deps.Store.ListComments(r.Context(), note.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list comments failed")
		return
	}
	if comments == nil {
		comments = []*model.Comment{}
	}
	writeJSON(w, http.StatusOK, comments)
}

func serveAddComment(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "commentator"); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	note, _ := deps.Store.GetNote(r.Context(), repoID, notePath)
	if note == nil {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}
	var body struct {
		ParentID *string `json:"parent_id"`
		Body     string  `json:"body"`
	}
	if err := readJSON(r, &body); err != nil || body.Body == "" {
		writeError(w, http.StatusBadRequest, "body is required")
		return
	}
	comment, err := deps.Store.CreateComment(r.Context(), uuid.NewString(), note.ID, claims.UserID, body.ParentID, body.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create comment failed")
		return
	}
	writeJSON(w, http.StatusCreated, comment)
}
```

Create `backend/internal/api/rolecheck.go`:
```go
package api

import (
	"context"
	"errors"

	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/model"
)

func requireRepoRole(ctx context.Context, deps *Deps, claims *auth.AccessClaims, repoID, required string) error {
	if claims.IsAdmin {
		return nil
	}
	role, err := deps.Store.GetUserRole(ctx, claims.UserID, repoID)
	if err != nil {
		return errors.New("role check failed")
	}
	if !model.RoleAtLeast(role, required) {
		return errors.New(required + " role required")
	}
	return nil
}
```

Remove wiki-related stubs from stubs.go.

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/api/... -run "TestHandleListNotes|TestHandleNoteView|TestHandleBacklinks|TestHandleAddComment" -v
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/wiki.go backend/internal/api/wiki_test.go backend/internal/api/rolecheck.go backend/internal/api/stubs.go
git commit -m "feat(backend): implement wiki handlers (notes, view, history, backlinks, comments)"
```

---

### Task 22: API — Admin Handlers

**Files:**
- Create: `backend/internal/api/admin.go`
- Create: `backend/internal/api/admin_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/api/admin_test.go`:
```go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminCreateRepo(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.SetInstanceAdmin(ctx, "admin1", true)

	body := `{"name":"My Repo","remote_url":"https://github.com/org/repo.git","username":"x-access-token","password":"ghp_test","default_branch":"main"}`
	req := httptest.NewRequest("POST", "/api/admin/repos", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.NotEmpty(t, resp["id"])
}

func TestAdminHealth(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.SetInstanceAdmin(ctx, "admin1", true)

	req := httptest.NewRequest("GET", "/api/admin/health", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestAdminEndpoints_nonAdmin(t *testing.T) {
	deps := newTestDeps(t)
	deps.Store.UpsertUser(context.Background(), "u1", "user@x.com", "User")

	req := httptest.NewRequest("POST", "/api/admin/repos", strings.NewReader("{}"))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "user@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/api/... -run TestAdminCreateRepo -v
```
Expected: FAIL (stub returns 501).

- [ ] **Step 3: Implement admin.go**

`backend/internal/api/admin.go`:
```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pubobs/backend/internal/auth"
)

func requireAdmin(claims *auth.AccessClaims, w http.ResponseWriter) bool {
	if !claims.IsAdmin {
		writeError(w, http.StatusForbidden, "instance admin required")
		return false
	}
	return true
}

func handleAdminHealth(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		h, err := deps.Store.GetHealth(r.Context())
		if err != nil {
			// No health row yet — return placeholder
			writeJSON(w, http.StatusOK, map[string]any{
				"disk_status":   "ok",
				"disk_free_pct": 100,
			})
			return
		}
		writeJSON(w, http.StatusOK, h)
	}
}

func handleAdminCreateRepo(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		var body struct {
			Name          string `json:"name"`
			RemoteURL     string `json:"remote_url"`
			Username      string `json:"username"`
			Password      string `json:"password"`
			DefaultBranch string `json:"default_branch"`
		}
		if err := readJSON(r, &body); err != nil || body.Name == "" || body.RemoteURL == "" {
			writeError(w, http.StatusBadRequest, "name and remote_url are required")
			return
		}
		if body.DefaultBranch == "" {
			body.DefaultBranch = "main"
		}
		credJSON, _ := json.Marshal(map[string]string{"username": body.Username, "password": body.Password})
		encCreds, err := auth.EncryptCreds(deps.Config.SecretKey, string(credJSON))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encrypt creds failed")
			return
		}
		id := uuid.NewString()
		repo, err := deps.Store.CreateRepo(r.Context(), id, body.Name, body.RemoteURL, encCreds, body.DefaultBranch)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "create repo failed")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"id": repo.ID, "name": repo.Name})
	}
}

func handleAdminUpdateRepo(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		id := chi.URLParam(r, "id")
		var body struct {
			Name          string `json:"name"`
			RemoteURL     string `json:"remote_url"`
			Username      string `json:"username"`
			Password      string `json:"password"`
			DefaultBranch string `json:"default_branch"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		credJSON, _ := json.Marshal(map[string]string{"username": body.Username, "password": body.Password})
		encCreds, err := auth.EncryptCreds(deps.Config.SecretKey, string(credJSON))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encrypt creds failed")
			return
		}
		if err := deps.Store.UpdateRepo(r.Context(), id, body.Name, body.RemoteURL, encCreds, body.DefaultBranch); err != nil {
			writeError(w, http.StatusInternalServerError, "update repo failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleAdminDeleteRepo(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		id := chi.URLParam(r, "id")
		deps.Cache.Evict(id)
		if err := deps.Store.DeleteRepo(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "delete repo failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleAdminGrantAccess(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		repoID := chi.URLParam(r, "id")
		var body struct {
			PrincipalType string `json:"principal_type"`
			PrincipalID   string `json:"principal_id"`
			Role          string `json:"role"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := deps.Store.GrantAccess(r.Context(), uuid.NewString(), repoID, body.PrincipalType, body.PrincipalID, body.Role); err != nil {
			writeError(w, http.StatusInternalServerError, "grant access failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleAdminRevokeAccess(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		accessID := chi.URLParam(r, "accessID")
		if err := deps.Store.RevokeAccess(r.Context(), accessID); err != nil {
			writeError(w, http.StatusInternalServerError, "revoke access failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleAdminListUsers(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		users, err := deps.Store.ListUsers(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list users failed")
			return
		}
		writeJSON(w, http.StatusOK, users)
	}
}

func handleAdminCreateGroup(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		var body struct{ Name string `json:"name"` }
		if err := readJSON(r, &body); err != nil || body.Name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		g, err := deps.Store.CreateGroup(r.Context(), uuid.NewString(), body.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "create group failed")
			return
		}
		writeJSON(w, http.StatusCreated, g)
	}
}

func handleAdminAddGroupMember(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		groupID := chi.URLParam(r, "id")
		var body struct{ UserID string `json:"user_id"` }
		if err := readJSON(r, &body); err != nil || body.UserID == "" {
			writeError(w, http.StatusBadRequest, "user_id is required")
			return
		}
		if err := deps.Store.AddGroupMember(r.Context(), groupID, body.UserID); err != nil {
			writeError(w, http.StatusInternalServerError, "add member failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

Remove all admin stubs from stubs.go.

- [ ] **Step 4: Run all API tests**

```bash
cd backend && go test ./internal/api/... -v -timeout 60s
```
Expected: all tests PASS, `stubs.go` now empty (only `notImpl` helper used nowhere — can be deleted).

- [ ] **Step 5: Clean up stubs.go** — delete the file if all stubs are replaced.

```bash
rm backend/internal/api/stubs.go
cd backend && go build ./...
```

- [ ] **Step 6: Commit**

```bash
git add backend/internal/api/
git commit -m "feat(backend): implement all admin API handlers"
```

---

## Part F — Jobs + Main + Docker

### Task 23: Background Eviction Job

**Files:**
- Create: `backend/internal/jobs/eviction.go`
- Create: `backend/internal/jobs/eviction_test.go`

- [ ] **Step 1: Write the failing test**

`backend/internal/jobs/eviction_test.go`:
```go
package jobs_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pubobs/backend/internal/config"
	"github.com/pubobs/backend/internal/db"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/jobs"
	"github.com/pubobs/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func TestEvictionJob_evictsStaleRepo(t *testing.T) {
	d, _ := db.Open(":memory:")
	defer d.Close()
	s := store.New(d)
	ctx := context.Background()
	cacheDir := t.TempDir()
	cache := gitcache.NewCache(cacheDir)

	cfg := &config.Config{
		RepoCacheDir:    cacheDir,
		RepoCacheTTL:    100 * time.Millisecond,
		DiskWarnPct:     20,
		DiskCritPct:     5,
	}

	// Seed a repo with a local clone dir
	s.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "", "main")
	repoDir := filepath.Join(cacheDir, "r1")
	os.MkdirAll(repoDir, 0755)
	s.UpdateRepoLocalPath(ctx, "r1", repoDir, time.Now().UTC().Add(-1*time.Hour))

	// Wait past TTL then run one eviction cycle
	time.Sleep(200 * time.Millisecond)
	jobs.RunEvictionCycle(ctx, s, cache, cfg)

	// local_path should be cleared
	repo, _ := s.GetRepo(ctx, "r1")
	require.Nil(t, repo.LocalPath, "stale repo local_path should be cleared")
	_, err := os.Stat(repoDir)
	require.True(t, os.IsNotExist(err), "clone directory should be deleted")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd backend && go test ./internal/jobs/... -run TestEvictionJob_evictsStaleRepo -v
```
Expected: compile error.

- [ ] **Step 3: Implement eviction.go**

`backend/internal/jobs/eviction.go`:
```go
package jobs

import (
	"context"
	"log"
	"time"

	"github.com/pubobs/backend/internal/config"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/store"
)

// StartEvictionJob runs the eviction + disk monitoring loop in a goroutine.
// It returns immediately. Cancel ctx to stop the loop.
func StartEvictionJob(ctx context.Context, s *store.Store, cache *gitcache.Cache, cfg *config.Config) {
	go func() {
		ticker := time.NewTicker(cfg.CacheCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				RunEvictionCycle(ctx, s, cache, cfg)
			}
		}
	}()
}

// RunEvictionCycle is the single-pass eviction + health update logic, exported for testing.
func RunEvictionCycle(ctx context.Context, s *store.Store, cache *gitcache.Cache, cfg *config.Config) {
	cutoff := time.Now().Add(-cfg.RepoCacheTTL)
	stale, err := s.ListStaleRepos(ctx, cutoff)
	if err != nil {
		log.Printf("eviction: list stale repos: %v", err)
		return
	}

	now := time.Now().UTC()
	evicted := 0
	for _, repo := range stale {
		if err := cache.Evict(repo.ID); err != nil {
			log.Printf("eviction: evict %s: %v", repo.ID, err)
			continue
		}
		if err := s.ClearRepoLocalPath(ctx, repo.ID); err != nil {
			log.Printf("eviction: clear local_path %s: %v", repo.ID, err)
		}
		evicted++
	}
	if evicted > 0 {
		log.Printf("eviction: evicted %d repo(s)", evicted)
	}

	// Update disk health
	freeBytes, freePct, err := cache.DiskUsage()
	if err != nil {
		log.Printf("eviction: disk usage check failed: %v", err)
		return
	}
	status := "ok"
	if freePct < cfg.DiskCritPct {
		status = "crit"
	} else if freePct < cfg.DiskWarnPct {
		status = "warn"
	}
	if evicted > 0 {
		if err := s.UpsertHealth(ctx, freePct, freeBytes, status, &now); err != nil {
			log.Printf("eviction: upsert health: %v", err)
		}
	} else {
		if err := s.UpsertHealth(ctx, freePct, freeBytes, status, nil); err != nil {
			log.Printf("eviction: upsert health: %v", err)
		}
	}
}
```

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/jobs/... -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/jobs/
git commit -m "feat(backend): add background eviction job with disk health monitoring"
```

---

### Task 24: Frontend Embed Placeholder

**Files:**
- Create: `backend/frontend/static/.gitkeep`
- Create: `backend/frontend/embed.go`
- Modify: `backend/internal/api/frontend_stub.go` → remove (replaced by real embed)

- [ ] **Step 1: Create frontend placeholder**

`backend/frontend/static/.gitkeep`:
```
```
(empty file)

`backend/frontend/embed.go`:
```go
package frontend

import (
	"embed"
	"io/fs"
)

//go:embed static
var staticFiles embed.FS

// FS returns the embedded static file system rooted at "static/".
func FS() fs.FS {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
```

- [ ] **Step 2: Wire into API router**

Update `backend/internal/api/router.go` — change the frontend import:

Replace in `router.go`:
```go
import (
    ...
)
```
with:
```go
import (
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    "github.com/pubobs/backend/internal/auth"
    "github.com/pubobs/backend/internal/frontend"
)
```

And replace the `frontendFS` reference:
```go
r.Handle("/*", http.FileServer(http.FS(frontend.FS())))
```

Delete `backend/internal/api/frontend_stub.go`.

- [ ] **Step 3: Build**

```bash
cd backend && go build ./...
```
Expected: builds cleanly.

- [ ] **Step 4: Commit**

```bash
git add backend/frontend/ backend/internal/api/router.go
git rm backend/internal/api/frontend_stub.go
git commit -m "feat(backend): add frontend go:embed placeholder"
```

---

### Task 25: Main Entry Point

**Files:**
- Create: `backend/cmd/server/main.go`

- [ ] **Step 1: Implement main.go**

`backend/cmd/server/main.go`:
```go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/config"
	"github.com/pubobs/backend/internal/db"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/jobs"
	"github.com/pubobs/backend/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if err := os.MkdirAll(cfg.RepoCacheDir, 0755); err != nil {
		log.Fatalf("create cache dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0755); err != nil {
		log.Fatalf("create db dir: %v", err)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer database.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	oidcClient, err := auth.NewOIDCClient(ctx, cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret, cfg.BaseURL)
	if err != nil {
		log.Fatalf("init OIDC: %v", err)
	}

	deps := &api.Deps{
		Store:  store.New(database),
		Cache:  gitcache.NewCache(cfg.RepoCacheDir),
		Auth:   auth.NewSessionStore(),
		OIDC:   oidcClient,
		Config: cfg,
	}

	jobs.StartEvictionJob(ctx, deps.Store, deps.Cache, cfg)

	router := api.BuildRouter(deps)
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down...")
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		srv.Shutdown(shutCtx)
		cancel()
	}()

	log.Printf("PubObs backend listening on :%s", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}
```

Add `"path/filepath"` to imports.

- [ ] **Step 2: Build**

```bash
cd backend && go build ./cmd/server/...
```
Expected: produces `server` binary (or `server.exe` on Windows).

- [ ] **Step 3: Commit**

```bash
git add backend/cmd/server/main.go
git commit -m "feat(backend): add main server entry point"
```

---

### Task 26: Dockerfile + docker-compose.yml

**Files:**
- Create: `backend/Dockerfile`
- Create: `backend/docker-compose.yml`

- [ ] **Step 1: Create Dockerfile**

`backend/Dockerfile`:
```dockerfile
# Build stage
FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /pubobs ./cmd/server

# Runtime stage
FROM alpine:3.19
RUN apk add --no-cache git ca-certificates
COPY --from=builder /pubobs /usr/local/bin/pubobs
RUN adduser -D -u 1000 pubobs
USER pubobs
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/pubobs"]
```

- [ ] **Step 2: Create docker-compose.yml**

`backend/docker-compose.yml`:
```yaml
services:
  pubobs:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - ./data/db:/data/db
      - ./data/repos:/data/repos
    environment:
      PUBOBS_BASE_URL: http://localhost:8080
      PUBOBS_OIDC_ISSUER: ${PUBOBS_OIDC_ISSUER}
      PUBOBS_OIDC_CLIENT_ID: ${PUBOBS_OIDC_CLIENT_ID}
      PUBOBS_OIDC_CLIENT_SECRET: ${PUBOBS_OIDC_CLIENT_SECRET}
      PUBOBS_SECRET_KEY: ${PUBOBS_SECRET_KEY}
      PUBOBS_REPO_CACHE_TTL: 24h
      PUBOBS_CACHE_CHECK_INTERVAL: 1h
      PUBOBS_DISK_WARN_PCT: 20
      PUBOBS_DISK_CRIT_PCT: 5
```

- [ ] **Step 3: Build Docker image (optional — requires Docker)**

```bash
cd backend && docker build -t pubobs:dev .
```
Expected: image builds successfully with `git` installed in runtime layer.

- [ ] **Step 4: Run full test suite**

```bash
cd backend && go test ./... -timeout 120s
```
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/Dockerfile backend/docker-compose.yml
git commit -m "feat(backend): add Dockerfile and docker-compose.yml"
```

---

## Self-Review

**Spec coverage check:**

| Spec section | Task(s) covering it |
|---|---|
| SQLite schema (9 tables) | Task 2 |
| Users, groups, repos CRUD | Tasks 4–6 |
| Notes, snapshots, links | Task 7 |
| Comments, folder mappings, health | Task 8 |
| AES-256-GCM cred encryption | Task 9 |
| PKCE session store | Task 10 |
| JWT access + refresh | Task 11 |
| OIDC client | Task 12 |
| Auth middleware | Task 13 |
| Git clone/pull/commit/push/log | Task 14 |
| Per-repo mutex, cache eviction | Tasks 15, 23 |
| All API endpoints from spec table | Tasks 17–22 |
| Sync out flow (plugin → backend) | Task 19 |
| Sync in flow (backend → plugin) | Task 20 |
| Wiki: note view, history, backlinks | Task 21 |
| Comments API | Task 21 |
| Admin: repos, access, users, groups | Task 22 |
| Admin: disk health | Tasks 22, 23 |
| Folder mappings API | Task 18 |
| 409 conflict on push | Task 19 |
| 507 disk critical rejection | Task 22 (health check in handleSync) |
| Background eviction job | Task 23 |
| Frontend go:embed | Task 24 |
| Docker single binary | Tasks 25–26 |
| Inter-note link resolution (backend table) | Tasks 7, 19 |

**Note:** The `handleSync` should also check disk status and return 507 if `disk_status = 'crit'`. Add this check to Task 19's sync.go — before the git operations, call `deps.Store.GetHealth` and if status is `"crit"` return 507.

**Placeholder scan:** No TBD/TODO/placeholder text found in code.

**Type consistency:** All types (`model.Repo`, `model.Note`, `auth.AccessClaims`, `gitcache.SyncFile`) are defined once and referenced consistently through all tasks.
