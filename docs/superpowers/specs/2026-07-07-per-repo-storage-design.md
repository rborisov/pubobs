# Per-repo storage destinations + aggressive git eviction

## Goal

Two related storage improvements, driven by real disk pressure on a
constrained VPS (live usage: 735 MB total / 480 MB free, of which repos are
699 MB, renders 36 MB, assets 0 B):

1. **Reclaim the 699 MB of git checkouts** by evicting them from local disk
   much sooner than the current 24 h TTL — re-cloning from the git remote on
   demand when next needed.
2. **Per-repo storage destinations** for renders and assets: an admin manages
   a named list of S3 destinations (buckets/credentials), and each repo
   independently chooses `Local` or one of those destinations for its
   rendered-note content and media assets.

These ship as two phases so the disk win (Phase 1) lands first and cheaply,
independent of the larger Phase 2 build.

## Context and prior decisions

This supersedes the instance-wide storage model from the prior feature
(`docs/superpowers/specs/2026-07-06-s3-storage-design.md`, shipped): a single
`storage_settings` row picked one S3 backend for the whole instance's renders
and assets. That machinery (`renderstore.RenderStore`, `EncryptingStore`,
`SwappableStore`, the S3 client, `RunMigrationCycle`, the admin Storage page)
is reused heavily here — this is a generalization of it, not a rewrite.

