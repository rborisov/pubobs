# Open comments to all note-readers + comment count badge

**Date:** 2026-07-09
**Status:** Approved (design)

## Goal

Improve comments in the PubObs web reader:

1. Anyone who can **read** a note can **read and write** comments on it — repo
   members, guest-open visitors, and share-link-only visitors (valid `?key=`).
2. Anonymous readers post under a display name (default `anonym`); logged-in
   users post under their account identity.
3. Each note row in the web reader note list shows a **comment count** badge.
4. Each `-comments.md` file carries a **wikilink to its parent note**, so
   Obsidian automatically surfaces the comments file as a backlink on the note.

Editing/deleting comments is done in Obsidian (out of scope for the web).

## Background: how comments work today

- Comments are stored as `path/to/note-comments.md` files in the repo's git
  clone (`gitcache.CommentsFilePath`), one `### name | RFC3339 | email | sha`
  header per comment (`gitcache.FormatComment` / `ParseComments`, split on
  `\n### `).
- **Read path:** `GET /pub/{repoId}/notes/{path}/comments` →
  `handlePubComments` (pub.go). The dispatcher (pub.go:195) currently gates it
  on `pubRepoAccess` — repo-level access only, so share-link-only visitors are
  denied.
- **Write path:** `POST /api/repos/{id}/notes/{path}/comments` →
  `serveAddComment` (wiki.go). Requires an authenticated user with `reader`
  role; uses that user's account name/email.
- **Frontend:** `reader-note.ts` `loadComments` reads via `/pub` (no key);
  the post form is shown only when `isAuthenticated()`, and posts via `/api`.
- Comment files are excluded from the notes DB at sync time (admin.go:125), so
  they are never listed as notes and never produce web backlinks.

## Access model

Comment read + write is gated on `pubNoteAccess(r, deps, repo, note)` — the
exact check that authorizes reading the note itself:

- repo-level access (guest-open repo, or bearer token with `reader`+), **or**
- the note is `shared_publicly` and the request carries a matching `?key=`.

This replaces the `pubRepoAccess`-only gate on the read dispatcher and is the
gate for the new write endpoint.

## Backend changes

### 1. Read comments — accept note-scoped access

`backend/internal/api/pub.go`, `handlePubGetNote` dispatcher (~line 195):
replace the `pubRepoAccess(...) == nil` guard for the `/comments` suffix with a
note-scoped check: load the note and require `pubNoteAccess(r, deps, repo,
note)`. `handlePubComments` itself is unchanged (it already reads and parses the
comments file). Update the comment at pub.go:196–198 that asserts share-link
visitors never see comments — that is the behavior being intentionally changed.

### 2. Write comments — new public endpoint

Add `POST /pub/{repoId}/notes/{path}/comments`, routed in the same place the
`/pub` GET note/comments routes are registered.

Handler logic:

1. Load repo; 404 if missing.
2. Load the note; require `pubNoteAccess(r, deps, repo, note)` — else 404
   (same "cannot enumerate / cannot access" shape as note read).
3. Decode `{ body, note_commit_sha, author_name }`; 400 if `body` is blank.
4. Determine identity:
   - If the request carries a valid bearer token that grants a repo role
     (reuse the `callerRepoRole` / token-verify logic), use that user's
     account `Name` / `Email`; **ignore** `author_name` (no spoofing a real
     account).
   - Otherwise (anonymous): `name = sanitizeCommentName(author_name)`, falling
     back to `anonym` when blank; `email = ""`.
5. `deps.Cache.AppendComment(...)` with the resolved name/email/body/sha.
6. 201 Created.

The existing `/api/.../comments` write path may remain for now but the frontend
stops using it; it can be retired in a later cleanup.

### 3. Display-name sanitization (security-critical)

Comment records are `name | RFC3339 | email | sha` split on `\n### `. A
user-controlled name containing `|`, a newline, or `### ` could forge fields or
whole comment blocks. Add `sanitizeCommentName(string) string` in `gitcache`
(next to `FormatComment`) that:

- strips CR/LF (replace with space),
- replaces `|` with a lookalike or space so it can't create a field boundary,
- trims, and collapses a leading `#`-run so a name can't start a `### ` header,
- caps length (e.g. 100 runes).

