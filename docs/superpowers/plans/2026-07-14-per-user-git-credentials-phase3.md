# Per-user git credentials — Phase 3 (frontend + plugin + cutover) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Phase-1/2 per-user-credential backend usable from the UI: editors enter their git credential in the Obsidian plugin (and verify it), admins see per-editor status + transfer ownership + flip a repo to strict mode, and the sync 403 becomes an actionable prompt. Also folds in the Phase-2 review Minors.

**Architecture:** A small backend increment (expose owner/strict on the repo API + a strict-cutover endpoint + two Minor fixes), then frontend admin-page additions and Obsidian plugin settings/sync changes. Full design: `docs/superpowers/specs/2026-07-14-per-user-git-credentials-design.md`. Builds directly on the `feature/per-user-git-creds-phase2` branch (Phases 1+2).

**Tech Stack:** Go backend (chi, `httptest`, testify); frontend TypeScript (esbuild bundle, `tsc --noEmit`); Obsidian plugin TypeScript (esbuild + jest). Backend module `github.com/pubobs/backend`.

## Global Constraints

- **This plan is implemented on top of the existing `feature/per-user-git-creds-phase2` branch** (do NOT branch from `main`; Phases 1+2 aren't merged yet). The whole feature (Phases 2+3) merges/deploys together at the end.
- Frontend and plugin have no shared HTTP test harness; frontend is verified by `cd frontend && npm run build` (runs `tsc --noEmit`), the plugin by `cd obsidian-plugin && npm test` (jest) + `npm run build`. Only the backend task is TDD with Go tests.
- Credential plaintext is submitted over HTTPS to the Phase-2 `PUT /api/repos/{id}/git-credential`; it is never stored client-side in plugin settings beyond what's needed to submit, and never rendered back.
- The Phase-2 review Minors folded in here: (a) owner-transfer target must be an admin; (b) keyed struct literal for `accessResp`; (c) the "verified" status reflects **read** access (`git ls-remote`) — label it accordingly in the UI, don't imply write access.

---

### Task 1: Backend — expose owner/strict, cutover endpoint, fold Minors

**Files:**
- Modify: `backend/internal/api/repos.go` (`handleListRepos` `repoResp`: add `owner_user_id`, `strict_credentials`)
- Modify: `backend/internal/api/admin.go` (keyed `accessResp` literal — Minor c)
- Modify: `backend/internal/api/admin_repo_owner.go` (owner-transfer target must be admin — Minor a)
- Create: `backend/internal/api/admin_repo_strict.go` (`handleAdminSetRepoStrictCredentials`)
- Modify: `backend/internal/api/router.go` (register `PUT /api/admin/repos/{id}/strict-credentials`)
- Test: `backend/internal/api/admin_test.go`

**Interfaces:**
- Consumes: `Store.SetRepoStrictCredentials` (Phase 1), repo `OwnerUserID`/`StrictCredentials`, `requireRepoManage`/`requireAdmin`, `User.IsAdmin`.
- Produces: `handleAdminSetRepoStrictCredentials(deps) http.HandlerFunc` — admin/manage-gated; body `{ "strict": true|false }`.

- [ ] **Step 1: Write the failing tests**

Add to `admin_test.go`:

```go
func TestAdminSetRepoStrictCredentials(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x/r1.git", "", "main")

	body := strings.NewReader(`{"strict":true}`)
	req := httptest.NewRequest("PUT", "/api/admin/repos/r1/strict-credentials", body)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	repo, _ := deps.Store.GetRepo(ctx, "r1")
	require.True(t, repo.StrictCredentials)
}

func TestListRepos_includesOwnerAndStrict(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x/r1.git", "", "main")
	deps.Store.SetRepoOwner(ctx, "r1", "admin1")
	deps.Store.SetRepoStrictCredentials(ctx, "r1", true)

	req := httptest.NewRequest("GET", "/api/repos", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), `"owner_user_id":"admin1"`)
	require.Contains(t, rr.Body.String(), `"strict_credentials":true`)
}

func TestAdminSetRepoOwner_rejectsNonAdminTarget(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.UpsertUser(ctx, "u2", "u2@x.com", "Plain") // not an admin
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x/r1.git", "", "main")

	req := httptest.NewRequest("POST", "/api/admin/repos/r1/owner", strings.NewReader(`{"owner_user_id":"u2"}`))
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "admin")
}
```

(Match `newTestDeps`/`bearerHeader`. `bearerHeader(..., isAdmin)` sets the instance-admin flag; verify how a user's `IsAdmin` is stored — if owner-transfer must check the *target* user's admin status, that comes from `Store.GetUserByID(...).IsAdmin` or `is_admin`/`is_instance_admin`; read `model.User` to use the right field. If "admin" for ownership means instance-admin OR repo-admin, check both — read `requireRepoManage`/`GetUserRole` and pick the check that matches "an admin can own"; simplest correct rule: target must be instance-admin OR have repo `admin` role.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/api/ -run 'TestAdminSetRepoStrictCredentials|TestListRepos_includesOwnerAndStrict|TestAdminSetRepoOwner_rejectsNonAdminTarget' -v`
Expected: FAIL — route 404 / fields absent / non-admin target currently accepted.

- [ ] **Step 3: Implement**

`repos.go` — add to `repoResp` and its literal (read the struct + how `OwnerUserID *string` should serialize; use `*string`/omitempty so nil → null or omitted):

```go
			OwnerUserID       *string `json:"owner_user_id"`
			StrictCredentials bool    `json:"strict_credentials"`
```
and populate from `repos[i].OwnerUserID` / `.StrictCredentials`.

`admin.go` — convert the `accessResp{...}` positional literal to keyed fields (Minor c):

```go
			out[i] = accessResp{
				ID: e.ID, RepoID: e.RepoID, PrincipalType: e.PrincipalType,
				PrincipalID: e.PrincipalID, Role: e.Role, GitCredential: gitCred,
			}
```

`admin_repo_owner.go` — after `GetUserByID` succeeds, require the target be an admin (Minor a). Determine "admin" per the note above (instance-admin OR repo `admin` role); e.g.:

```go
		targetAdmin := u.IsInstanceAdmin // or u.IsAdmin — use the real field
		if !targetAdmin {
			if role, _ := deps.Store.GetUserRole(r.Context(), body.OwnerUserID, repoID); role == "admin" {
				targetAdmin = true
			}
		}
		if !targetAdmin {
			writeError(w, http.StatusBadRequest, "owner must be an admin of the repo")
			return
		}
```

Create `admin_repo_strict.go` (mirror `handleAdminSetRepoGuestAccess`):

```go
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
)

func handleAdminSetRepoStrictCredentials(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		id := chi.URLParam(r, "id")
		if !requireRepoManage(r.Context(), deps, claims, id, w) {
			return
		}
		var body struct {
			Strict bool `json:"strict"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := deps.Store.SetRepoStrictCredentials(r.Context(), id, body.Strict); err != nil {
			writeError(w, http.StatusInternalServerError, "update failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

Register in `router.go` (admin group):

```go
		r.Put("/api/admin/repos/{id}/strict-credentials", handleAdminSetRepoStrictCredentials(deps))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/api/ -v`
Expected: PASS (new + existing).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/repos.go backend/internal/api/admin.go backend/internal/api/admin_repo_owner.go backend/internal/api/admin_repo_strict.go backend/internal/api/router.go backend/internal/api/admin_test.go
git commit -m "feat: expose repo owner/strict + strict-cutover endpoint; restrict owner-transfer to admins; keyed access literal"
```

---

### Task 2: Frontend API — types + owner/strict calls

**Files:**
- Modify: `frontend/src/api.ts`

**Interfaces:**
- Produces: `RepoAccess.git_credential?: string`; `Repo.owner_user_id?: string | null`, `Repo.strict_credentials?: boolean`; `setRepoOwner(id, ownerUserId)`, `setRepoStrictCredentials(id, strict)`.

- [ ] **Step 1:** Add `git_credential?: string;` to `RepoAccess` and `owner_user_id?: string | null; strict_credentials?: boolean;` to `Repo`.

- [ ] **Step 2:** Add calls (mirror `setRepoGuestAccess`):

```ts
export async function setRepoOwner(id: string, ownerUserId: string): Promise<void> {
  const resp = await authedFetch(`/api/admin/repos/${id}/owner`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ owner_user_id: ownerUserId }),
  });
  if (!resp.ok) throw new Error((await resp.text().catch(() => '')) || 'transfer failed');
}

export async function setRepoStrictCredentials(id: string, strict: boolean): Promise<void> {
  const resp = await authedFetch(`/api/admin/repos/${id}/strict-credentials`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ strict }),
  });
  if (!resp.ok) throw new Error((await resp.text().catch(() => '')) || 'update failed');
}
```

- [ ] **Step 3:** `cd frontend && npm run build` — expect zero TS errors.
- [ ] **Step 4: Commit**
```bash
git add frontend/src/api.ts
git commit -m "feat: frontend API for repo owner transfer + strict-credentials toggle"
```

---

### Task 3: Frontend admin page — status column, owner, strict toggle

**Files:**
- Modify: `frontend/src/views/repo-detail.ts`

**Interfaces:**
- Consumes: `RepoAccess.git_credential`, `Repo.owner_user_id`/`strict_credentials`, `setRepoOwner`, `setRepoStrictCredentials`, `listUsers`.

- [ ] **Step 1: Access table — git-credential status column.** In the access-table rendering (~line 220-246, where each `entry` row is built), add a cell showing `entry.git_credential` for `principal_type === 'user'`: map `verified`→"✓ verified (read)", `auth_failed`→"✗ auth failed", `unverified`→"• unverified", `none`/empty→"—". Add a matching header cell. (Label "verified (read)" per Minor c — it's `git ls-remote` read access.)

- [ ] **Step 2: Owner + transfer.** Near the repo header/settings, render the current owner (look up `repo.owner_user_id` in `users`, show email or "—") and a small "Transfer ownership" control: a `<select>` of admin users + a button calling `setRepoOwner(repo.id, selectedUserId)` then re-fetching. On error, show the message (e.g. "owner must be an admin of the repo").

- [ ] **Step 3: Strict-credentials toggle.** A checkbox/toggle "Require each editor's own git credential (strict)" bound to `repo.strict_credentials`, calling `setRepoStrictCredentials(repo.id, checked)` and updating local state on success; a short helper note that editors must configure a credential in the plugin before they can publish once strict is on.

- [ ] **Step 4:** `cd frontend && npm run build` — expect zero TS errors. (Follow the file's existing table/`grantForm` DOM patterns; keep styling consistent.)

- [ ] **Step 5: Commit**
```bash
git add frontend/src/views/repo-detail.ts backend/frontend/static/app.js
git commit -m "feat: admin page — per-editor git-credential status, owner transfer, strict toggle"
```

---

### Task 4: Plugin client — credential methods

**Files:**
- Modify: `obsidian-plugin/src/client.ts`

**Interfaces:**
- Produces (mirror the existing `request<T>` methods like `sync`/`shareNote`):
  - `setGitCredential(repoId, { username, token, gitName, gitEmail }): Promise<void>` → `PUT /api/repos/{repoId}/git-credential`
  - `deleteGitCredential(repoId): Promise<void>` → `DELETE .../git-credential`
  - `verifyGitCredential(repoId): Promise<{ status: string }>` → `POST .../git-credential/verify`

- [ ] **Step 1:** Add the three methods following the `request` pattern (auth header handled by `request`; body JSON for set). Payload keys: `username`, `token`, `git_name`, `git_email`.
- [ ] **Step 2:** `cd obsidian-plugin && npm run build` (and `npm test` if it has client tests) — expect clean.
- [ ] **Step 3: Commit**
```bash
git add obsidian-plugin/src/client.ts
git commit -m "feat: plugin client methods for git-credential set/delete/verify"
```

---

### Task 5: Plugin settings — per-repo credential entry

**Files:**
- Modify: `obsidian-plugin/src/settings.ts`

**Interfaces:**
- Consumes: `client.setGitCredential`/`verifyGitCredential`/`deleteGitCredential` (Task 4).

- [ ] **Step 1:** For each configured repo mapping (the existing `repoMappings` loop, ~line 65-90), add a "Git credential" sub-section: `Setting` rows for username, token (a password-type text input — do NOT persist the token in `this.plugin.settings`; hold it only in a local variable until submitted), optional git name + git email, and a "Save & verify" button that calls `client.setGitCredential(repoId, {...})` then `client.verifyGitCredential(repoId)` and shows the returned status via a `Notice`/inline text ("read access verified" / "authentication failed"). Add a "Remove credential" button calling `deleteGitCredential`.
- [ ] **Step 2:** `cd obsidian-plugin && npm run build` + `npm test` — expect clean.
- [ ] **Step 3: Commit**
```bash
git add obsidian-plugin/src/settings.ts
git commit -m "feat: plugin settings — per-repo git credential entry + verify"
```

---

### Task 6: Plugin sync — surface the strict 403

**Files:**
- Modify: `obsidian-plugin/src/sync.ts` (and/or `client.ts` where the sync response/error is handled)

**Interfaces:**
- Consumes: the `403` + message from `POST /sync` when strict mode is on and no credential is configured.

- [ ] **Step 1:** When `client.sync(...)` fails with a 403 whose body indicates a missing credential, show an actionable `Notice` (e.g. "PubObs: configure your git credential for this repo in Settings → to publish.") instead of a generic sync error. Read how `client.request` surfaces non-2xx (status/body) and how `sync.ts` currently reports sync errors, and branch on the 403.
- [ ] **Step 2:** `cd obsidian-plugin && npm run build` + `npm test` — expect clean.
- [ ] **Step 3: Commit**
```bash
git add obsidian-plugin/src/sync.ts obsidian-plugin/src/client.ts obsidian-plugin/main.js
git commit -m "feat: plugin surfaces strict-mode sync 403 as an actionable prompt"
```

(Note: `obsidian-plugin/main.js` is the built plugin bundle — rebuild it via the plugin's build before committing so the deployed plugin includes Tasks 4-6.)

---

### Task 7: Full verification + deploy build

- [ ] **Step 1:** `cd backend && go test ./...` — expect PASS.
- [ ] **Step 2:** `cd frontend && npm run build`; `cd obsidian-plugin && npm run build && npm test`.
- [ ] **Step 3: Manual check (record result):** as an admin, open a repo's page → see the per-editor status column, transfer control, and strict toggle; in the plugin, enter a credential + verify; flip strict on and confirm an editor without a credential gets the 403 prompt, and with a valid credential the commit is authored as them.
- [ ] **Step 4: Commit built bundles:**
```bash
git add backend/frontend/static/app.js obsidian-plugin/main.js && git commit -m "build: rebuild frontend + plugin bundles for phase 3" || true
```
- [ ] **Step 5: Merge the whole feature (Phases 1+2+3) to main, rebuild binaries, deploy** — via superpowers:finishing-a-development-branch: merge `feature/per-user-git-creds-phase2` to `main`, `cd backend && make build`, commit the rebuilt `backend/bin/pubobs-linux-*`, push `main`, then `install.sh --update` on the VPS.

## Notes

- The whole per-user-credentials feature (Phases 1+2+3) ships in one merge here, so this is the first time any of it reaches production. Do the final whole-branch review over the full `main..HEAD` range before merging.
- Deferred to a later pass (from the Phase-2 review): upgrading credential verification from `git ls-remote` (read) to a `git push --dry-run` (write) check; and sanitizing the raw git error currently returned in the sync 502 response.
