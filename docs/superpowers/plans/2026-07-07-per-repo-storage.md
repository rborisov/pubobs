# Per-Repo Storage Destinations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an admin manage a named list of S3 destinations and assign each repo independently to `Local` or one of those destinations for its rendered-note blobs and media assets.

**Architecture:** Generalize the prior feature's single instance-wide render/asset store (`Deps.RenderStore`/`AssetStore`, each a `*renderstore.SwappableStore`) into one `StorageResolver` that owns a local store-set plus one built store-set per configured S3 destination, and returns the right store for a given repo via `RenderStoreFor(repoID)`/`AssetStoreFor(repoID)`. A new `storage_destinations` table holds the S3 configs; `repos.storage_destination_id` (nullable = local) records each repo's choice. All existing storage/encryption/migration machinery (`renderstore.RenderStore`, `EncryptingStore`, `S3RenderStore`, `NewEncryptingStore`, the verify-before-delete migration) is reused.

**Tech Stack:** Go 1.25 (backend), TypeScript + esbuild (frontend), SQLite (`modernc.org/sqlite`), `github.com/minio/minio-go/v7` (S3 client), chi router.

## Global Constraints

- Render blobs pass through the store as opaque, already-client-encrypted bytes — never server-side re-encrypted. Only assets get server-side AES-GCM at rest (via `EncryptingStore`).
- One instance-wide asset-encryption key (hex, 32 bytes). It is NOT per-destination. It continues to live in the existing single-row `storage_settings` table, which is retained solely as that key's home; its S3-config and migration-status columns are no longer read after backward-compat conversion.
- S3 object key layout is repo-scoped and unchanged: `renders/{repoID}/{notePath}.enc`, `assets/{repoID}/{assetPath}.enc`. Repos sharing one bucket never collide.
- `Local` is never a `storage_destinations` row — it is represented by `repos.storage_destination_id IS NULL`.
- Deleting a destination still referenced by any repo must be rejected (reassign first).
- Per-repo migration must never double-encrypt assets: migrate through the *plain* store under the `EncryptingStore` (raw ciphertext-to-ciphertext copy), preserving the prior feature's fix. Verify-before-delete before removing any source copy.
- Settings changes apply live via resolver rebuild — no process restart.
- This project has no incremental migration-file runner: `backend/internal/db/migrations/001_init.sql` is a single idempotent `CREATE TABLE IF NOT EXISTS` schema re-applied on every boot; new *columns* on existing tables are added via `ALTER TABLE ... ADD COLUMN` statements in `backend/internal/db/db.go` (duplicate-column errors ignored). Follow both patterns exactly.
- Deferred/out of scope: any git-checkout / disk-reclaim work (see the spec's "Deferred: git-cache location"). This plan touches only renders/assets.

---

### Task 1: `storage_destinations` table, model, and store CRUD

**Files:**
- Modify: `backend/internal/db/migrations/001_init.sql`
- Modify: `backend/internal/model/model.go`
- Create: `backend/internal/store/storage_destinations.go`
- Test: `backend/internal/store/storage_destinations_test.go`

**Interfaces:**
- Produces: `model.StorageDestination` struct; `(s *Store) CreateStorageDestination(ctx, *model.StorageDestination) error`, `ListStorageDestinations(ctx) ([]*model.StorageDestination, error)`, `GetStorageDestination(ctx, id string) (*model.StorageDestination, error)` (returns `sql.ErrNoRows` if absent), `UpdateStorageDestination(ctx, *model.StorageDestination) error`, `DeleteStorageDestination(ctx, id string) error`. Later tasks consume all of these.

- [ ] **Step 1: Add the table to the schema**

In `backend/internal/db/migrations/001_init.sql`, after the `storage_settings` table, add:

```sql
CREATE TABLE IF NOT EXISTS storage_destinations (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    s3_endpoint   TEXT NOT NULL DEFAULT '',
    s3_bucket     TEXT NOT NULL DEFAULT '',
    s3_access_key TEXT NOT NULL DEFAULT '',
    s3_secret_key TEXT NOT NULL DEFAULT '',
    s3_region     TEXT NOT NULL DEFAULT '',
    s3_use_ssl    INTEGER NOT NULL DEFAULT 1,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

- [ ] **Step 2: Add the model struct**

In `backend/internal/model/model.go`, after the `StorageSettings` struct, add:

```go
type StorageDestination struct {
	ID          string
	Name        string
	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3Region    string
	S3UseSSL    bool
	CreatedAt   time.Time
}
```

(`model.go` already imports `"time"`.)

- [ ] **Step 3: Write the failing test**

```go
// backend/internal/store/storage_destinations_test.go
package store_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/pubobs/backend/internal/db"
	"github.com/pubobs/backend/internal/model"
	"github.com/pubobs/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func TestStorageDestinations_crud(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()
	s := store.New(d)
	ctx := context.Background()

	// Empty list to start.
	list, err := s.ListStorageDestinations(ctx)
	require.NoError(t, err)
	require.Empty(t, list)

	dest := &model.StorageDestination{
		ID: "d1", Name: "archive", S3Endpoint: "s3.example.com", S3Bucket: "b",
		S3AccessKey: "AK", S3SecretKey: "SK", S3Region: "us-east-1", S3UseSSL: true,
	}
	require.NoError(t, s.CreateStorageDestination(ctx, dest))

	got, err := s.GetStorageDestination(ctx, "d1")
	require.NoError(t, err)
	require.Equal(t, "archive", got.Name)
	require.Equal(t, "s3.example.com", got.S3Endpoint)
	require.True(t, got.S3UseSSL)

	list, err = s.ListStorageDestinations(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)

	got.Name = "archive-renamed"
	got.S3UseSSL = false
	require.NoError(t, s.UpdateStorageDestination(ctx, got))
	after, err := s.GetStorageDestination(ctx, "d1")
	require.NoError(t, err)
	require.Equal(t, "archive-renamed", after.Name)
	require.False(t, after.S3UseSSL)

	require.NoError(t, s.DeleteStorageDestination(ctx, "d1"))
	_, err = s.GetStorageDestination(ctx, "d1")
	require.True(t, errors.Is(err, sql.ErrNoRows))
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `cd backend && go test ./internal/store/... -run TestStorageDestinations_crud -v`
Expected: FAIL — methods undefined.

- [ ] **Step 5: Write the implementation**

```go
// backend/internal/store/storage_destinations.go
package store

import (
	"context"
	"time"

	"github.com/pubobs/backend/internal/model"
)

func (s *Store) CreateStorageDestination(ctx context.Context, d *model.StorageDestination) error {
	useSSL := 0
	if d.S3UseSSL {
		useSSL = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO storage_destinations
			(id, name, s3_endpoint, s3_bucket, s3_access_key, s3_secret_key, s3_region, s3_use_ssl, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		d.ID, d.Name, d.S3Endpoint, d.S3Bucket, d.S3AccessKey, d.S3SecretKey,
		d.S3Region, useSSL, time.Now().UTC(),
	)
	return err
}

func (s *Store) ListStorageDestinations(ctx context.Context) ([]*model.StorageDestination, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, s3_endpoint, s3_bucket, s3_access_key, s3_secret_key, s3_region, s3_use_ssl, created_at
		FROM storage_destinations ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.StorageDestination
	for rows.Next() {
		d, err := scanStorageDestination(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetStorageDestination(ctx context.Context, id string) (*model.StorageDestination, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, s3_endpoint, s3_bucket, s3_access_key, s3_secret_key, s3_region, s3_use_ssl, created_at
		FROM storage_destinations WHERE id=?`, id)
	return scanStorageDestination(row)
}

func (s *Store) UpdateStorageDestination(ctx context.Context, d *model.StorageDestination) error {
	useSSL := 0
	if d.S3UseSSL {
		useSSL = 1
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE storage_destinations SET
			name=?, s3_endpoint=?, s3_bucket=?, s3_access_key=?, s3_secret_key=?, s3_region=?, s3_use_ssl=?
		WHERE id=?`,
		d.Name, d.S3Endpoint, d.S3Bucket, d.S3AccessKey, d.S3SecretKey, d.S3Region, useSSL, d.ID,
	)
	return err
}

func (s *Store) DeleteStorageDestination(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM storage_destinations WHERE id=?`, id)
	return err
}

