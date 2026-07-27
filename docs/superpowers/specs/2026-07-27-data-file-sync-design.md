# Data file sync: `.base`, `.csv`, `.json`, `.yaml` between repo and vault

**Date:** 2026-07-27
**Status:** approved, not yet implemented

## Problem

A repo's non-markdown files are invisible to the Obsidian vault in both
directions. Both halves of sync are markdown-only:

- **repo → vault:** `GET /api/repos/:id/files` calls `Cache.ListFiles`
  ([cache.go:392](../../../backend/internal/gitcache/cache.go)), whose glob is
  `git ls-files -z -- '*.md'`; the plugin then filters `.endsWith('.md')` again
  ([sync.ts:148](../../../obsidian-plugin/src/sync.ts)).
- **vault → repo:** the push phase enumerates `f.extension === 'md'`
  ([sync.ts:251](../../../obsidian-plugin/src/sync.ts)).

Binary assets do reach git, but only as a side effect of rendering: they are
images discovered inside a note being published, never files chosen for their
own sake.

So a `.base` (Obsidian Bases), `.csv`, `.json` or `.yaml` file committed to the
repo never appears in the vault, and one authored in the vault never reaches
the repo.

## Decision

Introduce **data files** as a third category alongside notes and assets,
synced bidirectionally, never published.

| | carried as | note row / key / render blob | reaches git |
|---|---|---|---|
| notes (`.md`) | text + encrypted HTML | yes | yes |
| assets (images) | base64 | no | yes |
| **data files** | **text** | **no** | **yes** |

### Why a separate category rather than widening `files`

Every entry in the sync payload's `files` array is run through `UpsertNote`,
`extractMetadata`, `UpsertSnapshot`, `UpsertNoteLinks`, `GetOrCreateNoteKey`
and a render-store write ([sync.go:161-194](../../../backend/internal/api/sync.go)).
A `.csv` sent that way becomes a note: it appears in the notes list, in the
reader, and in wiki-link resolution.

Keeping data files in their own field also keeps `reconcileNotesWithGit`
correct without touching it. That function prunes note rows whose paths are no
longer in `ListFilePaths`, which stays markdown-only under this design — so
data files, having no note rows, are simply outside its world.

### Rejected: sync every tracked file that isn't a note

Sweeps in `.gitignore`, `LICENSE`, and CI configs, and needs a base64 path for
binaries, since file content is carried as text today. The allowlist is a
setting precisely so new types cost a settings edit rather than a Go + TS
change, a binary rebuild and a redeploy.

## Backend

### `Cache.ListDataFiles(repo, cred, exts, maxBytes)`

New, alongside `ListFiles`. Returns `{files, skipped}`:

- includes only tracked files whose extension is in `exts`
- excludes `_pubobs/` and `.md`
- moves anything over `maxBytes`, or whose content is not valid UTF-8, into
  `skipped` (`{path, size, reason}`) rather than dropping it silently

`ListFiles` and `ListFilePaths` keep their `*.md` glob and are not modified, so
note ingestion, admin import and reconcile behave exactly as they do today.

### `GET /api/repos/:id/data-files?ext=base,csv,json,yaml,yml`

Reader role, mirroring `/files`. Each requested extension is validated
server-side against `^[a-z0-9]{1,10}$`, and `md` is rejected, so a client
setting cannot ask for anything unsafe. Responds
`{files: [{path, content, sha, size}], skipped: [...]}`.

The cap travels as an optional `max_bytes` parameter so the plugin setting
drives it, clamped server-side to a hard ceiling constant
(`gitcache.MaxDataFileBytes`, 25 MB). A client asking for more gets the
ceiling, not an error; omitting it means the ceiling. The same ceiling bounds
`data_files` entries on the sync path, so the server never writes an unbounded
client-supplied file regardless of what the client claims its limit is.

### `POST /api/repos/:id/sync` gains `data_files`

