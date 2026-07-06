# S3-backed storage for render blobs and media assets

## Goal

Reduce VPS disk usage by letting render blobs (encrypted note HTML) and media
assets (images, etc.) be stored on S3-compatible object storage instead of
local disk, configurable from the admin panel (not just env vars), with
existing local data migratable to S3 on demand.

## Scope

**In scope:**

- Render blobs — already have a working `RenderStore` local/S3 abstraction
  (env-var configured only); needs DB-backed settings, admin-panel UI, and a
  migration path for existing data.
- Media/assets — currently unencrypted and living *inside* each repo's git
  working-tree checkout, served by reading directly from that checkout. This
  needs a new, dedicated store (mirroring the render-blob pattern), decoupled
  from the git checkout, with server-side encryption at rest.
- Admin panel: new "Storage" settings page (instance-admin only) to configure
  the backend and trigger migration, plus a local/S3 disk-usage breakdown.

**Out of scope:**

- Git repo checkouts (`backend/data/repos`) stay on local disk. They are a
  disposable, re-clonable working cache of the user's own external git
  remote (not primary data), already covered by the existing TTL- and
  disk-pressure-based eviction job (`backend/internal/jobs/eviction.go`).
  Making live git operations (clone/fetch/checkout) work against an
  S3-backed virtual filesystem would be a large, risky undertaking for data
  that's already reclaimable — not worth it here.

## Non-goals

- No change to how render blobs are encrypted. They are already encrypted
  client-side by the Obsidian plugin (AES-GCM) before upload, with the
  decryption key embedded only in the share URL fragment
  (`#/read/{repoId}/{path}&{key}`). The server never holds that key today,
  and this design does not change that — render blobs move between storage
  backends as opaque already-encrypted bytes, same as now.
- No per-repo storage configuration. One shared backend configuration serves
  the whole instance.
- No live-reload of storage settings without a restart (see "Apply model"
  below) — deliberately simple over building reload plumbing through every
  handler that holds a concrete store reference.

## Architecture

### Storage abstraction

Generalize the existing `backend/internal/renderstore` package into
`backend/internal/blobstore`, with the same interface shape, keyed generically
instead of by `(repoID, notePath)`:

```go
type BlobStore interface {
    Write(key string, data []byte) error
    Read(key string) ([]byte, error)
    Delete(key string) error
}

func New(settings StorageSettings) (BlobStore, error) // "local" or "s3"
```

The existing `local.go`/`s3.go` implementations move over largely unchanged.
Callers namespace keys themselves:

- Render blobs: `renders/{repoID}/{notePath}`
- Assets: `assets/{repoID}/{assetPath}`

One configured `BlobStore` instance serves both namespaces (one bucket, no
separate render/asset bucket configuration — YAGNI for a single-instance
self-hosted tool).

### Asset encryption

Assets have no encryption today (synced as plain base64 in the sync payload,
written as-is to the git working tree). Since "all data encrypted" is a
requirement and assets don't have the render blob's client-side zero-knowledge
encryption, add **server-side encryption at rest**:

- A new symmetric key is generated once (32 random bytes) on first boot and
  stored in the DB — a *separate* secret from `PUBOBS_SECRET_KEY` (which
  signs JWTs), so a leak of one doesn't compromise the other.
- `EncryptingStore` wraps a `BlobStore`: `Write` encrypts (AES-GCM) before
  delegating, `Read` decrypts after delegating. Used only for the `assets/`
  namespace. Render blobs go directly to the plain `BlobStore` — they're
  already encrypted before they arrive, so no double encryption.

### Sync flow change

`handleSync` (`backend/internal/api/sync.go`) keeps committing assets into the
git working tree exactly as today — that's the source-of-truth vault content,
pushed to the user's own git remote, and nothing here changes that. It
*additionally* writes each asset through `EncryptingStore` at sync time.

`handlePubGetAsset` (`backend/internal/api/pub.go`) switches from
`cache.ReadAsset` (reading the live git checkout) to reading (and decrypting)
from the new store. This is the key unlock: asset serving no longer depends
on the git checkout being present, so the existing eviction job can reclaim
checkouts more aggressively without breaking image loading on published
pages.

**Cutover for pre-existing assets:** notes published *before* this feature
ships have assets sitting only in their git checkout, not yet in the new
store. To avoid 404s on already-published pages until they happen to
re-sync, `handlePubGetAsset` tries the new store first and, on a miss, falls
back to `cache.ReadAsset` (git checkout) — and if found there, backfills the
new store (encrypted) so the next request hits the fast path and the
checkout can be evicted normally afterward. No separate one-time backfill
job needed; it self-heals as pages are viewed.

