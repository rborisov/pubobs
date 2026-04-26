# Phase 1 — PubObs Obsidian Plugin Design

**Date:** 2026-04-26
**Scope:** Core Obsidian plugin — Git sync for `.md` notes and assets, environment validation

---

## 1. Overview

Phase 1 delivers the Obsidian plugin that lets authors push their vault notes to a central Git repository. Reviewers and the frontend (Phases 3–4) read from that repository. No snapshot rendering in this phase — that is Phase 2.

**Key decisions:**
- Git library: `isomorphic-git` (pure JS, no binary dependency, cross-platform)
- Remote: any HTTPS Git host — Gogs, GitHub, GitLab, bare repo — configured per-vault in settings
- Auth: HTTPS + Personal Access Token
- Sync trigger: explicit command (primary) + optional auto-sync on save
- Validation failure: hard block, no bypass

---

## 2. Architecture

Four focused modules wired by a thin orchestrator.

### SettingsManager
Extends Obsidian's `Plugin.loadData()` / `saveData()`. Owns the settings data model and renders the settings tab in Obsidian's Settings panel.

**Settings model:**
```ts
interface PubObsSettings {
  remoteUrl: string;
  username: string;
  pat: string;
  branch: string;       // default: "main"
  autoSync: boolean;    // default: false
}
```

### EnvironmentValidator
Reads `workspace.json` from the root of the local repo clone. Compares the manifest requirements against the live Obsidian environment. Returns a typed result listing all failures (not just the first).

### GitService
Thin wrapper around `isomorphic-git` + its `@isomorphic-git/http-node` plugin. Exposes five methods:

| Method | Description |
|---|---|
| `clone()` | Initial clone of the remote into the vault directory |
| `pull()` | Fetch + merge latest from remote before staging |
| `stage(files)` | Add modified `.md` files and assets; respects `.gitignore` |
| `commit(message)` | Create commit with message `"pubobs: sync <ISO timestamp>"` |
| `push()` | Push to remote using HTTPS + PAT from settings |

### SyncOrchestrator
Entry point for all sync operations. Wires the other three modules, shows Obsidian `Notice` feedback at each step, and registers the file-watcher for auto-sync.

---

## 3. Sync Data Flow

```
① Trigger       Author runs "PubObs: Sync" command  OR  file saved (if autoSync enabled)
       ↓
② Validate      EnvironmentValidator.check()
                  → reads workspace.json from repo root
                  → checks app.version >= minObsidianVersion
                  → checks each requiredPlugin is installed and version >= minVersion
                  → ANY failure: red Notice with specific message → ABORT
       ↓
③ Pull          GitService.pull()  — fetch latest before staging to minimise conflicts
       ↓
④ Stage         GitService.stage() — all modified .md files + assets in vault
       ↓
⑤ Commit        GitService.commit("pubobs: sync <ISO timestamp>")
                  → skip if nothing staged (no empty commits)
       ↓
⑥ Push          GitService.push()
                  → success: green Notice with short commit hash
                  → auth failure: red Notice linking to PubObs settings
```

**Phase 2 insertion point:** snapshot rendering slots between ④ Stage and ⑤ Commit.

---

## 4. Settings UI

Settings tab inside **Obsidian → Settings → PubObs**, three sections:

**Git Remote**
- Remote URL (text, placeholder: `https://gogs.example.com/team/vault.git`)
- Username (text)
- Branch (text, default: `main`)
- Personal Access Token (password input, masked)
- "Test connection" button → inline Connected ✓ / Failed ✗ indicator

**Sync**
- Auto-sync on save (toggle, default off)

**Environment**
- workspace.json status badge (Valid / Invalid / Missing)
- Per-requirement breakdown (Obsidian version, each plugin)

---

## 5. workspace.json Manifest

Lives at the repository root. Authors create and maintain it manually (Phase 6 adds tooling).

```json
{
  "minObsidianVersion": "1.4.0",
  "requiredPlugins": [
    { "id": "dataview", "minVersion": "0.5.55" },
    { "id": "templater-obsidian", "minVersion": "2.0.0" }
  ],
  "snapshotFormat": "1.0"
}
```

| Field | Purpose |
|---|---|
| `minObsidianVersion` | Minimum Obsidian app version; compared via semver against `app.version` |
| `requiredPlugins` | List of plugins that must be installed and enabled; `id` matches plugin folder name |
| `snapshotFormat` | Reserved for Phase 2; read but not enforced in Phase 1 |

---

## 6. Validation Error Messages

Hard block — sync is refused until the author resolves the issue.

| Condition | Notice text |
|---|---|
| `workspace.json` missing | "PubObs: workspace.json not found in repo root. Create it to enable sync." |
| Obsidian version too old | "PubObs: Obsidian 1.4.0+ required. You have 1.3.2. Please upgrade before syncing." |
| Plugin not installed | "PubObs: Plugin 'dataview' is required but not installed." |
| Plugin version too old | "PubObs: Plugin 'dataview' 0.5.55+ required. Installed: 0.4.12. Please upgrade." |

---

## 7. File Structure

```
obsidian-plugin/
  src/
    main.ts               # Plugin entry point, registers commands + settings tab
    settings.ts           # SettingsManager + PubObsSettings model
    validator.ts          # EnvironmentValidator
    git.ts                # GitService (isomorphic-git wrapper)
    orchestrator.ts       # SyncOrchestrator
  tests/
    validator.test.ts
    git.test.ts
  manifest.json           # Obsidian plugin manifest
  package.json
  tsconfig.json
```

---

## 8. Testing

**Stack:** Jest + TypeScript. Obsidian API stubbed with a lightweight mock.

**Unit tests — EnvironmentValidator (`validator.test.ts`):**
- Valid environment passes
- Obsidian version below minimum → version error
- Required plugin not installed → missing-plugin error
- Plugin below minimum version → version error with correct name
- `workspace.json` missing → missing-manifest error
- Multiple failures all reported (not just first)

**Integration tests — GitService (`git.test.ts`):**
- `clone()` creates local working copy (local bare repo in `beforeAll`)
- `pull()` fetches new commits from remote
- `stage()` adds modified `.md` files and assets
- `commit()` creates commit with expected message format
- `push()` with valid token succeeds; invalid token throws auth error
- Nothing staged → commit skipped

**Manual checklist:**
- [ ] Settings tab renders correctly in Obsidian
- [ ] "Test connection" shows Connected / Failed correctly
- [ ] "Sync" command appears in command palette and runs
- [ ] Auto-sync fires on file save when toggle is on
- [ ] Validation failure shows correct red Notice and blocks push
- [ ] Successful sync shows green Notice with commit hash
- [ ] End-to-end test against a real Gogs instance

---

## 9. Out of Scope (Phase 1)

- Snapshot rendering (Phase 2)
- Backend auth server (Phase 3)
- Frontend web app (Phase 4)
- Comment syncing (Phase 5)
- workspace.json tooling / enforcement UI (Phase 6)
- SSH authentication
