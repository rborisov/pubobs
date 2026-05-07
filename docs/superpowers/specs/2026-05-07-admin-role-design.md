# Admin Role & Group Management — Design Spec
**Date:** 2026-05-07

---

## Overview

Introduce a user-level `is_admin` flag that allows non-superadmin users to create and manage repos and groups. Extend groups with per-member admin roles so group admins can manage membership independently. Superadmin (`is_instance_admin`) is unchanged — it sees everything.

### Role hierarchy summary

| Level | Flag / role | Can do |
|---|---|---|
| Superadmin | `users.is_instance_admin` | Everything, unrestricted |
| Admin | `users.is_admin` | Create repos/groups; manage repos where they hold `admin` repo role; manage groups where they hold `admin` group role; grant `is_admin` to others |
| Group admin | `group_members.role = 'admin'` | Manage membership of their group |
| Repo roles | `repo_access.role` | Unchanged: `reader`, `commentator`, `editor`, `admin` |

Any superadmin or `is_admin` user can grant `is_admin` to any other user.

---

## 1. Data Model

Two additive `ALTER TABLE` changes — no existing data touched.

### 1.1 `users` table

```sql
ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0
```

### 1.2 `group_members` table

```sql
ALTER TABLE group_members ADD COLUMN role TEXT NOT NULL DEFAULT 'member'
  CHECK (role IN ('member', 'admin'))
```

All existing group memberships default to `member`.

### 1.3 Go model changes

```go
// model/model.go
type User struct {
    // existing fields unchanged
    IsAdmin bool `json:"is_admin"`  // new
}

type GroupMember struct {
    GroupID string `json:"group_id"`
    UserID  string `json:"user_id"`
    Role    string `json:"role"` // "member" | "admin"
}
```

`Group` struct and `RepoAccess` struct are unchanged. `repo_access.role` CHECK constraint is unchanged.

### 1.4 Role resolution

`store.GetUserRole` is unchanged. It already returns the highest role a user holds on a repo, accounting for all group memberships. An `is_admin` user in a group with `admin` on a repo already receives `admin` from `GetUserRole`.

---

## 2. Auth / JWT

The existing JWT field `is_admin` maps to `is_instance_admin` (superadmin). A new field `is_user_admin` carries the new `is_admin` user flag. No existing field is renamed — backward compatibility is preserved.

### 2.1 JWT changes

```go
// auth/jwt.go
type AccessClaims struct {
    UserID      string
    Email       string
    IsAdmin     bool // is_instance_admin — unchanged
    IsUserAdmin bool // new: is_admin user flag
}

type accessJWTClaims struct {
    jwt.RegisteredClaims
    Email       string `json:"email"`
    IsAdmin     bool   `json:"is_admin"`
    IsUserAdmin bool   `json:"is_user_admin"` // new
    Type        string `json:"type"`
}
```

`IssueAccessToken` gains a `isUserAdmin bool` parameter. `issueTokenPair` gains the same parameter and reads `user.IsAdmin` alongside `user.IsInstanceAdmin`.

### 2.2 Permission check helpers (`api/rolecheck.go`)

**`requireAdmin`** (existing) — superadmin only. Unchanged. Used for ban, allowlist, set `is_instance_admin`.

**`requireAnyAdmin`** (new) — superadmin OR `is_admin` user:
```go
func requireAnyAdmin(claims *auth.AccessClaims, w http.ResponseWriter) bool {
    if claims.IsAdmin || claims.IsUserAdmin {
        return true
    }
    writeError(w, http.StatusForbidden, "admin required")
    return false
}
```

**`requireRepoManage`** (new) — superadmin, OR `is_admin` user whose effective repo role is `admin`:
```go
func requireRepoManage(ctx context.Context, deps *Deps, claims *auth.AccessClaims, repoID string) error {
    if claims.IsAdmin {
        return nil
    }
    if !claims.IsUserAdmin {
        return errors.New("admin required")
    }
    role, err := deps.Store.GetUserRole(ctx, claims.UserID, repoID)
    if err != nil {
        return errors.New("role check failed")
    }
    if role != "admin" {
        return errors.New("admin repo role required")
    }
    return nil
}
```

**`requireGroupAdmin`** (new) — superadmin, OR `is_admin` user with `admin` role in `group_members` for that group:
```go
func requireGroupAdmin(ctx context.Context, deps *Deps, claims *auth.AccessClaims, groupID string) error {
    if claims.IsAdmin {
        return nil
    }
    if !claims.IsUserAdmin {
        return errors.New("admin required")
    }
    ok, err := deps.Store.IsGroupAdmin(ctx, groupID, claims.UserID)
    if err != nil || !ok {
        return errors.New("group admin required")
    }
    return nil
}
```

New store method:
```go
func (s *Store) IsGroupAdmin(ctx context.Context, groupID, userID string) (bool, error)
// SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id=? AND user_id=? AND role='admin')
```

---

## 3. API Routes

### 3.1 Existing routes — permission change only

| Route | Before | After | Note |
|---|---|---|---|
| `POST /api/admin/repos` | superadmin | `requireAnyAdmin` | Auto-grants `admin` repo role to creator |
| `PUT /api/admin/repos/{id}` | superadmin | `requireRepoManage` | |
| `DELETE /api/admin/repos/{id}` | superadmin | `requireRepoManage` | |
| `PUT /api/admin/repos/{id}/guest-access` | superadmin | `requireRepoManage` | |
| `POST /api/admin/repos/{id}/import` | superadmin | `requireRepoManage` | |
| `GET /api/admin/repos/{id}/access` | superadmin | `requireRepoManage` | |
| `POST /api/admin/repos/{id}/access` | superadmin | `requireRepoManage` | |
| `DELETE /api/admin/repos/{id}/access/{accessID}` | superadmin | `requireRepoManage` | repoID already in URL |
| `GET /api/admin/users` | superadmin | `requireAnyAdmin` | |
| `POST /api/admin/groups` | superadmin | `requireAnyAdmin` | Auto-grants `admin` group role to creator |
| `POST /api/admin/groups/{id}/members` | superadmin | `requireGroupAdmin` | |

