# Per-user git credentials — Phase 2 (backend API + verification) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Backend surface for per-user git credentials: repos get an owner (backfilled + transferable), editors can set/delete/verify their own `(user, repo)` credential (verified with `git ls-remote`), sync commits use the credential's git identity, and admins can read per-editor verification status.

**Architecture:** New self-service credential endpoints under `/api/repos/{id}/git-credential`, an admin owner-transfer endpoint, a `git ls-remote` auth-check in `gitcache`, and read-only status for the admin access listing. All backend + `httptest`/bare-repo tests. Full design: `docs/superpowers/specs/2026-07-14-per-user-git-credentials-design.md`. Builds on Phase 1 (schema + store methods + attribution wiring already merged).

**Tech Stack:** Go (chi router, SQLite, `net/http/httptest`, testify), git via `os/exec`. Backend module `github.com/pubobs/backend`.

**Scope note:** Phase 2 is **backend-only**. Phase 3 adds the frontend: admin per-editor status **column**, the self-service credential-entry form + plugin UI, and the per-repo strict-mode cutover toggle. Until Phase 3, credentials are set via the API directly (and by tests); repos stay in legacy mode.

## Global Constraints

- Credential secrets are encrypted with `auth.EncryptCreds(deps.Config.SecretKey, plaintext)` / decrypted with `auth.DecryptCreds`; the plaintext credential is **never** returned by any endpoint (write-only; only status/identity are readable).
- The stored credential plaintext is a JSON object `{"username":"...","password":"..."}` — the same shape `gitcache.credentialedURL` already parses (see `http_auth_test.go`). The set endpoint builds this from submitted `username` + `token`.
- Auth: self-service credential endpoints require the caller to have **editor+** on the repo (`requireRepoRole(ctx, deps, claims, repoID, "editor")`); a user may only set/delete/verify **their own** credential (keyed by `claims.UserID`). Owner transfer requires `requireAdmin`.
- Verification runs `git ls-remote` (network, read-only) and classifies auth failures via the existing `classifyGitError` → `ErrGitAuthFailed`. Never log the token.
- Run backend tests from `backend/` with `go test ./...`. Auth-gated remote tests use `newAuthRequiredGitServer(t, ...)` (gitcache `http_auth_test.go`); api tests use `newTestDeps`/`bearerHeader`.

---

### Task 1: git ls-remote credential check

**Files:**
- Modify: `backend/internal/gitcache/git.go` (add `LsRemote`)
- Modify: `backend/internal/gitcache/cache.go` (add `VerifyRemoteCredential`)
- Test: `backend/internal/gitcache/http_auth_test.go`

**Interfaces:**
- Produces:
  - `func (g *GitRunner) LsRemote(remoteURL, credJSON string) error` — runs `git ls-remote --heads <authedURL>` in a throwaway dir with the fetch timeout; returns `nil` on success, a `classifyGitError`-wrapped error (so auth rejection → `ErrGitAuthFailed`) otherwise.
  - `func (c *Cache) VerifyRemoteCredential(remoteURL, credJSON string) error` — thin wrapper over `c.git.LsRemote`.
- Consumes: `credentialedURL`, `runNetwork`, `fetchTimeout`, `classifyGitError`.

- [ ] **Step 1: Write the failing test**

Add to `http_auth_test.go` (reuse the `newAuthRequiredGitServer` setup from `TestAddCommitPush_pushUsesPushCredNotCloneCred`):

```go
func TestLsRemote_validVsInvalidCredential(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)
	srv := newAuthRequiredGitServer(t, bareURL, "validuser", "validpass")
	repoURL := srv.URL + "/remote.git"
	g := gitcache.NewGitRunner()

	require.NoError(t, g.LsRemote(repoURL, `{"username":"validuser","password":"validpass"}`))

	err := g.LsRemote(repoURL, `{"username":"validuser","password":"wrong"}`)
	require.Error(t, err)
	require.ErrorIs(t, err, gitcache.ErrGitAuthFailed)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/gitcache/ -run TestLsRemote_validVsInvalidCredential -v`
Expected: FAIL — `g.LsRemote undefined`.

- [ ] **Step 3: Implement**

In `git.go`:

