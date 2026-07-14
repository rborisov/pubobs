# Per-user git credentials — Phase 1 (backend foundation) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make server-side git commits attributed to the acting user (note edits authored by the editor, comments by the commenter) and pushable with a per-`(user, repo)` credential, with the repo owner's credential used for clone/reads and all comment pushes.

**Architecture:** Add an owner + per-user credential data model, thread an explicit author/committer identity and a separate push credential through `gitcache`, and wire the sync/comment handlers to resolve them. Git stays server-side. Full design: `docs/superpowers/specs/2026-07-14-per-user-git-credentials-design.md`.

**Tech Stack:** Go (SQLite via `database/sql`, `net/http/httptest`, testify), git shelled out via `os/exec`. Backend module `github.com/pubobs/backend`.

**Scope note:** This is **Phase 1 of 3**. Phase 2 = credential API endpoints + `git ls-remote` verification + admin per-editor status column. Phase 3 = plugin credential-entry UI + owner-transfer UI + per-repo cutover control. This plan delivers the data model + attribution + push-credential wiring, defaulting to **legacy mode** (editor pushes fall back to the owner cred) so nothing breaks before Phase 2/3 ship the UI to configure credentials.

## Global Constraints

- Migrations are idempotent `ALTER TABLE ... ADD COLUMN` / `CREATE TABLE IF NOT EXISTS` appended in `backend/internal/db/db.go` `Open()`, tolerating `"duplicate column name"` (see existing block). New installs also get them from that path.
- Credentials are encrypted with `auth.EncryptCreds(deps.Config.SecretKey, plaintext)` / decrypted with `auth.DecryptCreds(...)`; the plaintext credential is **never** returned in any API response.
- Commit **author** = the acting user; note-edit commits set committer = the editor too; comment commits set committer to `pubobs` (owner is the pusher, not necessarily a git identity we hold). The push **credential** for note edits is the editor's `(user,repo)` cred (or owner cred in legacy mode); for clone/reads and comments it is always the owner cred (`repos.EncryptedCreds`).
- Run backend tests from `backend/` with `go test ./...`. Existing git tests use `newBareRepo(t)`/`seedBareRepo(t,url)` helpers (api_test) and the gitcache bare-repo helpers.

---

### Task 1: Schema — owner, strict flag, per-user credentials table

**Files:**
- Modify: `backend/internal/db/db.go` (migration block in `Open()`)
- Test: `backend/internal/db/db_test.go`

