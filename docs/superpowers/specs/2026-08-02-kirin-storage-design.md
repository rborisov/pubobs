# Kirin on S3 as PubObs source of truth (git retirement)

**Date:** 2026-08-02  
**Status:** Draft (revised)  
**Continues:** `2026-07-07-per-repo-storage-design.md` (git-cache disk problem)  
**Related:** `2026-07-06-s3-storage-design.md`, `2026-06-21-render-storage-encryption-design.md`

---

## Goal

**Replace Git remotes (GitHub, Gogs, etc.) and VPS git checkouts with Kirin
datasets stored on S3.** After migration, PubObs never keeps a per-repo git
snapshot on the VPS and never needs an external git host for day-to-day
operation.

| Today | Target |
|---|---|
| SoT = external Git remote | SoT = **Kirin dataset on S3** |
| VPS holds `/data/repos/{id}/` clone | **No git working trees** |
| Sync = write clone → commit → push | Sync = **Kirin commit** (CAS + tip) |
| Import = clone + SQLite note rows | **Init converter:** git history → Kirin commits, then drop git |

Git remains only as an **import format**: a one-shot converter (init / admin
migrate script) reads old remotes and writes Kirin.

---

## What Kirin provides here

[Kirin](https://github.com/ericmjl/kirin)-style storage:

- **Catalog** on S3 (`s3://bucket/pubobs/…`)
- **Dataset** = one PubObs repo
- **Linear commits** = publish/sync history (no branches)
- **Content-addressed blobs** = file bytes by hash (dedupe across commits)

Implemented in Go as `backend/internal/kirinstore` (not the Python package —
same install/single-binary constraints as before).

---

## Target architecture

```text
                    ┌──────────────────────────┐
  Obsidian plugin   │  PubObs Backend (Go)     │     S3-compatible bucket
  sync / pull  ───► │  API · ACL · SQLite      │──►  kirin/
                    │  kirinstore              │     blobs/…
                    │  (no gitcache in steady  │     datasets/{repoID}/
                    │   state)                 │       HEAD, commits/, trees/
                    └──────────────────────────┘

  One-time / init:
  GitHub / Gogs ──► git2kirin converter ──► same kirinstore on S3
  (clone ephemeral, delete when done)
```

### Components

| Component | Role |
|---|---|
| **`kirinstore`** | CAS blobs + linear commits + tip (`HEAD`) on local or S3 |
| **S3 destination** | Durable home for all repo content (existing admin destinations) |
| **SQLite** | Users, ACL, note encryption keys, repo metadata, `kirin_tip` |
| **Obsidian plugin** | Sync in/out against PubObs API only — no git remote config |
| **`git2kirin` converter** | Init/migrate script: old remotes → Kirin commits |
| **Reader / pub API** | Serve tip (and optional historical commit) from Kirin |

### What goes away (steady state)

- `gitcache` warm clones under `/data/repos/`
- Repo `remote_url` / owner PAT / per-user git credentials as required fields
- Sync path that `git add` / `commit` / `push`
- Dependence on Gogs/GitHub uptime for PubObs to serve notes

Optional later: “Export zip” or “Export git bundle” for portability — not part
of the write path.

---

## S3 object layout

One bucket (or per-destination bucket), prefix configurable:

```text
s3://{bucket}/{optional_prefix}/
  kirin/
    blobs/
      ab/abcd1234…                 # content-addressed file bytes
    datasets/
      {repoID}/
        HEAD                       # tip commit hash
        commits/{commitHash}.json  # parent, message, author, tree_hash, time
        trees/{treeHash}.json      # path → content hash
```

Logical paths inside a tree (same dataset = whole vault snapshot):

| Prefix | Content |
|---|---|
| `md/` | note markdown |
| `data/` | data files (`.base`, csv, json, yaml, …) |
| `assets/` | media (server-encrypted before CAS, same policy as today) |
| `renders/` | client-encrypted HTML (opaque) |
| `meta/comments/` | `*-comments.md` companions (so comments need no clone) |

Local boot without S3 still works: `PUBOBS_KIRIN_DIR=/data/kirin` with the
same layout (dev / small installs). Production intent is **S3**.

---

## Runtime data flow

### Sync (plugin → server)

```text
POST /api/repos/{id}/sync
  → build CommitRequest from md/data/assets/renders/deletes
  → parent = current HEAD (409 if client tip is stale — see Open questions)
  → kirinstore.Commit → new HEAD on S3
  → SQLite: notes, keys, metadata, repos.kirin_tip
  → response { kirin_commit, note_keys }
```

No git. No `/data/repos` write.

### Pull (server → plugin)

```text
GET /api/repos/{id}/files          → List(tip) filter md/
GET /api/repos/{id}/data-files     → List(tip) filter data/
```

Compare `content_hash` (CAS hash of stored bytes) to plugin local state.

### Reader

```text
GET /pub/{repoId}/…/render|assets  → Resolve(tip, path) → ReadBlob
History                              → walk commit parents, filter path
```

---

## Git → Kirin converter (init / migrate)

This is the **only** supported use of git after cutover: a dedicated tool that
runs at install/migrate time (and optionally from admin UI), then discards the
clone.

### Placement

Recommended: Go command in-repo, invoked like other ops tooling:

```text
backend/cmd/git2kirin/          # or: pubobs migrate-git …
install.sh / migrate path      # calls it as one of the init steps
```

Also exposable as admin job: “Import from Git URL” (reuses same library as
today’s `importRepoFromGit`, but writes Kirin instead of SQLite-only rows).

### Inputs

```text
git2kirin \
  --remote https://github.com/org/wiki.git \
  --branch main \
  --username … --password … \          # or SSH not required; HTTPS+PAT
  --repo-id <uuid> \                   # existing or newly created PubObs repo
  --destination <storage_destination_id|local> \
  --history full|tip-only \            # default tip-only for speed; full optional
  --include-renders false              # old HTML in git is legacy; usually skip
```

### Algorithm

1. **Ephemeral clone** to a temp dir (`os.MkdirTemp`, never under long-lived
   `/data/repos` cache). Shallow (`--depth=1`) if `--history tip-only`; full
   clone if `--history full`.
2. **Create / open** Kirin dataset `repoID` on the chosen S3 (or local) store.
3. **Tip-only mode (default, recommended for init):**
   - Walk tree at `HEAD`: `.md` → `md/`, data exts → `data/`, binary/media →
     `assets/` (encrypt), skip `.git` and legacy `.html` unless flagged.
   - Comments files `*-comments.md` → `meta/comments/`.
   - Single Kirin commit: message
     `git2kirin: import tip {gitSHA} from {remote}`.
4. **Full-history mode (optional):**
   - `git rev-list --reverse branch`.
   - For each git commit: diff against parent, translate path ops into Kirin
     `Put`/`Delete`, commit with original author/date/message + trailer
     `Git-Commit: {sha}`.
   - Linearize merge commits as squash-to-first-parent (Kirin has no merges).
5. **SQLite seed:** upsert `notes` rows for every `md/` path; set
   `repos.kirin_tip`; store `repos.migrated_from_remote` / `migrated_git_sha`
   for audit; **clear or null out** `encrypted_creds` after success if
   operator opts in (`--forget-git-creds`).
6. **Delete temp clone.** Do not register the path with `gitcache`.
7. **Renders:** not reconstructed from old git HTML. First Obsidian sync after
   migrate regenerates client-encrypted renders into the next Kirin commit
   (same as post–render-encryption onboarding).

### Init-script wiring

As part of install/reinstall/migrate (alongside DB bootstrap and storage
destination seed):

```text
# After storage_destinations exist and S3 is configured:
pubobs migrate-git --config /opt/pubobs/backend/migrate-repos.yaml
```

Example manifest:

```yaml
destination: default          # storage_destinations.name
history: tip-only
forget_git_creds: true
repos:
  - name: Team Wiki
    remote: https://gogs.example.com/org/wiki.git
    branch: main
    username: oauth2
    password_env: GOGS_PAT
  - name: Personal
    remote: https://github.com/me/notes.git
    branch: main
    username: x-access-token
    password_env: GITHUB_PAT
```

Idempotency: if dataset `HEAD` already exists and
`migrated_git_sha == current remote HEAD`, skip; if tip differs, either fail
or add a new import commit (`--if-changed commit`).

### Relation to today’s `importRepoFromGit`

Current admin import only upserts SQLite note metadata from a
`gitcache` list — it does **not** copy file bytes into a durable blob store.
`git2kirin` **supersedes** that path: one library function
`MigrateGitRemote(ctx, …) (MigrateResult, error)` used by CLI and by
`POST /api/admin/repos/{id}/import`.

---

## Repo model changes

```sql
-- repos: git fields become migration leftovers, not required for new repos
remote_url              TEXT NULL          -- was NOT NULL; set only for migrate audit
encrypted_creds         TEXT NULL          -- cleared after migrate when forget=true
default_branch          TEXT NULL
kirin_tip               TEXT NULL          -- dataset HEAD
migrated_from_remote    TEXT NULL
migrated_git_sha        TEXT NULL
storage_destination_id  TEXT NULL          -- prefer non-NULL S3 in production
```

**Create repo (post-cutover):** name + ACL + storage destination; empty Kirin
dataset (genesis commit with empty tree) or first sync creates genesis.
No remote URL required.

**Credentials APIs** (`/git-credential`, strict_credentials): deprecated;
migrate UI may still accept remote+PAT solely for `git2kirin`.

---

## Encryption (unchanged policy)

- Renders: client AES-GCM → CAS as opaque `renders/…`
- Assets: server AES-GCM → then hash/store under `assets/…`
- Markdown / data / comments: plaintext in Kirin (instance admin + S3 IAM can
  read) — same trust as “PAT holder could read git” today

---

## Phased delivery (revised for full git retirement)

### Phase 1 — `kirinstore` on S3 + `git2kirin` tip import

- Implement local + S3 Kirin store.
- Wire `StorageResolver.KirinStoreFor(repoID)`.
- Ship `pubobs migrate-git` / init manifest support.
- Admin “Import from Git” writes a Kirin tip commit + SQLite notes.
- Sync still dual-writes git **or** (feature flag) Kirin-only for new repos.

**Exit:** old Gogs/GitHub wikis importable to S3; readable via tip list API.

### Phase 2 — Sync + pull + reader are Kirin-only

- Plugin sync commits to Kirin; pull lists from tip.
- Pub render/asset/comments from tip.
- New repos cannot set `remote_url` (or ignore it).
- Stop calling `gitcache.Sync` when `repos.kirin_tip` is set / flag on.

**Exit:** steady-state traffic does not touch git.

### Phase 3 — Remove gitcache from runtime

- Delete warm-cache eviction complexity for repos; drop `/data/repos` volume
  requirement from compose defaults (keep temp dir for converter only).
- Remove per-user git credential flows from plugin/settings.
- GC unreachable CAS blobs; history API from Kirin parents.
- Optional: zip/bundle export.

**Exit:** no git binary required in the server image except for the converter
image/tag or a one-shot migrate container.

---

## VPS disk picture

| Path | After cutover |
|---|---|
| `/data/db/` | SQLite (small) |
| `/data/kirin/` | Only if destination is local; empty when all repos on S3 |
| `/data/repos/` | **Unused** (remove volume) |
| `/data/renders/`, `/data/assets/` | Legacy path-keyed stores; drain then remove after Kirin cutover |
| S3 | All dataset bytes + CAS blobs |

---

## Non-goals

- Keeping GitHub/Gogs as a live mirror after migrate
- Permanent VPS git snapshots
- Python Kirin runtime
- Preserving full git merge topology (first-parent linearization only)
- Reconstructing historical encrypted renders from pre-encryption git HTML

---

## Testing

- `git2kirin` tip-only: fixture bare repo → S3 (MinIO) → `List(tip)` matches
  tree; temp clone deleted.
- Full-history: N git commits → N (or N after first-parent) Kirin commits;
  file at old commit hash resolves.
- Idempotent re-run with same remote HEAD is a no-op.
- Sync after migrate: second commit parent = import tip; plugin pull sees
  hashes.
- Repo with S3 destination uses zero bytes under `/data/repos`.

---

## Open questions

1. **Default import depth:** tip-only vs full history for the init manifest?
   Recommendation: **tip-only** (fast, enough for PubObs); full history opt-in.
2. **Stale tip on sync:** HTTP 409 + force pull, or last-write-wins?
   Recommendation: **409** once Kirin is SoT (no git merge safety net).
3. **Should migrate encrypt assets** with the instance asset key during
   import? Yes — same as sync path.
4. **Multi-remote manifest ownership:** who becomes `owner_user_id` for
   imported repos in init script (first admin email)?
5. **Keep git in Docker image** only on a `migrate` profile vs always for
   `git2kirin` admin button?

---

## Summary

Architecture for your clarified goal:

1. **S3 Kirin catalog** = only durable content store for every repo.  
2. **No standing git snapshots on the VPS.**  
3. **`git2kirin`** (init script + admin import) is the bridge from old
   GitHub/Gogs remotes: ephemeral clone → Kirin commit(s) on S3 → drop clone
   and forget git creds.  
4. **Plugin and reader** talk only to PubObs → Kirin tip thereafter.