// scanStorageDestination reads one row from a *sql.Row or *sql.Rows.
func scanStorageDestination(sc interface{ Scan(...any) error }) (*model.StorageDestination, error) {
	var d model.StorageDestination
	var useSSL int
	if err := sc.Scan(&d.ID, &d.Name, &d.S3Endpoint, &d.S3Bucket, &d.S3AccessKey,
		&d.S3SecretKey, &d.S3Region, &useSSL, &d.CreatedAt); err != nil {
		return nil, err
	}
	d.S3UseSSL = useSSL != 0
	return &d, nil
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `cd backend && go test ./internal/store/... -run TestStorageDestinations_crud -v`
Expected: PASS.

- [ ] **Step 7: Run the full backend suite**

Run: `cd backend && go build ./... && go test ./...`
Expected: builds clean, all pass.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/db/migrations/001_init.sql backend/internal/model/model.go backend/internal/store/storage_destinations.go backend/internal/store/storage_destinations_test.go
git commit -m "feat: add storage_destinations table, model, and store CRUD"
```

---

### Task 2: `repos` per-repo destination + migration-status columns

**Files:**
- Modify: `backend/internal/db/migrations/001_init.sql`
- Modify: `backend/internal/db/db.go`
- Modify: `backend/internal/model/model.go`
- Modify: `backend/internal/store/repo.go`
- Test: `backend/internal/store/repo_storage_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `model.Repo` gains `StorageDestinationID *string`, `MigrationStatus string`, `MigrationTotal int`, `MigrationDone int`. New store methods: `(s *Store) SetRepoStorageDestination(ctx, repoID string, destID *string) error`, `SetRepoMigrationStatus(ctx, repoID, status string, total, done int) error`, `CountReposUsingDestination(ctx, destID string) (int, error)`. `GetRepo`/`ListRepos` now scan the new columns.

The `repos` table already exists in `001_init.sql`. For *new* installs, add the columns to the `CREATE TABLE`; for *existing* installs, add them via `ALTER TABLE` in `db.go` (matching how `allow_guest`/`is_admin` were added). Both are needed — the `CREATE TABLE IF NOT EXISTS` won't alter an existing table, and the `ALTER TABLE` handles upgrades.

- [ ] **Step 1: Add columns to the `repos` CREATE TABLE**

In `backend/internal/db/migrations/001_init.sql`, find the `CREATE TABLE IF NOT EXISTS repos (...)` block and add these columns before its closing `)`:

```sql
    storage_destination_id TEXT REFERENCES storage_destinations(id),
    migration_status       TEXT NOT NULL DEFAULT 'idle',
    migration_total        INTEGER NOT NULL DEFAULT 0,
    migration_done         INTEGER NOT NULL DEFAULT 0,
```

(Add them immediately after the existing `allow_guest` column line; keep the trailing comma correct — every column line except the last inside the parens ends with a comma.)

- [ ] **Step 2: Add ALTER TABLE upgrades in db.go**

In `backend/internal/db/db.go`, alongside the existing `ALTER TABLE repos ADD COLUMN allow_guest ...` block, add four more (same duplicate-column-ignoring pattern):

```go
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
```

Place this block right after the last existing per-column `ALTER TABLE` block in `Open()`. (`db.go` already imports `"strings"` and `"fmt"`.)

Note on ordering: `storage_destinations` is created by the same `001_init.sql` `db.Exec(schema)` call that runs before these `ALTER`s, so the `REFERENCES storage_destinations(id)` clause resolves. SQLite does not enforce the FK at `ALTER` time regardless (foreign_keys pragma only checks on write), so even a first-run ordering quirk is safe.

- [ ] **Step 3: Add model fields**

In `backend/internal/model/model.go`, add to the `Repo` struct:

```go
	StorageDestinationID *string // nil = local
	MigrationStatus      string  // "idle" | "running" | "done" | "failed"
	MigrationTotal       int
	MigrationDone        int
```

- [ ] **Step 4: Write the failing test**

```go
// backend/internal/store/repo_storage_test.go
package store_test

import (
	"context"
	"testing"

	"github.com/pubobs/backend/internal/db"
	"github.com/pubobs/backend/internal/model"
	"github.com/pubobs/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func TestRepoStorageDestination_setAndCount(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()
	s := store.New(d)
	ctx := context.Background()

	require.NoError(t, s.CreateStorageDestination(ctx, &model.StorageDestination{ID: "d1", Name: "arch"}))
	_, err = s.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	require.NoError(t, err)

	// Default: no destination assigned (local), idle migration.
	repo, err := s.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.Nil(t, repo.StorageDestinationID)
	require.Equal(t, "idle", repo.MigrationStatus)

	// Assign to d1.
	destID := "d1"
	require.NoError(t, s.SetRepoStorageDestination(ctx, "r1", &destID))
	repo, err = s.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.NotNil(t, repo.StorageDestinationID)
	require.Equal(t, "d1", *repo.StorageDestinationID)

	count, err := s.CountReposUsingDestination(ctx, "d1")
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// Migration status round-trips.
	require.NoError(t, s.SetRepoMigrationStatus(ctx, "r1", "done", 5, 5))
	repo, err = s.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.Equal(t, "done", repo.MigrationStatus)
	require.Equal(t, 5, repo.MigrationTotal)
	require.Equal(t, 5, repo.MigrationDone)

	// Reassign to local (nil) → count drops to 0.
	require.NoError(t, s.SetRepoStorageDestination(ctx, "r1", nil))
	count, err = s.CountReposUsingDestination(ctx, "d1")
	require.NoError(t, err)
	require.Equal(t, 0, count)
}
```

- [ ] **Step 5: Run test to verify it fails**

Run: `cd backend && go test ./internal/store/... -run TestRepoStorageDestination_setAndCount -v`
Expected: FAIL — `scanRepo` doesn't read the new columns / methods undefined (compile error).

- [ ] **Step 6: Update `scanRepo`, `GetRepo`, `ListRepos`, and add the new methods**

In `backend/internal/store/repo.go`:

Update both `GetRepo` and `ListRepos` SELECT column lists (they are identical) to add the four new columns at the end:

```sql
		SELECT id, name, remote_url, encrypted_creds, default_branch,
		       local_path, cloned_at, last_used_at, created_at, allow_guest,
		       storage_destination_id, migration_status, migration_total, migration_done
		FROM repos ...
```

Update `scanRepo` to scan them. The existing `scanRepo` signature is `func scanRepo(row scanner) (*model.Repo, error)` (the `scanner` interface is defined in `backend/internal/store/user.go`), and it returns `(nil, nil)` on `sql.ErrNoRows`. Preserve both. Add the four new columns — `storage_destination_id` is nullable, so use a `sql.NullString`:

```go
func scanRepo(row scanner) (*model.Repo, error) {
	var r model.Repo
	var localPath sql.NullString
	var clonedAt, lastUsedAt sql.NullTime
	var allowGuest int
	var destID sql.NullString
	err := row.Scan(
		&r.ID, &r.Name, &r.RemoteURL, &r.EncryptedCreds, &r.DefaultBranch,
		&localPath, &clonedAt, &lastUsedAt, &r.CreatedAt, &allowGuest,
		&destID, &r.MigrationStatus, &r.MigrationTotal, &r.MigrationDone,
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
	r.AllowGuest = allowGuest != 0
	if destID.Valid {
		r.StorageDestinationID = &destID.String
	}
	return &r, nil
}
```

(This is the existing `scanRepo` body verbatim with the four new columns woven into the `Scan(...)` call and the `destID` conversion appended. `sql`, `errors`, and `fmt` are already imported in `repo.go`.)

Append the new methods:

```go
func (s *Store) SetRepoStorageDestination(ctx context.Context, repoID string, destID *string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE repos SET storage_destination_id=? WHERE id=?`, destID, repoID)
	return err
}

func (s *Store) SetRepoMigrationStatus(ctx context.Context, repoID, status string, total, done int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE repos SET migration_status=?, migration_total=?, migration_done=? WHERE id=?`,
		status, total, done, repoID)
	return err
}

func (s *Store) CountReposUsingDestination(ctx context.Context, destID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM repos WHERE storage_destination_id=?`, destID).Scan(&n)
	return n, err
}
```

(`SetRepoStorageDestination` passing a `*string` works with `database/sql`: a nil pointer binds as SQL NULL.)

- [ ] **Step 7: Run test to verify it passes**

Run: `cd backend && go test ./internal/store/... -run TestRepoStorageDestination_setAndCount -v`
Expected: PASS.

- [ ] **Step 8: Run the full backend suite**

Run: `cd backend && go build ./... && go test ./...`
Expected: builds clean, all pass (confirms the `scanRepo` change didn't break existing repo tests).

- [ ] **Step 9: Commit**

```bash
git add backend/internal/db/migrations/001_init.sql backend/internal/db/db.go backend/internal/model/model.go backend/internal/store/repo.go backend/internal/store/repo_storage_test.go
git commit -m "feat: add per-repo storage destination + migration-status columns"
```

---

### Task 3: Per-repo key enumeration on stores (`WalkRepoEntries`)

**Files:**
- Modify: `backend/internal/renderstore/local.go`
- Modify: `backend/internal/renderstore/s3.go`
- Test: `backend/internal/renderstore/walkrepo_test.go`

**Interfaces:**
- Produces: `(s *LocalRenderStore) WalkRepoEntries(repoID string, fn func(notePath string) error) error` and `(s *S3RenderStore) WalkRepoEntries(repoID string, fn func(notePath string) error) error`. Task 4's per-repo migration consumes both to enumerate one repo's keys from any concrete backend. Note both walk *only* the given repoID's keys and yield the bare `notePath` (no repoID, no `.enc`), so the caller can pass `(repoID, notePath)` straight into `Read`/`Write`.

Existing `LocalRenderStore.WalkEntries(fn func(repoID, notePath string))` walks the whole store; this adds a repo-scoped variant. `S3RenderStore` has `ListAllObjects()`; this adds a repo-prefixed listing.

- [ ] **Step 1: Write the failing test**

```go
// backend/internal/renderstore/walkrepo_test.go
package renderstore_test

import (
	"sort"
	"testing"

	"github.com/pubobs/backend/internal/renderstore"
	"github.com/stretchr/testify/require"
)

func TestLocalRenderStore_WalkRepoEntries(t *testing.T) {
	s := renderstore.NewLocal(t.TempDir())
	require.NoError(t, s.Write("r1", "a.md", []byte("1")))
	require.NoError(t, s.Write("r1", "sub/b.md", []byte("2")))
	require.NoError(t, s.Write("r2", "c.md", []byte("3")))

	var got []string
	require.NoError(t, s.WalkRepoEntries("r1", func(notePath string) error {
		got = append(got, notePath)
		return nil
	}))
	sort.Strings(got)
	require.Equal(t, []string{"a.md", "sub/b.md"}, got)

	// Missing repo dir → no entries, no error.
	require.NoError(t, s.WalkRepoEntries("nope", func(string) error { return nil }))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/renderstore/... -run TestLocalRenderStore_WalkRepoEntries -v`
Expected: FAIL — `WalkRepoEntries` undefined.

- [ ] **Step 3: Implement `WalkRepoEntries` on `LocalRenderStore`**

Add to `backend/internal/renderstore/local.go`:

```go
// WalkRepoEntries calls fn for every notePath stored under repoID, derived
// from the on-disk <baseDir>/<repoID>/<notePath>.enc layout. A missing repo
// directory yields no entries and no error.
func (s *LocalRenderStore) WalkRepoEntries(repoID string, fn func(notePath string) error) error {
	repoDir := filepath.Join(s.baseDir, repoID)
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		return nil
	}
	return filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(repoDir, path)
		if rerr != nil {
			return nil
		}
		return fn(strings.TrimSuffix(rel, ".enc"))
	})
}
```

(`local.go` already imports `os`, `filepath`, `strings`.)

- [ ] **Step 4: Implement `WalkRepoEntries` on `S3RenderStore`**

Add to `backend/internal/renderstore/s3.go`:

```go
// WalkRepoEntries calls fn for every notePath stored under repoID in this
// store's namespace, by listing objects under the <keyPrefix><repoID>/
// prefix and stripping the prefix and the .enc suffix.
func (s *S3RenderStore) WalkRepoEntries(repoID string, fn func(notePath string) error) error {
	prefix := s.keyPrefix + repoID + "/"
	ctx := context.Background()
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return obj.Err
		}
		notePath := strings.TrimSuffix(strings.TrimPrefix(obj.Key, prefix), ".enc")
		if err := fn(notePath); err != nil {
			return err
		}
	}
	return nil
}
```

(`s3.go` already imports `context`, `strings`, and the `minio` package. Confirm `strings` is imported — it was added in the prior feature's Task 8; if the build complains it's missing, add it.)

- [ ] **Step 5: Run test to verify it passes**

Run: `cd backend && go test ./internal/renderstore/... -run TestLocalRenderStore_WalkRepoEntries -v`
Expected: PASS. (The S3 variant compiles; it's exercised end-to-end by Task 4's migration test via a fake S3 server only if needed — for this task, the local test plus a successful build cover it. If you want direct S3 coverage, reuse the fake-S3-server pattern from `backend/internal/renderstore/s3_fake_server_test.go`, but it is not required here.)

- [ ] **Step 6: Run the full renderstore suite**

Run: `cd backend && go build ./... && go test ./internal/renderstore/...`
Expected: builds clean, all pass.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/renderstore/local.go backend/internal/renderstore/s3.go backend/internal/renderstore/walkrepo_test.go
git commit -m "feat: add per-repo key enumeration (WalkRepoEntries) to local and S3 stores"
```

