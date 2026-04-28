# PubObs Platform Redesign Design

**Date:** 2026-04-28
**Replaces:** `docs/superpowers/specs/2026-04-26-phase1-obsidian-plugin-design.md` (Phase 1 plugin is the starting point; this spec supersedes it for all future phases)

---

## 1. Overview

PubObs is a self-hosted collaborative publishing platform built around Obsidian. Authors write notes in Obsidian and sync them to a central server; the server commits them to any HTTPS git remote and serves a read-only wiki with comments and version history.

**Core principles:**

- Self-hosted: one instance per organisation, deployed via Docker Compose
- Git-agnostic: works with any HTTPS git remote (GitHub, GitLab, Gitea, bare server)
- Obsidian-faithful: the web viewer shows HTML rendered by Obsidian's own renderer
- Collaboration is Obsidian-to-Obsidian: the web is read-only; editing happens in Obsidian
- Identity is delegated: PubObs never stores passwords; it consumes an external OIDC provider

---

## 2. Architecture

### Components

| Component | Language | Role |
|---|---|---|
| **PubObs Backend** | Go | Central API, git cache, auth, background jobs |
| **Web Frontend** | React + Vite + TypeScript | Admin panel + wiki viewer + comments; static files embedded in Go binary |
| **Obsidian Plugin** | TypeScript | Thin HTTP client; syncs notes via backend API |
| **SQLite** | — | All server-side state |
| **Git remote** | any | Source of truth for note content; admin provides URL + PAT |
| **OIDC Provider** | external | Identity (Keycloak, Google, GitHub, Azure AD, Okta) |

### Component diagram

```
┌─────────────────────────┐    ┌───────────────────────────────┐
│   Obsidian Plugin (TS)  │    │   Web Frontend (React/Vite)   │
│  PKCE auth · sync out   │    │  admin panel · wiki · comments│
│  sync in · env validate │    │  served by Go binary (embed)  │
└────────────┬────────────┘    └───────────────┬───────────────┘
             │ HTTPS REST · Bearer JWT          │ HTTPS REST · Bearer JWT
             ▼                                  ▼
┌──────────────────────────────────────────────────────────────┐
│                    PubObs Backend (Go)                       │
│  auth · sync API · pull API · wiki API · comment API         │
│  user/group/repo mgmt · repo cache · background jobs         │
└────────────────┬────────────────────────┬────────────────────┘
                 │ system git commands     │ SQL
                 ▼                         ▼
   ┌─────────────────────────┐   ┌─────────────────────────┐
   │  Local git clones       │   │       SQLite            │
   │  /data/repos/{id}/      │   │  users · groups · repos │
   │  (cache, evicted on TTL)│   │  snapshots · comments   │
   └────────────┬────────────┘   └─────────────────────────┘
                │ git push/pull (HTTPS + PAT)
                ▼
   ┌─────────────────────────┐   ┌─────────────────────────┐
   │  Any HTTPS git remote   │   │     OIDC Provider       │
   │  GitHub · GitLab · bare │   │  Keycloak · Google · AD │
   └─────────────────────────┘   └─────────────────────────┘
```

---

## 3. Data Model (SQLite)

### `users`
| Column | Type | Notes |
|---|---|---|
| id | TEXT UUID PK | |
| email | TEXT UNIQUE | from OIDC ID token |
| name | TEXT | |
| is_instance_admin | BOOL | can manage all repos, users, groups |
| created_at | DATETIME | |

### `groups`
| Column | Type | Notes |
|---|---|---|
| id | TEXT UUID PK | |
| name | TEXT UNIQUE | |
| created_at | DATETIME | |

### `group_members`
| Column | Type | Notes |
|---|---|---|
| group_id | TEXT FK → groups | |
| user_id | TEXT FK → users | |
| PK | (group_id, user_id) | |

### `repos`
| Column | Type | Notes |
|---|---|---|
| id | TEXT UUID PK | |
| name | TEXT | display name |
| remote_url | TEXT | any HTTPS git remote |
| encrypted_creds | TEXT | PAT encrypted with `PUBOBS_SECRET_KEY` |
| default_branch | TEXT | e.g. "main" |
| local_path | TEXT nullable | NULL = not currently cached |
| cloned_at | DATETIME nullable | when local clone was created |
| last_used_at | DATETIME nullable | drives cache eviction |
| created_at | DATETIME | |

### `repo_access`
| Column | Type | Notes |
|---|---|---|
| id | TEXT UUID PK | |
| repo_id | TEXT FK → repos | |
| principal_type | TEXT | 'user' or 'group' |
| principal_id | TEXT | user.id or group.id |
| role | TEXT | 'reader', 'commentator', 'editor', 'admin' |
| UNIQUE | (repo_id, principal_type, principal_id) | |