**Interfaces:**
- Produces: new columns `repos.owner_user_id TEXT`, `repos.strict_credentials INTEGER NOT NULL DEFAULT 0`; new table `repo_user_credentials`.

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/db/db_test.go` (follow the existing test's `db.Open(...)` setup; adapt the open call to match the file's existing helper):

```go
func TestOpen_perUserCredentialSchema(t *testing.T) {
	dbConn, err := Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	defer dbConn.Close()

	// New repos columns exist.
	_, err = dbConn.Exec(`INSERT INTO repos (id, name, remote_url, encrypted_creds, default_branch, owner_user_id, strict_credentials) VALUES ('r1','R','u','','main','u1',1)`)
	require.NoError(t, err)

	// repo_user_credentials table exists with the expected columns.
	_, err = dbConn.Exec(`INSERT INTO repo_user_credentials (repo_id, user_id, encrypted_creds, git_name, git_email, verify_status, updated_at) VALUES ('r1','u1','enc','Al','a@x','unverified',CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	var n int
	require.NoError(t, dbConn.QueryRow(`SELECT COUNT(*) FROM repo_user_credentials WHERE repo_id='r1' AND user_id='u1'`).Scan(&n))
	require.Equal(t, 1, n)
}
```

Ensure `db_test.go` imports `path/filepath` and `github.com/stretchr/testify/require` (match the file's existing imports; if `Open` has a different signature there, mirror the existing test's call).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/db/ -run TestOpen_perUserCredentialSchema -v`
Expected: FAIL — `no column named owner_user_id` (or `no such table: repo_user_credentials`).

- [ ] **Step 3: Add the migrations**

In `backend/internal/db/db.go`, immediately before `return db, nil`, add:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/db/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/db/db.go backend/internal/db/db_test.go
git commit -m "feat: schema for repo owner + per-user git credentials"
```

---

### Task 2: Store methods — owner, strict flag, credentials

**Files:**
- Create: `backend/internal/store/git_credential.go`
- Modify: `backend/internal/store/repo.go` (owner + strict getters/setters; add fields to `model.Repo` scan if the repo struct is scanned by column list)
- Modify: `backend/internal/model/*.go` (add `OwnerUserID *string`, `StrictCredentials bool` to `Repo`)
- Test: `backend/internal/store/git_credential_test.go`

**Interfaces:**
- Produces:
  - `func (s *Store) SetRepoOwner(ctx, repoID, ownerUserID string) error`
  - `func (s *Store) SetRepoStrictCredentials(ctx, repoID string, strict bool) error`
  - `type UserCredential struct { RepoID, UserID, GitName, GitEmail, VerifyStatus, VerifyError string; VerifiedAt *time.Time }`
  - `func (s *Store) UpsertUserCredential(ctx, repoID, userID, encryptedCreds, gitName, gitEmail string) error`
  - `func (s *Store) GetUserCredentialSecret(ctx, repoID, userID string) (encryptedCreds string, ok bool, err error)`
  - `func (s *Store) DeleteUserCredential(ctx, repoID, userID string) error`
  - `func (s *Store) SetUserCredentialVerification(ctx, repoID, userID, status, errMsg string) error`
  - `func (s *Store) ListUserCredentials(ctx, repoID string) ([]UserCredential, error)` (status only — no secret)
- Consumes: `model.Repo` now carries `OwnerUserID *string`, `StrictCredentials bool` (Task-1 columns). Verify `noteColumns`-style repo column lists include the new columns so `GetRepo`/`ListRepos` scan them; if repo scanning uses an explicit column list, add `owner_user_id, strict_credentials` there and to `scanRepo`.

- [ ] **Step 1: Write the failing test**

`backend/internal/store/git_credential_test.go`:

```go
package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pubobs/backend/internal/db"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return New(conn)
}

func TestUserCredential_roundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, err := s.CreateRepo(ctx, "r1", "R", "https://x/r1.git", "", "main")
	require.NoError(t, err)

	// none yet
	_, ok, err := s.GetUserCredentialSecret(ctx, "r1", "u1")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, s.UpsertUserCredential(ctx, "r1", "u1", "ENC", "Alice", "a@x.com"))
	sec, ok, err := s.GetUserCredentialSecret(ctx, "r1", "u1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "ENC", sec)

	require.NoError(t, s.SetUserCredentialVerification(ctx, "r1", "u1", "verified", ""))
	list, err := s.ListUserCredentials(ctx, "r1")
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "Alice", list[0].GitName)
	require.Equal(t, "verified", list[0].VerifyStatus)

	require.NoError(t, s.DeleteUserCredential(ctx, "r1", "u1"))
	_, ok, _ = s.GetUserCredentialSecret(ctx, "r1", "u1")
	require.False(t, ok)
}

func TestRepoOwnerAndStrict(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, err := s.CreateRepo(ctx, "r1", "R", "https://x/r1.git", "", "main")
	require.NoError(t, err)
	require.NoError(t, s.SetRepoOwner(ctx, "r1", "u1"))
	require.NoError(t, s.SetRepoStrictCredentials(ctx, "r1", true))
	repo, err := s.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.NotNil(t, repo.OwnerUserID)
	require.Equal(t, "u1", *repo.OwnerUserID)
	require.True(t, repo.StrictCredentials)
}
```

(If `New(conn)` / `CreateRepo` signatures differ, match the existing store constructor and `CreateRepo` used elsewhere — e.g. `deps.Store.CreateRepo(ctx, id, name, url, creds, branch)`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/store/ -run 'TestUserCredential_roundtrip|TestRepoOwnerAndStrict' -v`
Expected: FAIL — undefined methods.

- [ ] **Step 3: Implement**

Add `OwnerUserID *string` and `StrictCredentials bool` to `model.Repo`, and ensure `scanRepo`/`GetRepo`/`ListRepos` select+scan `owner_user_id, strict_credentials` (mirror how `allow_guest`/`migration_status` are scanned).

Create `backend/internal/store/git_credential.go`:

```go
package store

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) SetRepoOwner(ctx context.Context, repoID, ownerUserID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE repos SET owner_user_id=? WHERE id=?`, ownerUserID, repoID)
	return err
}

func (s *Store) SetRepoStrictCredentials(ctx context.Context, repoID string, strict bool) error {
	v := 0
	if strict {
		v = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE repos SET strict_credentials=? WHERE id=?`, v, repoID)
	return err
}

type UserCredential struct {
	RepoID       string
	UserID       string
	GitName      string
	GitEmail     string
	VerifyStatus string
	VerifyError  string
	VerifiedAt   *time.Time
}

func (s *Store) UpsertUserCredential(ctx context.Context, repoID, userID, encryptedCreds, gitName, gitEmail string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO repo_user_credentials (repo_id, user_id, encrypted_creds, git_name, git_email, verify_status, updated_at)
		VALUES (?, ?, ?, ?, ?, 'unverified', ?)
		ON CONFLICT(repo_id, user_id) DO UPDATE SET
			encrypted_creds=excluded.encrypted_creds,
			git_name=excluded.git_name,
			git_email=excluded.git_email,
			verify_status='unverified', verify_error='', verified_at=NULL,
			updated_at=excluded.updated_at`,
		repoID, userID, encryptedCreds, gitName, gitEmail, time.Now().UTC())
	return err
}

func (s *Store) GetUserCredentialSecret(ctx context.Context, repoID, userID string) (string, bool, error) {
	var enc string
	err := s.db.QueryRowContext(ctx,
		`SELECT encrypted_creds FROM repo_user_credentials WHERE repo_id=? AND user_id=?`, repoID, userID).Scan(&enc)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return enc, true, nil
}

func (s *Store) DeleteUserCredential(ctx context.Context, repoID, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM repo_user_credentials WHERE repo_id=? AND user_id=?`, repoID, userID)
	return err
}