---

### Task 4: Per-repo migration job (`RunRepoMigration`)

**Files:**
- Create: `backend/internal/jobs/repo_migration.go`
- Test: `backend/internal/jobs/repo_migration_test.go`

**Interfaces:**
- Consumes: `WalkRepoEntries` (Task 3), `renderstore.RenderStore` interface, `*LocalRenderStore`/`*S3RenderStore` concrete types.
- Produces: `jobs.RunRepoMigration(ctx context.Context, repoID string, srcRenders, dstRenders, srcAssets, dstAssets renderstore.RenderStore) (migrated, failed int, err error)`. Task 9's per-repo migration handler consumes it. Callers pass **plain** (non-`EncryptingStore`) asset stores for both src and dst, so assets copy as raw ciphertext — see the double-encryption note.

This mirrors the existing `RunMigrationCycle` (verify-before-delete, per-entry skip) but scopes to one repo and enumerates via `WalkRepoEntries` (which works for local *and* S3 sources, unlike the instance-wide job that only handled local sources). The double-encryption guard from the prior feature is a *caller* responsibility here: the caller must hand `RunRepoMigration` the plain asset stores under any `EncryptingStore`, exactly as `runMigrationInBackground` already does via `EncryptingStore.Inner()`.

- [ ] **Step 1: Write the failing test**

```go
// backend/internal/jobs/repo_migration_test.go
package jobs_test

import (
	"context"
	"testing"

	"github.com/pubobs/backend/internal/jobs"
	"github.com/pubobs/backend/internal/renderstore"
	"github.com/stretchr/testify/require"
)

func TestRunRepoMigration_movesOnlyThatRepo(t *testing.T) {
	ctx := context.Background()
	srcRenders := renderstore.NewLocal(t.TempDir())
	dstRenders := renderstore.NewLocal(t.TempDir())
	srcAssets := renderstore.NewLocal(t.TempDir())
	dstAssets := renderstore.NewLocal(t.TempDir())

	require.NoError(t, srcRenders.Write("r1", "note.md", []byte("render-1")))
	require.NoError(t, srcAssets.Write("r1", "img.png", []byte("asset-1")))
	// A different repo's data must be untouched.
	require.NoError(t, srcRenders.Write("r2", "other.md", []byte("render-2")))

	migrated, failed, err := jobs.RunRepoMigration(ctx, "r1", srcRenders, dstRenders, srcAssets, dstAssets)
	require.NoError(t, err)
	require.Equal(t, 2, migrated)
	require.Equal(t, 0, failed)

	// r1 moved.
	gotR, err := dstRenders.Read("r1", "note.md")
	require.NoError(t, err)
	require.Equal(t, []byte("render-1"), gotR)
	gotA, err := dstAssets.Read("r1", "img.png")
	require.NoError(t, err)
	require.Equal(t, []byte("asset-1"), gotA)
	// r1 source deleted after verified write.
	srcGone, err := srcRenders.Read("r1", "note.md")
	require.NoError(t, err)
	require.Nil(t, srcGone)

	// r2 untouched.
	r2, err := srcRenders.Read("r2", "other.md")
	require.NoError(t, err)
	require.Equal(t, []byte("render-2"), r2)
	r2moved, err := dstRenders.Read("r2", "other.md")
	require.NoError(t, err)
	require.Nil(t, r2moved)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/jobs/... -run TestRunRepoMigration_movesOnlyThatRepo -v`
Expected: FAIL — `RunRepoMigration` undefined.

- [ ] **Step 3: Implement `RunRepoMigration`**

```go
// backend/internal/jobs/repo_migration.go
package jobs

import (
	"context"
	"log"

	"github.com/pubobs/backend/internal/renderstore"
)

// repoWalker is implemented by both *LocalRenderStore and *S3RenderStore
// (Task 3). It enumerates the keys stored for one repo.
type repoWalker interface {
	WalkRepoEntries(repoID string, fn func(notePath string) error) error
}

// RunRepoMigration copies one repo's render and asset entries from the source
// stores to the destination stores, verifying each write by reading it back
// before deleting the source copy. A per-entry failure is logged and skipped;
// the source is never deleted unless its destination write verifies.
//
// Callers MUST pass PLAIN asset stores (the store *under* any EncryptingStore,
// via renderstore.EncryptingStore.Inner()) for srcAssets/dstAssets, so asset
// ciphertext copies verbatim and is never re-encrypted. srcRenders/dstRenders
// are already plain (render blobs are never server-side encrypted).
func RunRepoMigration(
	ctx context.Context,
	repoID string,
	srcRenders, dstRenders, srcAssets, dstAssets renderstore.RenderStore,
) (migrated, failed int, err error) {
	migrateOne := func(source, dest renderstore.RenderStore, path string) {
		data, rerr := source.Read(repoID, path)
		if rerr != nil || data == nil {
			log.Printf("repo-migration: read %s/%s: %v", repoID, path, rerr)
			failed++
			return
		}
		if werr := dest.Write(repoID, path, data); werr != nil {
			log.Printf("repo-migration: write %s/%s: %v", repoID, path, werr)
			failed++
			return
		}
		verify, verr := dest.Read(repoID, path)
		if verr != nil || string(verify) != string(data) {
			log.Printf("repo-migration: verify %s/%s failed", repoID, path)
			failed++
			return
		}
		if derr := source.Delete(repoID, path); derr != nil {
			log.Printf("repo-migration: delete source %s/%s: %v", repoID, path, derr)
			// Not counted as failed — data is safely on the destination.
		}
		migrated++
	}

	migrateSide := func(source, dest renderstore.RenderStore) error {
		walker, ok := source.(repoWalker)
		if !ok {
			return nil // source can't be enumerated (e.g. an unexpected wrapper); nothing to migrate
		}
		return walker.WalkRepoEntries(repoID, func(notePath string) error {
			migrateOne(source, dest, notePath)
			return nil
		})
	}

	if werr := migrateSide(srcRenders, dstRenders); werr != nil {
		return migrated, failed, werr
	}
	if werr := migrateSide(srcAssets, dstAssets); werr != nil {
		return migrated, failed, werr
	}
	_ = ctx // reserved for cancellation; unused for now
	return migrated, failed, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/jobs/... -run TestRunRepoMigration_movesOnlyThatRepo -v`
Expected: PASS.

- [ ] **Step 5: Run the full jobs suite**

Run: `cd backend && go build ./... && go test ./internal/jobs/...`
Expected: builds clean, all pass.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/jobs/repo_migration.go backend/internal/jobs/repo_migration_test.go
git commit -m "feat: add per-repo migration job (RunRepoMigration)"
```

---

### Task 5: `StorageResolver`

**Files:**
- Create: `backend/internal/storageresolver/resolver.go`
- Test: `backend/internal/storageresolver/resolver_test.go`

**Interfaces:**
- Consumes: `store.Store` (for `GetRepo` → `StorageDestinationID`, and `ListStorageDestinations`), `renderstore.New`/`NewEncryptingStore`/`RenderStore`/`EncryptingStore`, `model.StorageDestination`.
- Produces:
  - `storageresolver.New(st *store.Store, localRenderDir, localAssetDir string, assetKey []byte) (*Resolver, error)` — builds the local store-set and the per-destination store-sets from the DB, returns a ready resolver.
  - `(r *Resolver) RenderStoreFor(ctx, repoID string) (renderstore.RenderStore, error)`
  - `(r *Resolver) AssetStoreFor(ctx, repoID string) (renderstore.RenderStore, error)`
  - `(r *Resolver) PlainAssetStoreFor(ctx, repoID string) (renderstore.RenderStore, error)` — the un-`EncryptingStore`-wrapped asset store for a repo's destination (used by per-repo migration to avoid double-encryption).
  - `(r *Resolver) Rebuild(ctx) error` — re-reads destinations from the DB and rebuilds the internal map (call after any destination add/edit/delete). Thread-safe.
  - `(r *Resolver) LocalRenderStore() renderstore.RenderStore` / `LocalAssetStorePlain() renderstore.RenderStore` — the local plain stores, for migration source/dest when a repo is local.
  - `(r *Resolver) DestinationRenderStore(destID string) (renderstore.RenderStore, bool)` — the render store for a destination id, for the usage endpoint's per-destination listing.

A **store-set** is the trio `{render RenderStore, assetPlain RenderStore, asset RenderStore (EncryptingStore-wrapped)}`. The resolver holds one for `local` (keyed by empty string / a sentinel) and one per destination id. `RenderStoreFor` looks up the repo's `StorageDestinationID`; nil → local set, else the destination's set.

- [ ] **Step 1: Write the failing test**

```go
// backend/internal/storageresolver/resolver_test.go
package storageresolver_test