`data_files: [{path, content}]` — plain text, no `encrypted_html`, no
frontmatter, no note key. `Cache.Sync` writes them into the working tree
alongside `files` and `assets`; they land in the same commit. The size cap is
re-enforced server-side rather than trusted from the client.

Deletion needs no new field: `deleted_paths` already removes the file from the
working tree, and its `DeleteNote` / render-store calls are no-ops for a path
that never had a note row.

## Plugin

### Settings

- `dataFileExtensions` — comma-separated, default `base, csv, json, yaml, yml`
- `dataFileMaxMB` — default `5`

Global rather than per-repo: no evidence yet that one vault wants different
types per repo, and per-repo multiplies the settings UI.

### Pull phase

After the note pull, fetch data files and apply the protections notes already
get:

- skip when the returned SHA matches the stored `pullSHAs` entry
- skip when the local file's hash differs from the last-synced hash (unpushed
  local edits are never clobbered)
- write via the existing `repoPathToVaultPath` mapping, so data files land in
  the same vault folder as the repo's notes

Wrapped in its own try/catch, like the existing pull phase: a data-file
failure — including a `404` from a backend that predates this feature — warns
and lets the note sync complete.

### Push phase

Enumerate vault files under `vaultFolder` whose extension is allowlisted, hash
them with the existing `fnv1a` change detection, skip unchanged ones, and send
the rest as `data_files`. No render pass, no note key, no encryption.

### The deletion trap

`knownPaths` is built from `syncHashes` and `pullSHAs`, and any known path
missing from `currentRepoPaths` is reported in `deleted_paths`
([sync.ts:321-328](../../../obsidian-plugin/src/sync.ts)). `currentRepoPaths`
is populated from a `.md`-only vault listing.

Putting data files into those shared maps without also enumerating them in the
push phase would therefore report **every data file as deleted on the next
sync**, removing them from the repo. The push-phase enumeration is what makes
reusing the shared maps safe; the two changes cannot be split.

The same mechanism has a second door, found during implementation review: the
enumeration is gated on the extension list being non-empty, so *narrowing or
clearing* `dataFileExtensions` also drops those paths out of
`currentRepoPaths` while leaving them in `syncHashes` — deleting from the repo
every file of a type the user just stopped syncing. The settings hint invites
exactly this ("Leave empty to sync notes only").

**Removing an extension means "stop syncing these files", never "delete them
from the repo."** A settings filter must not be a destructive operation on a
git repo. So a known path is eligible for deletion only when this sync
actually enumerates it — a `.md` note, or a data file whose extension is
currently configured.

The accepted consequence: once an extension is de-configured, deleting such a
file from the vault no longer propagates to the repo. That follows directly
from "we no longer sync this type", and the path's `syncHashes` entry drops
out on the next successful sync, so the plugin stops tracking it entirely.

## Path traversal guard (folded in)

The backend has no traversal guard on client-supplied sync paths — not in
`gitcache`, not in `api`:

```go
fullPath := filepath.Join(dir, f.Path)   // cache.go:319, f.Path from the client
os.MkdirAll(filepath.Dir(fullPath), 0755)
os.WriteFile(fullPath, []byte(f.MDContent), 0644)
```

`filepath.Join("/data/repos/r1", "../../../etc/cron.d/x")` cleans to
`/etc/cron.d/x`, and the backend container runs as root. `deleted_paths`
reaches `os.Remove` by the same route. Any editor on any repo can reach both
today.

This predates the feature, but `data_files` adds a third caller to those exact
loops. One shared `validRepoPath` helper — rejecting absolute paths, any `..`
segment, and anything under `.git/` — is applied to `files`, `assets`,
`deleted_paths` and `data_files` together.

## Error handling

| condition | behavior |
|---|---|
| file over cap (either direction) | skipped, named in the sync Notice and console |
| content not valid UTF-8 | skipped server-side, reported in `skipped` |
| `/data-files` returns 404 (old backend) | warn, continue with note sync |
| data-file pull fails | warn, continue to push phase |
| invalid `ext` parameter | 400, no partial listing |

