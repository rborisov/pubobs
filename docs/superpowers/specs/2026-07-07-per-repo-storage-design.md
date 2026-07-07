# Per-repo storage destinations (+ deferred git-cache decision)

## Goal

Two related storage improvements, driven by real disk pressure on a
constrained VPS (live usage: 735 MB total / 480 MB free, of which repos are
699 MB, renders 36 MB, assets 0 B):

1. **Per-repo storage destinations** for renders and assets: an admin manages
   a named list of S3 destinations (buckets/credentials), and each repo
   independently chooses `Local` or one of those destinations for its
   rendered-note content and media assets. **This is the work being
   implemented now** (was "Phase 2" in an earlier draft).
2. **Reclaiming the 699 MB of git checkouts** — the larger disk win, but
   **deliberately deferred**: how the git cache should be handled is an open
   question with more than one candidate approach (see "Deferred: git-cache
   location" below), and the user wants to decide it *after* the S3 work
   lands, in its own design pass. It is out of scope for the implementation
   plan that follows from this spec.

Reordering rationale: the two are independent, and the per-repo S3 work is
well-understood and ready to build, whereas the git-cache approach is not yet
settled.

## Context and prior decisions

This supersedes the instance-wide storage model from the prior feature
(`docs/superpowers/specs/2026-07-06-s3-storage-design.md`, shipped): a single
`storage_settings` row picked one S3 backend for the whole instance's renders
and assets. That machinery (`renderstore.RenderStore`, `EncryptingStore`,
`SwappableStore`, the S3 client, `RunMigrationCycle`, the admin Storage page)
is reused heavily here — this is a generalization of it, not a rewrite.

Note on git data: this spec does **not** put git repo data on S3. The git
*remote* (GitHub/Gitea/etc.) is already a complete, durable copy of every
repo, so the 699 MB of local checkouts is purely disposable cache. How to
reclaim that cache is deferred to its own design pass (below); the per-repo
work here concerns only renders and assets.

## Deferred: git-cache location (separate future design)

**Not implemented by this spec's plan.** Captured here so the candidate
approaches aren't lost; to be decided in its own brainstorm after the
per-repo S3 work ships.

Candidate approaches discussed so far:

1. **Aggressive eviction + re-clone from remote.** Lower the idle TTL
   (`PUBOBS_REPO_CACHE_TTL` 24 h → ~1 h) and check interval, touch
   `LastUsedAt` on the read paths that still hit the checkout (comment reads
   via `handlePubComments`→`ReadRawFile`; legacy-note reads via
   `handlePubGetNote`→`ReadRenderedHTML`) so active repos stay warm. Nearly
   zero new code; reclaims disk within ~1 h of inactivity; re-clones from the
   remote on demand. Safe because the remote is the source of truth.
2. **Store the git cache inside Obsidian (user's preferred idea to explore).**
   The Obsidian vault on the user's own machine already *is* the content, and
   the plugin already runs there. The concept: shift git responsibility toward
   the plugin/vault side so the VPS need not hold a working checkout at all.
   This is a meaningfully larger architectural change (the plugin would take
   on git-push responsibility currently done server-side; the backend's
   reasons for keeping a checkout — committing synced markdown + comments and
   pushing to the remote — would need to move or be re-hosted). It needs its
   own brainstorm to work out feasibility and boundaries before any
   commitment.

Rejected earlier: git-on-S3 via bundles or a FUSE-mounted object store —
significant complexity (encrypt/upload/restore, or a privileged container
with slow/fragile FUSE-over-S3 for git's write patterns) for a benefit the
git remote already provides.

## Per-repo storage destinations (renders + assets) — current scope

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