import (
	"context"
	"crypto/rand"
	"testing"

	"github.com/pubobs/backend/internal/db"
	"github.com/pubobs/backend/internal/model"
	"github.com/pubobs/backend/internal/renderstore"
	"github.com/pubobs/backend/internal/storageresolver"
	"github.com/pubobs/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func newKey(t *testing.T) []byte {
	k := make([]byte, 32)
	_, err := rand.Read(k)
	require.NoError(t, err)
	return k
}

func TestResolver_localWhenNoDestination(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()
	st := store.New(d)
	ctx := context.Background()
	_, err = st.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	require.NoError(t, err)

	r, err := storageresolver.New(st, t.TempDir(), t.TempDir(), newKey(t))
	require.NoError(t, err)

	rs, err := r.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	_, isLocal := rs.(*renderstore.LocalRenderStore)
	require.True(t, isLocal, "repo with no destination resolves to the local store")

	as, err := r.AssetStoreFor(ctx, "r1")
	require.NoError(t, err)
	_, isEnc := as.(*renderstore.EncryptingStore)
	require.True(t, isEnc, "asset store is always EncryptingStore-wrapped")
}

func TestResolver_usesDestinationAfterRebuild(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()
	st := store.New(d)
	ctx := context.Background()
	_, err = st.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	require.NoError(t, err)

	r, err := storageresolver.New(st, t.TempDir(), t.TempDir(), newKey(t))
	require.NoError(t, err)

	// Add a destination + assign the repo, then rebuild.
	require.NoError(t, st.CreateStorageDestination(ctx, &model.StorageDestination{
		ID: "d1", Name: "arch", S3Endpoint: "localhost:9000", S3Bucket: "b",
		S3AccessKey: "ak", S3SecretKey: "sk", S3Region: "us-east-1", S3UseSSL: false,
	}))
	destID := "d1"
	require.NoError(t, st.SetRepoStorageDestination(ctx, "r1", &destID))
	require.NoError(t, r.Rebuild(ctx))

	rs, err := r.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	_, isS3 := rs.(*renderstore.S3RenderStore)
	require.True(t, isS3, "repo assigned to an S3 destination resolves to an S3 store")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/storageresolver/... -v`
Expected: FAIL — package/type undefined.

- [ ] **Step 3: Implement the resolver**

```go
// backend/internal/storageresolver/resolver.go
package storageresolver

import (
	"context"
	"fmt"
	"sync"

	"github.com/pubobs/backend/internal/model"
	"github.com/pubobs/backend/internal/renderstore"
	"github.com/pubobs/backend/internal/store"
)

// storeSet is the render + asset stores for one destination (or local).
// assetPlain is the un-encrypted base; asset is the EncryptingStore wrapping it.
type storeSet struct {
	render     renderstore.RenderStore
	assetPlain renderstore.RenderStore
	asset      renderstore.RenderStore
}

// Resolver returns the right render/asset store for a repo based on its
// assigned storage destination (nil = local). It rebuilds its per-destination
// map on demand when destinations change — no process restart.
type Resolver struct {
	st             *store.Store
	localRenderDir string
	localAssetDir  string
	assetKey       []byte

	mu    sync.RWMutex
	local storeSet
	byID  map[string]storeSet // destination id → store-set
}

func New(st *store.Store, localRenderDir, localAssetDir string, assetKey []byte) (*Resolver, error) {
	if len(assetKey) != 32 {
		return nil, fmt.Errorf("asset key must be 32 bytes")
	}
	r := &Resolver{
		st:             st,
		localRenderDir: localRenderDir,
		localAssetDir:  localAssetDir,
		assetKey:       assetKey,
	}
	local, err := r.buildSet("local", "", "", "", "", "", false)
	if err != nil {
		return nil, err
	}
	r.local = local
	if err := r.Rebuild(context.Background()); err != nil {
		return nil, err
	}
	return r, nil
}

// buildSet constructs a store-set. storeType "local" uses the local dirs;
// otherwise it's an S3 destination with the given config.
func (r *Resolver) buildSet(storeType, endpoint, bucket, accessKey, secretKey, region string, useSSL bool) (storeSet, error) {
	render, err := renderstore.New(storeType, r.localRenderDir, endpoint, bucket, accessKey, secretKey, region, useSSL, "renders/")
	if err != nil {
		return storeSet{}, err
	}
	assetPlain, err := renderstore.New(storeType, r.localAssetDir, endpoint, bucket, accessKey, secretKey, region, useSSL, "assets/")
	if err != nil {
		return storeSet{}, err
	}
	asset, err := renderstore.NewEncryptingStore(assetPlain, r.assetKey)
	if err != nil {
		return storeSet{}, err
	}
	return storeSet{render: render, assetPlain: assetPlain, asset: asset}, nil
}

// Rebuild re-reads all destinations from the DB and rebuilds the per-id map.
func (r *Resolver) Rebuild(ctx context.Context) error {
	dests, err := r.st.ListStorageDestinations(ctx)
	if err != nil {
		return err
	}
	next := make(map[string]storeSet, len(dests))
	for _, d := range dests {
		set, err := r.buildSet("s3", d.S3Endpoint, d.S3Bucket, d.S3AccessKey, d.S3SecretKey, d.S3Region, d.S3UseSSL)
		if err != nil {
			return fmt.Errorf("build store-set for destination %s: %w", d.ID, err)
		}
		next[d.ID] = set
	}
	r.mu.Lock()
	r.byID = next
	r.mu.Unlock()
	return nil
}

func (r *Resolver) setForRepo(ctx context.Context, repoID string) (storeSet, error) {
	repo, err := r.st.GetRepo(ctx, repoID)
	if err != nil {
		return storeSet{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if repo.StorageDestinationID == nil {
		return r.local, nil
	}
	set, ok := r.byID[*repo.StorageDestinationID]
	if !ok {
		// Assigned destination no longer exists in the map — fall back to
		// local rather than fail hard; a Rebuild should reconcile this.
		return r.local, nil
	}
	return set, nil
}

func (r *Resolver) RenderStoreFor(ctx context.Context, repoID string) (renderstore.RenderStore, error) {
	set, err := r.setForRepo(ctx, repoID)
	if err != nil {
		return nil, err
	}
	return set.render, nil
}

func (r *Resolver) AssetStoreFor(ctx context.Context, repoID string) (renderstore.RenderStore, error) {
	set, err := r.setForRepo(ctx, repoID)
	if err != nil {
		return nil, err
	}
	return set.asset, nil
}

func (r *Resolver) PlainAssetStoreFor(ctx context.Context, repoID string) (renderstore.RenderStore, error) {
	set, err := r.setForRepo(ctx, repoID)
	if err != nil {
		return nil, err
	}
	return set.assetPlain, nil
}

func (r *Resolver) LocalRenderStore() renderstore.RenderStore     { return r.local.render }
func (r *Resolver) LocalAssetStorePlain() renderstore.RenderStore { return r.local.assetPlain }

// DestinationRenderStore returns the render store for a destination id, and
// whether it exists in the current map.
func (r *Resolver) DestinationRenderStore(destID string) (renderstore.RenderStore, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	set, ok := r.byID[destID]
	if !ok {
		return nil, false
	}
	return set.render, true
}

// StoresForDestination returns the render + plain-asset stores for a
// destination id (or the local set when destID is nil). Used by per-repo
// migration to build source/dest pairs. Plain (non-encrypting) asset store is
// returned so asset ciphertext copies verbatim.
func (r *Resolver) StoresForDestination(destID *string) (render, assetPlain renderstore.RenderStore, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if destID == nil {
		return r.local.render, r.local.assetPlain, true
	}
	set, found := r.byID[*destID]
	if !found {
		return nil, nil, false
	}
	return set.render, set.assetPlain, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/storageresolver/... -v`
Expected: PASS. (`renderstore.New("s3", ...)` builds a client without dialing, so the S3 store-set constructs successfully against a placeholder endpoint.)

- [ ] **Step 5: Run the full backend suite**

Run: `cd backend && go build ./... && go test ./...`
Expected: builds clean, all pass.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/storageresolver/
git commit -m "feat: add StorageResolver mapping repos to per-destination store-sets"
```

---

### Task 6: Wire resolver into `Deps` + boot + backward-compat conversion

**Files:**
- Modify: `backend/internal/api/deps.go`
- Modify: `backend/cmd/server/main.go`
- Modify: `backend/cmd/server/storage_boot.go`
- Test: `backend/cmd/server/storage_boot_test.go`

**Interfaces:**
- Consumes: `storageresolver.New` (Task 5), `store.Store` destination/repo methods (Tasks 1-2), the existing `loadOrSeedStorageSettings`.
- Produces: `Deps.Resolver *storageresolver.Resolver` (new field). `Deps.RenderStore`/`Deps.AssetStore` are **removed** (Tasks 7 & 9 update all their users). A new boot helper `convertLegacyStorageSettings(ctx, *store.Store, *model.StorageSettings) error` that, on first boot after upgrade, converts an S3-configured legacy `storage_settings` row into destination "default" and assigns all existing repos to it (idempotent — skips if a "default" conversion already ran).

- [ ] **Step 1: Add `Resolver` to `Deps`, remove the two store fields**

In `backend/internal/api/deps.go`:

```go
type Deps struct {
	Store         *store.Store
	Cache         *gitcache.Cache
	Auth          *auth.SessionStore
	OIDCProviders []*auth.NamedProvider
	Config        *config.Config
	Resolver      *storageresolver.Resolver
}
```

Update the import block: remove `"github.com/pubobs/backend/internal/renderstore"` if it becomes unused, add `"github.com/pubobs/backend/internal/storageresolver"`.

(This will break compilation of `admin_storage.go`, `sync.go`, `pub.go` until Tasks 7 & 9 update them. That's expected — this task's build-green checkpoint is Step 6, after the boot wiring; the api package won't compile until Task 7. To keep this task independently committable, this task also does the mechanical `sync.go`/`pub.go` call-site swap inline in Step 4 so the tree builds; the admin handlers are handled in Task 9, so temporarily keep them compiling by the minimal shim described in Step 5.)

- [ ] **Step 2: Write the failing test for the conversion helper**

```go
// backend/cmd/server/storage_boot_test.go — add
func TestConvertLegacyStorageSettings_s3BecomesDefaultDestination(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()
	s := store.New(d)
	ctx := context.Background()

	_, err = s.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	require.NoError(t, err)

	legacy := &model.StorageSettings{
		StoreType: "s3", S3Endpoint: "s3.example.com", S3Bucket: "b",
		S3AccessKey: "AK", S3SecretKey: "SK", S3Region: "us-east-1", S3UseSSL: true,
		AssetEncryptionKey: "00", MigrationStatus: "idle",
	}
	require.NoError(t, convertLegacyStorageSettings(ctx, s, legacy))

	dests, err := s.ListStorageDestinations(ctx)
	require.NoError(t, err)
	require.Len(t, dests, 1)
	require.Equal(t, "default", dests[0].Name)
	require.Equal(t, "s3.example.com", dests[0].S3Endpoint)

	repo, err := s.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.NotNil(t, repo.StorageDestinationID)
	require.Equal(t, dests[0].ID, *repo.StorageDestinationID)

	// Idempotent: a second call does not create a duplicate destination.
	require.NoError(t, convertLegacyStorageSettings(ctx, s, legacy))
	dests2, err := s.ListStorageDestinations(ctx)
	require.NoError(t, err)
	require.Len(t, dests2, 1)
}

func TestConvertLegacyStorageSettings_localIsNoop(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()
	s := store.New(d)
	ctx := context.Background()
	_, err = s.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	require.NoError(t, err)

	require.NoError(t, convertLegacyStorageSettings(ctx, s, &model.StorageSettings{StoreType: "local"}))
	dests, err := s.ListStorageDestinations(ctx)
	require.NoError(t, err)
	require.Empty(t, dests)
	repo, err := s.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.Nil(t, repo.StorageDestinationID)
}
```

(This test file already exists from the prior feature and imports `db`, `store`, `model`, `context`, `testing`, `require`. Add these two functions.)

- [ ] **Step 3: Run test to verify it fails**

Run: `cd backend && go test ./cmd/server/... -run TestConvertLegacyStorageSettings -v`
Expected: FAIL — `convertLegacyStorageSettings` undefined.

- [ ] **Step 4: Update `sync.go` and `pub.go` call sites to the resolver**

These are the mechanical swaps that keep the api package compiling once `Deps.RenderStore`/`AssetStore` are gone. In `backend/internal/api/sync.go`:

- Replace `deps.RenderStore.Write(repoID, f.Path, encBytes)` with:
  ```go
  if rstore, rerr := deps.Resolver.RenderStoreFor(r.Context(), repoID); rerr == nil {
      if werr := rstore.Write(repoID, f.Path, encBytes); werr != nil {
          fmt.Printf("renderstore write %s/%s: %v\n", repoID, f.Path, werr)
      }
  } else {
      fmt.Printf("resolve render store %s: %v\n", repoID, rerr)
  }
  ```
  (Replace the existing `if werr := deps.RenderStore.Write(...)` block wholesale. `r` is the `*http.Request` in scope in `handleSync`.)
- Replace the asset write `deps.AssetStore.Write(repoID, a.Path, a.Content)` block similarly, using `deps.Resolver.AssetStoreFor(r.Context(), repoID)`.
- Replace the deleted-path `deps.RenderStore.Delete(repoID, p)` block using `deps.Resolver.RenderStoreFor(r.Context(), repoID)` then `.Delete(repoID, p)`.

In `backend/internal/api/pub.go`:

- In `handlePubGetRender`, replace `data, err := deps.RenderStore.Read(repoID, notePath)` with:
  ```go
  rstore, rerr := deps.Resolver.RenderStoreFor(r.Context(), repoID)
  if rerr != nil {
      writeError(w, http.StatusInternalServerError, "resolve render store failed")
      return
  }
  data, err := rstore.Read(repoID, notePath)
  ```
- In `handlePubGetAsset`, replace `deps.AssetStore.Read(repoID, assetPath)` and the backfill `deps.AssetStore.Write(repoID, assetPath, data)` with resolver-obtained asset stores (`deps.Resolver.AssetStoreFor(r.Context(), repoID)`), preserving the existing fallback-to-git-checkout + backfill logic. Obtain the asset store once at the top of the handler and use it for both the read and the backfill write.

- [ ] **Step 5: Add a temporary shim so admin_storage.go still compiles**

`admin_storage.go` references `deps.RenderStore`/`deps.AssetStore` in the settings/usage/migrate handlers, which Task 9 rewrites. To keep *this* task's tree building, the cleanest move is to **remove the four now-obsolete instance-wide routes and their handlers now** (they're superseded by Task 8/9's per-destination + per-repo endpoints), rather than shim them:

- In `backend/internal/api/router.go`, delete the four lines registering `GET/PUT /api/admin/storage-settings`, `GET /api/admin/storage-usage`, `POST /api/admin/storage-migrate`.
- In `backend/internal/api/admin_storage.go`, delete `handleAdminGetStorageSettings`, `handleAdminUpdateStorageSettings`, `handleAdminStorageUsage`, `handleAdminMigrateStorage`, `runMigrationInBackground`, `storageSettingsResponse`, `toStorageSettingsResponse`, `updateStorageSettingsBody`. **Keep** `validateS3Settings`, `S3ValidateFunc`, `errValidationMismatch`, and `dirSizeBytes` — Tasks 8-10 reuse them. Delete the corresponding tests in `admin_storage_test.go` that target the removed handlers (Task 8/9/10 add new ones); keep any test helpers still referenced.

This leaves `admin_storage.go` compiling with only the reused helpers. (If deleting the tests is noisy, comment them with a `// removed in per-repo storage rework` note and delete in Task 9 — but prefer clean deletion.)

- [ ] **Step 6: Implement the conversion helper and wire boot**

Add to `backend/cmd/server/storage_boot.go`:

```go
// convertLegacyStorageSettings performs a one-time upgrade: if the prior
// instance-wide storage_settings row was configured for S3 and no
// destinations exist yet, create a "default" destination from it and assign
// every existing repo to it, so their already-uploaded renders/assets keep
// resolving to the same bucket. Idempotent: a no-op once any destination
// exists, or when the legacy settings were local.
func convertLegacyStorageSettings(ctx context.Context, s *store.Store, legacy *model.StorageSettings) error {
	if legacy.StoreType != "s3" {
		return nil
	}
	existing, err := s.ListStorageDestinations(ctx)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil // conversion already ran (or destinations otherwise exist)
	}
	dest := &model.StorageDestination{
		ID:          uuid.NewString(),
		Name:        "default",
		S3Endpoint:  legacy.S3Endpoint,
		S3Bucket:    legacy.S3Bucket,
		S3AccessKey: legacy.S3AccessKey,
		S3SecretKey: legacy.S3SecretKey,
		S3Region:    legacy.S3Region,
		S3UseSSL:    legacy.S3UseSSL,
	}
	if err := s.CreateStorageDestination(ctx, dest); err != nil {
		return err
	}
	repos, err := s.ListRepos(ctx)
	if err != nil {
		return err
	}
	for _, repo := range repos {
		if err := s.SetRepoStorageDestination(ctx, repo.ID, &dest.ID); err != nil {
			return err
		}
	}
	return nil
}
```

Add `"github.com/google/uuid"` to `storage_boot.go`'s imports (already a project dependency — used in `admin.go`).

In `backend/cmd/server/main.go`, replace the whole render/asset/swappable construction block (from `rs, err := renderstore.New(...)` through `assetSwap := renderstore.NewSwappableStore(as)`) with:

```go
	assetKeyBytes, err := hex.DecodeString(storageSettings.AssetEncryptionKey)
	if err != nil || len(assetKeyBytes) != 32 {
		log.Fatalf("invalid asset encryption key in storage_settings")
	}
	if err := convertLegacyStorageSettings(ctx, appStore, storageSettings); err != nil {
		log.Fatalf("convert legacy storage settings: %v", err)
	}
	resolver, err := storageresolver.New(appStore, cfg.RenderDir, cfg.AssetDir, assetKeyBytes)
	if err != nil {
		log.Fatalf("storage resolver: %v", err)
	}
```

Update the `Deps` literal: remove `RenderStore`/`AssetStore`, add `Resolver: resolver`. Update `main.go` imports: drop `renderstore` if now unused, add `"github.com/pubobs/backend/internal/storageresolver"`.

- [ ] **Step 7: Run tests to verify they pass**

Run: `cd backend && go test ./cmd/server/... -run TestConvertLegacyStorageSettings -v`
Expected: PASS.

- [ ] **Step 8: Build and run the whole backend suite**

Run: `cd backend && go build ./... && go test ./...`
Expected: builds clean; all pass. (Expect to have deleted the prior feature's `admin_storage_test.go` cases for the removed handlers in Step 5 — the suite should be green with the new tests plus the surviving ones.)

- [ ] **Step 9: Commit**

```bash
git add backend/internal/api/deps.go backend/internal/api/sync.go backend/internal/api/pub.go backend/internal/api/router.go backend/internal/api/admin_storage.go backend/internal/api/admin_storage_test.go backend/cmd/server/main.go backend/cmd/server/storage_boot.go backend/cmd/server/storage_boot_test.go
git commit -m "feat: wire StorageResolver into Deps/boot with legacy-settings conversion"
```

---

### Task 7: Verify call-site behavior end-to-end (sync + serve through resolver)

**Files:**
- Test: `backend/internal/api/sync_test.go` (update the existing asset write-through test)
- Test: `backend/internal/api/pub_test.go` (update the existing asset-serving tests)

**Interfaces:**
- Consumes: `Deps.Resolver`. This task has no production code of its own — it updates the prior feature's tests, which set `deps.RenderStore`/`deps.AssetStore` directly (now-removed fields) so they no longer compile, to build a resolver instead.

Because Task 6 removed `Deps.RenderStore`/`AssetStore`, the prior tests that assigned them won't compile. This task fixes those tests to construct a `Deps.Resolver` and asserts the same behaviors through it. Splitting this out keeps Task 6's diff focused on production wiring and this one on test migration.

- [ ] **Step 1: Add a resolver test-helper**

In `backend/internal/api/` test code (e.g. append to `sync_test.go`), add a helper that builds a local-only resolver for tests:

```go
func newTestResolver(t *testing.T) *storageresolver.Resolver {
	t.Helper()
	// A fresh in-memory store the resolver reads repos/destinations from.
	// Tests that need the resolver to see specific repos should create them
	// in the SAME store the Deps uses — so build the resolver from deps.Store.
	panic("use newTestResolverFor(deps, t) instead")
}

func newTestResolverFor(t *testing.T, deps *api.Deps) *storageresolver.Resolver {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	r, err := storageresolver.New(deps.Store, t.TempDir(), t.TempDir(), key)
	require.NoError(t, err)
	return r
}
```

(Delete the `newTestResolver` panic stub if it trips linting — it's only there to signal intent; prefer just defining `newTestResolverFor`. Add `"github.com/pubobs/backend/internal/storageresolver"` to the test imports.)

- [ ] **Step 2: Update `TestHandleSync_writesAssetsToAssetStore`**

Replace its `deps.AssetStore = ...` line with `deps.Resolver = newTestResolverFor(t, deps)` (constructed after the repo is created in `deps.Store`, so the resolver resolves `r1` to local). Replace the final assertion `deps.AssetStore.Read("r1", "img.png")` with:

```go
	as, err := deps.Resolver.AssetStoreFor(context.Background(), "r1")
	require.NoError(t, err)
	data, err := as.Read("r1", "img.png")
	require.NoError(t, err)
	require.Equal(t, []byte("pngbytes"), data)
```

Also update `newTestDepsWithCache` (in `sync_test.go`) so it no longer sets `deps.RenderStore` (that field is gone); if a test needs a resolver it calls `newTestResolverFor` explicitly after creating its repos.

- [ ] **Step 3: Update `pub_test.go` asset tests**

`newTestDepsForPub` currently sets `deps.RenderStore`/`deps.AssetStore`. Replace those with `deps.Resolver = newTestResolverFor(t, deps)` — but note the resolver must be built *after* the repo exists in `deps.Store`, so move resolver construction to each test after its `CreateRepo`/`SetRepoAllowGuest` calls (or have `newTestDepsForPub` return `deps` and let each test build the resolver last). The two tests then assert render/asset reads through `deps.Resolver.RenderStoreFor`/`AssetStoreFor` as in Step 2. The git-checkout fallback + backfill test still writes the "legacy" asset into the git checkout dir and asserts the backfill landed in `deps.Resolver.AssetStoreFor(...)`.

- [ ] **Step 4: Run the api suite**

Run: `cd backend && go test ./internal/api/... -v`
Expected: PASS — sync writes and pub reads resolve through the resolver to the local store.

- [ ] **Step 5: Full suite**

Run: `cd backend && go build ./... && go test ./...`
Expected: builds clean, all pass.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/api/sync_test.go backend/internal/api/pub_test.go
git commit -m "test: migrate sync/pub tests to the StorageResolver"
```

---

### Task 8: Admin API — storage destinations CRUD

**Files:**
- Create: `backend/internal/api/admin_destinations.go`
- Modify: `backend/internal/api/router.go`
- Test: `backend/internal/api/admin_destinations_test.go`

**Interfaces:**
- Consumes: `store` destination methods (Task 1), `store.CountReposUsingDestination` (Task 2), `validateS3Settings`/`S3ValidateFunc` (retained from prior feature), `deps.Resolver.Rebuild` (Task 5), `requireAdmin`/`writeJSON`/`writeError`/`readJSON`.
- Produces: `GET /api/admin/storage-destinations`, `POST /api/admin/storage-destinations`, `PUT /api/admin/storage-destinations/{id}`, `DELETE /api/admin/storage-destinations/{id}`. Task 11 (frontend) calls these. The GET response omits secret keys.

- [ ] **Step 1: Write the failing tests**

```go
// backend/internal/api/admin_destinations_test.go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/model"
	"github.com/stretchr/testify/require"
)

func TestAdminDestinations_createListDeleteGuard(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Resolver = newTestResolverFor(t, deps)
	// Substitute S3 validation so create doesn't need a live endpoint.
	orig := api.S3ValidateFunc
	api.S3ValidateFunc = func(_ *model.StorageSettings) error { return nil }
	defer func() { api.S3ValidateFunc = orig }()

	// Create.
	body := `{"name":"arch","s3_endpoint":"s3.example.com","s3_bucket":"b","s3_access_key":"AK","s3_secret_key":"SK","s3_region":"us-east-1","s3_use_ssl":true}`
	req := httptest.NewRequest("POST", "/api/admin/storage-destinations", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var created map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &created))
	destID, _ := created["id"].(string)
	require.NotEmpty(t, destID)

	// List omits secret key.
	req = httptest.NewRequest("GET", "/api/admin/storage-destinations", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr = httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.NotContains(t, rr.Body.String(), "SK", "secret key must not be echoed")

	// Assign a repo, then delete must be rejected.
	deps.Store.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	deps.Store.SetRepoStorageDestination(ctx, "r1", &destID)
	req = httptest.NewRequest("DELETE", "/api/admin/storage-destinations/"+destID, nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr = httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())

	// Unassign, then delete succeeds.
	deps.Store.SetRepoStorageDestination(ctx, "r1", nil)
	req = httptest.NewRequest("DELETE", "/api/admin/storage-destinations/"+destID, nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr = httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/api/... -run TestAdminDestinations -v`
Expected: FAIL — routes 404.

- [ ] **Step 3: Implement the handlers**

```go
// backend/internal/api/admin_destinations.go
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/model"
)

type destinationResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	S3Endpoint  string `json:"s3_endpoint"`
	S3Bucket    string `json:"s3_bucket"`
	S3AccessKey string `json:"s3_access_key"`
	S3Region    string `json:"s3_region"`
	S3UseSSL    bool   `json:"s3_use_ssl"`
}

func toDestinationResponse(d *model.StorageDestination) destinationResponse {
	return destinationResponse{
		ID: d.ID, Name: d.Name, S3Endpoint: d.S3Endpoint, S3Bucket: d.S3Bucket,
		S3AccessKey: d.S3AccessKey, S3Region: d.S3Region, S3UseSSL: d.S3UseSSL,
	}
}

type destinationBody struct {
	Name        string `json:"name"`
	S3Endpoint  string `json:"s3_endpoint"`
	S3Bucket    string `json:"s3_bucket"`
	S3AccessKey string `json:"s3_access_key"`
	S3SecretKey string `json:"s3_secret_key"`
	S3Region    string `json:"s3_region"`
	S3UseSSL    bool   `json:"s3_use_ssl"`
}

// validateBody runs the S3 round-trip on a candidate destination by adapting
// it to the model.StorageSettings shape S3ValidateFunc expects.
func validateDestinationBody(b destinationBody) error {
	return S3ValidateFunc(&model.StorageSettings{
		StoreType: "s3", S3Endpoint: b.S3Endpoint, S3Bucket: b.S3Bucket,
		S3AccessKey: b.S3AccessKey, S3SecretKey: b.S3SecretKey,
		S3Region: b.S3Region, S3UseSSL: b.S3UseSSL,
	})
}

func handleAdminListDestinations(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		dests, err := deps.Store.ListStorageDestinations(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list destinations failed")
			return
		}
		out := make([]destinationResponse, 0, len(dests))
		for _, d := range dests {
			out = append(out, toDestinationResponse(d))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func handleAdminCreateDestination(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		var b destinationBody
		if err := readJSON(r, &b); err != nil || b.Name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		if err := validateDestinationBody(b); err != nil {
			writeError(w, http.StatusBadRequest, "S3 settings invalid: "+err.Error())
			return
		}
		d := &model.StorageDestination{
			ID: uuid.NewString(), Name: b.Name, S3Endpoint: b.S3Endpoint, S3Bucket: b.S3Bucket,
			S3AccessKey: b.S3AccessKey, S3SecretKey: b.S3SecretKey, S3Region: b.S3Region, S3UseSSL: b.S3UseSSL,
		}
		if err := deps.Store.CreateStorageDestination(r.Context(), d); err != nil {
			writeError(w, http.StatusInternalServerError, "create destination failed")
			return
		}
		if err := deps.Resolver.Rebuild(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "rebuild resolver failed")
			return
		}
		writeJSON(w, http.StatusCreated, toDestinationResponse(d))
	}
}

func handleAdminUpdateDestination(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		id := chi.URLParam(r, "id")
		existing, err := deps.Store.GetStorageDestination(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "destination not found")
			return
		}
		var b destinationBody
		if err := readJSON(r, &b); err != nil || b.Name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		existing.Name = b.Name
		existing.S3Endpoint = b.S3Endpoint
		existing.S3Bucket = b.S3Bucket
		existing.S3AccessKey = b.S3AccessKey
		if b.S3SecretKey != "" {
			existing.S3SecretKey = b.S3SecretKey // blank = keep existing
		}
		existing.S3Region = b.S3Region
		existing.S3UseSSL = b.S3UseSSL
		// Validate the merged candidate (with the effective secret).
		if err := S3ValidateFunc(&model.StorageSettings{
			StoreType: "s3", S3Endpoint: existing.S3Endpoint, S3Bucket: existing.S3Bucket,
			S3AccessKey: existing.S3AccessKey, S3SecretKey: existing.S3SecretKey,
			S3Region: existing.S3Region, S3UseSSL: existing.S3UseSSL,
		}); err != nil {
			writeError(w, http.StatusBadRequest, "S3 settings invalid: "+err.Error())
			return
		}
		if err := deps.Store.UpdateStorageDestination(r.Context(), existing); err != nil {
			writeError(w, http.StatusInternalServerError, "update destination failed")
			return
		}
		if err := deps.Resolver.Rebuild(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "rebuild resolver failed")
			return
		}
		writeJSON(w, http.StatusOK, toDestinationResponse(existing))
	}
}

func handleAdminDeleteDestination(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		id := chi.URLParam(r, "id")
		count, err := deps.Store.CountReposUsingDestination(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "check destination usage failed")
			return
		}
		if count > 0 {
			writeError(w, http.StatusConflict, "destination in use — reassign its repos first")
			return
		}
		if err := deps.Store.DeleteStorageDestination(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "delete destination failed")
			return
		}
		if err := deps.Resolver.Rebuild(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "rebuild resolver failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}
```

- [ ] **Step 4: Register routes**

In `backend/internal/api/router.go`, in the admin group, add:

```go
		r.Get("/api/admin/storage-destinations", handleAdminListDestinations(deps))
		r.Post("/api/admin/storage-destinations", handleAdminCreateDestination(deps))
		r.Put("/api/admin/storage-destinations/{id}", handleAdminUpdateDestination(deps))
		r.Delete("/api/admin/storage-destinations/{id}", handleAdminDeleteDestination(deps))
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd backend && go test ./internal/api/... -run TestAdminDestinations -v`
Expected: PASS.

- [ ] **Step 6: Full suite**

Run: `cd backend && go build ./... && go test ./...`
Expected: builds clean, all pass.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/api/admin_destinations.go backend/internal/api/admin_destinations_test.go backend/internal/api/router.go
git commit -m "feat: add admin API for storage destination CRUD"
```

---

### Task 9: Admin API — assign repo destination + per-repo migration

**Files:**
- Create: `backend/internal/api/admin_repo_storage.go`
- Modify: `backend/internal/api/router.go`
- Test: `backend/internal/api/admin_repo_storage_test.go`

**Interfaces:**
- Consumes: `store` repo methods (Task 2), `deps.Resolver.StoresForDestination` (Task 5), `jobs.RunRepoMigration` (Task 4).
- Produces: `PUT /api/admin/repos/{id}/storage` (assign a repo to a destination or local, and trigger a per-repo migration of its data), plus per-repo migration status via the existing repo listing (Task 2's fields, surfaced in the repo response — see note). Task 11 calls this.

Assigning a destination does two things: (1) persist the new `storage_destination_id`; (2) kick off a background `RunRepoMigration` moving the repo's `renders/`/`assets/` from the *old* destination's stores to the *new* one, updating the repo's migration-status fields. The migration uses **plain** asset stores on both ends (`StoresForDestination` returns the plain asset store) so ciphertext copies verbatim — preserving the no-double-encryption invariant.

- [ ] **Step 1: Write the failing test**

```go
// backend/internal/api/admin_repo_storage_test.go
package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/model"
	"github.com/stretchr/testify/require"
)

func TestAdminRepoStorage_assignAndMigrate(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")

	// Two "destinations" both backed by local dirs (stand-ins), so migration
	// exercises the real path without a live S3 endpoint. Create dest rows.
	deps.Store.CreateStorageDestination(ctx, &model.StorageDestination{ID: "d1", Name: "d1"})
	deps.Resolver = newTestResolverFor(t, deps)

	// Seed r1 render data into its CURRENT (local) store, then assign to d1.
	rs, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rs.Write("r1", "note.md", []byte("data")))

	dest := "d1"
	_ = dest
	body := `{"storage_destination_id":"d1"}`
	req := httptest.NewRequest("PUT", "/api/admin/repos/r1/storage", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusAccepted, rr.Code, rr.Body.String())

	// Assignment persisted immediately.
	repo, err := deps.Store.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.NotNil(t, repo.StorageDestinationID)
	require.Equal(t, "d1", *repo.StorageDestinationID)

	// Background migration finishes.
	require.Eventually(t, func() bool {
		rp, _ := deps.Store.GetRepo(ctx, "r1")
		return rp.MigrationStatus == "done"
	}, 2*time.Second, 10*time.Millisecond)

	// Data now readable at the new (d1) store.
	rsNew, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	got, err := rsNew.Read("r1", "note.md")
	require.NoError(t, err)
	require.Equal(t, []byte("data"), got)
}
```

Note: for this test both the local set and destination "d1" resolve to local dirs (d1 has empty S3 config → `renderstore.New("s3", ...)` with an empty endpoint still constructs a client; reads/writes would fail against it). To keep the test hermetic without a live S3, the resolver's `buildSet` for an S3 destination with an empty endpoint won't actually work for reads. **Therefore:** in this test, instead of relying on d1 being S3, assert only that (a) the assignment persisted and (b) migration status reaches a terminal state — drop the "data readable at new store" assertion, OR back d1 with a fake S3 server (reuse `s3_fake_server_test.go`'s helper). Choose the simpler hermetic option: assert persistence + terminal migration status; leave byte-level destination verification to the `RunRepoMigration` unit test (Task 4), which already covers it with real local stores. Adjust the test body accordingly before implementing.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/... -run TestAdminRepoStorage -v`
Expected: FAIL — route 404.

- [ ] **Step 3: Implement the handler**

```go
// backend/internal/api/admin_repo_storage.go
package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/jobs"
)

type assignStorageBody struct {
	StorageDestinationID *string `json:"storage_destination_id"` // null = local
}

func handleAdminAssignRepoStorage(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		repoID := chi.URLParam(r, "id")
		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}
		if repo.MigrationStatus == "running" {
			writeError(w, http.StatusConflict, "a migration is already running for this repo")
			return
		}
		var b assignStorageBody
		if err := readJSON(r, &b); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		// Validate the target destination exists (nil = local is always valid).
		if b.StorageDestinationID != nil {
			if _, err := deps.Store.GetStorageDestination(r.Context(), *b.StorageDestinationID); err != nil {
				writeError(w, http.StatusBadRequest, "unknown destination")
				return
			}
		}

		oldDest := repo.StorageDestinationID
		newDest := b.StorageDestinationID

		// Resolve source (old) and dest (new) store pairs BEFORE reassigning,
		// so the source still points at where the data currently lives.
		srcRender, srcAssetPlain, ok1 := deps.Resolver.StoresForDestination(oldDest)
		dstRender, dstAssetPlain, ok2 := deps.Resolver.StoresForDestination(newDest)
		if !ok1 || !ok2 {
			writeError(w, http.StatusInternalServerError, "resolve storage failed")
			return
		}

		// Persist the new assignment immediately (reads/writes go to the new
		// destination from now on; migration moves the existing data over).
		if err := deps.Store.SetRepoStorageDestination(r.Context(), repoID, newDest); err != nil {
			writeError(w, http.StatusInternalServerError, "assign destination failed")
			return
		}
		if err := deps.Store.SetRepoMigrationStatus(r.Context(), repoID, "running", 0, 0); err != nil {
			writeError(w, http.StatusInternalServerError, "set migration status failed")
			return
		}

		go func() {
			ctx := context.Background()
			migrated, failed, merr := jobs.RunRepoMigration(ctx, repoID, srcRender, dstRender, srcAssetPlain, dstAssetPlain)
			status := "done"
			if merr != nil {
				status = "failed"
			}
			if err := deps.Store.SetRepoMigrationStatus(ctx, repoID, status, migrated+failed, migrated); err != nil {
				// best-effort status write
				_ = err
			}
		}()

		writeJSON(w, http.StatusAccepted, map[string]string{"status": "running"})
	}
}
```

- [ ] **Step 4: Register route**

In `backend/internal/api/router.go`, in the admin group:

```go
		r.Put("/api/admin/repos/{id}/storage", handleAdminAssignRepoStorage(deps))
```

- [ ] **Step 5: Surface per-repo storage fields in the repo listing**

Task 11's UI needs each repo's `storage_destination_id` and `migration_status`. The repo-list response is built by `handleListRepos` in `backend/internal/api/repos.go`, via an inline `repoResp` struct. Add two fields to that struct and populate them:

```go
		type repoResp struct {
			ID                   string  `json:"id"`
			Name                 string  `json:"name"`
			RemoteURL            string  `json:"remote_url"`
			DefaultBranch        string  `json:"default_branch"`
			IsCloned             bool    `json:"is_cloned"`
			Role                 string  `json:"role"`
			AllowGuest           bool    `json:"allow_guest"`
			StorageDestinationID *string `json:"storage_destination_id"`
			MigrationStatus      string  `json:"migration_status"`
		}
```

and in the `out[i] = repoResp{...}` literal add `StorageDestinationID: repo.StorageDestinationID, MigrationStatus: repo.MigrationStatus`. (Read the current `repos.go` to match the exact existing field set before editing — the above is that struct with the two new fields appended.) File to add to the commit: `backend/internal/api/repos.go` (not `admin.go`).

- [ ] **Step 6: Run test to verify it passes**

Run: `cd backend && go test ./internal/api/... -run TestAdminRepoStorage -v`
Expected: PASS.

- [ ] **Step 7: Full suite**

Run: `cd backend && go build ./... && go test ./...`
Expected: builds clean, all pass.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/api/admin_repo_storage.go backend/internal/api/admin_repo_storage_test.go backend/internal/api/router.go backend/internal/api/admin.go
git commit -m "feat: add per-repo storage assignment + background migration API"
```

---

### Task 10: Admin API — per-destination usage with error surfacing

**Files:**
- Create: `backend/internal/api/admin_storage_usage.go` (move/rewrite the usage handler here; delete the old one if any remains)
- Modify: `backend/internal/api/router.go`
- Test: `backend/internal/api/admin_storage_usage_test.go`

**Interfaces:**
- Consumes: `deps.Cache.DiskUsage()`, `dirSizeBytes` (retained helper), `deps.Store.ListStorageDestinations`, `deps.Resolver.DestinationRenderStore` (Task 5), `renderstore.S3RenderStore.ListAllObjects`/`SumObjectSizesWithPrefix`.
- Produces: `GET /api/admin/storage-usage` returning `{ local: {free_bytes, repos_bytes, renders_bytes, assets_bytes}, destinations: [{id, name, renders_bytes, assets_bytes, error}] }`. A destination whose S3 listing fails reports a non-empty `error` string (not a silent `0`) — closing the prior feature's deferred gap.

- [ ] **Step 1: Write the failing test**

```go
// backend/internal/api/admin_storage_usage_test.go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/stretchr/testify/require"
)

func TestAdminStorageUsage_localBreakdown(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")

	deps.Config.RepoCacheDir = t.TempDir()
	deps.Config.RenderDir = t.TempDir()
	deps.Config.AssetDir = t.TempDir()
	deps.Cache = gitcache.NewCache(deps.Config.RepoCacheDir)
	deps.Resolver = newTestResolverFor(t, deps)
	require.NoError(t, os.WriteFile(filepath.Join(deps.Config.RenderDir, "x.enc"), []byte("12345"), 0644))

	req := httptest.NewRequest("GET", "/api/admin/storage-usage", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var body struct {
		Local struct {
			RendersBytes float64 `json:"renders_bytes"`
		} `json:"local"`
		Destinations []any `json:"destinations"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, float64(5), body.Local.RendersBytes)
	require.Empty(t, body.Destinations, "no S3 destinations configured in this test")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/... -run TestAdminStorageUsage_localBreakdown -v`
Expected: FAIL — either route removed (from Task 6) so 404, or shape mismatch.

- [ ] **Step 3: Implement the handler**

```go
// backend/internal/api/admin_storage_usage.go
package api

import (
	"net/http"

	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/renderstore"
)

type destUsage struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RendersBytes int64  `json:"renders_bytes"`
	AssetsBytes  int64  `json:"assets_bytes"`
	Error        string `json:"error,omitempty"`
}

func handleAdminStorageUsage(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		freeBytes, _, err := deps.Cache.DiskUsage()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "disk usage check failed")
			return
		}

		dests, err := deps.Store.ListStorageDestinations(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list destinations failed")
			return
		}
		destUsages := make([]destUsage, 0, len(dests))
		for _, d := range dests {
			du := destUsage{ID: d.ID, Name: d.Name}
			rstore, ok := deps.Resolver.DestinationRenderStore(d.ID)
			if s3, isS3 := rstore.(*renderstore.S3RenderStore); ok && isS3 {
				objects, lerr := s3.ListAllObjects()
				if lerr != nil {
					du.Error = lerr.Error()
				} else {
					du.RendersBytes = renderstore.SumObjectSizesWithPrefix(objects, "renders/")
					du.AssetsBytes = renderstore.SumObjectSizesWithPrefix(objects, "assets/")
				}
			}
			destUsages = append(destUsages, du)
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"local": map[string]any{
				"free_bytes":    freeBytes,
				"repos_bytes":   dirSizeBytes(deps.Config.RepoCacheDir),
				"renders_bytes": dirSizeBytes(deps.Config.RenderDir),
				"assets_bytes":  dirSizeBytes(deps.Config.AssetDir),
			},
			"destinations": destUsages,
		})
	}
}
```

- [ ] **Step 4: Register the route** (if not already re-added)

In `backend/internal/api/router.go`, in the admin group:

```go
		r.Get("/api/admin/storage-usage", handleAdminStorageUsage(deps))
```

(If Task 6 removed the old `storage-usage` route, this re-adds it pointing at the new handler. If any prior `handleAdminStorageUsage` definition survives in `admin_storage.go`, delete it — this file now owns it.)

- [ ] **Step 5: Run test to verify it passes**

Run: `cd backend && go test ./internal/api/... -run TestAdminStorageUsage_localBreakdown -v`
Expected: PASS.

- [ ] **Step 6: Full suite**

Run: `cd backend && go build ./... && go test ./...`
Expected: builds clean, all pass.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/api/admin_storage_usage.go backend/internal/api/admin_storage.go backend/internal/api/router.go backend/internal/api/admin_storage_usage_test.go
git commit -m "feat: per-destination storage usage with error surfacing"
```

---

### Task 11: Frontend — destination management + per-repo selector + usage

**Files:**
- Modify: `frontend/src/api.ts`
- Rewrite: `frontend/src/views/storage-settings.ts`
- Modify: `frontend/src/views/repo-detail.ts` (add the per-repo destination selector)

**Interfaces:**
- Consumes: the Task 8/9/10 endpoints.
- Produces: a Storage admin page listing/creating/editing/deleting destinations and showing per-destination + local usage; a destination dropdown on each repo's detail view that calls the assign endpoint.

- [ ] **Step 1: Replace the storage API client in `api.ts`**

Remove the prior feature's `getStorageSettings`/`updateStorageSettings`/`StorageSettings`/`StorageSettingsUpdate` (the instance-wide endpoints are gone). Add:

```typescript
export interface StorageDestination {
  id: string;
  name: string;
  s3_endpoint: string;
  s3_bucket: string;
  s3_access_key: string;
  s3_region: string;
  s3_use_ssl: boolean;
}

export interface StorageDestinationInput {
  name: string;
  s3_endpoint: string;
  s3_bucket: string;
  s3_access_key: string;
  s3_secret_key: string; // blank on edit = keep existing
  s3_region: string;
  s3_use_ssl: boolean;
}

export interface StorageUsage {
  local: { free_bytes: number; repos_bytes: number; renders_bytes: number; assets_bytes: number };
  destinations: { id: string; name: string; renders_bytes: number; assets_bytes: number; error?: string }[];
}

export async function listStorageDestinations(): Promise<StorageDestination[]> {
  return json<StorageDestination[]>(await authedFetch('/api/admin/storage-destinations'));
}

export async function createStorageDestination(input: StorageDestinationInput): Promise<StorageDestination> {
  const resp = await authedFetch('/api/admin/storage-destinations', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input),
  });
  if (!resp.ok) throw new Error((await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }))).error ?? `HTTP ${resp.status}`);
  return resp.json() as Promise<StorageDestination>;
}

export async function updateStorageDestination(id: string, input: StorageDestinationInput): Promise<void> {
  const resp = await authedFetch(`/api/admin/storage-destinations/${id}`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input),
  });
  if (!resp.ok) throw new Error((await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }))).error ?? `HTTP ${resp.status}`);
}