```go
// LsRemote checks whether credJSON can reach remoteURL (read access), without
// needing a local clone. Auth rejection is returned as ErrGitAuthFailed via
// classifyGitError; used to verify a user's stored git credential.
func (g *GitRunner) LsRemote(remoteURL, credJSON string) error {
	authedURL := credentialedURL(remoteURL, credJSON)
	// ls-remote doesn't touch the working dir; run it in a throwaway temp dir.
	dir, err := os.MkdirTemp("", "pubobs-lsremote-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	_, err = g.runNetwork(dir, g.fetchTimeout(), "ls-remote", "--heads", authedURL)
	return err
}
```

(`runNetwork` already wraps errors through `classifyGitError`/`ErrGitOpTimedOut` — confirm by reading it; if it does not, wrap: `return classifyGitError(err)`.)

In `cache.go`:

```go
// VerifyRemoteCredential reports whether credJSON grants access to remoteURL.
func (c *Cache) VerifyRemoteCredential(remoteURL, credJSON string) error {
	return c.git.LsRemote(remoteURL, credJSON)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/gitcache/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/gitcache/git.go backend/internal/gitcache/cache.go backend/internal/gitcache/http_auth_test.go
git commit -m "feat: git ls-remote credential verification in gitcache"
```

---

### Task 2: Owner backfill on create + owner transfer endpoint

**Files:**
- Modify: `backend/internal/api/admin.go` (`handleAdminCreateRepo`: set owner = creator)
- Create: `backend/internal/api/admin_repo_owner.go` (`handleAdminSetRepoOwner`)
- Modify: `backend/internal/api/router.go` (register `POST /api/admin/repos/{id}/owner`)
- Test: `backend/internal/api/admin_test.go` (or a new `admin_repo_owner_test.go`)

**Interfaces:**
- Consumes: `Store.SetRepoOwner`, `Store.GetRepo`, `requireAdmin`, `claims.UserID`.
- Produces: `handleAdminSetRepoOwner(deps *Deps) http.HandlerFunc` — body `{ "owner_user_id": "<userID>" }`; validates the target user exists (`Store.GetUserByID`); sets owner.

- [ ] **Step 1: Write the failing test**