### 3.2 New routes

| Route | Access | Purpose |
|---|---|---|
| `GET /api/admin/groups` | `requireAnyAdmin` | List groups (superadmin: all; `is_admin` user: groups where they are `admin`) |
| `GET /api/admin/groups/{id}/members` | `requireGroupAdmin` | List members with roles |
| `DELETE /api/admin/groups/{id}` | `requireGroupAdmin` | Delete group |
| `DELETE /api/admin/groups/{id}/members/{userID}` | `requireGroupAdmin` | Remove member |
| `PUT /api/admin/groups/{id}/members/{userID}/role` | `requireGroupAdmin` | Set member role (`member` \| `admin`) |
| `POST /api/admin/users/{id}/user-admin` | `requireAnyAdmin` | Grant/revoke `is_admin` flag |

### 3.3 Unchanged superadmin-only routes

`POST /api/admin/users/{id}/admin` (set `is_instance_admin`), ban, allowlist, health — no change.

### 3.4 Repo listing (`GET /api/repos`)

Already correct. Superadmin uses `ListRepos` (all repos); everyone else uses `ListUserRepos` (repos with any access). `is_admin` users fall into the latter path and see only repos they were granted access to or created.

---

## 4. Store Methods

New methods in `store/`:

```go
// store/group.go
func (s *Store) ListGroups(ctx context.Context) ([]*model.Group, error)
func (s *Store) ListAdminGroups(ctx context.Context, userID string) ([]*model.Group, error)
func (s *Store) DeleteGroup(ctx context.Context, id string) error
func (s *Store) ListGroupMembers(ctx context.Context, groupID string) ([]*model.GroupMember, error)
func (s *Store) RemoveGroupMember(ctx context.Context, groupID, userID string) error
func (s *Store) SetGroupMemberRole(ctx context.Context, groupID, userID, role string) error
func (s *Store) IsGroupAdmin(ctx context.Context, groupID, userID string) (bool, error)

// store/user.go
func (s *Store) SetUserAdmin(ctx context.Context, id string, isAdmin bool) error
```

`AddGroupMember` (existing) gains an optional `role` parameter, defaulting to `member`.

---

## 5. Frontend

### 5.1 `api.ts`

- `User` / `Me` types gain `is_admin: boolean`
- New functions: `listGroups`, `createGroup`, `deleteGroup`, `listGroupMembers`, `addGroupMember`, `removeGroupMember`, `setGroupMemberRole`, `setUserIsAdmin`

### 5.2 `main.ts` — navigation

| User | Default route | Nav links |
|---|---|---|
| `is_instance_admin` | `/repos` | Repos, Users, Allowlist (unchanged) |
| `is_admin` | `/repos` | Repos, Groups, Users |
| Regular | `/dashboard` | My repos (unchanged) |

Route `/groups` registered, pointing to new `groupsView`.

### 5.3 `views/repos.ts`

Signature: `reposView(me: Me)`. Management controls (Edit, Delete, Import, Guest toggle) shown only when `me.is_instance_admin || repo.role === 'admin'`. The `+ New repo` button shown for both superadmin and `is_admin` users.

### 5.4 `views/users.ts`

Signature: `usersView(me: Me)`. For superadmin: existing behaviour (instance-admin toggle + ban). For `is_admin` user: only "Make admin" / "Remove admin" button using `setUserIsAdmin` — no ban button, no instance-admin toggle.

### 5.5 `views/groups.ts` (new)

- Lists groups the current user can manage
- `+ New group` form (name field only)
- Per group: expandable member list with roles, "Add member" (by user ID or email), "Remove" per member, role toggle (`member` ↔ `admin`)

---

## 6. Files Affected

**Backend:**
- `internal/db/db.go` — two `ALTER TABLE` migration calls
- `internal/db/migrations/001_init.sql` — update base schema for new installs
- `internal/model/model.go` — `User.IsAdmin`, `GroupMember` type
- `internal/auth/jwt.go` — `AccessClaims.IsUserAdmin`, `IssueAccessToken` signature
- `internal/api/auth.go` — `issueTokenPair` signature, pass `user.IsAdmin`
- `internal/api/rolecheck.go` — add `requireAnyAdmin`, `requireRepoManage`, `requireGroupAdmin`
- `internal/api/admin.go` — update permission checks on existing handlers; add group and user-admin handlers
- `internal/api/router.go` — register new routes
- `internal/store/group.go` — new store methods
- `internal/store/user.go` — add `SetUserAdmin`
- `internal/store/access.go` — no change

**Frontend:**
- `frontend/src/api.ts` — new types and API functions
- `frontend/src/main.ts` — routing and nav logic
- `frontend/src/views/repos.ts` — conditional controls, accept `me` param
- `frontend/src/views/users.ts` — conditional controls, accept `me` param
- `frontend/src/views/groups.ts` — new file

---

## Out of Scope

- Group access to allowlist or ban controls
- Group admins granting `is_admin` (only users with `is_admin` flag can do that)
- UI for `is_admin` users to manage other admins' groups