export async function deleteStorageDestination(id: string): Promise<void> {
  const resp = await authedFetch(`/api/admin/storage-destinations/${id}`, { method: 'DELETE' });
  if (!resp.ok) throw new Error((await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }))).error ?? `HTTP ${resp.status}`);
}

export async function getStorageUsage(): Promise<StorageUsage> {
  return json<StorageUsage>(await authedFetch('/api/admin/storage-usage'));
}

export async function assignRepoStorage(repoId: string, destinationId: string | null): Promise<void> {
  const resp = await authedFetch(`/api/admin/repos/${repoId}/storage`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ storage_destination_id: destinationId }),
  });
  if (!resp.ok) throw new Error((await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }))).error ?? `HTTP ${resp.status}`);
}
```

(Match the exact error-handling idiom of the surrounding functions in `api.ts` — the sketch above follows the `updateRepo` pattern already in the file.)

- [ ] **Step 2: Rewrite `storage-settings.ts` as destination management + usage**

Replace the view's body with: (a) a usage panel calling `getStorageUsage()` rendering `Local: … (repos/renders/assets)` and one line per destination `name: renders + assets` (or `unavailable — <error>` when `error` is set); (b) a destinations list (name + bucket) each with Edit/Delete buttons; (c) an add/edit form (name, endpoint, bucket, access key, secret key [placeholder "leave blank to keep existing" on edit], region, use-SSL). Delete surfaces the 409 "in use" error inline. Follow the plain-DOM style of the existing `frontend/src/views/allowlist.ts` and the prior `storage-settings.ts` (formatting helper `formatBytes`, `document.createElement`, no framework). Keep the `#/storage` route and nav link already registered in `main.ts` from the prior feature — no `main.ts` change needed unless the nav label wording changes.