Add (mirror an existing admin repo test's bootstrap with an admin bearer token):

```go
func TestAdminSetRepoOwner(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.UpsertUser(ctx, "u2", "u2@x.com", "User Two")
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x/r1.git", "", "main")

	body := strings.NewReader(`{"owner_user_id":"u2"}`)
	req := httptest.NewRequest("POST", "/api/admin/repos/r1/owner", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true)) // isAdmin=true
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	repo, _ := deps.Store.GetRepo(ctx, "r1")
	require.NotNil(t, repo.OwnerUserID)
	require.Equal(t, "u2", *repo.OwnerUserID)
}
```

Also add an assertion (in an existing create-repo test, or a new one) that `handleAdminCreateRepo` sets `owner_user_id` to the creating admin's `claims.UserID`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run 'TestAdminSetRepoOwner' -v`
Expected: FAIL — route 404 / handler undefined.

- [ ] **Step 3: Implement**

In `handleAdminCreateRepo` (admin.go), after `CreateRepo` succeeds, set the owner to the creator:

```go
	if err := deps.Store.SetRepoOwner(r.Context(), repo.ID, claims.UserID); err != nil {
		// non-fatal: log; the repo is usable, owner can be set later via transfer
		log.Printf("set repo owner on create %s: %v", repo.ID, err)
	}
```

Create `admin_repo_owner.go`:

```go
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
)

func handleAdminSetRepoOwner(deps *Deps) http.HandlerFunc {
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
		var body struct {
			OwnerUserID string `json:"owner_user_id"`
		}
		if err := readJSON(r, &body); err != nil || body.OwnerUserID == "" {
			writeError(w, http.StatusBadRequest, "owner_user_id required")
			return
		}
		if u, err := deps.Store.GetUserByID(r.Context(), body.OwnerUserID); err != nil || u == nil {
			writeError(w, http.StatusBadRequest, "unknown user")
			return
		}
		if err := deps.Store.SetRepoOwner(r.Context(), repoID, body.OwnerUserID); err != nil {
			writeError(w, http.StatusInternalServerError, "set owner failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
```

Register in `router.go` (admin group, near the other `/api/admin/repos/{id}/...` routes):

```go
		r.Post("/api/admin/repos/{id}/owner", handleAdminSetRepoOwner(deps))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/api/ -run 'TestAdminSetRepoOwner|TestAdminCreateRepo' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/admin.go backend/internal/api/admin_repo_owner.go backend/internal/api/router.go backend/internal/api/admin_test.go
git commit -m "feat: repo owner backfill on create + admin owner-transfer endpoint"
```

---

### Task 3: Self-service set/delete git credential

**Files:**
- Create: `backend/internal/api/git_credential.go` (`handleSetGitCredential`, `handleDeleteGitCredential`)
- Modify: `backend/internal/api/router.go` (register `PUT`/`DELETE /api/repos/{id}/git-credential`)
- Modify: `backend/internal/store/git_credential.go` (add `GetUserCredentialGitIdentity`)
- Test: `backend/internal/api/git_credential_test.go`

**Interfaces:**
- Produces:
  - `handleSetGitCredential` — editor+ on repo; body `{ "username": "...", "token": "...", "git_name": "...", "git_email": "..." }`; builds `{"username","password":token}` JSON, encrypts, `UpsertUserCredential(repoID, claims.UserID, enc, gitName, gitEmail)`. 400 if username/token blank.
  - `handleDeleteGitCredential` — editor+; `DeleteUserCredential(repoID, claims.UserID)`.
  - `func (s *Store) GetUserCredentialGitIdentity(ctx, repoID, userID string) (name, email string, ok bool, err error)` — for the sync author (Task 5).
- Consumes: `requireRepoRole(ctx, deps, claims, repoID, "editor")`, `auth.EncryptCreds`, Phase-1 store methods.

- [ ] **Step 1: Write the failing test**

`backend/internal/api/git_credential_test.go`:

```go
func TestSetAndDeleteGitCredential(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "u1@x.com", "User One")
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x/r1.git", "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")

	// set
	body := strings.NewReader(`{"username":"u1","token":"tok","git_name":"One","git_email":"one@x.com"}`)
	req := httptest.NewRequest("PUT", "/api/repos/r1/git-credential", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "u1@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	enc, ok, _ := deps.Store.GetUserCredentialSecret(ctx, "r1", "u1")
	require.True(t, ok)
	require.NotEmpty(t, enc)
	require.NotContains(t, enc, "tok") // stored encrypted, not plaintext
	name, email, ok, _ := deps.Store.GetUserCredentialGitIdentity(ctx, "r1", "u1")
	require.True(t, ok)
	require.Equal(t, "One", name)
	require.Equal(t, "one@x.com", email)

	// a reader (no editor role) is forbidden
	deps.Store.UpsertUser(ctx, "u2", "u2@x.com", "Two")
	deps.Store.GrantAccess(ctx, "a2", "r1", "user", "u2", "reader")
	req2 := httptest.NewRequest("PUT", "/api/repos/r1/git-credential", strings.NewReader(`{"username":"x","token":"y"}`))
	req2.Header.Set("Authorization", bearerHeader(t, deps, "u2", "u2@x.com", false))
	rr2 := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr2, req2)
	require.Equal(t, http.StatusForbidden, rr2.Code)

	// delete
	del := httptest.NewRequest("DELETE", "/api/repos/r1/git-credential", nil)
	del.Header.Set("Authorization", bearerHeader(t, deps, "u1", "u1@x.com", false))
	dr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(dr, del)
	require.Equal(t, http.StatusOK, dr.Code)
	_, ok, _ = deps.Store.GetUserCredentialSecret(ctx, "r1", "u1")
	require.False(t, ok)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run TestSetAndDeleteGitCredential -v`
Expected: FAIL — routes 404 / `GetUserCredentialGitIdentity` undefined.

- [ ] **Step 3: Implement**

Add to `store/git_credential.go`:

```go
func (s *Store) GetUserCredentialGitIdentity(ctx context.Context, repoID, userID string) (string, string, bool, error) {
	var name, email string
	err := s.db.QueryRowContext(ctx,
		`SELECT git_name, git_email FROM repo_user_credentials WHERE repo_id=? AND user_id=?`, repoID, userID).Scan(&name, &email)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return name, email, true, nil
}
```

Create `api/git_credential.go`:

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
)