**Roles (cumulative):**
- `reader` — view notes and version history in wiki
- `commentator` — reader + post comments
- `editor` — commentator + sync notes in/out via plugin
- `admin` — editor + manage repo settings, users, permissions

### `notes`
| Column | Type | Notes |
|---|---|---|
| id | TEXT UUID PK | |
| repo_id | TEXT FK → repos | |
| path | TEXT | relative path within repo, e.g. `docs/intro.md` |
| updated_at | DATETIME | |
| UNIQUE | (repo_id, path) | |

### `note_snapshots`
| Column | Type | Notes |
|---|---|---|
| id | TEXT UUID PK | |
| note_id | TEXT FK → notes | |
| html_content | TEXT | rendered by Obsidian's `MarkdownRenderer` |
| metadata_json | TEXT | `{headings, links, tags, frontmatter}` extracted server-side |
| synced_by | TEXT FK → users | |
| git_commit_sha | TEXT | |
| synced_at | DATETIME | |

One row per note (upserted on sync). Git history is the version store; this row is the fast-read cache for the wiki viewer.

### `comments`
| Column | Type | Notes |
|---|---|---|
| id | TEXT UUID PK | |
| note_id | TEXT FK → notes | |
| user_id | TEXT FK → users | |
| parent_id | TEXT FK → comments nullable | threading |
| body | TEXT | |
| created_at | DATETIME | |

### `note_links`
| Column | Type | Notes |
|---|---|---|
| source_note_id | TEXT FK → notes | note that contains the link |
| target_path | TEXT | raw link target as written in Obsidian (e.g. `other-note` or `folder/note`) |
| PK | (source_note_id, target_path) | |

Populated by the backend on each sync by parsing `metadata_json.links`. Used to serve backlinks ("notes that link here") without re-parsing HTML. `target_path` is the unresolved alias — resolution to a `note.id` happens at query time by matching against `notes.path` in the same repo.

### `folder_mappings`
| Column | Type | Notes |
|---|---|---|
| user_id | TEXT FK → users | |
| repo_id | TEXT FK → repos | |
| vault_folder | TEXT | vault folder path (relative to vault root) |
| repo_subfolder | TEXT | subfolder within repo (empty = root) |
| PK | (user_id, repo_id) | |

Stored server-side so folder paths are remembered across the user's devices. One mapping per user per repo — a user can sync different `repo_subfolder` paths from the same repo only by registering multiple repos pointing to the same remote.

### `system_health`
| Column | Type | Notes |
|---|---|---|
| id | INT PK | always 1 (single row) |
| disk_free_pct | REAL | percentage of free disk on `/data/` |
| disk_free_bytes | INT | |
| disk_status | TEXT | 'ok', 'warn', 'crit' |
| last_eviction_at | DATETIME | last time the eviction job ran |
| checked_at | DATETIME | last time the background job updated this row |

---

## 4. Repo Cache

Local git clones are a **cache**, not permanent storage. The git remote is the source of truth.

**Policy:**
- Clone on demand when a sync or pull request arrives and `local_path IS NULL`
- Update `last_used_at` on every clone access
- Background goroutine runs on `PUBOBS_CACHE_CHECK_INTERVAL` (default: 1h)
  - Deletes clone directories where `last_used_at < now - PUBOBS_REPO_CACHE_TTL` (default: 24h)
  - Sets `local_path = NULL`, `cloned_at = NULL` on evicted repos
- Per-repo mutex serialises all git operations on the same repo

**Disk monitoring (same background goroutine):**
- Reads free disk on `/data/` after eviction pass
- Updates `system_health` row
- `disk_status`:
  - `ok` — free ≥ `PUBOBS_DISK_WARN_PCT` (default: 20%)
  - `warn` — free < warn threshold → warning banner in admin panel
  - `crit` — free < `PUBOBS_DISK_CRIT_PCT` (default: 5%) → critical banner + new clone requests rejected with 507

---

## 5. Key Flows

### 5.1 Plugin Auth (OAuth 2.0 PKCE + OIDC)