`FormatComment` (or `AppendComment`) applies it to the name defensively so any
caller is safe. Bodies remain safe: the web escapes + markdown-renders them
client-side (`esc` + `renderCommentBody`), and author names are already `esc`'d
on render.

### 4. Parent-note wikilink in comments file

`backend/internal/gitcache/comments.go`:

- Change `commentsFileHeader(notePath)` to embed a wikilink to the parent note
  so Obsidian creates the backlink automatically, e.g.:

  ```
  ---
  type: comments
  note: "[[<note basename without .md>]]"
  ---

  Comments on [[<note basename without .md>]]

  ```

  Use the note's link target as Obsidian expects (basename without `.md`; keep
  the existing `note:` path value semantics if any code reads it — verify no
  consumer parses `note:` as a bare path before changing its format; if one
  does, keep `note:` as the raw path and put the wikilink only in the body
  line).

- `AppendComment` (cache.go ~489): on create it already prepends the header. For
  an **existing** comments file that lacks the wikilink line, insert the
  `Comments on [[...]]` body line (idempotently) when appending, so older files
  gain the backlink going forward.

This only affects the vault/Obsidian view. Comment files are not indexed as
notes, so no web backlink appears and the web comment view is unchanged.

### 5. Comment count in note list

`backend/internal/api/handlePubListNotes` (pub.go):

- Build a `map[notePath]int` of comment counts by enumerating `*-comments.md`
  files once (approach A). Reuse the git file listing already available for
  reconciliation (e.g. `Cache.ListFilePaths`), filter to paths ending in
  `-comments.md`, read+`ParseComments` each, and map back to the parent note
  path (strip the `-comments.md` → `.md` suffix). Only files that exist are
  read, so cost scales with the number of *commented* notes, not all notes.
- Add `comment_count int json:"comment_count"` to the `noteItem` struct and
  populate it (0 when the note has no comments file).

## Frontend changes

`frontend/src/api.ts`:

- Add `comment_count: number` to `PubNote`.
- Add a public `addComment` variant hitting
  `POST /pub/{repoId}/notes/{path}/comments`, accepting `authorName` and an
  optional share `key`, used for all viewers. (Keep or replace the existing
  authed `addComment`.)
- `pubListComments(repoId, notePath, key?)` — pass `?key=` through when present.

`frontend/src/views/reader-note.ts`:

- `loadComments`: pass the effective share key so share-link visitors can read
  comments.
- Comment form: always render it (remove the `isAuthenticated()` gate that
  shows "Sign in to leave a comment"). For anonymous viewers, add a display-name
  `<input>` prefilled with `anonym`; hide it for logged-in users. Post via the
  `/pub` endpoint, sending the name (anonymous) and the effective key
  (share-link) along with body + `git_commit_sha`.

`frontend/src/views/reader-list.ts`:

- In `renderNoteList`, when `note.comment_count > 0`, render a small badge
  (e.g. `💬 {n}`) in the row's right-hand actions area, styled with the
  existing faint/tag styles. Nothing shown when count is 0.

## Testing

- **Backend unit:** `sanitizeCommentName` (pipes, newlines, `###`, length,
  blank→`anonym` handled at handler level); `ParseComments` round-trips a
  sanitized name; comments-file header contains the parent wikilink; count map
  derives correct note paths from `-comments.md` names.
- **Backend handler:** `pubNoteAccess` gating on read+write — share-link visitor
  with valid key can read/post; without key on a guest-closed repo is 404;
  logged-in identity overrides client `author_name`; anonymous blank name →
  `anonym`.
- **List:** `comment_count` populated for notes with/without comment files.
- **Manual/e2e:** open a shared note as an anonymous visitor, read existing
  comments, post one under a custom name and under default `anonym`; verify it
  appears and (in Obsidian) the note backlinks to its comments file; verify the
  count badge on the list.

## Deployment note

`app.js` is `go:embed`'d into the server binary the Dockerfile copies, so
shipping requires: commit source, then `make build` (backend), then commit the
rebuilt `backend/bin/pubobs-linux-*` binaries, then push — otherwise
`install.sh --update` ships stale JS.

## Out of scope

Web-side comment editing/deleting (done in Obsidian), rate-limiting / spam
control (openness accepted), and reworking the `commentator` role.