func handleSetGitCredential(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")
		if err := requireRepoRole(r.Context(), deps, claims, repoID, "editor"); err != nil {
			writeError(w, http.StatusForbidden, "editor role required")
			return
		}
		var body struct {
			Username string `json:"username"`
			Token    string `json:"token"`
			GitName  string `json:"git_name"`
			GitEmail string `json:"git_email"`
		}
		if err := readJSON(r, &body); err != nil || body.Username == "" || body.Token == "" {
			writeError(w, http.StatusBadRequest, "username and token required")
			return
		}
		credPlain, _ := json.Marshal(map[string]string{"username": body.Username, "password": body.Token})
		enc, err := auth.EncryptCreds(deps.Config.SecretKey, string(credPlain))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encrypt failed")
			return
		}
		if err := deps.Store.UpsertUserCredential(r.Context(), repoID, claims.UserID, enc, body.GitName, body.GitEmail); err != nil {
			writeError(w, http.StatusInternalServerError, "save failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
	}
}

func handleDeleteGitCredential(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")
		if err := requireRepoRole(r.Context(), deps, claims, repoID, "editor"); err != nil {
			writeError(w, http.StatusForbidden, "editor role required")
			return
		}
		if err := deps.Store.DeleteUserCredential(r.Context(), repoID, claims.UserID); err != nil {
			writeError(w, http.StatusInternalServerError, "delete failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}
```

Register in `router.go` (authenticated group):

```go
		r.Put("/api/repos/{id}/git-credential", handleSetGitCredential(deps))
		r.Delete("/api/repos/{id}/git-credential", handleDeleteGitCredential(deps))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/api/ -run TestSetAndDeleteGitCredential -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/git_credential.go backend/internal/api/router.go backend/internal/store/git_credential.go backend/internal/api/git_credential_test.go
git commit -m "feat: self-service set/delete per-user git credential"
```

---

### Task 4: Verify endpoint

**Files:**
- Modify: `backend/internal/api/git_credential.go` (`handleVerifyGitCredential`)
- Modify: `backend/internal/api/router.go` (register `POST /api/repos/{id}/git-credential/verify`)
- Test: `backend/internal/api/git_credential_test.go`

**Interfaces:**
- Produces: `handleVerifyGitCredential` — editor+; loads the caller's stored cred, decrypts, runs `deps.Cache.VerifyRemoteCredential(repo.RemoteURL, cred)`, then `Store.SetUserCredentialVerification(repoID, userID, status, errMsg)` where status is `"verified"` or `"auth_failed"`; returns the status. 404 if no credential stored.
- Consumes: `Cache.VerifyRemoteCredential` (Task 1), `GetUserCredentialSecret`, `SetUserCredentialVerification`, `ErrGitAuthFailed`.

- [ ] **Step 1: Write the failing test**

Add (uses an auth-gated remote so verification is real):

```go
func TestVerifyGitCredential(t *testing.T) {
	deps, _ := newTestDepsForPub(t) // gives a real Cache
	ctx := context.Background()
	bareURL := newBareRepo(t); seedBareRepo(t, bareURL)
	srv := newAuthRequiredGitServer(t, bareURL, "validuser", "validpass")
	repoURL := srv.URL + "/remote.git"
	deps.Store.UpsertUser(ctx, "u1", "u1@x.com", "One")
	deps.Store.CreateRepo(ctx, "r1", "R", repoURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")

	setCred := func(user, pass string) {
		body := strings.NewReader(`{"username":"` + user + `","token":"` + pass + `"}`)
		req := httptest.NewRequest("PUT", "/api/repos/r1/git-credential", body)
		req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "u1@x.com", false))
		rr := httptest.NewRecorder()
		api.BuildRouter(deps).ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	}
	verify := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/repos/r1/git-credential/verify", nil)
		req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "u1@x.com", false))
		rr := httptest.NewRecorder()
		api.BuildRouter(deps).ServeHTTP(rr, req)
		return rr
	}

	setCred("validuser", "validpass")
	rr := verify()
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "verified")
	list, _ := deps.Store.ListUserCredentials(ctx, "r1")
	require.Equal(t, "verified", list[0].VerifyStatus)

	setCred("validuser", "wrongpass") // re-save resets to unverified
	rr2 := verify()
	require.Equal(t, http.StatusOK, rr2.Code)
	require.Contains(t, rr2.Body.String(), "auth_failed")
}
```

(`newAuthRequiredGitServer` lives in the gitcache test package. If it's not exported to `api_test`, replicate its minimal handler in the api test helpers, or move verification tests that need it to the gitcache layer and keep the api test asserting status transitions with a stubbed Cache — implementer chooses based on what's reachable; prefer the real auth server if importable.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run TestVerifyGitCredential -v`
Expected: FAIL — verify route 404.

