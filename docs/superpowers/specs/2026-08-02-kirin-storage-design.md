# Kirin-style versioned object storage for PubObs

**Date:** 2026-08-02  
**Status:** Draft  
**Continues:** `2026-07-07-per-repo-storage-design.md` (deferred git-cache decision)  
**Related:** `2026-07-06-s3-storage-design.md`, `2026-06-21-render-storage-encryption-design.md`, `2026-04-28-pubobs-redesign-design.md`

---

## Goal

Adopt [Kirin](https://github.com/ericmjl/kirin)'s storage model — **linear
dataset commits + content-addressed blobs on local disk or S3** — as a
first-class PubObs backend layer. Use it to:

1. Give every sync an explicit, immutable publish tip (not just "latest path
   overwrite" for renders/assets).
2. Unlock note/file history and safer rollback without depending on a warm
   git checkout or deep git history.
3. Shrink or eliminate the VPS git working-tree cache (today ~700 MB of the
   disk problem deferred in the per-repo storage spec).
4. Unify change detection around content hashes instead of the current split
   (plugin FNV-1a vs git blob SHA vs path-keyed store keys).

This design does **not** replace the user's external Git remote as the
portable vault source of truth. Git remains the human-facing backup and
collaboration surface; Kirin becomes the server's durable, queryable publish
store.

---

## What Kirin is (and what we take from it)

Kirin (Python, Eric J. Ma) is version-controlled dataset storage:

| Kirin concept | Meaning |
|---|---|
| **Catalog** | Root URI (`/data/...` or `s3://bucket/prefix`) |
| **Dataset** | Named collection of files under the catalog |
| **Commit** | Linear snapshot (message + file set); no branches |
| **Content-addressed storage** | Blobs keyed by content hash; dedupe + integrity |
| **Checkout** | Materialize a commit's files for local use |
| **Cloud FS** | `fsspec`/`s3fs` detect S3/GCS/etc. from URI |

PubObs already has analogous pieces under different names:

| PubObs today | Closest Kirin analogue |
|---|---|
| `repos` row + `StorageResolver` destination | Catalog + per-dataset backend |
| One sync (`POST /api/repos/{id}/sync`) | Dataset commit |
| Git remote + local `gitcache` clone | External SoT + working checkout |
| `RenderStore` / asset store (path-keyed) | Blob store **without** CAS or commits |
| `note_snapshots` (latest-only upsert) | Tip pointer, not history |

**What we adopt:** the data model (CAS blobs + linear commits + tip).  
**What we do not adopt:** the Python package itself.

### Why not call the Python Kirin library

PubObs is a single Go binary in Docker. Pulling in a Python runtime / sidecar
for storage would fight the install story (`install.sh`, in-app updater,
`go:embed` frontend) and add a second process lifecycle. Instead:

> Implement a small Go package `backend/internal/kirinstore` that follows
> Kirin's semantics, backed by the existing local + S3 destinations already
> managed by `StorageResolver`.

The package name and docs may say "Kirin-style" / `kirinstore`; we are not
claiming API compatibility with the PyPI package.

---

## Problem statement (current seams)

```text
Obsidian plugin
    │  POST /sync  (md + encrypted HTML + assets + data files)
    ▼
┌─────────────────────────────────────────────────────────────┐
│  sync.go                                                     │
│   ├─ gitcache.Sync     → commit+push external Git            │
│   ├─ SQLite notes      → keys, ACL, latest snapshot metadata │
│   ├─ RenderStore       → path overwrite  renders/{repo}/{p}  │
│   └─ AssetStore        → path overwrite  assets/{repo}/{p}   │
└─────────────────────────────────────────────────────────────┘
```

Pain this creates:

1. **Non-atomic layers.** Git push, SQLite, and blob writes can diverge;
   heal/prune paths exist because of this.
2. **Latest-only blobs.** Unshare / key rotation and "show previous render"
   have no object history to lean on.
3. **History UX unfinished.** Redesign promised `GET .../notes/{path}/history`;
   shallow clones dropped it (`2026-05-07-git-experience-improvement`).
4. **Checkout gravity.** Comments, file listing, and some asset fallbacks
   still need a local clone → disk pressure; aggressive eviction fights
   latency.
5. **Weak sync identity.** Three hash schemes; force re-sync exists to paper
   over drift.
6. **No drafts.** Every successful sync is immediately pushed to the user's
   remote.

---

## Design principles

1. **Git remote stays authoritative for vault portability.** Authors must
   still be able to clone their repo elsewhere and recover markdown + data
   files + assets without PubObs.
2. **Kirin tip is authoritative for what the server publishes.** The reader
   and pull APIs prefer the Kirin commit tip over "whatever is in the working
   tree right now."
3. **Reuse existing S3 destinations.** Same admin Storage page, same
   per-repo assignment, same encryption rules (client-encrypted renders,
   server AES-GCM assets).
4. **Linear commits only.** No branches, merges, or rebases in `kirinstore`.
   Matches Kirin and matches PubObs sync (one tip per repo).
5. **Opaque bytes stay opaque.** `kirinstore` never decrypts render blobs;
   it stores whatever bytes sync gives it.
6. **Phased delivery.** Ship value without a big-bang rewrite of gitcache.

---

## Architecture

### New package: `backend/internal/kirinstore`

```go
package kirinstore

// Store is one catalog root (local dir or S3 prefix) holding many datasets.
type Store interface {
    Commit(ctx context.Context, dataset string, req CommitRequest) (Commit, error)
    Tip(ctx context.Context, dataset string) (Commit, error)
    GetCommit(ctx context.Context, dataset, hash string) (Commit, error)
    History(ctx context.Context, dataset string, lim int) ([]Commit, error)
    // Resolve returns the content hash for path at tip (or at commit).
    Resolve(ctx context.Context, dataset, commitOrTip, path string) (string, error)
    ReadBlob(ctx context.Context, contentHash string) (io.ReadCloser, error)
    // List paths (+ hashes) at a commit.
    List(ctx context.Context, dataset, commitOrTip string) ([]FileEntry, error)
}

type CommitRequest struct {
    Message   string
    Author    string            // email or display
    // Put: path → bytes (or precomputed hash + reader for large assets)
    Put       map[string][]byte
    // Delete paths from the parent tip when forming the new tree
    Delete    []string
    Parent    string            // empty = genesis; normally tip hash
}

type Commit struct {
    Hash      string            // hash of commit metadata
    Parent    string
    Message   string
    Author    string
    CreatedAt time.Time
    // Tree: path → content hash (logical tree; stored as one JSON object)
    TreeHash  string
}

type FileEntry struct {
    Path        string
    ContentHash string
    Size        int64
}
```

### On-disk / S3 layout (per destination)

Reuse destination credentials; namespace under a single prefix so migration
and usage accounting stay familiar:

```text
{destination root}/
  kirin/
    blobs/
      ab/
        abcd1234...          # content-addressed object (hash hex)
    datasets/
      {repoID}/
        HEAD                 # tip commit hash (small object)
        commits/
          {commitHash}.json  # {parent, message, author, created_at, tree_hash}
        trees/
          {treeHash}.json    # { "notes/a.md": "hash…", "renders/notes/a.md": "hash…", … }
```

Local: under `/data/kirin/` (or destination-local equivalent).  
S3: `kirin/blobs/...`, `kirin/datasets/{repoID}/...` on the repo's assigned
bucket (alongside existing `renders/` / `assets/` during migration).

`dataset` name = PubObs `repoID` (UUID). One dataset per repo.

### Logical tree paths inside a commit

A sync commit records **one tree** covering every published artifact:

| Tree path prefix | Contents | Encryption |
|---|---|---|
| `md/` | note markdown (utf-8) | none (same as git today) |
| `data/` | data files (`.base`, csv, …) | none |
| `assets/` | media bytes | **server** AES-GCM before CAS write (same as today) |
| `renders/` | Obsidian HTML | **client** AES-GCM already applied; store opaque |
| `meta/` | optional: links JSON, frontmatter extract, comment snapshot | none |

Comments can later move into `meta/comments/{notePath}.md` (or stay in git
companion files during Phase 1–2). Prefer eventually committing them into the
same tree so comment reads do not need a clone.

### Integration with `StorageResolver`

```go
resolver.KirinStoreFor(repoID) kirinstore.Store
```

- `NULL` destination → local Kirin root (`PUBOBS_KIRIN_DIR`, default
  `/data/kirin`).
- Non-null → S3-backed Kirin store using that destination's credentials,
  key prefix `kirin/`.
- Live rebuild on destination add/edit/reassign, same as render/asset stores.
- Per-repo migration job gains a third namespace: copy `kirin/datasets/{repoID}/`
  and **only the blobs referenced by that dataset's reachable commits**
  (not the whole `kirin/blobs/` tree).

---

## Sync flow (target)

```text
Plugin POST /sync
        │
        ▼
  Validate ACL + credentials
        │
        ├─1─ Build kirinstore.CommitRequest from payload
        │      md/*, data/*, assets/* (encrypt), renders/* (opaque)
        │      Delete ← deleted_paths (all prefixes)
        │
        ├─2─ kirin.Commit(repoID, req)  → new tip hash
        │      (CAS put blobs; write tree + commit; CAS-swap HEAD)
        │
        ├─3─ SQLite: notes, note_keys, tip_commit_hash, metadata
        │
        ├─4─ (Policy) Export to Git remote
        │      Materialize tip → gitcache working tree → commit → push
        │      OR (later) plugin-side git push; server skips clone
        │
        └─5─ Response: { commit_sha: tipHash, git_sha?, note_keys, … }
```

**Ordering invariant:** Kirin commit succeeds **before** Git export and
before SQLite tip update is visible to readers. If Git export fails, the
Kirin tip still advanced; retry/export job reconciles. Readers always follow
Kirin tip, not "last successful git push."

Today those two are reversed in spirit (git first, blobs second). Flipping
that is the main consistency win.

---

## Read paths (target)

| API | Today | With Kirin |
|---|---|---|
| `GET /api/repos/{id}/files` | `gitcache.ListFiles` | `kirin.List(tip)` filter `md/` |
| `GET /api/repos/{id}/data-files` | clone walk | `kirin.List(tip)` filter `data/` |
| `GET /pub/.../render/*` | path-keyed `RenderStore` | `Resolve(tip, renders/…)` → `ReadBlob` |
| `GET /pub/.../assets/*` | path-keyed asset store | same with `assets/` + decrypt wrapper |
| `GET .../notes/{path}/history` | removed (shallow) | `History` + per-path hash changes across commits |
| Comments | `*-comments.md` in clone | Phase 2+: `meta/comments/…` at tip |

Plugin pull compares **content hashes from the tip** to local state (replace
git blob SHA / FNV-1a with one scheme). `pullSHAs` becomes `pullHashes`
keyed by content hash.

---

## Encryption (unchanged policy, new placement)

- **Renders:** plugin encrypts; `kirinstore` stores ciphertext under
  `renders/{path}` as a CAS blob. Share-key rotation = new blob + new tip
  commit that points the path at the new hash (old ciphertext remains
  reachable from old commits until GC).
- **Assets:** `EncryptingStore`-equivalent wraps **before** `Put` into
  Kirin (encrypt then hash). Do not hash plaintext and store ciphertext under
  that hash.
- **Markdown / data files:** plaintext in Kirin (and in Git), same trust
  model as today — the remote PAT holder and instance admin can read them.

---

## Relationship to the deferred git-cache decision

`2026-07-07-per-repo-storage-design.md` listed two candidates for reclaiming
~700 MB of checkouts. Kirin is a third, complementary option:

| Approach | Role of VPS | Role of Git remote |
|---|---|---|
| Aggressive eviction | Disposable cache; re-clone | SoT + all reads that miss cache |
| Git in Obsidian | No checkout; plugin pushes | SoT; server may not hold md at all |
| **Kirin publish store (this)** | Durable CAS + tip for publish/pull | Portable SoT / export; checkout optional |

Recommended stance:

- **Near term:** Kirin becomes the server publish/read store; gitcache remains
  for **export push only** (and can be ephemeral: clone → push → delete).
- **Medium term:** optional plugin-side git (candidate 2) so the server never
  needs credentials to push — Kirin tip is still what PubObs serves.
- **Rejected again:** git-on-S3 / FUSE (same reasons as the per-repo spec).

This unblocks disk reclaim without forcing the larger "move git into
Obsidian" redesign up front.

---

## Phased delivery

### Phase 0 — Spec + spike (this document)

- Agree boundaries: Git remains export SoT; Kirin is publish tip.
- No user-visible change.

### Phase 1 — `kirinstore` + dual-write renders/assets

**Scope:**

- Implement `kirinstore` (local + S3) with Commit / Tip / ReadBlob / List.
- On sync: dual-write render + asset bytes into Kirin tree **and** keep
  existing path-keyed `RenderStore` / `AssetStore` writes.
- Store `repos.kirin_tip` (or `note_snapshots`-level is wrong — tip is
  per-repo) in SQLite for fast display.
- Reader still uses path-keyed stores.

**Exit criteria:** every sync produces a Kirin commit; admin can show tip
hash + history length; no behavior change for readers.

### Phase 2 — Reader + pull from Kirin tip

**Scope:**

- `GET /pub/.../render|assets` and plugin pull list endpoints read from tip.
- Stop writing path-keyed render/asset objects on new syncs (leave old keys
  for fallback until re-sync).
- Restore `GET /api/repos/{id}/notes/{path}/history` from Kirin history
  (path filter across commits).
- Plugin: prefer content hashes from list API; deprecate FNV-1a for push skip.

**Exit criteria:** eviction of git checkout does not break reader or pull for
repos fully synced after Phase 2.

### Phase 3 — Source tree in Kirin; ephemeral git export

**Scope:**

- Commit `md/` + `data/` into the same Kirin commit as renders/assets
  (already in CommitRequest from Phase 1 if we include them early — preferred).
- `gitcache.Sync` becomes "materialize tip → commit → push → optional delete
  working tree."
- Comments: either keep clone-only briefly, or commit `meta/comments/` in the
  same tip (preferred before declaring gitcache optional).

**Exit criteria:** `PUBOBS_REPO_CACHE_TTL` can be minutes; cold repos use
zero disk beyond Kirin + SQLite.

### Phase 4 — Optional drafts / publish gate (stretch)

- `Commit` to Kirin without Git export (`draft` tip vs `published` tip), or
  a single tip with `exported_git_sha` null until publish.
- Admin/plugin "Publish to Git" action.
- Out of scope until Phases 1–3 prove the store.

---

## Data model changes (SQLite)

```sql
-- on repos
kirin_tip TEXT NULL              -- current dataset HEAD commit hash
kirin_exported_git_sha TEXT NULL -- last successfully pushed git SHA (Phase 3)

-- optional audit (can derive from kirinstore.History; SQLite cache optional)
-- sync_commits(repo_id, kirin_hash, git_sha, author, message, created_at)
```

No change to `notes.encryption_key` or ACL tables. `note_snapshots` remains
latest metadata cache; its `git_commit_sha` gains a sibling
`kirin_commit_hash` (or we store Kirin hash there once Git export is
secondary — prefer a new column to avoid overloading meaning).

---

## API surface (additive)

| Method | Path | Phase | Purpose |
|---|---|---|---|
| GET | `/api/repos/{id}/commits` | 1 | Kirin history (limit, cursor) |
| GET | `/api/repos/{id}/commits/{hash}` | 1 | Commit metadata + file list |
| GET | `/api/repos/{id}/notes/{path}/history` | 2 | Per-path history from Kirin |
| POST | `/api/repos/{id}/sync` | 1–3 | Response gains `kirin_commit` |
| GET | `/api/repos/{id}/files` | 2 | Served from tip |

Admin Storage / repo detail: show tip, commit count, Kirin bytes used
(reachable blobs), destination (already shown).

---

## Plugin changes (Phase 2+)

- List/pull responses include `content_hash` (hex of CAS hash of **plaintext
  md/data** as stored server-side).
- Local `pullSHAs` → store those hashes; skip pull when equal.
- Push skip: hash local file the same way (SHA-256 of bytes) instead of
  FNV-1a; force re-sync remains as escape hatch.
- Sync response: persist `kirin_commit` per repo for diagnostics.

No change to PKCE auth, folder mappings, or render encryption.

---

## Garbage collection

CAS blobs become unreferenced when no reachable commit tree lists them.

- **Phase 1–2:** no GC; disk/S3 growth is acceptable (dedupe already limits
  growth for unchanged files).
- **Phase 3:** periodic job per destination: mark hashes reachable from each
  dataset's last N commits (or all commits), delete unreachable blobs.
  Never GC blobs referenced by `HEAD` or by commits newer than retention.

Retention policy: config `PUBOBS_KIRIN_RETAIN_COMMITS` (default unlimited for
self-hosted; operators may cap).

---

## Migration / backward compatibility

1. Existing path-keyed `renders/` + `assets/` keep working until Phase 2 cutover.
2. First sync after Phase 1 creates genesis Kirin commit from that sync's
   payload only (not a full historical backfill). Optional admin "Build
   Kirin tip from current stores + git HEAD" job can synthesize a genesis
   commit for cold repos.
3. Storage destination migration copies reachable Kirin objects as described
   above; path-keyed migration stays as today until Phase 2 removes writers.
4. Env bootstrap: no new required env vars. Optional `PUBOBS_KIRIN_DIR` for
   local root. S3 uses existing destinations.

---

## Non-goals

- Embedding or shelling out to the Python `kirin` package.
- Branching / PRs / merge commits inside PubObs.
- Storing git packfiles or `.git` directories on S3.
- Changing OIDC, ACL, or share-link crypto.
- Multi-tenant bucket-per-user (still one instance, shared destinations,
  repo-scoped dataset names).
- Real-time collaborative editing.

---

## Testing strategy

- **kirinstore unit tests:** commit / parent chain / tip CAS; dedupe identical
  bytes; delete path tombstones in new tree; concurrent commits (last writer
  wins with parent check — reject if `Parent != current HEAD`).
- **S3 integration tests:** against MinIO in CI (same pattern as renderstore).
- **Sync dual-write:** after Phase 1 sync, tip tree hashes match written
  render/asset plaintext-or-ciphertext as specified.
- **Cutover:** Phase 2 reader tests with gitcache directory deleted.
- **Migration:** move repo between local and S3 destination; tip and blob
  reads succeed; unreachable blobs from other repos untouched.
- **Encryption invariants:** asset round-trip plaintext; render bytes equal
  plugin ciphertext; no double encryption.

---

## Open questions

1. **Include `md/` + `data/` in Phase 1 commits** (recommended: yes — one
   tree from day one) vs renders/assets only until Phase 3?
2. **Conflict policy:** if two editors sync concurrently, reject stale parent
   (Kirin) and force pull-rebase-in-plugin — or last-write-wins on tip?
   Recommendation: **reject stale parent** with HTTP 409 + current tip; plugin
   pulls and retries (matches collaborative honesty better than silent clobber).
3. **Comments in tree vs git-only** until Phase 3 — recommendation: move in
   Phase 3 with ephemeral git export so checkout can die.
4. **GC retention default** for small VPS operators — unlimited vs 50 commits?
5. Naming in UI: "Publish history" vs "Kirin commits" — prefer user-facing
   **Publish history**; keep "Kirin" in eng docs/package name only.

---

## Summary

PubObs today splits "versioned content" (Git) from "published blobs"
(path-keyed local/S3) and pays for that with disk, inconsistency, and a
missing history API. A Go **Kirin-style** store — linear commits +
content-addressed objects on the same local/S3 destinations we already
admin — becomes the server's publish tip. Git remains the portable export.
Phased dual-write then cutover reclaims the deferred git-cache disk problem
without requiring Python or FUSE, and restores the version-history promise
from the platform redesign.