**Explicitly rejected during design:** putting git repo data itself on S3
(via bundles or a FUSE-mounted object store). Rationale: the git *remote*
(GitHub/Gitea/etc.) is already a complete, durable copy of every repo, so the
699 MB of local checkouts is purely disposable cache. Aggressive eviction +
re-clone-from-remote reclaims that disk with near-zero complexity, whereas
git-on-S3 (bundles: encrypt/upload/restore plumbing; FUSE: privileged
container, slow/fragile for git's write patterns) adds significant complexity
for a benefit the remote already provides. Git checkouts therefore stay on
local disk; only renders/assets are ever stored on S3.

## Phase 1: Aggressive git eviction

### Behavior

The existing eviction job (`backend/internal/jobs/eviction.go`,
`RunEvictionCycle`) already evicts repos whose `LastUsedAt` is older than
`cfg.RepoCacheTTL` (default 24 h) and re-clones on next access. Phase 1 is a
**tuning change, not new architecture**: lower the default idle TTL so
checkouts are reclaimed within roughly an hour of inactivity instead of a day.

- Default `PUBOBS_REPO_CACHE_TTL` drops from `24h` to `1h`.
- `PUBOBS_CACHE_CHECK_INTERVAL` (how often the job runs, default `1h`) drops
  to a shorter interval (e.g. `15m`) so an idle repo is actually reclaimed
  within ~1 h of its last use rather than lagging a full check cycle behind.
- `LastUsedAt` must be touched by every operation that uses the checkout so a
  genuinely active repo stays warm: syncs (already call `TouchLastUsedAt`),
  plus the read paths that still hit the checkout — comment reads
  (`handlePubComments` → `ReadRawFile`) and legacy-note reads
  (`handlePubGetNote` → `ReadRenderedHTML` when a note has no render key).
  Audit these paths and add `TouchLastUsedAt` where missing, so a repo being
  actively read isn't evicted out from under a burst of reads.

### Why this is safe

The git remote remains the source of truth. An evicted checkout is
re-cloned from the remote on the next sync/comment/legacy read (existing
`getOrClone` behavior). Renders and assets are already served from their own
stores (prior feature), so evicting the checkout does not affect normal note
rendering. Steady-state disk for an idle repo drops to ~0; it spikes only
briefly during active use.

### Out of scope for Phase 1

No change to *where* checkouts live (always local) and no per-repo eviction
policy — the shorter TTL is instance-wide.

## Phase 2: Per-repo storage destinations (renders + assets)

### Data model

**New `storage_destinations` table** — the admin-managed list of S3 configs:

```sql
id            TEXT PRIMARY KEY
name          TEXT NOT NULL          -- admin-facing label, e.g. "default", "archive-bucket"
s3_endpoint   TEXT NOT NULL
s3_bucket     TEXT NOT NULL
s3_access_key TEXT NOT NULL
s3_secret_key TEXT NOT NULL
s3_region     TEXT NOT NULL
s3_use_ssl    INTEGER NOT NULL DEFAULT 1
created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
```

`Local` is never a row — it is represented by a repo having no destination
assigned.

**`repos` table** gains one nullable column:

```sql
storage_destination_id TEXT NULL REFERENCES storage_destinations(id)
```

`NULL` means local. New repos default to `NULL`.

The **asset-encryption key** stays instance-wide (one server-held key). The
prior feature's `storage_settings` table is **retained solely as the home for
this key** (its S3-config columns and migration-status columns are no longer
read once conversion has run — see backward compatibility); every S3
destination encrypts asset bytes at rest with that same key. Render blobs
remain client-side-encrypted opaque bytes as before.

**Per-repo migration status** lives on the `repos` table as three columns
(mirroring the prior feature's instance-wide fields): `migration_status`
(`idle`/`running`/`done`/`failed`), `migration_total`, `migration_done`. This
avoids a separate table and keeps a repo's migration state co-located with its
destination assignment.

### Store resolution: `StorageResolver`

The prior feature's per-instance `Deps.RenderStore`/`AssetStore`
(`*SwappableStore`) generalize into a single `StorageResolver` that owns:

- the local render/asset stores (always present), and
- one built store-set (render store + `EncryptingStore`-wrapped asset store)
  per configured S3 destination.

Callers use:

```go
resolver.RenderStoreFor(repoID) renderstore.RenderStore
resolver.AssetStoreFor(repoID)  renderstore.RenderStore
```

The resolver looks up the repo's `storage_destination_id`, returns that
destination's store-set (or the local one if `NULL`). Internally each
destination's stores are built with the same `renderstore.New(...)` +
`NewEncryptingStore` calls already in use. This is a lookup layer over the
existing machinery, not new storage plumbing.

**Live updates:** when the admin adds/edits/removes a destination or reassigns
a repo, the resolver rebuilds its internal destination→store-set map — same
no-restart live-swap principle as the prior `SwappableStore`. A per-repo lock
(reuse `gitcache`'s existing per-repo mutex) guards a repo from being served
mid-reassignment.

**Key layout** is unchanged and already repo-scoped, so repos sharing one
bucket never collide: `renders/{repoID}/{notePath}.enc`,
`assets/{repoID}/{assetPath}.enc`.

### Call-site changes

Every current use of `deps.RenderStore`/`deps.AssetStore` (in `sync.go`,
`pub.go`) changes from a direct field access to
`deps.Resolver.RenderStoreFor(repoID)` / `AssetStoreFor(repoID)`. The repoID
is already in scope at all these call sites.

### Admin UX

- The existing **Storage** admin page becomes **destination management**:
  list/add/edit/delete named destinations. Add/edit validates the S3 config
  with the existing write/read/delete round-trip before saving.
- Each **repo** gets a destination selector (`Local` + each destination) on
  its admin detail view. Changing it triggers a per-repo migration (below).
- The disk-usage panel shows a per-destination size breakdown (local, and
  each S3 destination's `renders/` + `assets/` totals via the existing
  `ListAllObjects` + `SumObjectSizesWithPrefix`).

### Per-repo migration

Reuses the existing verify-before-delete `jobs.RunMigrationCycle`, scoped to a
single repo's keys: source = the repo's old destination's stores, dest = the
new destination's stores, walking only `renders/{repoID}/*` and
`assets/{repoID}/*`. Same background-job + status-tracking pattern as the
instance-wide migration, keyed per repo (migration status stored per repo, not
in a global settings row). The asset double-encryption fix from the prior
feature (migrate through the plain store under the `EncryptingStore`, never
re-encrypting already-encrypted bytes) carries forward and must be preserved.

### Backward compatibility

On upgrade, the prior feature's single `storage_settings` row is converted:

- If it was configured for S3, its S3 fields become the first
  `storage_destinations` row (name "default"), and every existing repo is
  assigned that destination's id — so their already-uploaded
  `renders/`/`assets/` keys keep resolving to the same bucket. No data moves.
- If it was local, no destination is created and all repos stay `NULL` (local).
- The instance-wide asset-encryption key is preserved as-is (still one key).

### Error handling

- Deleting a destination still referenced by any repo is rejected with a
  clear error naming the count of repos still using it (reassign first).
- A repo pointed at an unreachable/misconfigured destination surfaces a clear
  error on read and in the usage panel — **not** a silent `0`/empty. This also
  closes the deferred gap from the prior feature (S3 listing errors silently
  reporting `0`); the usage endpoint returns a per-destination error marker
  the UI renders as "unavailable" rather than "0 B".
- Destination config is validated (round-trip) before it is saved or a repo
  is assigned to it.

## Testing

- **Phase 1:** eviction reclaims an idle repo's checkout after the shortened
  TTL; an actively-read repo (comment/legacy read touches `LastUsedAt`) is not
  evicted mid-activity; an evicted repo re-clones correctly on next access.
- **Resolver:** returns the correct store-set per repo based on its
  `storage_destination_id` (local for `NULL`, the right destination
  otherwise); rebuilds correctly after a destination edit or repo reassignment.
- **Per-repo migration:** moves only the target repo's `renders/`/`assets/`
  keys, verifies-before-delete, and does not double-encrypt assets (the prior
  fix's invariant), asserted by round-tripping to original plaintext.
- **Delete-in-use rejection:** deleting a referenced destination fails and
  changes nothing.
- **Backward compat:** an existing single-row `storage_settings` (S3 case)
  converts to destination "default" with all repos assigned and no data
  movement; the asset key is preserved.
- **Error surfacing:** an unreachable destination reports an error (not `0`)
  in the usage panel and on read.