func (s *Store) SetUserCredentialVerification(ctx context.Context, repoID, userID, status, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE repo_user_credentials SET verify_status=?, verify_error=?, verified_at=? WHERE repo_id=? AND user_id=?`,
		status, errMsg, time.Now().UTC(), repoID, userID)
	return err
}

func (s *Store) ListUserCredentials(ctx context.Context, repoID string) ([]UserCredential, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT repo_id, user_id, git_name, git_email, verify_status, verify_error, verified_at
		 FROM repo_user_credentials WHERE repo_id=? ORDER BY user_id`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserCredential
	for rows.Next() {
		var c UserCredential
		if err := rows.Scan(&c.RepoID, &c.UserID, &c.GitName, &c.GitEmail, &c.VerifyStatus, &c.VerifyError, &c.VerifiedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/store/ -v`
Expected: PASS (new tests + existing store tests, confirming repo scanning still works with the new columns).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/store/git_credential.go backend/internal/store/repo.go backend/internal/model backend/internal/store/git_credential_test.go
git commit -m "feat: store methods for repo owner + per-user git credentials"
```

---

### Task 3: git.go — per-commit author/committer identity

**Files:**
- Modify: `backend/internal/gitcache/git.go`
- Test: `backend/internal/gitcache/git_test.go`

**Interfaces:**
- Produces: `AddCommitPush` gains identity params:
  `func (g *GitRunner) AddCommitPush(dir, remoteURL, pushCredJSON, branch, message, authorName, authorEmail, committerName, committerEmail string) (string, error)`
  Empty identity fields fall back to `pubobs` / `pubobs@localhost`.
- Consumes: existing `run`, `runNetwork`, `credentialedURL`.

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/gitcache/git_test.go` (uses the file's existing bare-repo helper — mirror `TestAddCommitPush`'s setup for `newBareRepo`/init):

```go
func TestAddCommitPush_setsAuthor(t *testing.T) {
	// Reuse the same local-bare-remote + clone setup TestAddCommitPush uses.
	remote := newBareRepo(t)
	dir := t.TempDir()
	g := NewGitRunner( /* same construction as existing tests */ )
	_, err := g.Clone(dir, remote, "", "main") // match existing Clone signature/helper
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "n.md"), []byte("# hi"), 0o644))

	_, err = g.AddCommitPush(dir, remote, "", "main", "msg", "Alice", "alice@example.com", "Alice", "alice@example.com")
	require.NoError(t, err)

	an, _ := g.run(dir, "log", "-1", "--format=%an")
	ae, _ := g.run(dir, "log", "-1", "--format=%ae")
	require.Equal(t, "Alice", strings.TrimSpace(an))
	require.Equal(t, "alice@example.com", strings.TrimSpace(ae))
}
```

If the existing `TestAddCommitPush` builds its runner/clone a specific way, copy that exact construction (this repo already has a passing `TestAddCommitPush`).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/gitcache/ -run TestAddCommitPush_setsAuthor -v`
Expected: FAIL — too many arguments to `AddCommitPush` (signature has no identity params yet).