- [ ] **Step 3: Implement**

Add to `git_credential.go`:

```go
func handleVerifyGitCredential(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")
		if err := requireRepoRole(r.Context(), deps, claims, repoID, "editor"); err != nil {
			writeError(w, http.StatusForbidden, "editor role required")
			return
		}
		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}
		enc, ok, err := deps.Store.GetUserCredentialSecret(r.Context(), repoID, claims.UserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "lookup failed")
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "no credential configured")
			return
		}
		cred, err := auth.DecryptCreds(deps.Config.SecretKey, enc)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "decrypt failed")
			return
		}
		status, errMsg := "verified", ""
		if verr := deps.Cache.VerifyRemoteCredential(repo.RemoteURL, cred); verr != nil {
			status = "auth_failed"
			errMsg = "could not authenticate to the remote" // never echo the token/raw error verbatim
		}
		if serr := deps.Store.SetUserCredentialVerification(r.Context(), repoID, claims.UserID, status, errMsg); serr != nil {
			writeError(w, http.StatusInternalServerError, "status write failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": status})
	}
}
```

Register:

```go
		r.Post("/api/repos/{id}/git-credential/verify", handleVerifyGitCredential(deps))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/api/ -run TestVerifyGitCredential -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/git_credential.go backend/internal/api/router.go backend/internal/api/git_credential_test.go
git commit -m "feat: verify per-user git credential via git ls-remote"
```

---

### Task 5: Sync uses the credential's git identity; fold in deferred Minors

**Files:**
- Modify: `backend/internal/api/sync.go`
- Modify: `backend/internal/gitcache/git.go` (`gitIdentityEnv` email fallback)
- Test: `backend/internal/api/sync_test.go`, `backend/internal/gitcache/git_test.go`

**Interfaces:**
- Consumes: `Store.GetUserCredentialGitIdentity` (Task 3).

- [ ] **Step 1: Write the failing tests**

Add a `git_test.go` case that a non-empty email survives an empty name:

```go
func TestGitIdentityEnv_keepsEmailWhenNameEmpty(t *testing.T) {
	env := gitIdentityEnv("", "alice@example.com", "", "")
	joined := strings.Join(env, "\n")
	require.Contains(t, joined, "GIT_AUTHOR_EMAIL=alice@example.com")
	require.NotContains(t, joined, "GIT_AUTHOR_EMAIL=pubobs@localhost")
}
```

(`gitIdentityEnv` is package-private in `gitcache`; put this test in `package gitcache` — check whether `git_test.go` is `package gitcache` or `gitcache_test`; if the latter, add an internal `git_internal_test.go` with `package gitcache`.)