## Testing

**Backend**

- `ListDataFiles`: extension filtering, `_pubobs/` and `.md` exclusion, size
  cap, non-UTF-8 skip
- `/data-files`: reader-role enforcement, `ext` validation, `md` rejected
- sync with `data_files`: file reaches git, and creates **no** note row, note
  key or render blob
- deleting a data file via `deleted_paths` removes it from git
- `validRepoPath`: `../` escape, absolute path, and `.git/` writes are rejected
  for each of `files`, `assets`, `deleted_paths`, `data_files`

**Plugin (jest)**

- pull writes a data file to the mapped vault path
- pull does not overwrite a locally-edited data file
- push includes allowlisted files and excludes others
- a data file present in both vault and repo is never reported in
  `deleted_paths` (regression test for the trap above)
- over-cap files are skipped with a Notice naming them
- extension-setting parsing: whitespace, leading dots, empty entries, `md`

## Compatibility

- old plugin + new backend: no `data_files` in the payload, behavior unchanged
- new plugin + old backend: `/data-files` 404s, handled as above

## Deployment

Touches Go, the plugin, and plugin settings, so it needs the full path from
[README](../../../README.md): `make build`, commit
`backend/bin/pubobs-linux-*` in their own `build:` commit, `npm run build` in
`obsidian-plugin/`, keep the root `manifest.json` in sync with the plugin's on
the version bump, push, then run the updater on the VPS.

## Known follow-ups (open after merge)

Found by the final whole-branch review, triaged as non-blocking. Recorded
here rather than lost with the scratch workspace.

1. **Credentials still leak into the server log.** The response-side leak is
   closed (`gitcache.Redact` on both list endpoints), but the same
   credentialed remote URL is still written unredacted to the container log
   by `gitcache`'s own `log.Printf` calls — observed live during review.
   Anyone who can read container logs sees the repo owner's token. Predates
   this branch; deserves its own pass over every `log.Printf` that formats a
   git error.
2. **`ListDataFiles` has no aggregate response cap.** Every matched file is
   read fully into memory and marshalled at once; only the per-file ceiling
   is bounded. A repo with, say, 200 tracked 5 MB JSON files produces a
   multi-gigabyte allocation and can OOM the container. Fix: stop
   accumulating past a total-bytes budget and report the remainder in
   `skipped`.
3. **The pending-deletion branch can drop the only tracking record.** In the
   data-file pull, when no local file exists and the remote SHA has changed,
   `delete storedPullSHAs[path]` removes the last anchor if `syncHashes` has
   no entry — the deletion goes unreported and the next sync re-creates the
   file. Notes survive this because `noteKeys` is a third anchor; data files
   have only two. End state is self-consistent, so this undoes a deletion
   rather than destroying data.
4. **Re-enabling a de-configured extension can delete a repo file.**
   De-configuring drops `syncHashes` entries but leaves `pullSHAs` intact.
   Deleting the local file while the extension is off, then re-enabling it,
   reports the path as deleted. Either prune `pullSHAs` on de-configure, or
   keep the README's guarantee scoped to "while the extension remains
   removed" (currently the latter).
5. **Pull-side skip Notices repeat forever.** One Latin-1 or over-cap file in
   the repo produces a 10-second Notice on every sync, since skipped files
   never get a `pullSHAs` entry to suppress them.
6. **`:(icase)` can list both `data.csv` and `data.CSV`.** They collide on a
   case-insensitive vault filesystem. Requires an externally-created pair;
   worst case is one repo path's content replaced, recoverable from git.
7. **Unverified assumption:** that `vault.getFiles()` reflects a file created
   moments earlier by `vault.create()` in the same run. The note pull has
   shipped on it for many releases, but it is now asserted only in a test
   mock. Worth one manual in-vault confirmation.
