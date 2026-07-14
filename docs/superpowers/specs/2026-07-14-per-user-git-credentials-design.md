# Per-user git credentials & commit attribution

**Date:** 2026-07-14
**Status:** Design — pending user review, not implemented.
**Supersedes the relevant part of:** `2026-07-08-server-side-git-removal-design.md` (which explored moving git into the plugin). That migration is **explicitly NOT pursued** — see Non-goals.

## Goal

Two problems with how PubObs writes to the git remote today:

1. **All commits are authored by a fixed placeholder** — `GIT_AUTHOR_NAME=pubobs / pubobs@localhost` is hard-coded in `git.go`'s `run()`. Every note sync and every comment, from every user, is attributed to "pubobs". We want each editor's note changes attributed to **that editor**, and each comment to **that commenter**.
2. **A single repo-level credential (the creator's) pushes everything.** We want each editor to **publish under their own git account/credential**, so the repo isn't tied to one admin account that could be revoked ("repo lives without admin"). The owner role is transferable.

Credentials are transferred to the server over HTTPS and stored **encrypted at rest** (same mechanism as today's repo `EncryptedCreds`) — git stays on the server; we do **not** move git into the plugin.

## Non-goals

- **Moving git to the plugin** (the `2026-07-08` migration). Not doing it. Git clone/commit/push stay server-side.
- **Disk reduction.** Handled separately as "Phase 0" (`git gc --auto` after commits + eviction tuning). Independent of this design.
- Comments leaving git. Comments stay as `-comments.md` files in git.

## Current state (verified in code)

- `model.Repo` has `EncryptedCreds` (one credential per repo, set at creation in `handleAdminCreateRepo`). **No owner field, no per-user credentials.**
- Access is a flat `repo_access(repo_id, principal_type, principal_id, role)` table; roles `reader < commentator < editor < admin` (`store/access.go`). `GetUserRole` returns the best role (direct or via group).
- Writes: `gitcache.Cache.Sync` (note push) and `Cache.AppendComment` (comment push) both call `GitRunner.AddCommitPush(dir, remoteURL, credJSON, branch, msg)`, which commits with the fixed `pubobs` identity (env in `git.go:313-318`) and pushes using `credJSON` = the repo's single `EncryptedCreds`.
- Reads: `getOrClone` clones/fetches with the same repo `EncryptedCreds`.
- `decryptCreds(deps, repo.EncryptedCreds)` decrypts at each call site.

## Core principle: separate READ credential from WRITE credential

| Operation | Credential used | Commit author | If missing |
|-----------|-----------------|---------------|------------|
| Clone / fetch (server keeps its working tree) | **Owner** service cred | — | repo unusable (owner must fix) |
| Plugin pull-phase / serving readers | none (served from server's owner-cloned content) | — | — |
| **Note sync push** (editor publishes edits) | **that editor's** `(user, repo)` cred | that editor | **reject the push** — "configure your git credentials to publish" |
| Comment push (anonymous or logged-in) | **Owner** service cred | the commenter's display/account name | — |

Reading is **never** blocked by a missing personal credential. Only publishing your own note edits requires your own credential.

## Data model changes

1. `repos.owner_user_id TEXT` (nullable during migration; set to the creator going forward). The repo's existing `EncryptedCreds` is re-framed as **the owner's service credential**. Owner transfer = update `owner_user_id` and (re)supply the service credential.

2. New table:

```sql
CREATE TABLE repo_user_credentials (
  repo_id         TEXT NOT NULL,
  user_id         TEXT NOT NULL,
  encrypted_creds TEXT NOT NULL,          -- same encryption as repos.EncryptedCreds
  git_name        TEXT NOT NULL DEFAULT '', -- author name; defaults to account name
  git_email       TEXT NOT NULL DEFAULT '', -- author email; defaults to account email
  verify_status   TEXT NOT NULL DEFAULT 'unverified', -- 'verified' | 'auth_failed' | 'unverified'
  verify_error    TEXT NOT NULL DEFAULT '',
  verified_at     TIMESTAMP,
  updated_at      TIMESTAMP NOT NULL,
  PRIMARY KEY (repo_id, user_id)
);
```

## Behavior

### Note sync (push) — `handleSync` / `Cache.Sync`
- The clone/fetch step uses the **owner's** service credential.
- The commit+push step uses the **syncing user's** `(user, repo)` credential and sets `GIT_AUTHOR_NAME/EMAIL` + `GIT_COMMITTER_NAME/EMAIL` to that user's `git_name`/`git_email`.
- If the syncing user has no `repo_user_credentials` row → **reject** with `403` + a clear message; the plugin surfaces "configure your git credentials for this repo to publish." No content is committed. (Reads earlier in the same sync still succeeded.)
- Consequence: `Cache.Sync` / `AddCommitPush` must accept a **push credential + author identity** distinct from the clone credential.

### Reads / clone
- `getOrClone` and all fetch/reset use the **owner's** service credential (unchanged source, just re-labeled). Readers and editors pull from the server; they never present a git credential.

### Comments — `AppendComment` (`wiki.go`, `pub.go`)
- **Always** push with the **owner's** service credential (decided — C1=B), for every commenter: anonymous, commentator, or editor. Set the commit **author** (name + email) to the commenter — anonymous display name (no/placeholder email), or the logged-in account's name + email — while the committer is the owner. No per-user git credential is ever required to comment, and author attribution stays correct (incl. GitHub, when the author email matches).

### Access verification (#3)
- `verifyCredential(repoID, userID)`: run `git ls-remote <authedURL>` (read-check) and/or a dry-run push against the remote with the user's credential; store `verify_status` + `verified_at` + `verify_error`.
- Triggered: when a user submits/updates their credential, and on demand from the admin page. (A push failure at sync time also updates the status.)
- The admin repo-access page shows, per editor/admin, a **git-write status** chip: verified / unverified / auth-failed (+ error tooltip).

### Owner & transfer
- On repo create, `owner_user_id` = creator; `EncryptedCreds` = the creator's service cred (as today).
- Admin action "transfer ownership" sets `owner_user_id` to another admin and prompts the new owner to supply the service credential.

## Backend changes

- **Schema/store:** migration for `repos.owner_user_id` + `repo_user_credentials`; store methods `SetRepoOwner`, `GetRepoOwner`, `UpsertUserCredential`, `GetUserCredential`, `DeleteUserCredential`, `ListUserCredentialStatuses(repoID)`.
- **git.go:** `run()` stops hard-coding the author; add a variant of `AddCommitPush` (or extend it) taking `pushCredJSON`, `authorName`, `authorEmail`, and pass `GIT_AUTHOR_*`/`GIT_COMMITTER_*` via env per call. Keep clone/fetch on the owner cred.
- **gitcache.Cache.Sync:** accept `(ownerCredJSON, pushCredJSON, authorName, authorEmail)`; clone with owner, push with pusher.
- **gitcache.Cache.AppendComment:** accept `authorName/authorEmail`; push with owner cred.
- **api/sync.go:** resolve the syncing user's `(user, repo)` credential; 403 if absent; pass owner cred + user cred + identity into `Cache.Sync`.
- **api/wiki.go, api/pub.go (comments):** pass the commenter's name; push with owner cred.
- **api credential endpoints:** `PUT/DELETE /api/repos/{id}/git-credential` (self, for editors/admins with access), `POST /api/repos/{id}/git-credential/verify`, `POST /api/admin/repos/{id}/owner` (transfer), and a read endpoint feeding the admin status column.
- Credential value is accepted only over the authenticated API, encrypted with the same key/scheme as `EncryptedCreds`, and **never returned** in any response (write-only; only status is readable).

## Plugin changes

- Settings UI (per configured repo): fields for git credential (username + token) and optional git name/email (defaults to the account). "Save & verify" → `PUT .../git-credential` then `.../verify`; show the verification result.
- On sync, surface a `403 credentials-required` from `/sync` as an actionable message pointing at that settings UI.

## Frontend (admin) changes

- Repo access page: a **git-write status** column per editor/admin (verified / unverified / auth-failed), and an owner row + "transfer ownership" control.

## Migration / cutover

Per-repo, not a global flip:

- Backfill `owner_user_id` for existing repos (best-effort: the creating admin if recorded, else an admin picks; the repo's existing `EncryptedCreds` stays as the owner service cred).
- A repo runs in **legacy mode** (editor pushes fall back to owner cred, as today) until it's explicitly **cut over** to strict per-user mode. After cutover, editors must have a verified credential to publish.
- This lets you migrate one repo at a time and let editors set up credentials before enforcement.

## Security considerations

- Per-user credentials are as sensitive as the repo credential; same encryption at rest, same "never echoed back" rule, transmitted only over HTTPS on authenticated endpoints.
- A user can only set/verify **their own** `(user, repo)` credential, and only for repos they have editor/admin access to. Admins can see status, never the secret.
- Verification must not leak the token in logs/errors.

## Testing

- Store: owner get/set; credential upsert/get/delete; status listing.
- git.go: author/committer env is set per-call; clone uses owner cred while push uses pusher cred (exercised against a bare-repo remote, asserting `git log --format=%an/%cn`).
- Sync handler: editor with a credential → commit authored as them; editor without → 403, nothing committed; reads unaffected.
- Comments: authored as the commenter, pushed via owner cred (bare-repo round-trip).
- Verification: valid cred → verified; bad cred → auth_failed with a sanitized error.

## Deploy note

`app.js` is `go:embed`'d into the server binary the Dockerfile copies — ship via commit source → `make build` → commit rebuilt `backend/bin/pubobs-linux-*` → push, or `install.sh --update` serves stale JS/binary.

## Open / confirmed decisions

- **Confirmed:** per-`(user, repo)` credential scope; owner cred for clone/reads and for all comment pushes; reject note-push when the editor has no credential; owner is transferable; git stays server-side.
- **C1 (decided → B):** all comments push with the owner cred, commit author = the commenter (name + email); the committer is the owner. No commenter ever needs a git credential. Per-user credentials are enforced only for note-edit pushes.
- **C2 (decided → legacy + per-repo cutover):** each repo runs in legacy mode (editor pushes fall back to the owner cred, as today) until an admin explicitly cuts that repo over to strict per-user mode. No global flip; editors set up credentials before their repo is enforced.