## Settings & admin panel

### DB-backed settings

New migration `backend/internal/db/migrations/002_storage_settings.sql`, a
single-row table: backend type (`local`/`s3`), S3 endpoint/bucket/access
key/secret key/region/useSSL, plus the generated asset-encryption key and
migration-status fields (see below).

On first boot, if the table is empty, seed it from the existing `PUBOBS_*`
env vars — upgrading installs keep working with zero config changes. After
that, the DB row is authoritative; env vars are no longer consulted for this
setting.

### Admin API

`GET/PUT /api/admin/storage-settings` — instance-admin only, same auth
pattern as the existing repos/users/groups endpoints.

On `PUT`, before saving: run a validation round-trip against the *new*
settings (write a small test object, read it back, delete it) so a bad
bucket/credential is caught immediately rather than bricking the instance on
next boot.

### Apply model: restart-to-apply

Saving valid new settings writes them to the DB, then the handler exits the
process. Docker's existing `restart: unless-stopped` policy brings the
container back up, which reads the (now-updated) settings on boot. The admin
UI shows a "restarting…" state and polls `/healthz` until the instance is
back.

This deliberately avoids building live-reload plumbing through every part of
the code that currently holds a concrete store reference at startup — a
restart is simple, correct, and a few seconds of downtime for a config change
on a single-admin self-hosted instance is an acceptable trade.

`/healthz` must not depend on the configured store being reachable, so a
misconfigured store (if validation somehow missed it) doesn't crash-loop the
container — store errors surface per-request (404/500), not at boot.

### Migration

A "Migrate existing data to S3" action on the storage settings page, enabled
once S3 settings are saved and the restart has completed. Triggers a
background job (same pattern as the existing eviction job) that:

1. Walks all repos' render blobs + assets currently on local disk.
2. For each: reads local, writes to the new backend, reads back to verify,
   then deletes the local copy — only after the remote write is confirmed.
3. On a per-file failure: logs and skips (leaves that file local) rather than
   aborting the whole run. The admin can re-run the action to retry just the
   stragglers.

Progress (`n of m migrated`, done/failed counts) is stored alongside the
settings row and polled by the admin UI.

### Disk usage panel

Shown on the storage settings page:

- **Local:** host free space (existing `cache.DiskUsage()`, already used by
  the eviction health check) plus a breakdown of what pubobs itself occupies
  locally — `backend/data/repos` (git checkouts) and any render/asset blobs
  still stored locally.
- **S3:** sum of object sizes under the `renders/` and `assets/` prefixes via
  paginated `ListObjectsV2`, computed on-demand when the settings page loads
  (not background-cached — a self-hosted single-instance tool doesn't need
  that complexity, and an on-load list call is cheap and always accurate).

Displayed side-by-side: `Local: X GB (repos: Y GB, blobs: Z GB) · S3: W GB` —
directly answering whether the migration actually freed disk space.

### Admin UI

New view `frontend/src/views/storage-settings.ts`, linked from the
instance-admin nav alongside Repos/Users/Groups/Allowlist. Contains: backend
type + S3 field form, save button (triggers validate → save → restart flow),
migration status/action section, and the disk-usage breakdown.

## Error handling

- **Bad S3 config on save:** caught by the validation round-trip before
  writing to DB/restarting.
- **Startup resilience:** `/healthz` never depends on store reachability.
- **Migration failures:** per-file skip+log+retryable; never deletes a local
  copy before its remote write is verified; never aborts the whole run for
  one bad file.
- **Disk health check** (`DiskWarnPct`/`DiskCritPct`) is unaffected by any of
  this — git checkouts stay local regardless of render/asset backend choice.

## Testing

- Unit tests for the `blobstore` local/S3 implementations (adapting the
  existing `renderstore` tests).
- Unit tests for `EncryptingStore`: round-trip correctness, and that
  tampered ciphertext fails to decrypt (GCM auth tag rejection).
- Test that settings seed correctly from env vars on first boot, and are
  ignored on subsequent boots once the DB row exists.
- Test for the migration job's core logic (migrate + verify + delete-local,
  and skip-on-failure), following the existing `RunEvictionCycle`
  exported-for-testing pattern.