- [ ] **Step 3: Implement**

In `git.go`, refactor `run` to route through an env-taking helper, and give `commit` a per-call identity. Replace the current `run` env block:

```go
func gitIdentityEnv(authorName, authorEmail, committerName, committerEmail string) []string {
	if authorName == "" {
		authorName, authorEmail = "pubobs", "pubobs@localhost"
	}
	if committerName == "" {
		committerName, committerEmail = "pubobs", "pubobs@localhost"
	}
	return []string{
		"GIT_AUTHOR_NAME=" + authorName,
		"GIT_AUTHOR_EMAIL=" + authorEmail,
		"GIT_COMMITTER_NAME=" + committerName,
		"GIT_COMMITTER_EMAIL=" + committerEmail,
		"GIT_TERMINAL_PROMPT=0",
	}
}

// runEnv is run() with an explicit identity/env set.
func (g *GitRunner) runEnv(dir string, identityEnv []string, args ...string) (string, error) {
	// ... identical body to run(), but: cmd.Env = append(os.Environ(), identityEnv...)
}

func (g *GitRunner) run(dir string, args ...string) (string, error) {
	return g.runEnv(dir, gitIdentityEnv("", "", "", ""), args...)
}
```

(Move the existing `run` body into `runEnv`; `run` keeps the previous fixed-`pubobs` behavior for every non-commit command.)

Then update `AddCommitPush`:

```go
func (g *GitRunner) AddCommitPush(dir, remoteURL, pushCredJSON, branch, message, authorName, authorEmail, committerName, committerEmail string) (string, error) {
	if _, err := g.run(dir, "add", "-A"); err != nil {
		return "", err
	}
	status, _ := g.run(dir, "status", "--porcelain")
	if status == "" {
		return g.run(dir, "rev-parse", "HEAD")
	}
	if _, err := g.runEnv(dir, gitIdentityEnv(authorName, authorEmail, committerName, committerEmail), "commit", "-m", message); err != nil {
		return "", err
	}
	authedURL := credentialedURL(remoteURL, pushCredJSON)
	if _, err := g.runNetwork(dir, g.fetchTimeout(), "push", authedURL, "HEAD:"+branch); err != nil {
		return "", err
	}
	_, _ = g.run(dir, "gc", "--auto")
	return g.run(dir, "rev-parse", "HEAD")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/gitcache/ -v`