- [ ] **Step 3: Add the per-repo destination selector to `repo-detail.ts`**

On the repo detail view, add a "Storage" row: a `<select>` with `Local` + each destination (from `listStorageDestinations()`), defaulting to the repo's current `storage_destination_id`, plus a small status line showing the repo's `migration_status` when it is `running`/`failed`. On change, call `assignRepoStorage(repoId, selectedIdOrNull)` and show a "migrating…" state; on success, note it applies in the background. Read the current `repo-detail.ts` to match its layout/section conventions before inserting.

- [ ] **Step 4: Type-check and build**

Run: `cd frontend && npm run build`
Expected: `tsc --noEmit` passes; esbuild writes `../backend/frontend/static/app.js` with no errors.

- [ ] **Step 5: Manually verify in a browser**

Per this project's established practice (used throughout the prior storage feature), drive the built app against a mock backend (adapt the mock-server pattern used earlier for reader-note / storage-settings verification) and click through: Storage page loads with usage; add a destination (bad creds → inline validation error; good creds → appears in list); repo detail shows the destination dropdown and reflects a selection; delete an in-use destination → inline 409 message. If a live browser check truly can't run in the environment, say so explicitly in the report rather than claiming it.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/api.ts frontend/src/views/storage-settings.ts frontend/src/views/repo-detail.ts
git commit -m "feat: destination management UI + per-repo storage selector"
```

---

### Task 12: Rebuild deploy artifacts + README

**Files:**
- Modify: `backend/frontend/static/app.js` (regenerated)
- Modify: `backend/bin/pubobs-linux-amd64`, `backend/bin/pubobs-linux-arm64` (regenerated)
- Modify: `README.md`

**Interfaces:** none — packaging only.

- [ ] **Step 1: Rebuild the frontend bundle**

Run: `cd frontend && npm run build`
Expected: clean build; `../backend/frontend/static/app.js` updated.

- [ ] **Step 2: Rebuild the Linux binaries**

Run: `cd backend && make build`
Expected: `bin/pubobs-linux-amd64` and `bin/pubobs-linux-arm64` regenerated (they `go:embed` the new bundle).

- [ ] **Step 3: Update the README Storage section**

In `README.md`, update the "Storage" section to describe the new model: env vars (`PUBOBS_RENDER_STORE`/`PUBOBS_S3_*`/`PUBOBS_ASSET_DIR`) still bootstrap-only and, on upgrade, an S3-configured legacy setting becomes a "default" destination with all repos assigned to it. The admin Storage page now manages a *list* of S3 destinations; each repo picks Local or a destination on its detail page, and changing it migrates that repo's renders/assets in the background. Git checkouts remain local (disk reclaim for repos is a separate, still-pending change).

- [ ] **Step 4: Final full verification**

Run: `cd backend && go build ./... && go test ./... && cd ../frontend && npm run build`
Expected: all green.

- [ ] **Step 5: Commit**

```bash
git add backend/frontend/static/app.js backend/bin/pubobs-linux-amd64 backend/bin/pubobs-linux-arm64 README.md
git commit -m "build: rebuild deploy artifacts + document per-repo storage destinations"
```
