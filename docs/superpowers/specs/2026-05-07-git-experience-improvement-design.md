# Git Experience Improvement — Design Spec
**Date:** 2026-05-07
**Scope:** Track 1 (items 1, 2, 3, 5 from git-experience-improvement-2026-05-07.md)
**Track 2** (branch-based PR collaboration, item 4) is deferred to a separate spec.

---

## 1. Shallow Clones

### Problem
The server-side git cache clones full repository history and accumulates it on disk indefinitely. Only the latest file state is needed for sync.

### Solution
Use `--depth=1` on all git clone and fetch operations in `gitcache`.

**Clone:**
```
git clone --depth=1 --single-branch {url} {dir}
```

**Update (replaces `git pull`):**
```
git fetch --depth=1 origin
git reset --hard origin/{branch}
```

### Tradeoffs
The `GET /api/repos/{id}/notes/{path}/history` endpoint runs `git log -- {path}`, which with depth=1 returns only the latest commit. This endpoint is not called by the plugin. It is **removed** — shallow clones are incompatible with meaningful history, and the feature can be reintroduced with a proper design later.

### Files affected
- `backend/internal/gitcache/cache.go` — clone and update logic
- `backend/internal/api/wiki.go` — remove history route handler
- `backend/internal/api/router.go` — remove history route registration

---

## 2. Plugin Metadata in Note Frontmatter

### Problem
Plugin requirements are stored in a centralized `_pubobs/note-plugins.json` file. This is opaque, not portable with the note, and ignored on pull.

### Solution
On sync, the plugin writes a `pubobs-plugins` field into each changed note's frontmatter before pushing. Plugin IDs and versions are read from `app.plugins.manifests`.

**Example frontmatter:**
```yaml
---
title: My Note
pubobs-plugins:
  - id: dataview
    version: "0.5.55"
  - id: templater-obsidian
    version: "1.16.0"
---
```

Only plugins actively used during rendering (already detected in `renderer.ts`) are written. Notes with no plugin-specific content get no `pubobs-plugins` field.

### Pull compatibility check
For each incoming note during sync, if `pubobs-plugins` is present in frontmatter:
1. For each required plugin, check if it is installed (`app.plugins.manifests`) and installed version ≥ required version (semver comparison, not string comparison).
2. If all pass: pull normally.
3. If any fail: show dialog — *"This note requires [plugin v0.0.0] which isn't installed. Create a local copy with a link to the original instead?"*

   - **Accepted:** create `{name}-local-copy.md` with frontmatter `pubobs-parent: {original-path}` and the same content. If the name already exists, append `-2`, `-3`, etc. This copy is never synced back to the server.
   - **Declined:** skip the note entirely (do not overwrite local version).

### Dropped
`_pubobs/note-plugins.json` is no longer written or read. The backend requires no changes — frontmatter passes through `sync.go` verbatim.

### Files affected
- `obsidian-plugin/src/renderer.ts` — expose detected plugin IDs
- `obsidian-plugin/src/sync.ts` — write `pubobs-plugins` into frontmatter before push; compatibility check on pull; local copy creation logic

---

## 3. Unified Sync Command

### Problem
Two separate commands ("Sync all repos" = push, "Pull all repos" = pull) are confusing. Users don't know which to run or in what order.

### Solution
A single "Sync" command per repo performs pull-then-push in one pass:

1. **Pull phase:** `GET /api/repos/{id}/files` — fetch server file list with blob SHAs. For each file where server SHA ≠ stored pull SHA: run compatibility check (Section 2), pull or offer copy. Update stored pull SHAs.
2. **Push phase:** For each local `.md` file where content hash ≠ stored sync hash: collect changed files, assets, and deleted paths. `POST /api/repos/{id}/sync`. Update stored sync hashes.

**Conflict (same note changed locally and on server):** Pull server version first, then push local version over it. Last-write-wins. Proper conflict resolution is deferred to Track 2.

### Commands
- "Sync all repos" — unified pull-then-push (replaces both old commands)
- "Pull all repos" — **removed**

### Files affected
- `obsidian-plugin/src/sync.ts` — merge `pullRepo` logic into `syncRepo`, delete `pullRepo`
- `obsidian-plugin/src/main.ts` — remove "Pull all repos" command registration

---

## 5. Stale Comment Marking

### Problem
Comments in `note-comments.md` companion files have no record of which note version they were written against. After a note is edited, old comments appear alongside new content with no indication they may be outdated.

### Solution
Each comment block stores the note's `git_commit_sha` at the time of posting as a 4th field in the header line.

**New format:**
```
### AuthorName | 2024-01-15T10:30:00Z | author@email.com | abc123de

Comment body
```

**Staleness check:** When listing comments, compare each comment's SHA against the current `note_snapshots.git_commit_sha`. If they differ, include `"is_outdated": true` in the response.

**Display:** Outdated comments are shown with muted styling and a label *"added before last edit"*. They are never hidden.

**Legacy comments:** Comments without a SHA (4th field absent) are treated as `is_outdated: false` — not marked outdated on upgrade, to avoid noise.

**API change:** `POST /api/repos/{id}/notes/*/comments` requires a `note_commit_sha` field in the request body. The note's current SHA is already available to callers via the note response (`git_commit_sha`).

### Files affected
- `backend/internal/gitcache/comments.go` — `AppendComment` writes SHA; `ParseComments` reads optional 4th field, returns `IsOutdated bool`
- `backend/internal/gitcache/cache.go` — pass current commit SHA into `AppendComment`
- `backend/internal/model/model.go` — add `IsOutdated bool` and `NoteCommitSHA string` to `Comment`
- `backend/internal/api/wiki.go` — `serveAddComment` reads `note_commit_sha` from request; `serveListComments` sets `is_outdated` by comparing SHAs
- `backend/internal/api/pub.go` — `handlePubComments` same staleness comparison
- `frontend/` — render outdated comments with muted style + label

---

## Out of Scope (Track 2)
- Branch-based PR workflow for non-admin editors
- Note locking during pending PRs
- Web merge UI
- Conflict resolution beyond last-write-wins