Expected: PASS (new test + existing `TestAddCommitPush` — its callers are updated in Task 4, so temporarily the gitcache package's own callers must compile; there are none besides `Cache.Sync`/`AppendComment`, updated next, so build the package with those updated together if needed — if this task's package doesn't compile alone because `cache.go` calls the old signature, fold Task 3+4 into one commit).

> **Note for the implementer:** `AddCommitPush` is called from `cache.go` (`Sync`, `AppendComment`). Changing its signature breaks those calls, so **Tasks 3 and 4 compile/commit together** — implement both, then run tests. Keep them as one commit if the package won't build otherwise.

- [ ] **Step 5: Commit** (with Task 4)

---

### Task 4: Cache.Sync / AppendComment — clone cred vs push cred + identity

**Files:**
- Modify: `backend/internal/gitcache/cache.go`
- Test: `backend/internal/gitcache/cache_test.go`

**Interfaces:**
- Produces:
  - `func (c *Cache) Sync(ctx, repo *model.Repo, cloneCredJSON, pushCredJSON string, files []SyncFile, assets []SyncAsset, deletedPaths []string, commitMsg, authorName, authorEmail string) (string, error)` — clone/fetch with `cloneCredJSON` (owner), commit authored+committed as author, push with `pushCredJSON`.
  - `func (c *Cache) AppendComment(ctx, repo *model.Repo, cloneCredJSON, pushCredJSON, notePath, authorName, authorEmail, body, noteCommitSHA string) error` — commit author = comment author, committer = `pubobs`, push with `pushCredJSON` (owner).
- Consumes: `AddCommitPush(... authorName, authorEmail, committerName, committerEmail)` from Task 3.

- [ ] **Step 1: Write the failing test**

Add to `cache_test.go` (mirror the existing `TestCache_SyncAndListFiles`/`TestCache_AppendComment` bare-repo setup):

```go
func TestCache_Sync_authoredByUser(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)
	cache := gitcache.NewCache(t.TempDir())
	repo := &model.Repo{ID: "r1", RemoteURL: bareURL, DefaultBranch: "main"}

	_, err := cache.Sync(context.Background(), repo, "", "", []gitcache.SyncFile{{Path: "n.md", MDContent: "# hi"}},
		nil, nil, "sync", "Alice", "alice@example.com")
	require.NoError(t, err)

	// author of the last commit in the local clone is Alice
	// (read via a fresh runner or reuse cache internals through a helper if available)
}
```

Keep the assertion to what the test can reach; if `Cache` exposes no log helper, assert the call succeeds and add the author assertion in the git_test (Task 3) instead. The behavioral contract is proven in Task 3's `%an`/`%ae` check.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/gitcache/ -run TestCache_Sync_authoredByUser -v`
Expected: FAIL — signature mismatch.

- [ ] **Step 3: Implement**

In `cache.go` `Sync`: change the signature; call `getOrClone(repo, cloneCredJSON)`; build the commit via `AddCommitPush(dir, repo.RemoteURL, pushCredJSON, branch, commitMsg, authorName, authorEmail, authorName, authorEmail)` (author == committer == the editor). If `pushCredJSON == ""`, fall back to `cloneCredJSON` (legacy/owner) — a small guard so callers can pass `""` to mean "use owner":

```go
	if pushCredJSON == "" {
		pushCredJSON = cloneCredJSON
	}
```

In `AppendComment`: change the signature to take `cloneCredJSON, pushCredJSON`; clone with `cloneCredJSON`; commit with `AddCommitPush(dir, repo.RemoteURL, pushCredJSON, branch, msg, authorName, authorEmail, "pubobs", "pubobs@localhost")` (author = commenter, committer = pubobs); same `pushCredJSON == "" → cloneCredJSON` guard.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/gitcache/ -v`
Expected: PASS.

- [ ] **Step 5: Commit** (Tasks 3+4 together)

```bash
git add backend/internal/gitcache/git.go backend/internal/gitcache/git_test.go backend/internal/gitcache/cache.go backend/internal/gitcache/cache_test.go
git commit -m "feat: thread per-commit author identity + separate push credential through gitcache"
```

---

### Task 5: Sync handler — resolve editor credential, strict 403, wire identity

**Files:**
- Modify: `backend/internal/api/sync.go`
- Test: `backend/internal/api/sync_test.go`

**Interfaces:**
- Consumes: `Cache.Sync(... cloneCred, pushCred, ..., authorName, authorEmail)` (Task 4); `Store.GetUserCredentialSecret`, `Store.GetUserByID`, repo `OwnerUserID`/`StrictCredentials`, `decryptCreds`.

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/api/sync_test.go` (mirror the existing sync test that posts to `/api/repos/{id}/sync` with a bearer token and a bare-repo remote):

```go
func TestHandleSync_strictRejectsWhenNoUserCredential(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	bareURL := newBareRepo(t); seedBareRepo(t, bareURL)
	deps.Store.UpsertUser(ctx, "u1", "u1@x.com", "User One")
	deps.Store.CreateRepo(ctx, "r1", "R", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")
	require.NoError(t, deps.Store.SetRepoStrictCredentials(ctx, "r1", true)) // strict, no user cred

	body := strings.NewReader(`{"files":[{"path":"n.md","md_content":"# hi"}],"assets":[],"deleted_paths":[]}`)
	req := httptest.NewRequest("POST", "/api/repos/r1/sync", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "u1@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "credential")
}
```

(Match the existing sync test's deps/bootstrap and the `bearerHeader` helper.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run TestHandleSync_strictRejectsWhenNoUserCredential -v`
Expected: FAIL — currently returns 200/…; strict rejection not implemented.

- [ ] **Step 3: Implement**

In `handleSync`, after decrypting the owner cred (`credJSON` = owner service cred from `repo.EncryptedCreds`) and getting `user`, resolve the push credential + identity:

```go
	// Owner service credential (clone/reads + fallback push).
	ownerCred := credJSON

	// Push credential = the syncing user's own (user, repo) credential.
	pushCred := ""
	encUserCred, ok, uerr := deps.Store.GetUserCredentialSecret(r.Context(), repoID, claims.UserID)
	if uerr != nil {
		writeError(w, http.StatusInternalServerError, "credential lookup failed")
		return
	}
	if ok {
		if pushCred, err = auth.DecryptCreds(deps.Config.SecretKey, encUserCred); err != nil {
			writeError(w, http.StatusInternalServerError, "credential decrypt failed")
			return
		}
	} else if repo.StrictCredentials {
		writeError(w, http.StatusForbidden, "configure your git credential for this repo before publishing")
		return
	}
	// Legacy mode (not strict) with no user cred: pushCred stays "" → Cache.Sync
	// falls back to the owner cred.

	authorName := user.Name
	authorEmail := user.Email
	// (Phase 2 lets the user set git_name/git_email explicitly; until then use
	// their account identity.)

	sha, err := deps.Cache.Sync(r.Context(), repo, ownerCred, pushCred, cacheFiles, cacheAssets, payload.DeletedPaths, commitMsg, authorName, authorEmail)
```

(Ensure `auth` is imported in `sync.go` — it is used via `decryptCreds`→`auth.DecryptCreds`; import if not already direct.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/api/ -run 'TestHandleSync' -v`
Expected: PASS (new strict-reject test + existing sync tests, which run in legacy mode — no strict flag, no user cred → owner-cred fallback, unchanged behavior).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/sync.go backend/internal/api/sync_test.go
git commit -m "feat: sync pushes under the editor's own credential; strict mode rejects when unconfigured"
```

---

### Task 6: Comment handlers — commit author = commenter, push with owner cred

**Files:**
- Modify: `backend/internal/api/wiki.go` (`serveAddComment`), `backend/internal/api/pub.go` (`handlePubPostComment`)
- Test: `backend/internal/api/pub_test.go`

**Interfaces:**
- Consumes: `Cache.AppendComment(ctx, repo, cloneCred, pushCred, notePath, authorName, authorEmail, body, sha)` (Task 4). Both comment handlers pass the owner cred for both clone and push, and the resolved commenter name/email as the commit author.

- [ ] **Step 1: Write the failing test**

Add to `pub_test.go` (extends the existing `TestPubPostComment_*` bare-repo setup):

```go
func TestPubPostComment_gitAuthorIsCommenter(t *testing.T) {
	deps, _ := newTestDepsForPub(t)
	ctx := context.Background()
	bareURL := newBareRepo(t); seedBareRepo(t, bareURL)
	deps.Store.CreateRepo(ctx, "r1", "R", bareURL, "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", true))
	note, err := deps.Store.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "<h1>Hi</h1>", "{}", "u1", "sha1"))

	body := strings.NewReader(`{"body":"hello","note_commit_sha":"sha1","author_name":"Bob"}`)
	req := httptest.NewRequest("POST", "/pub/r1/notes/docs/intro.md/comments", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	// The comment commit exists and reads back (author attribution verified at
	// the gitcache layer in Task 3); assert the round-trip still works end-to-end.
	get := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md/comments", nil)
	gr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(gr, get)
	require.Contains(t, gr.Body.String(), "\"author_name\":\"Bob\"")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run TestPubPostComment_gitAuthorIsCommenter -v`
Expected: FAIL — `AppendComment` signature mismatch (compile error) until updated.

- [ ] **Step 3: Implement**

In both `serveAddComment` (wiki.go) and `handlePubPostComment` (pub.go), the credential is the owner service cred (`repo.EncryptedCreds` decrypted). Update the `AppendComment` call to the new signature, passing that cred for BOTH clone and push, and the resolved commenter `name`/`email` as author:

```go
	if err := deps.Cache.AppendComment(r.Context(), repo, credJSON, credJSON, notePath, name, email, body.Body, body.NoteCommitSHA); err != nil {
```

(`name`/`email` are already computed — `resolveCommenter` in pub.go, and `user.Name`/`user.Email` in wiki.go.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/api/ -v`
Expected: PASS (whole api package).

- [ ] **Step 5: Full backend suite + commit**

Run: `cd backend && go test ./...`
Expected: PASS.

```bash
git add backend/internal/api/wiki.go backend/internal/api/pub.go backend/internal/api/pub_test.go
git commit -m "feat: comment commits authored by the commenter, pushed with the owner credential"
```

---

### Task 7: Deploy build

- [ ] **Step 1: Full test suite**
Run: `cd backend && go test ./...` — expect PASS.

- [ ] **Step 2: Build binaries**
Run: `cd backend && make build` (rebuilds frontend bundle + linux binaries).

- [ ] **Step 3: Commit source bundle + binaries, push**
```bash
git add backend/frontend/static/app.js
git commit -m "build: rebuild frontend bundle" || true
git add backend/bin/pubobs-linux-amd64 backend/bin/pubobs-linux-arm64
git commit -m "build: rebuild deployed binaries with per-user commit attribution (phase 1)"
git push origin main
```

Note: Phase 1 defaults every repo to **legacy mode** (`strict_credentials=0`, no user creds configured), so behavior is unchanged except that commit **authors** are now the acting user instead of `pubobs`. Per-user push credentials only take effect once Phase 2 (API to set them) and Phase 3 (plugin UI + cutover) ship.

## Follow-on plans (not in this plan)

- **Phase 2:** `PUT/DELETE /api/repos/{id}/git-credential` (self), `POST .../git-credential/verify` (runs `git ls-remote` with the cred), `POST /api/admin/repos/{id}/owner` (transfer), a read endpoint for per-editor status; wire owner backfill on repo create (`owner_user_id = creator`).
- **Phase 3:** plugin settings UI to submit a credential + git name/email and surface the sync 403; admin repo-access page column showing per-editor verify status + owner/transfer + a per-repo "enable strict credentials" (cutover) toggle.
