# Render Storage & Encryption Design

**Date:** 2026-06-21
**Status:** Approved

## Problem

Three issues to address:

1. **Empty repo initialization** — registering a new, empty Git repo in PubObs fails because `git clone --depth=1` of an empty remote leaves a repo with no HEAD. Subsequent operations (`git show HEAD:...`, `ListFiles`) fail. Users must manually push an initial commit before PubObs can use the repo.

2. **Rendered HTML in git** — the plugin currently renders notes to HTML and the backend commits `.html` files alongside `.md` files in the remote git repo. This bloats git history, couples the render pipeline to the git sync, and makes the remote repo harder to use independently.

3. **No configurable storage for rendered pages** — rendered HTML lives on the PubObs server under the git cache directory. When server disk space runs low there is no path to offload to external storage.

## Goals

- Auto-initialize empty git repos on first sync (any provider, git-native only)
- Remove rendered HTML from git entirely
- Store encrypted rendered HTML in a pluggable backend (local filesystem by default, S3-compatible for external storage)
- Per-note stable shared links: a URL + key pair generated once per note, valid forever, always pointing to the latest render
- Browser-side decryption: PubObs server never holds or serves plaintext HTML

## Non-Goals

- Gogs-specific API integration
- Consumer sync services (Google Drive, Yandex Disk native APIs) — S3-compatible object storage (Yandex Object Storage etc.) covers this via the S3 abstraction
- Moving assets (images, CSS) out of git — only rendered HTML pages
- Moving comments out of git

---

## Section 1: Auto-init Empty Repos

### Problem detail

`gitcache.Cache.getOrClone` runs `git clone --depth=1` which may succeed on an empty remote (exit 0) but produces a repo with no HEAD ref. Any subsequent `git show HEAD:path` or `git rev-parse HEAD` fails.

### Fix

`GitRunner` gets one new method:

```go
func (g *GitRunner) InitializeIfEmpty(dir, remoteURL, credJSON, branch string) error
```

After `Clone()`, `getOrClone` calls `RevParseHEAD`. If that fails, it calls `InitializeIfEmpty`, which:

1. `git commit --allow-empty -m "pubobs: initialize"`
2. `git push <authedURL> HEAD:<branch>`

All operations use plain `git` CLI — works against any remote that supports HTTPS push (GitHub, GitLab, Gitea, Gogs, bare git servers).

### Files changed

- `backend/internal/gitcache/git.go` — add `InitializeIfEmpty`
- `backend/internal/gitcache/cache.go` — call it in `getOrClone` after clone

---

## Section 2: RenderStore Abstraction

### Interface

New package `backend/internal/renderstore/`:

```go
type RenderStore interface {
    Write(repoID, notePath string, data []byte) error
    Read(repoID, notePath string) ([]byte, error)
    Delete(repoID, notePath string) error
}
```

### Implementations

**`LocalRenderStore`**
- Files stored at `<render_dir>/<repoID>/<notePath>.enc`
- Default render dir: `~/.pubobs/renders/` (or `/data/renders/` when `/data` exists)
- Configured via `PUBOBS_RENDER_DIR`

**`S3RenderStore`**
- Uses `minio-go` client — covers AWS S3, Yandex Object Storage, MinIO, and any S3-compatible service
- Object key: `<repoID>/<notePath>.enc`
- Required env vars when `PUBOBS_RENDER_STORE=s3`:
  - `PUBOBS_S3_ENDPOINT`
  - `PUBOBS_S3_BUCKET`
  - `PUBOBS_S3_ACCESS_KEY`
  - `PUBOBS_S3_SECRET_KEY`
  - `PUBOBS_S3_REGION` (optional)

### Config

`PUBOBS_RENDER_STORE=local` (default) or `s3`. Selected at startup in `config.go`; the chosen implementation is injected into `Deps` as `deps.RenderStore`.

### Git cache changes

`gitcache.Cache.Sync()` stops writing `.html` files to the local clone. `ReadRenderedHTML` is retained in `cache.go` as a read-only fallback for notes not yet re-synced after the upgrade — it is never called in the write path.

### New endpoint

```
GET /pub/:repoId/render/*notePath
```

Reads encrypted bytes from `RenderStore.Read`, serves as `application/octet-stream`. Uses the same `pubRepoAccess` guard as the existing note endpoint. No decryption happens server-side.

### Files changed