Add a `sync_test.go` case that the stored git identity is used as author when present (assert via reading the pushed commit's `%an`/`%ae` against a bare repo — mirror the Phase-1 attribution test approach; or, if reaching git log from the api test is awkward, assert the store identity is looked up by making the handler behavior observable — simplest: set a credential with `git_name="Custom"/git_email="custom@x"`, sync against a bare repo, and read `%an/%ae` from the server's clone dir). Keep the assertion to what the api test can reach; the identity-selection logic itself is small.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/gitcache/ -run TestGitIdentityEnv_keepsEmailWhenNameEmpty -v`
Expected: FAIL — current fallback keys on name only, so email becomes `pubobs@localhost`.

- [ ] **Step 3: Implement**

In `git.go` `gitIdentityEnv`, fall back per-field:

```go
func gitIdentityEnv(authorName, authorEmail, committerName, committerEmail string) []string {
	if authorName == "" {
		authorName = "pubobs"
	}
	if authorEmail == "" {
		authorEmail = "pubobs@localhost"
	}
	if committerName == "" {
		committerName = "pubobs"
	}
	if committerEmail == "" {
		committerEmail = "pubobs@localhost"
	}
	return []string{
		"GIT_AUTHOR_NAME=" + authorName,
		"GIT_AUTHOR_EMAIL=" + authorEmail,
		"GIT_COMMITTER_NAME=" + committerName,
		"GIT_COMMITTER_EMAIL=" + committerEmail,
		"GIT_TERMINAL_PROMPT=0",
	}
}
```

In `sync.go`, harden the nil-deref (deferred Phase-1 Minor) and prefer the stored git identity:

```go
	user, err := deps.Store.GetUserByID(r.Context(), claims.UserID)
	if err != nil || user == nil {
		writeError(w, http.StatusInternalServerError, "user lookup failed")
		return
	}
	// ... existing commitMsg, ownerCred, pushCred resolution ...

	authorName := user.Name
	authorEmail := user.Email
	if gn, ge, ok, _ := deps.Store.GetUserCredentialGitIdentity(r.Context(), repoID, claims.UserID); ok {
		if gn != "" {
			authorName = gn
		}
		if ge != "" {
			authorEmail = ge
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/gitcache/ ./internal/api/ -v` (focused runs, then the packages)
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/sync.go backend/internal/gitcache/git.go backend/internal/api/sync_test.go backend/internal/gitcache/git_test.go
git commit -m "feat: sync author uses stored git identity; harden nil-deref + per-field identity fallback"
```

---

### Task 6: Admin read — per-editor credential status in the access listing

**Files:**
- Modify: `backend/internal/api/admin.go` (`handleAdminListRepoAccess` — augment with credential status), or add a small `handleAdminListRepoGitCredentials`
- Test: `backend/internal/api/admin_test.go`

**Interfaces:**
- Consumes: `Store.ListUserCredentials(repoID)` (Phase 1), the repo's `OwnerUserID`.
- Produces: the admin access-list response gains, per user, their `git_credential` status (`verified` | `auth_failed` | `unverified` | `none`) and the repo's `owner_user_id`. Never includes the secret.

- [ ] **Step 1: Write the failing test**

Read `handleAdminListRepoAccess` and its existing test first; then add a test that after granting `u1` editor and upserting+verifying a credential, `GET /api/admin/repos/r1/access` (admin token) includes `u1`'s git-credential status `verified` and the repo `owner_user_id`. Assert on the JSON shape the handler actually returns (match its existing structure).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run 'TestAdminListRepoAccess' -v`
Expected: FAIL — status/owner fields absent.

- [ ] **Step 3: Implement**

Extend the access-list handler's response: fetch `ListUserCredentials(repoID)` into a `map[userID]status`, add a `git_credential` field per access entry (default `"none"` when the user has no row), and include `owner_user_id` at the top level. Keep it read-only; do not select or return `encrypted_creds`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/api/ -v`
Expected: PASS.

- [ ] **Step 5: Full suite + commit**

Run: `cd backend && go test ./...` — expect PASS.

```bash
git add backend/internal/api/admin.go backend/internal/api/admin_test.go
git commit -m "feat: expose per-editor git-credential status + owner in admin access listing"
```

---

### Task 7: Deploy build

- [ ] **Step 1:** `cd backend && go test ./...` — expect PASS.
- [ ] **Step 2:** `cd backend && make build`.
- [ ] **Step 3:** commit rebuilt binaries (+ app.js if changed) and push:
```bash
git add backend/frontend/static/app.js && git commit -m "build: rebuild frontend bundle" || true
git add backend/bin/pubobs-linux-amd64 backend/bin/pubobs-linux-arm64
git commit -m "build: rebuild deployed binaries with per-user git-credential API (phase 2)"
git push origin main
```

Phase 2 still ships with every repo in **legacy mode**; the new endpoints exist and work, but there's no UI to reach them yet — that's Phase 3.

## Follow-on plan (Phase 3, not here)

Frontend/plugin: admin repo-access page **column** showing each editor's git-credential status + an owner display and "transfer ownership" control; a self-service credential-entry form (web) and the Obsidian plugin settings UI that `PUT`s the credential and calls verify, surfacing the sync `403`; and a per-repo "enable strict credentials" (cutover) toggle wired to `SetRepoStrictCredentials`.