1. User enters backend URL in plugin settings, clicks "Connect to PubObs"
2. Plugin generates PKCE `code_verifier` + `code_challenge`, starts local HTTP server on a random port
3. Plugin opens browser: `{backend}/auth/plugin?redirect_uri=http://localhost:{port}/callback&code_challenge={hash}&code_challenge_method=S256&state={random}`
4. Backend redirects browser to OIDC provider login page
5. User authenticates with org credentials on OIDC provider
6. OIDC provider redirects back to backend with auth code
7. Backend exchanges code with OIDC provider, validates ID token, upserts user in DB
8. Backend generates a short-lived PKCE auth code, redirects to `localhost:{port}/callback?code={code}&state={state}`
9. Plugin verifies state, POSTs `{code, code_verifier}` to `{backend}/auth/token`
10. Backend verifies PKCE, issues signed JWT (24h) + refresh token (30d)
11. Plugin stores JWT + refresh token + backend URL in settings, closes local server

### 5.2 Sync Out (Plugin → Backend)

1. User triggers "Sync" command in Obsidian
2. `EnvironmentValidator` checks `workspace.json` — Obsidian version + required plugins; abort with red Notice on any failure
3. Plugin identifies changed `.md` files in the linked vault folder (files whose modification time is newer than the timestamp stored in the plugin's local sync manifest for that repo)
4. For each file: render HTML via `MarkdownRenderer.render()`, extract frontmatter
5. Plugin POSTs `{path, md_content, html_content, frontmatter}[]` to `POST /api/repos/{id}/sync` with Bearer JWT
6. Backend validates JWT, checks editor+ role on repo
7. Backend acquires per-repo mutex
8. If `local_path IS NULL`: clone repo (fail with 502 if remote unreachable)
9. `git pull` → write `.md` files to clone dir → `git add` → `git commit -m "pubobs: sync {ISO timestamp} by {user}"` → `git push`
10. Upsert `notes` + `note_snapshots` rows in SQLite; update `last_used_at`
11. Return `{commit_sha}` to plugin
12. Plugin shows green Notice: "Synced N file(s) — {sha7}"

### 5.3 Sync In (Backend → Plugin)

1. User triggers "Pull" command in Obsidian (or auto on connect)
2. Plugin GETs `/api/repos/{id}/files` with Bearer JWT
3. Backend validates JWT, checks reader+ role
4. Backend acquires per-repo mutex
5. If `local_path IS NULL`: clone repo
6. `git pull` → read file tree → return `[{path, content, sha}]`
7. Plugin writes `.md` files to vault folder, skipping files whose SHA matches the last pulled version (SHAs stored in the plugin's local sync manifest per repo)
8. Plugin updates sync manifest with new SHAs + timestamp
9. Plugin shows green Notice: "Pulled N file(s)"

---

## 6. API Surface (Backend)

| Method | Path | Role required | Description |
|---|---|---|---|
| GET | `/auth/plugin` | — | Start PKCE flow, redirect to OIDC |
| POST | `/auth/token` | — | Exchange PKCE code for JWT |
| POST | `/auth/refresh` | — | Refresh JWT using refresh token |
| GET | `/api/me` | any | Current user info + repo access list |
| GET | `/api/repos` | any | Repos the user has access to |
| POST | `/api/repos/{id}/sync` | editor+ | Sync notes from plugin |
| GET | `/api/repos/{id}/files` | reader+ | Pull latest files to plugin |
| GET | `/api/repos/{id}/notes` | reader+ | Folder tree for wiki |
| GET | `/api/repos/{id}/notes/{path}` | reader+ | HTML snapshot for wiki viewer |
| GET | `/api/repos/{id}/notes/{path}/history` | reader+ | Git log for note |
| POST | `/api/repos/{id}/notes/{path}/comments` | commentator+ | Add comment |
| GET | `/api/repos/{id}/notes/{path}/comments` | reader+ | List comments |
| GET | `/api/admin/health` | instance_admin | Disk + cache status |
| POST | `/api/admin/repos` | instance_admin | Register new repo |
| PUT | `/api/admin/repos/{id}` | instance_admin | Update repo config |
| DELETE | `/api/admin/repos/{id}` | instance_admin | Remove repo |
| POST | `/api/admin/repos/{id}/access` | repo admin role | Grant access |
| DELETE | `/api/admin/repos/{id}/access/{access_id}` | repo admin role | Revoke access |
| GET | `/api/admin/users` | instance_admin | List users |
| POST | `/api/admin/groups` | instance_admin | Create group |
| POST | `/api/admin/groups/{id}/members` | instance_admin | Add member |

---

## 7. Sync Payload Format

Plugin sends to `POST /api/repos/{id}/sync`:

```json
{
  "files": [
    {
      "path": "docs/intro.md",
      "md_content": "# Introduction\n...",
      "html_content": "<h1>Introduction</h1>...",
      "frontmatter": {
        "obsidian_version": "1.5.3",
        "pubobs_plugin_version": "0.2.0",
        "tags": ["intro", "docs"],
        "aliases": []
      }
    }
  ]
}
```

`metadata_json` stored in `note_snapshots` is extracted server-side from `md_content` (headings, links) merged with `frontmatter`.

---

## 8. Inter-Note Link Resolution

Obsidian renders `[[other-note]]` as `<a class="internal-link" href="other-note">other-note</a>` in its HTML output. These hrefs are bare note names or paths — they don't point to any URL on the web.

**Resolution strategy: frontend, at render time.**

The wiki viewer intercepts all clicks on `.internal-link` elements. On click it looks up the link target against the note index (the flat list of `{path, id}` pairs already returned by `GET /api/repos/{id}/notes`):

- Target found in index → navigate to `/wiki/repos/{id}/notes/{path}`
- Target not found → show an inline tooltip: "This note hasn't been synced yet"

Resolution is always current: if a linked note is synced after the linking note, the link works automatically on the next page visit — no re-sync or HTML re-render needed.

**Backend: `note_links` table.**

On each sync the backend parses `metadata_json.links` (inter-note link targets extracted from the `.md` file) and upserts rows into `note_links`. This powers two features:

- `GET /api/repos/{id}/notes/{path}/backlinks` — "notes that link here", useful for the wiki viewer sidebar
- Future graph view of the note network

**API addition:**

| Method | Path | Role required | Description |
|---|---|---|---|
| GET | `/api/repos/{id}/notes/{path}/backlinks` | reader+ | Notes in this repo that link to this note |

---

## 9. Error Handling

| Scenario | HTTP status | Plugin behaviour |
|---|---|---|
| Env validation fails | — (local) | Red Notice, sync aborted before any HTTP call |
| JWT expired | 401 | Plugin auto-refreshes; prompts re-auth if refresh fails |
| Insufficient role | 403 | Red Notice: "You don't have editor access to this repo" |
| git push conflict | 409 | Red Notice: "Pull first, then sync" |
| Remote unreachable / bad creds | 502 | Red Notice: "Could not reach git remote. Check repo config." |
| Disk critical | 507 | Red Notice: "Server disk full. Contact admin." |

---

## 9. Deployment

Single `docker-compose.yml`:

```yaml
services:
  pubobs:
    image: ghcr.io/org/pubobs:latest
    ports:
      - "8080:8080"
    volumes:
      - ./data/db:/data/db        # SQLite file
      - ./data/repos:/data/repos  # git clone cache
    environment:
      PUBOBS_OIDC_ISSUER: https://accounts.google.com
      PUBOBS_OIDC_CLIENT_ID: ...
      PUBOBS_OIDC_CLIENT_SECRET: ...
      PUBOBS_SECRET_KEY: ...          # JWT signing + cred encryption
      PUBOBS_REPO_CACHE_TTL: 24h
      PUBOBS_CACHE_CHECK_INTERVAL: 1h
      PUBOBS_DISK_WARN_PCT: 20
      PUBOBS_DISK_CRIT_PCT: 5
```

**Backup:** `cp -r ./data/ backup/` — captures DB + all cached clones (remotes are the real backup).

**Updates:** `docker compose pull && docker compose up -d`

**GitHub Releases:** pre-built Linux amd64/arm64 binaries for users who don't want Docker.

The Go binary embeds the frontend static files via `go:embed` — no nginx or separate static server needed.

---

## 10. Testing Strategy

### Phase 2 — Backend
- **Unit:** auth/PKCE logic, permission resolution, eviction policy, disk threshold calculation
- **Integration:** sync flow against a real local bare git repo (created in `TestMain`)
- **HTTP handler tests:** each API endpoint tested with a real SQLite in-memory DB

### Phase 3 — Plugin
- **Unit:** PKCE code generation, `SnapshotRenderer` output shape, `AuthClient` token refresh logic
- **Integration:** `SyncClient` against a running Phase 2 backend (or a recorded mock)
- **EnvironmentValidator tests:** unchanged from Phase 1

### Phase 4 — Frontend
- **Component tests:** Vitest + React Testing Library for admin forms, wiki viewer, comment thread
- **E2E:** Playwright against a running backend with seeded data

---

## 11. Out of Scope

- SSH authentication for git remotes (HTTPS + PAT only in this design)
- Real-time collaboration (simultaneous editing in Obsidian — sync is push/pull, not CRDT)
- Email notifications (disk warnings are in-app only)
- Mobile Obsidian support (plugin is desktop-only, inheriting Phase 1 constraint)
- workspace.json tooling UI (Phase 6, unchanged from Phase 1 plan)