- `backend/internal/renderstore/` — new package (`store.go`, `local.go`, `s3.go`)
- `backend/internal/config/config.go` — add render store config fields
- `backend/internal/api/deps.go` — add `RenderStore renderstore.RenderStore`
- `backend/cmd/server/main.go` — construct and inject RenderStore
- `backend/internal/api/sync.go` — write encrypted bytes to RenderStore
- `backend/internal/api/pub.go` — new render endpoint; replace `html_content` with `render_url`/`render_key`
- `backend/internal/gitcache/cache.go` — remove HTML write from `Sync()`

---

## Section 3: Per-Note Client-Side Encryption

### Key lifecycle

- **First sync of a note**: generate random 256-bit AES-GCM key → inject `pubobs-render-url` and `pubobs-render-key` into frontmatter → commit to git alongside the markdown
- **Subsequent syncs (note changed)**: read existing `pubobs-render-key` from frontmatter → re-encrypt new HTML with same key → overwrite blob in RenderStore
- **Unchanged notes**: skip entirely, blob and frontmatter unchanged

The key is never rotated. A shared link remains valid forever and always shows the latest render.

### Encryption scheme

AES-256-GCM. Random 96-bit IV prepended to ciphertext. Final blob layout:

```
[12 bytes IV][ciphertext]
```

### Plugin changes (`obsidian-plugin/src/sync.ts`)

In `buildSyncFile`, before sending the sync payload:

1. Read `pubobs-render-key` from existing frontmatter (via `parseFrontmatter`)
2. If absent: `crypto.getRandomValues(new Uint8Array(32))` → base64url-encode → new key
3. Construct render URL: `<settings.serverURL>/pub/<repoId>/render/<repoPath>`
4. Inject `pubobs-render-url` and `pubobs-render-key` into the note's frontmatter (written to the `.md` file in git)
5. Encrypt HTML: AES-256-GCM with random IV → prepend IV → base64-encode
6. Send `encrypted_html` (base64) in the sync payload instead of `html_content`

The `SyncFile` type gains `encrypted_html: string` and drops `html_content`.

### Backend sync handler changes (`api/sync.go`)

- `syncFilePayload` replaces `html_content` JSON field with `encrypted_html`
- Decode base64 → `deps.RenderStore.Write(repoID, f.Path, bytes)`
- For each path in `deleted_paths`: call `deps.RenderStore.Delete(repoID, path)` in addition to `deps.Store.DeleteNote`
- No HTML is passed to `gitcache.Cache.Sync`

### Metadata extraction

`render_url` and `render_key` are standard frontmatter fields — the existing `extractMetadata` path stores them in `MetadataJSON` alongside other frontmatter. No schema changes.

---

## Section 4: Frontend Reader

### Note API response (`pub.go`)

`handlePubGetNote` reads `render_url` and `render_key` from `snap.MetadataJSON.frontmatter` and returns them in the response. The `html_content` field is omitted when both are present.

**Backward compatibility:** if `render_key` is absent from frontmatter (note not yet re-synced after upgrade), the handler falls back to `Cache.ReadRenderedHTML` / `snap.HTMLContent` exactly as today.

### Frontend decryption (`frontend/src/views/reader-note.ts`)

When response contains `render_url` + `render_key`:

```
1. fetch(render_url) → ArrayBuffer (encrypted bytes)
2. crypto.subtle.importKey("raw", base64url_decode(render_key), "AES-GCM", false, ["decrypt"])
3. iv = encrypted_bytes.slice(0, 12)
4. ciphertext = encrypted_bytes.slice(12)
5. crypto.subtle.decrypt({name:"AES-GCM", iv}, key, ciphertext) → plaintext bytes
6. new TextDecoder().decode(plaintext) → HTML string → set as note content
```

When response contains legacy `html_content`, render as today.

Helper: ~25-line `decryptRenderBlob(url, keyB64)` function, no new dependencies.

---

## Data Flow Summary (After Changes)

```
Plugin sync:
  note changed?
    → read/generate AES-GCM key
    → inject render_url + render_key into frontmatter
    → encrypt HTML (AES-256-GCM)
    → POST /sync { encrypted_html, md_content (with injected frontmatter), ... }
      → backend: RenderStore.Write(repoID, path, encryptedBytes)
      → gitcache.Sync: commit md_content (with frontmatter) + assets to git (no HTML)

Reader views note:
  GET /pub/:repoId/notes/:path
    → backend: return { render_url, render_key, ... } from MetadataJSON.frontmatter
  GET /pub/:repoId/render/:path
    → backend: RenderStore.Read → serve encrypted bytes
  Browser: fetch + decrypt → display HTML
```

## Migration

No data migration required. Existing notes continue to be served via the git-cache fallback until they are re-synced by the plugin, at which point they receive a key, their frontmatter is updated, and their HTML is moved to RenderStore.
