# Admin Role & Group Management — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a user-level `is_admin` flag so non-superadmin users can create and manage repos/groups, without seeing resources they have no access to.

**Architecture:** Two additive DB columns (`users.is_admin`, `group_members.role`). JWT gains `is_user_admin` claim alongside the existing `is_admin` (superadmin) claim. Three new permission helpers gate the extended endpoints. Frontend adds a Groups view and conditionally shows management controls based on user level and repo role.

**Tech Stack:** Go 1.23, SQLite (modernc.org/sqlite), chi v5, testify, TypeScript, esbuild

---

## File Map

**Created:**
- `frontend/src/views/groups.ts`

**Modified:**
- `backend/internal/db/db.go`
- `backend/internal/db/migrations/001_init.sql`
- `backend/internal/model/model.go`
- `backend/internal/auth/jwt.go`
- `backend/internal/auth/jwt_test.go`
- `backend/internal/api/auth.go`
- `backend/internal/api/rolecheck.go`
- `backend/internal/api/admin.go`
- `backend/internal/api/router.go`
- `backend/internal/api/me.go`
- `backend/internal/api/admin_test.go`
- `backend/internal/api/me_test.go`
- `backend/internal/store/user.go`
- `backend/internal/store/user_test.go`
- `backend/internal/store/group.go`
- `backend/internal/store/group_test.go`
- `frontend/src/api.ts`
- `frontend/src/main.ts`
- `frontend/src/views/repos.ts`
- `frontend/src/views/users.ts`

---

### Task 1: DB migrations and Go model types

**Files:**
- Modify: `backend/internal/db/migrations/001_init.sql`
- Modify: `backend/internal/db/db.go`
- Modify: `backend/internal/model/model.go`

- [ ] **Step 1: Update base schema for new installs**

In `backend/internal/db/migrations/001_init.sql`, add `is_admin` to the `users` table and `role` to `group_members`:

```sql
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    email TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    is_instance_admin INTEGER NOT NULL DEFAULT 0,
    is_banned         INTEGER NOT NULL DEFAULT 0,
    is_admin          INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

```sql
CREATE TABLE IF NOT EXISTS group_members (
    group_id TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id  TEXT NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    role     TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('member', 'admin')),
    PRIMARY KEY (group_id, user_id)
);
```

- [ ] **Step 2: Add migrations for existing installs in `db.go`**

After the existing `allow_guest` migration block, add:

```go
if _, err := db.Exec(`ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0`); err != nil {
    if !strings.Contains(err.Error(), "duplicate column name") {
        db.Close()
        return nil, fmt.Errorf("migrate users.is_admin: %w", err)
    }
}
if _, err := db.Exec(`ALTER TABLE group_members ADD COLUMN role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('member', 'admin'))`); err != nil {
    if !strings.Contains(err.Error(), "duplicate column name") {
        db.Close()
        return nil, fmt.Errorf("migrate group_members.role: %w", err)
    }
}
```

- [ ] **Step 3: Add `IsAdmin` to User model and `GroupMember` type in `model.go`**

In `User`, add `IsAdmin bool` after `IsBanned`:

```go
type User struct {
	ID              string    `json:"id"`
	Email           string    `json:"email"`
	Name            string    `json:"name"`
	IsInstanceAdmin bool      `json:"is_instance_admin"`
	IsBanned        bool      `json:"is_banned"`
	IsAdmin         bool      `json:"is_admin"`
	CreatedAt       time.Time `json:"created_at"`
}
```

Add `GroupMember` type after `Group`:

```go
type GroupMember struct {
	GroupID string `json:"group_id"`
	UserID  string `json:"user_id"`
	Role    string `json:"role"` // "member" | "admin"
}
```

- [ ] **Step 4: Verify existing tests still pass**

```bash
cd backend && go test ./...
```

Expected: all packages pass (db, store, api, auth).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/db/migrations/001_init.sql backend/internal/db/db.go backend/internal/model/model.go
git commit -m "feat: add is_admin to users and role to group_members schema"
```

---

### Task 2: Store — user `is_admin`

**Files:**
- Modify: `backend/internal/store/user.go`
- Modify: `backend/internal/store/group.go`
- Modify: `backend/internal/store/user_test.go`

- [ ] **Step 1: Write failing test for `SetUserAdmin`**

Add to `backend/internal/store/user_test.go`:

```go
func TestSetUserAdmin(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.UpsertUser(ctx, "u1", "a@x.com", "A")

	err := s.SetUserAdmin(ctx, "u1", true)
	require.NoError(t, err)

	u, err := s.GetUserByID(ctx, "u1")
	require.NoError(t, err)
	require.True(t, u.IsAdmin)

	require.NoError(t, s.SetUserAdmin(ctx, "u1", false))
	u, _ = s.GetUserByID(ctx, "u1")
	require.False(t, u.IsAdmin)
}
```

- [ ] **Step 2: Run to confirm it fails**

```bash
cd backend && go test ./internal/store/... -run TestSetUserAdmin -v
```

Expected: compile error — `SetUserAdmin` undefined and `IsAdmin` field missing from scan.

- [ ] **Step 3: Update `scanUser` and all user SELECT queries in `user.go`**

Replace `scanUser`:

```go
func scanUser(row scanner) (*model.User, error) {
	var u model.User
	var admin, banned, isAdmin int
	err := row.Scan(&u.ID, &u.Email, &u.Name, &admin, &banned, &isAdmin, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	u.IsInstanceAdmin = admin == 1
	u.IsBanned = banned == 1
	u.IsAdmin = isAdmin == 1
	return &u, nil
}
```

Update every `SELECT ... FROM users` in `user.go` to include `is_admin` between `is_banned` and `created_at`:

```go
// GetUserByID
`SELECT id, email, name, is_instance_admin, is_banned, is_admin, created_at FROM users WHERE id=?`

// GetUserByEmail
`SELECT id, email, name, is_instance_admin, is_banned, is_admin, created_at FROM users WHERE email=?`

// ListUsers
`SELECT id, email, name, is_instance_admin, is_banned, is_admin, created_at FROM users ORDER BY created_at`

// ListInstanceAdmins
`SELECT id, email, name, is_instance_admin, is_banned, is_admin, created_at FROM users WHERE is_instance_admin=1`
```

Add `SetUserAdmin` at the bottom of `user.go`:

```go
func (s *Store) SetUserAdmin(ctx context.Context, userID string, admin bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET is_admin=? WHERE id=?`, admin, userID)
	return err
}
```

- [ ] **Step 4: Update `GetGroupMembers` query in `group.go` to include `is_admin`**

`GetGroupMembers` uses `scanUser`, so its SELECT also needs `is_admin`:

```go
func (s *Store) GetGroupMembers(ctx context.Context, groupID string) ([]*model.User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.email, u.name, u.is_instance_admin, u.is_banned, u.is_admin, u.created_at
		FROM users u
		JOIN group_members gm ON gm.user_id = u.id
		WHERE gm.group_id=?`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Run tests**

```bash
cd backend && go test ./internal/store/... -v
```

Expected: all store tests pass including `TestSetUserAdmin`.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/store/user.go backend/internal/store/user_test.go backend/internal/store/group.go
git commit -m "feat: add SetUserAdmin and is_admin field to user store"
```

---

### Task 3: Store — group roles and admin helpers

**Files:**
- Modify: `backend/internal/store/group.go`
- Modify: `backend/internal/store/group_test.go`

- [ ] **Step 1: Write failing tests**

Add to `backend/internal/store/group_test.go`:

```go
func TestGroupMemberRoles(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.UpsertUser(ctx, "u2", "b@x.com", "B")
	s.CreateGroup(ctx, "g1", "Team")

	require.NoError(t, s.AddGroupMember(ctx, "g1", "u1", "admin"))
	require.NoError(t, s.AddGroupMember(ctx, "g1", "u2", "member"))

	members, err := s.ListGroupMembers(ctx, "g1")
	require.NoError(t, err)
	require.Len(t, members, 2)

	roles := map[string]string{}
	for _, m := range members {
		roles[m.UserID] = m.Role
	}
	require.Equal(t, "admin", roles["u1"])
	require.Equal(t, "member", roles["u2"])
}

func TestIsGroupAdmin(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.UpsertUser(ctx, "u2", "b@x.com", "B")
	s.CreateGroup(ctx, "g1", "Team")
	s.AddGroupMember(ctx, "g1", "u1", "admin")
	s.AddGroupMember(ctx, "g1", "u2", "member")

	ok, err := s.IsGroupAdmin(ctx, "g1", "u1")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = s.IsGroupAdmin(ctx, "g1", "u2")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestSetGroupMemberRole(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.CreateGroup(ctx, "g1", "Team")
	s.AddGroupMember(ctx, "g1", "u1", "member")

	require.NoError(t, s.SetGroupMemberRole(ctx, "g1", "u1", "admin"))

	ok, _ := s.IsGroupAdmin(ctx, "g1", "u1")
	require.True(t, ok)
}

func TestListAdminGroups(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.CreateGroup(ctx, "g1", "Alpha")
	s.CreateGroup(ctx, "g2", "Beta")
	s.AddGroupMember(ctx, "g1", "u1", "admin")
	s.AddGroupMember(ctx, "g2", "u1", "member")

	groups, err := s.ListAdminGroups(ctx, "u1")
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, "g1", groups[0].ID)
}

func TestDeleteGroup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateGroup(ctx, "g1", "Team")
	require.NoError(t, s.DeleteGroup(ctx, "g1"))

	groups, _ := s.ListGroups(ctx)
	require.Len(t, groups, 0)
}
```

- [ ] **Step 2: Run to confirm failures**

```bash
cd backend && go test ./internal/store/... -run "TestGroupMemberRoles|TestIsGroupAdmin|TestSetGroupMemberRole|TestListAdminGroups|TestDeleteGroup" -v
```

Expected: compile errors — methods undefined.

- [ ] **Step 3: Update `AddGroupMember` signature and add new methods to `group.go`**

Replace `AddGroupMember`:

```go
func (s *Store) AddGroupMember(ctx context.Context, groupID, userID, role string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO group_members (group_id, user_id, role) VALUES (?,?,?)
		 ON CONFLICT(group_id, user_id) DO NOTHING`,
		groupID, userID, role)
	return err
}
```

Add new methods after `GetUserGroupIDs`:

```go
func (s *Store) ListGroupMembers(ctx context.Context, groupID string) ([]*model.GroupMember, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT group_id, user_id, role FROM group_members WHERE group_id=? ORDER BY user_id`,
		groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.GroupMember
	for rows.Next() {
		var m model.GroupMember
		if err := rows.Scan(&m.GroupID, &m.UserID, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

func (s *Store) IsGroupAdmin(ctx context.Context, groupID, userID string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id=? AND user_id=? AND role='admin')`,
		groupID, userID,
	).Scan(&exists)
	return exists == 1, err
}

func (s *Store) SetGroupMemberRole(ctx context.Context, groupID, userID, role string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE group_members SET role=? WHERE group_id=? AND user_id=?`,
		role, groupID, userID)
	return err
}

func (s *Store) ListAdminGroups(ctx context.Context, userID string) ([]*model.Group, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.id, g.name, g.created_at
		FROM groups g
		JOIN group_members gm ON gm.group_id = g.id
		WHERE gm.user_id=? AND gm.role='admin'
		ORDER BY g.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Group
	for rows.Next() {
		var g model.Group
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}

func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM groups WHERE id=?`, id)
	return err
}
```

- [ ] **Step 4: Update existing `TestGroupMembers` to pass role to `AddGroupMember`**

In `group_test.go`, change:

```go
require.NoError(t, s.AddGroupMember(ctx, "g1", "u1"))
require.NoError(t, s.AddGroupMember(ctx, "g1", "u2"))
```

to:

```go
require.NoError(t, s.AddGroupMember(ctx, "g1", "u1", "member"))
require.NoError(t, s.AddGroupMember(ctx, "g1", "u2", "member"))
```

Also update `RemoveGroupMember` test line:
```go
require.NoError(t, s.RemoveGroupMember(ctx, "g1", "u1"))
```
(This stays the same — `RemoveGroupMember` signature is unchanged.)

- [ ] **Step 5: Fix `handleAdminAddGroupMember` in `admin.go` to pass role**

The production call site for `AddGroupMember` is in `admin.go`. Update the call to pass `"member"`:

```go
if err := deps.Store.AddGroupMember(r.Context(), groupID, body.UserID, "member"); err != nil {
```

- [ ] **Step 6: Run all store tests**

```bash
cd backend && go test ./internal/store/... -v
```

Expected: all tests pass.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/store/group.go backend/internal/store/group_test.go backend/internal/api/admin.go
git commit -m "feat: add group member roles and admin helpers to store"
```

---

### Task 4: JWT — add `IsUserAdmin` claim

**Files:**
- Modify: `backend/internal/auth/jwt.go`
- Modify: `backend/internal/auth/jwt_test.go`
- Modify: `backend/internal/api/auth.go`
- Modify: `backend/internal/api/me_test.go`

- [ ] **Step 1: Write failing test for `IsUserAdmin` in JWT**

Add to `backend/internal/auth/jwt_test.go`:

```go
func TestIssueAccessToken_withUserAdmin(t *testing.T) {
	key := testKey()
	token, err := auth.IssueAccessToken(key, "u1", "a@x.com", false, true, time.Hour)
	require.NoError(t, err)

	claims, err := auth.VerifyAccessToken(key, token)
	require.NoError(t, err)
	require.False(t, claims.IsAdmin)
	require.True(t, claims.IsUserAdmin)
}
```

- [ ] **Step 2: Run to confirm it fails**

```bash
cd backend && go test ./internal/auth/... -run TestIssueAccessToken_withUserAdmin -v
```

Expected: compile error — `IssueAccessToken` called with wrong arity, `IsUserAdmin` not on `AccessClaims`.

- [ ] **Step 3: Update `jwt.go`**

Replace the entire file content:

```go
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type AccessClaims struct {
	UserID      string
	Email       string
	IsAdmin     bool // is_instance_admin
	IsUserAdmin bool // is_admin user flag
}

type accessJWTClaims struct {
	jwt.RegisteredClaims
	Email       string `json:"email"`
	IsAdmin     bool   `json:"is_admin"`
	IsUserAdmin bool   `json:"is_user_admin"`
	Type        string `json:"type"`
}

type refreshJWTClaims struct {
	jwt.RegisteredClaims
	Type string `json:"type"`
}

func IssueAccessToken(key []byte, userID, email string, isAdmin, isUserAdmin bool, ttl time.Duration) (string, error) {
	claims := accessJWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
		},
		Email:       email,
		IsAdmin:     isAdmin,
		IsUserAdmin: isUserAdmin,
		Type:        "access",
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
}

func VerifyAccessToken(key []byte, tokenStr string) (*AccessClaims, error) {
	var claims accessJWTClaims
	_, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return key, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}
	if claims.Type != "access" {
		return nil, errors.New("not an access token")
	}
	return &AccessClaims{
		UserID:      claims.Subject,
		Email:       claims.Email,
		IsAdmin:     claims.IsAdmin,
		IsUserAdmin: claims.IsUserAdmin,
	}, nil
}

func IssueRefreshToken(key []byte, userID string, ttl time.Duration) (string, error) {
	claims := refreshJWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
		},
		Type: "refresh",
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
}

func VerifyRefreshToken(key []byte, tokenStr string) (string, error) {
	var claims refreshJWTClaims
	_, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return key, nil
	})
	if err != nil {
		return "", fmt.Errorf("parse refresh token: %w", err)
	}
	if claims.Type != "refresh" {
		return "", errors.New("not a refresh token")
	}
	return claims.Subject, nil
}
```

- [ ] **Step 4: Update existing `TestIssueAndVerifyAccessToken` in `jwt_test.go`**

Change the call in the existing test to pass the new parameter:

```go
func TestIssueAndVerifyAccessToken(t *testing.T) {
	key := testKey()
	token, err := auth.IssueAccessToken(key, "user-1", "alice@x.com", false, false, 24*time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := auth.VerifyAccessToken(key, token)
	require.NoError(t, err)
	require.Equal(t, "user-1", claims.UserID)
	require.Equal(t, "alice@x.com", claims.Email)
	require.False(t, claims.IsAdmin)
	require.False(t, claims.IsUserAdmin)
}
```

Also update `TestAccessToken_expired`:

```go
func TestAccessToken_expired(t *testing.T) {
	key := testKey()
	token, _ := auth.IssueAccessToken(key, "u1", "a@x.com", false, false, -1*time.Second)
	_, err := auth.VerifyAccessToken(key, token)
	require.Error(t, err)
}
```

- [ ] **Step 5: Update `issueTokenPair` in `api/auth.go`**

Change `issueTokenPair` signature and its two callers:

```go
func issueTokenPair(w http.ResponseWriter, deps *Deps, userID, email string, isAdmin, isUserAdmin bool) {
	access, err := auth.IssueAccessToken(deps.Config.SecretKey, userID, email, isAdmin, isUserAdmin, 24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "issue token failed")
		return
	}
	refresh, err := auth.IssueRefreshToken(deps.Config.SecretKey, userID, 30*24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "issue refresh failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"expires_in":    int(24 * time.Hour / time.Second),
	})
}
```

In `handleToken`, change the call:

```go
issueTokenPair(w, deps, user.ID, user.Email, user.IsInstanceAdmin, user.IsAdmin)
```

In `handleRefresh`, change the call:

```go
issueTokenPair(w, deps, user.ID, user.Email, user.IsInstanceAdmin, user.IsAdmin)
```

- [ ] **Step 6: Add `bearerHeaderUserAdmin` helper to `me_test.go` without changing `bearerHeader`**

Add after `bearerHeader`:

```go
func bearerHeaderUserAdmin(t *testing.T, deps *api.Deps, userID, email string) string {
	t.Helper()
	token, err := auth.IssueAccessToken(deps.Config.SecretKey, userID, email, false, true, time.Hour)
	require.NoError(t, err)
	return "Bearer " + token
}
```

Also update the existing `bearerHeader` body to use the new 6-arg signature:

```go
func bearerHeader(t *testing.T, deps *api.Deps, userID, email string, isAdmin bool) string {
	t.Helper()
	token, err := auth.IssueAccessToken(deps.Config.SecretKey, userID, email, isAdmin, false, time.Hour)
	require.NoError(t, err)
	return "Bearer " + token
}
```

- [ ] **Step 7: Run all tests**

```bash
cd backend && go test ./...
```

Expected: all packages pass.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/auth/jwt.go backend/internal/auth/jwt_test.go backend/internal/api/auth.go backend/internal/api/me_test.go
git commit -m "feat: add IsUserAdmin claim to JWT"
```

---

### Task 5: Permission helpers

**Files:**
- Modify: `backend/internal/api/rolecheck.go`

- [ ] **Step 1: Replace `rolecheck.go` with the three helpers**

```go
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/model"
)

func requireRepoRole(ctx context.Context, deps *Deps, claims *auth.AccessClaims, repoID, required string) error {
	if claims.IsAdmin {
		return nil
	}
	role, err := deps.Store.GetUserRole(ctx, claims.UserID, repoID)
	if err != nil {
		return errors.New("role check failed")
	}
	if !model.RoleAtLeast(role, required) {
		return errors.New(required + " role required")
	}
	return nil
}

func requireAnyAdmin(claims *auth.AccessClaims, w http.ResponseWriter) bool {
	if claims.IsAdmin || claims.IsUserAdmin {
		return true
	}
	writeError(w, http.StatusForbidden, "admin required")
	return false
}

func requireRepoManage(ctx context.Context, deps *Deps, claims *auth.AccessClaims, repoID string, w http.ResponseWriter) bool {
	if claims.IsAdmin {
		return true
	}
	if !claims.IsUserAdmin {
		writeError(w, http.StatusForbidden, "admin required")
		return false
	}
	role, err := deps.Store.GetUserRole(ctx, claims.UserID, repoID)
	if err != nil || role != "admin" {
		writeError(w, http.StatusForbidden, "admin repo role required")
		return false
	}
	return true
}

func requireGroupAdmin(ctx context.Context, deps *Deps, claims *auth.AccessClaims, groupID string, w http.ResponseWriter) bool {
	if claims.IsAdmin {
		return true
	}
	if !claims.IsUserAdmin {
		writeError(w, http.StatusForbidden, "admin required")
		return false
	}
	ok, err := deps.Store.IsGroupAdmin(ctx, groupID, claims.UserID)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "group admin required")
		return false
	}
	return true
}
```

- [ ] **Step 2: Verify compilation**

```bash
cd backend && go build ./...
```

Expected: builds without error.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/api/rolecheck.go
git commit -m "feat: add requireAnyAdmin, requireRepoManage, requireGroupAdmin helpers"
```

---

### Task 6: API — repo creation, user-admin endpoint, and `/api/me`

**Files:**
- Modify: `backend/internal/api/admin.go`
- Modify: `backend/internal/api/me.go`
- Modify: `backend/internal/api/admin_test.go`

- [ ] **Step 1: Write failing tests**

Add to `backend/internal/api/admin_test.go`:

```go
func TestAdminCreateRepo_userAdmin(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "ua1", "ua@x.com", "UA")
	deps.Store.SetUserAdmin(ctx, "ua1", true)

	body := `{"name":"UA Repo","remote_url":"https://github.com/org/repo.git","username":"x","password":"p","default_branch":"main"}`
	req := httptest.NewRequest("POST", "/api/admin/repos", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeaderUserAdmin(t, deps, "ua1", "ua@x.com"))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	repoID := resp["id"]
	require.NotEmpty(t, repoID)

	// creator auto-gets admin repo role
	role, err := deps.Store.GetUserRole(ctx, "ua1", repoID)
	require.NoError(t, err)
	require.Equal(t, "admin", role)
}

func TestAdminCreateRepo_regularUser_forbidden(t *testing.T) {
	deps := newTestDeps(t)
	deps.Store.UpsertUser(context.Background(), "u1", "u@x.com", "U")

	req := httptest.NewRequest("POST", "/api/admin/repos", strings.NewReader(`{"name":"R","remote_url":"https://x.com/r.git","default_branch":"main"}`))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "u@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

func TestAdminSetUserAdmin(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "ua1", "ua@x.com", "UA")
	deps.Store.SetUserAdmin(ctx, "ua1", true)
	deps.Store.UpsertUser(ctx, "target", "target@x.com", "Target")

	body := `{"admin":true}`
	req := httptest.NewRequest("POST", "/api/admin/users/target/user-admin", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeaderUserAdmin(t, deps, "ua1", "ua@x.com"))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)

	u, _ := deps.Store.GetUserByID(ctx, "target")
	require.True(t, u.IsAdmin)
}
```

- [ ] **Step 2: Run to confirm failures**

```bash
cd backend && go test ./internal/api/... -run "TestAdminCreateRepo_userAdmin|TestAdminCreateRepo_regularUser_forbidden|TestAdminSetUserAdmin" -v
```

Expected: FAIL — `SetUserAdmin` undefined on store (from admin_test) and route not registered.

- [ ] **Step 3: Update `handleAdminCreateRepo` in `admin.go`**

Replace the permission check and add auto-grant after repo creation:

```go
func handleAdminCreateRepo(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAnyAdmin(claims, w) {
			return
		}
		var body struct {
			Name          string `json:"name"`
			RemoteURL     string `json:"remote_url"`
			Username      string `json:"username"`
			Password      string `json:"password"`
			DefaultBranch string `json:"default_branch"`
		}
		if err := readJSON(r, &body); err != nil || body.Name == "" || body.RemoteURL == "" {
			writeError(w, http.StatusBadRequest, "name and remote_url are required")
			return
		}
		if body.DefaultBranch == "" {
			body.DefaultBranch = "main"
		}
		credJSON, _ := json.Marshal(map[string]string{"username": body.Username, "password": body.Password})
		encCreds, err := auth.EncryptCreds(deps.Config.SecretKey, string(credJSON))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encrypt creds failed")
			return
		}
		repo, err := deps.Store.CreateRepo(r.Context(), uuid.NewString(), body.Name, body.RemoteURL, encCreds, body.DefaultBranch)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "create repo failed")
			return
		}
		if !claims.IsAdmin {
			if err := deps.Store.GrantAccess(r.Context(), uuid.NewString(), repo.ID, "user", claims.UserID, "admin"); err != nil {
				log.Printf("[pubobs] auto-grant admin on repo %s for user %s failed: %v", repo.ID, claims.UserID, err)
			}
		}
		userID := claims.UserID
		go func() {
			n, err := importRepoFromGit(context.Background(), deps, repo.ID, userID)
			if err != nil {
				log.Printf("[pubobs] background import for %s failed: %v", repo.ID, err)
			} else if n > 0 {
				log.Printf("[pubobs] imported %d note(s) from existing repo %s", n, repo.ID)
			}
		}()
		writeJSON(w, http.StatusCreated, map[string]string{"id": repo.ID, "name": repo.Name})
	}
}
```

- [ ] **Step 4: Add `handleAdminSetUserAdmin` to `admin.go`**

```go
func handleAdminSetUserAdmin(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAnyAdmin(claims, w) {
			return
		}
		id := chi.URLParam(r, "id")
		var body struct {
			Admin bool `json:"admin"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := deps.Store.SetUserAdmin(r.Context(), id, body.Admin); err != nil {
			writeError(w, http.StatusInternalServerError, "update failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

- [ ] **Step 5: Update `handleMe` in `me.go` to include `is_admin`**

```go
func handleMe(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		user, err := deps.Store.GetUserByID(r.Context(), claims.UserID)
		if err != nil || user == nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":                user.ID,
			"email":             user.Email,
			"name":              user.Name,
			"is_instance_admin": user.IsInstanceAdmin,
			"is_admin":          user.IsAdmin,
		})
	}
}
```

- [ ] **Step 6: Register new route in `router.go`**

Add after the existing `POST /api/admin/users/{id}/ban` line:

```go
r.Post("/api/admin/users/{id}/user-admin", handleAdminSetUserAdmin(deps))
```

- [ ] **Step 7: Run tests**

```bash
cd backend && go test ./internal/api/... -v
```

Expected: all tests pass including the three new ones.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/api/admin.go backend/internal/api/me.go backend/internal/api/router.go backend/internal/api/admin_test.go
git commit -m "feat: allow user-admin to create repos with auto-grant; add user-admin endpoint"
```

---

### Task 7: API — repo management and user list permission updates

**Files:**
- Modify: `backend/internal/api/admin.go`
- Modify: `backend/internal/api/admin_test.go`

- [ ] **Step 1: Write failing tests**

Add to `backend/internal/api/admin_test.go`:

```go
func setupUserAdminWithRepo(t *testing.T, deps *api.Deps) (userID, repoID string) {
	t.Helper()
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "ua1", "ua@x.com", "UA")
	deps.Store.SetUserAdmin(ctx, "ua1", true)
	repo, _ := deps.Store.CreateRepo(ctx, "r1", "Repo", "https://x.com/r.git", "creds", "main")
	deps.Store.GrantAccess(ctx, "acc1", repo.ID, "user", "ua1", "admin")
	return "ua1", repo.ID
}

func TestAdminUpdateRepo_userAdmin(t *testing.T) {
	deps := newTestDeps(t)
	userID, repoID := setupUserAdminWithRepo(t, deps)

	body := `{"name":"Updated","remote_url":"https://x.com/r.git","default_branch":"main","username":"","password":""}`
	req := httptest.NewRequest("PUT", "/api/admin/repos/"+repoID, strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeaderUserAdmin(t, deps, userID, "ua@x.com"))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
}

func TestAdminUpdateRepo_userAdmin_noAccess_forbidden(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "ua1", "ua@x.com", "UA")
	deps.Store.SetUserAdmin(ctx, "ua1", true)
	deps.Store.CreateRepo(ctx, "r1", "Repo", "https://x.com/r.git", "c", "main")

	body := `{"name":"Hack","remote_url":"https://x.com/r.git","default_branch":"main","username":"","password":""}`
	req := httptest.NewRequest("PUT", "/api/admin/repos/r1", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeaderUserAdmin(t, deps, "ua1", "ua@x.com"))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

func TestAdminDeleteRepo_userAdmin(t *testing.T) {
	deps := newTestDeps(t)
	userID, repoID := setupUserAdminWithRepo(t, deps)

	req := httptest.NewRequest("DELETE", "/api/admin/repos/"+repoID, nil)
	req.Header.Set("Authorization", bearerHeaderUserAdmin(t, deps, userID, "ua@x.com"))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestAdminListUsers_userAdmin(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "ua1", "ua@x.com", "UA")
	deps.Store.SetUserAdmin(ctx, "ua1", true)

	req := httptest.NewRequest("GET", "/api/admin/users", nil)
	req.Header.Set("Authorization", bearerHeaderUserAdmin(t, deps, "ua1", "ua@x.com"))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}
```

- [ ] **Step 2: Run to confirm failures**

```bash
cd backend && go test ./internal/api/... -run "TestAdminUpdateRepo_userAdmin|TestAdminUpdateRepo_userAdmin_noAccess|TestAdminDeleteRepo_userAdmin|TestAdminListUsers_userAdmin" -v
```

Expected: FAIL with 403.

- [ ] **Step 3: Update permission checks in `admin.go`**

In `handleAdminUpdateRepo`, replace `if !requireAdmin(claims, w) { return }` with:

```go
if !requireRepoManage(r.Context(), deps, claims, id, w) {
    return
}
```

In `handleAdminDeleteRepo`, same replacement.

In `handleAdminSetRepoGuestAccess`, same replacement (uses `id := chi.URLParam(r, "id")`).

In `handleAdminImportRepo`, same replacement.

In `handleAdminListRepoAccess`, same replacement.

In `handleAdminGrantAccess`, same replacement (uses `repoID := chi.URLParam(r, "id")`).

In `handleAdminRevokeAccess`, replace with:

```go
repoID := chi.URLParam(r, "id")
if !requireRepoManage(r.Context(), deps, claims, repoID, w) {
    return
}
```

In `handleAdminListUsers`, replace `if !requireAdmin(claims, w) { return }` with:

```go
if !requireAnyAdmin(claims, w) {
    return
}
```

- [ ] **Step 4: Run all API tests**

```bash
cd backend && go test ./internal/api/... -v
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/admin.go backend/internal/api/admin_test.go
git commit -m "feat: extend repo management and user list to user-admin with role check"
```

---

### Task 8: API — group management handlers

**Files:**
- Modify: `backend/internal/api/admin.go`
- Modify: `backend/internal/api/router.go`
- Modify: `backend/internal/api/admin_test.go`

- [ ] **Step 1: Write failing tests**

Add to `backend/internal/api/admin_test.go`:

```go
func TestAdminCreateGroup_userAdmin_autoGrantsAdminRole(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "ua1", "ua@x.com", "UA")
	deps.Store.SetUserAdmin(ctx, "ua1", true)

	body := `{"name":"My Group"}`
	req := httptest.NewRequest("POST", "/api/admin/groups", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeaderUserAdmin(t, deps, "ua1", "ua@x.com"))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	var resp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	groupID := resp["id"]
	require.NotEmpty(t, groupID)

	ok, _ := deps.Store.IsGroupAdmin(ctx, groupID, "ua1")
	require.True(t, ok)
}

func TestAdminListGroups_userAdmin(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "ua1", "ua@x.com", "UA")
	deps.Store.SetUserAdmin(ctx, "ua1", true)
	g, _ := deps.Store.CreateGroup(ctx, "g1", "Team")
	deps.Store.AddGroupMember(ctx, g.ID, "ua1", "admin")
	deps.Store.CreateGroup(ctx, "g2", "Other") // not admin here

	req := httptest.NewRequest("GET", "/api/admin/groups", nil)
	req.Header.Set("Authorization", bearerHeaderUserAdmin(t, deps, "ua1", "ua@x.com"))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var groups []map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&groups))
	require.Len(t, groups, 1)
	require.Equal(t, "g1", groups[0]["id"])
}

func TestAdminListGroupMembers(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "ua1", "ua@x.com", "UA")
	deps.Store.UpsertUser(ctx, "u2", "u2@x.com", "U2")
	deps.Store.SetUserAdmin(ctx, "ua1", true)
	deps.Store.CreateGroup(ctx, "g1", "Team")
	deps.Store.AddGroupMember(ctx, "g1", "ua1", "admin")
	deps.Store.AddGroupMember(ctx, "g1", "u2", "member")

	req := httptest.NewRequest("GET", "/api/admin/groups/g1/members", nil)
	req.Header.Set("Authorization", bearerHeaderUserAdmin(t, deps, "ua1", "ua@x.com"))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var members []map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&members))
	require.Len(t, members, 2)
}

func TestAdminRemoveGroupMember(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "ua1", "ua@x.com", "UA")
	deps.Store.UpsertUser(ctx, "u2", "u2@x.com", "U2")
	deps.Store.SetUserAdmin(ctx, "ua1", true)
	deps.Store.CreateGroup(ctx, "g1", "Team")
	deps.Store.AddGroupMember(ctx, "g1", "ua1", "admin")
	deps.Store.AddGroupMember(ctx, "g1", "u2", "member")

	req := httptest.NewRequest("DELETE", "/api/admin/groups/g1/members/u2", nil)
	req.Header.Set("Authorization", bearerHeaderUserAdmin(t, deps, "ua1", "ua@x.com"))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestAdminSetGroupMemberRole(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "ua1", "ua@x.com", "UA")
	deps.Store.UpsertUser(ctx, "u2", "u2@x.com", "U2")
	deps.Store.SetUserAdmin(ctx, "ua1", true)
	deps.Store.CreateGroup(ctx, "g1", "Team")
	deps.Store.AddGroupMember(ctx, "g1", "ua1", "admin")
	deps.Store.AddGroupMember(ctx, "g1", "u2", "member")

	req := httptest.NewRequest("PUT", "/api/admin/groups/g1/members/u2/role",
		strings.NewReader(`{"role":"admin"}`))
	req.Header.Set("Authorization", bearerHeaderUserAdmin(t, deps, "ua1", "ua@x.com"))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)

	ok, _ := deps.Store.IsGroupAdmin(ctx, "g1", "u2")
	require.True(t, ok)
}

func TestAdminDeleteGroup(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "ua1", "ua@x.com", "UA")
	deps.Store.SetUserAdmin(ctx, "ua1", true)
	deps.Store.CreateGroup(ctx, "g1", "Team")
	deps.Store.AddGroupMember(ctx, "g1", "ua1", "admin")

	req := httptest.NewRequest("DELETE", "/api/admin/groups/g1", nil)
	req.Header.Set("Authorization", bearerHeaderUserAdmin(t, deps, "ua1", "ua@x.com"))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)
}
```

- [ ] **Step 2: Run to confirm failures**

```bash
cd backend && go test ./internal/api/... -run "TestAdminCreateGroup_userAdmin|TestAdminListGroups|TestAdminListGroupMembers|TestAdminRemoveGroupMember|TestAdminSetGroupMemberRole|TestAdminDeleteGroup" -v
```

Expected: FAIL — routes not registered, handlers not defined.

- [ ] **Step 3: Update `handleAdminCreateGroup` in `admin.go`**

Replace the handler:

```go
func handleAdminCreateGroup(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAnyAdmin(claims, w) {
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := readJSON(r, &body); err != nil || body.Name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		g, err := deps.Store.CreateGroup(r.Context(), uuid.NewString(), body.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "create group failed")
			return
		}
		if !claims.IsAdmin {
			if err := deps.Store.AddGroupMember(r.Context(), g.ID, claims.UserID, "admin"); err != nil {
				log.Printf("[pubobs] auto-grant group admin on %s for user %s failed: %v", g.ID, claims.UserID, err)
			}
		}
		writeJSON(w, http.StatusCreated, map[string]string{"id": g.ID, "name": g.Name})
	}
}
```

- [ ] **Step 4: Replace `handleAdminAddGroupMember` with full updated handler**

```go
func handleAdminAddGroupMember(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		groupID := chi.URLParam(r, "id")
		if !requireGroupAdmin(r.Context(), deps, claims, groupID, w) {
			return
		}
		var body struct {
			UserID string `json:"user_id"`
		}
		if err := readJSON(r, &body); err != nil || body.UserID == "" {
			writeError(w, http.StatusBadRequest, "user_id is required")
			return
		}
		if err := deps.Store.AddGroupMember(r.Context(), groupID, body.UserID, "member"); err != nil {
			writeError(w, http.StatusInternalServerError, "add member failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

- [ ] **Step 5: Add new group handlers to `admin.go`**

```go
func handleAdminListGroups(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAnyAdmin(claims, w) {
			return
		}
		var groups []*model.Group
		var err error
		if claims.IsAdmin {
			groups, err = deps.Store.ListGroups(r.Context())
		} else {
			groups, err = deps.Store.ListAdminGroups(r.Context(), claims.UserID)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list groups failed")
			return
		}
		if groups == nil {
			groups = []*model.Group{}
		}
		writeJSON(w, http.StatusOK, groups)
	}
}

func handleAdminDeleteGroup(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		id := chi.URLParam(r, "id")
		if !requireGroupAdmin(r.Context(), deps, claims, id, w) {
			return
		}
		if err := deps.Store.DeleteGroup(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "delete group failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleAdminListGroupMembers(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		id := chi.URLParam(r, "id")
		if !requireGroupAdmin(r.Context(), deps, claims, id, w) {
			return
		}
		members, err := deps.Store.ListGroupMembers(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list members failed")
			return
		}
		if members == nil {
			members = []*model.GroupMember{}
		}
		writeJSON(w, http.StatusOK, members)
	}
}

func handleAdminRemoveGroupMember(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		groupID := chi.URLParam(r, "id")
		if !requireGroupAdmin(r.Context(), deps, claims, groupID, w) {
			return
		}
		userID := chi.URLParam(r, "userID")
		if err := deps.Store.RemoveGroupMember(r.Context(), groupID, userID); err != nil {
			writeError(w, http.StatusInternalServerError, "remove member failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleAdminSetGroupMemberRole(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		groupID := chi.URLParam(r, "id")
		if !requireGroupAdmin(r.Context(), deps, claims, groupID, w) {
			return
		}
		userID := chi.URLParam(r, "userID")
		var body struct {
			Role string `json:"role"`
		}
		if err := readJSON(r, &body); err != nil || (body.Role != "member" && body.Role != "admin") {
			writeError(w, http.StatusBadRequest, "role must be 'member' or 'admin'")
			return
		}
		if err := deps.Store.SetGroupMemberRole(r.Context(), groupID, userID, body.Role); err != nil {
			writeError(w, http.StatusInternalServerError, "set role failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

- [ ] **Step 6: Register new routes in `router.go`**

After the existing `r.Post("/api/admin/groups/{id}/members", handleAdminAddGroupMember(deps))` line, add:

```go
r.Get("/api/admin/groups", handleAdminListGroups(deps))
r.Delete("/api/admin/groups/{id}", handleAdminDeleteGroup(deps))
r.Get("/api/admin/groups/{id}/members", handleAdminListGroupMembers(deps))
r.Delete("/api/admin/groups/{id}/members/{userID}", handleAdminRemoveGroupMember(deps))
r.Put("/api/admin/groups/{id}/members/{userID}/role", handleAdminSetGroupMemberRole(deps))
```

- [ ] **Step 7: Run all backend tests**

```bash
cd backend && go test ./...
```

Expected: all packages pass.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/api/admin.go backend/internal/api/router.go backend/internal/api/admin_test.go
git commit -m "feat: add group management endpoints for user-admins and group admins"
```

---

### Task 9: Frontend — `api.ts` types and functions

**Files:**
- Modify: `frontend/src/api.ts`

- [ ] **Step 1: Add `is_admin` to `User`/`Me` types**

In `api.ts`, update the `User` interface:

```typescript
export interface User {
  id: string;
  email: string;
  name: string;
  is_instance_admin: boolean;
  is_banned: boolean;
  is_admin: boolean;
}
```

- [ ] **Step 2: Add `Group` and `GroupMember` interfaces after `AllowlistEntry`**

```typescript
export interface Group {
  id: string;
  name: string;
}

export interface GroupMember {
  group_id: string;
  user_id: string;
  role: string; // "member" | "admin"
}
```

- [ ] **Step 3: Add group API functions before the `PubNote` interface**

```typescript
export async function listGroups(): Promise<Group[]> {
  return json<Group[]>(await authedFetch('/api/admin/groups'));
}

export async function createGroup(name: string): Promise<Group> {
  return json(await authedFetch('/api/admin/groups', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  }));
}

export async function deleteGroup(id: string): Promise<void> {
  const resp = await authedFetch(`/api/admin/groups/${id}`, { method: 'DELETE' });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export async function listGroupMembers(groupId: string): Promise<GroupMember[]> {
  return json<GroupMember[]>(await authedFetch(`/api/admin/groups/${groupId}/members`));
}

export async function addGroupMember(groupId: string, userId: string): Promise<void> {
  const resp = await authedFetch(`/api/admin/groups/${groupId}/members`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ user_id: userId }),
  });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export async function removeGroupMember(groupId: string, userId: string): Promise<void> {
  const resp = await authedFetch(`/api/admin/groups/${groupId}/members/${userId}`, { method: 'DELETE' });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export async function setGroupMemberRole(groupId: string, userId: string, role: string): Promise<void> {
  const resp = await authedFetch(`/api/admin/groups/${groupId}/members/${userId}/role`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ role }),
  });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}

export async function setUserIsAdmin(id: string, admin: boolean): Promise<void> {
  const resp = await authedFetch(`/api/admin/users/${id}/user-admin`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ admin }),
  });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}
```

- [ ] **Step 4: Verify TypeScript compiles**

```bash
cd frontend && npm run build 2>&1 | head -20
```

Expected: build succeeds with no type errors.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/api.ts
git commit -m "feat: add Group/GroupMember types and group management API functions"
```

---

### Task 10: Frontend — `main.ts` routing and nav

**Files:**
- Modify: `frontend/src/main.ts`

- [ ] **Step 1: Import `groupsView` and update routing**

Add import at the top:

```typescript
import { groupsView } from './views/groups';
```

Register the `/groups` route after the existing route registrations:

```typescript
register('/groups', () => groupsView(currentUser!));
```

- [ ] **Step 2: Update the root redirect to handle `is_admin` users**

Change the `'/'` route handler:

```typescript
register('/', () => {
  navigate(isAuthenticated()
    ? (currentUser?.is_instance_admin || currentUser?.is_admin ? '/repos' : '/dashboard')
    : '/login');
  return document.createElement('div');
});
```

- [ ] **Step 3: Update `renderNav` for `is_admin` users**

After the `if (me.is_instance_admin)` block, add an `else if` branch before the final `else`:

```typescript
} else if (me.is_admin) {
  nav.innerHTML = `
    ${logoHtml}
    <div style="width:1px;height:20px;background:#3d5470;margin:0 4px"></div>
    <a href="#/repos" style="${linkStyle}">Repos</a>
    <a href="#/groups" style="${linkStyle}">Groups</a>
    <a href="#/users" style="${linkStyle}">Users</a>
    <span style="flex:1"></span>
    <span style="color:#8094AF;font-size:0.8rem">${esc(me.email)}</span>
    <button id="signout-btn"
      style="background:none;border:none;color:#a8bbd0;cursor:pointer;font-size:0.875rem;padding:6px 10px;
             border-radius:4px">
      Sign out
    </button>
  `;
} else {
```

- [ ] **Step 4: Update the boot function to redirect `is_admin` users to `/repos`**

In `boot()`, change the post-login redirect:

```typescript
if (!location.hash || location.hash === '#' || location.hash === '#/') {
  navigate(currentUser.is_instance_admin || currentUser.is_admin ? '/repos' : '/dashboard');
}
```

- [ ] **Step 5: Pass `currentUser` to `reposView` call**

Change:

```typescript
register('/repos', () => reposView());
```

to:

```typescript
register('/repos', () => reposView(currentUser!));
```

- [ ] **Step 6: Build and verify**

```bash
cd frontend && npm run build 2>&1 | head -20
```

Expected: no TypeScript errors.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/main.ts
git commit -m "feat: update routing and nav for is_admin users"
```

---

### Task 11: Frontend — `repos.ts` conditional controls

**Files:**
- Modify: `frontend/src/views/repos.ts`

- [ ] **Step 1: Update `reposView` to accept `me` parameter**

Change signature:

```typescript
import { listRepos, createRepo, updateRepo, deleteRepo, importRepo, setRepoGuestAccess, type Repo, type Me } from '../api';

export async function reposView(me: Me): Promise<HTMLElement> {
```

- [ ] **Step 2: Pass `me` down to `renderTable`**

Change the call:

```typescript
renderTable(tableWrap, repos, me);
```

And update `renderTable` signature:

```typescript
function renderTable(container: HTMLElement, repos: Repo[], me: Me): void {
```

Update all recursive calls inside `renderTable`:

```typescript
renderTable(container, fresh, me);
```

- [ ] **Step 3: Gate management controls per repo**

Inside the `for (const repo of repos)` loop, add a flag after the row is created:

```typescript
const canManage = me.is_instance_admin || repo.role === 'admin';
```

Then wrap the actions cell conditionally. Replace the block that creates `editBtn`, `importBtn`, `delBtn` and appends them:

```typescript
const actionsCell = document.createElement('td');
actionsCell.style.whiteSpace = 'nowrap';

if (canManage) {
  const editBtn = mkBtn('Edit', 'link');
  editBtn.style.marginRight = '8px';
  const importBtn = mkBtn('Import', 'link');
  importBtn.style.marginRight = '8px';
  const delBtn = mkBtn('Delete', 'link-danger');
  actionsCell.appendChild(editBtn);
  actionsCell.appendChild(importBtn);
  actionsCell.appendChild(delBtn);

  editBtn.addEventListener('click', () => {
    const existing = tbody.querySelector('tr.inline-form');
    if (existing) existing.remove();
    if (row.nextSibling && (row.nextSibling as HTMLElement).classList?.contains('inline-form')) return;
    const formRow = document.createElement('tr');
    formRow.className = 'inline-form';
    const formCell = document.createElement('td');
    formCell.colSpan = 6;
    formCell.style.padding = '0';
    formCell.appendChild(
      repoForm(repo, async data => {
        await updateRepo(repo.id, data);
        const fresh = await listRepos();
        renderTable(container, fresh, me);
      }, () => { formRow.remove(); })
    );
    formRow.appendChild(formCell);
    row.after(formRow);
  });

  importBtn.addEventListener('click', async () => {
    importBtn.textContent = 'Importing…';
    importBtn.disabled = true;
    try {
      const { imported } = await importRepo(repo.id);
      alert(`Imported ${imported} note(s) from git.`);
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : String(e));
    } finally {
      importBtn.textContent = 'Import';
      importBtn.disabled = false;
    }
  });

  delBtn.addEventListener('click', async () => {
    if (!confirm(`Delete repo "${repo.name}"?\n\nThis removes the repo from PubObs (the remote git repo is not affected).`)) return;
    try {
      await deleteRepo(repo.id);
      const fresh = await listRepos();
      renderTable(container, fresh, me);
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : String(e));
    }
  });
}
```

- [ ] **Step 4: Gate guest toggle**

Wrap the `guestCell` content similarly — only show toggle if `canManage`:

```typescript
const guestCell = document.createElement('td');
if (canManage) {
  const guestBtn = mkBtn(repo.allow_guest ? 'On' : 'Off', repo.allow_guest ? 'toggle-on' : 'toggle-off');
  guestCell.appendChild(guestBtn);
  guestBtn.addEventListener('click', async () => {
    const next = !repo.allow_guest;
    guestBtn.disabled = true;
    try {
      await setRepoGuestAccess(repo.id, next);
      repo.allow_guest = next;
      guestBtn.textContent = next ? 'On' : 'Off';
      applyToggleStyle(guestBtn, next);
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : String(e));
    } finally {
      guestBtn.disabled = false;
    }
  });
} else {
  guestCell.textContent = repo.allow_guest ? 'On' : 'Off';
  guestCell.style.color = repo.allow_guest ? '#16a34a' : '#94a3b8';
}
```

- [ ] **Step 5: Gate the `+ New repo` button**

In `reposView`, only show the button if `me.is_instance_admin || me.is_admin`:

```typescript
if (me.is_instance_admin || me.is_admin) {
  const newBtn = mkBtn('+ New repo', 'primary');
  header.appendChild(newBtn);
  newBtn.addEventListener('click', () => {
    if (formWrap.firstChild) { formWrap.innerHTML = ''; return; }
    formWrap.appendChild(
      repoForm(null, async data => {
        await createRepo(data);
        repos = await listRepos();
        renderTable(tableWrap, repos, me);
        formWrap.innerHTML = '';
      }, () => { formWrap.innerHTML = ''; })
    );
  });
}
```

- [ ] **Step 6: Build**

```bash
cd frontend && npm run build 2>&1 | head -20
```

Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/views/repos.ts
git commit -m "feat: gate repo management controls by role in repos view"
```

---

### Task 12: Frontend — `users.ts` conditional controls

**Files:**
- Modify: `frontend/src/views/users.ts`

- [ ] **Step 1: Update imports and function signature**

```typescript
import { listUsers, setUserAdmin, setUserBanned, setUserIsAdmin, type User, type Me } from '../api';

export async function usersView(me: Me): Promise<HTMLElement> {
```

- [ ] **Step 2: Pass `me` to `renderTable`**

```typescript
renderTable(tableWrap, users, me, async () => {
  users = await listUsers();
  renderTable(tableWrap, users, me, async () => {});
});
```

Update `renderTable` signature:

```typescript
function renderTable(container: HTMLElement, users: User[], me: Me, refresh: () => Promise<void>): void {
```

- [ ] **Step 3: Update the actions cell to show different controls based on `me`**

Replace the `actionsCell` building block inside the user loop:

```typescript
const actionsCell = document.createElement('td');
actionsCell.style.cssText = 'white-space:nowrap;display:flex;gap:8px';

if (me.is_instance_admin) {
  const adminBtn = mkBtn(user.is_instance_admin ? 'Remove superadmin' : 'Make superadmin', 'link');
  adminBtn.addEventListener('click', async () => {
    adminBtn.disabled = true;
    try {
      await setUserAdmin(user.id, !user.is_instance_admin);
      await refresh();
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : String(e));
      adminBtn.disabled = false;
    }
  });
  actionsCell.appendChild(adminBtn);

  const banBtn = mkBtn(user.is_banned ? 'Unban' : 'Ban', user.is_banned ? 'link' : 'link-danger');
  banBtn.addEventListener('click', async () => {
    if (!user.is_banned && !confirm(`Ban ${user.email}? They will be locked out immediately.`)) return;
    banBtn.disabled = true;
    try {
      await setUserBanned(user.id, !user.is_banned);
      await refresh();
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : String(e));
      banBtn.disabled = false;
    }
  });
  actionsCell.appendChild(banBtn);
} else {
  // is_admin user: only toggle is_admin flag
  const userAdminBtn = mkBtn(user.is_admin ? 'Remove admin' : 'Make admin', 'link');
  userAdminBtn.addEventListener('click', async () => {
    userAdminBtn.disabled = true;
    try {
      await setUserIsAdmin(user.id, !user.is_admin);
      await refresh();
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : String(e));
      userAdminBtn.disabled = false;
    }
  });
  actionsCell.appendChild(userAdminBtn);
}
```

- [ ] **Step 4: Update `main.ts` to pass `currentUser` to `usersView`**

In `main.ts`, change:

```typescript
register('/users', () => usersView(currentUser!));
```

- [ ] **Step 5: Build**

```bash
cd frontend && npm run build 2>&1 | head -20
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/views/users.ts frontend/src/main.ts
git commit -m "feat: show role-appropriate controls in users view; pass currentUser to views"
```

---

### Task 13: Frontend — `groups.ts` new view

**Files:**
- Create: `frontend/src/views/groups.ts`

- [ ] **Step 1: Create `groups.ts`**

```typescript
import {
  listGroups, createGroup, deleteGroup,
  listGroupMembers, addGroupMember, removeGroupMember, setGroupMemberRole,
  type Group, type GroupMember, type Me,
} from '../api';

export async function groupsView(me: Me): Promise<HTMLElement> {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'max-width:900px;margin:0 auto;padding:32px 24px;font-family:system-ui,sans-serif';

  let groups: Group[];
  try {
    groups = await listGroups();
  } catch (e: unknown) {
    wrap.innerHTML = `<p style="color:#c00">${e instanceof Error ? e.message : String(e)}</p>`;
    return wrap;
  }

  const header = document.createElement('div');
  header.style.cssText = 'display:flex;align-items:center;margin-bottom:24px';
  const title = document.createElement('h2');
  title.style.cssText = 'margin:0;font-size:1.25rem;flex:1';
  title.textContent = 'Groups';
  header.appendChild(title);

  const newBtn = mkBtn('+ New group', 'primary');
  header.appendChild(newBtn);
  wrap.appendChild(header);

  const formWrap = document.createElement('div');
  wrap.appendChild(formWrap);

  newBtn.addEventListener('click', () => {
    if (formWrap.firstChild) { formWrap.innerHTML = ''; return; }
    formWrap.appendChild(groupForm(async name => {
      await createGroup(name);
      groups = await listGroups();
      renderGroups(listWrap, groups, me);
      formWrap.innerHTML = '';
    }, () => { formWrap.innerHTML = ''; }));
  });

  const listWrap = document.createElement('div');
  wrap.appendChild(listWrap);
  renderGroups(listWrap, groups, me);

  return wrap;
}

function renderGroups(container: HTMLElement, groups: Group[], me: Me): void {
  container.innerHTML = '';

  if (groups.length === 0) {
    const p = document.createElement('p');
    p.style.color = '#888';
    p.textContent = 'No groups yet.';
    container.appendChild(p);
    return;
  }

  for (const group of groups) {
    container.appendChild(groupCard(group, me, () => renderGroups(container, groups.filter(g => g.id !== group.id), me)));
  }
}

function groupCard(group: Group, me: Me, onDeleted: () => void): HTMLElement {
  const card = document.createElement('div');
  card.style.cssText = 'border:1px solid #e2e8f0;border-radius:8px;padding:16px;margin-bottom:16px';

  const cardHeader = document.createElement('div');
  cardHeader.style.cssText = 'display:flex;align-items:center;gap:12px;margin-bottom:12px';
  const nameEl = document.createElement('span');
  nameEl.style.cssText = 'font-weight:500;font-size:1rem;flex:1';
  nameEl.textContent = group.name;
  cardHeader.appendChild(nameEl);

  const delBtn = mkBtn('Delete', 'link-danger');
  delBtn.addEventListener('click', async () => {
    if (!confirm(`Delete group "${group.name}"?`)) return;
    try {
      await deleteGroup(group.id);
      onDeleted();
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : String(e));
    }
  });
  cardHeader.appendChild(delBtn);
  card.appendChild(cardHeader);

  const membersWrap = document.createElement('div');
  card.appendChild(membersWrap);

  loadMembers(membersWrap, group.id, me);

  return card;
}

async function loadMembers(container: HTMLElement, groupId: string, me: Me): Promise<void> {
  let members: GroupMember[];
  try {
    members = await listGroupMembers(groupId);
  } catch (e: unknown) {
    container.innerHTML = `<p style="color:#c00;font-size:0.8rem">${e instanceof Error ? e.message : String(e)}</p>`;
    return;
  }
  renderMembers(container, groupId, members, me);
}

function renderMembers(container: HTMLElement, groupId: string, members: GroupMember[], me: Me): void {
  container.innerHTML = '';

  if (members.length === 0) {
    const p = document.createElement('p');
    p.style.cssText = 'color:#94a3b8;font-size:0.85rem;margin:0 0 8px';
    p.textContent = 'No members.';
    container.appendChild(p);
  } else {
    const list = document.createElement('div');
    list.style.cssText = 'display:flex;flex-direction:column;gap:6px;margin-bottom:12px';

    for (const m of members) {
      const row = document.createElement('div');
      row.style.cssText = 'display:flex;align-items:center;gap:8px;font-size:0.85rem';

      const uid = document.createElement('span');
      uid.style.cssText = 'flex:1;color:#374151;font-family:monospace';
      uid.textContent = m.user_id;

      const roleToggle = mkBtn(m.role === 'admin' ? 'Admin' : 'Member', m.role === 'admin' ? 'toggle-on' : 'toggle-off');
      roleToggle.addEventListener('click', async () => {
        const next = m.role === 'admin' ? 'member' : 'admin';
        roleToggle.disabled = true;
        try {
          await setGroupMemberRole(groupId, m.user_id, next);
          m.role = next;
          roleToggle.textContent = next === 'admin' ? 'Admin' : 'Member';
          applyToggleStyle(roleToggle, next === 'admin');
        } catch (e: unknown) {
          alert(e instanceof Error ? e.message : String(e));
        } finally {
          roleToggle.disabled = false;
        }
      });

      const removeBtn = mkBtn('Remove', 'link-danger');
      removeBtn.addEventListener('click', async () => {
        removeBtn.disabled = true;
        try {
          await removeGroupMember(groupId, m.user_id);
          await loadMembers(container, groupId, me);
        } catch (e: unknown) {
          alert(e instanceof Error ? e.message : String(e));
          removeBtn.disabled = false;
        }
      });

      row.appendChild(uid);
      row.appendChild(roleToggle);
      row.appendChild(removeBtn);
      list.appendChild(row);
    }
    container.appendChild(list);
  }

  // Add member form
  const addRow = document.createElement('div');
  addRow.style.cssText = 'display:flex;gap:8px;align-items:center';
  const input = document.createElement('input');
  input.placeholder = 'User ID';
  input.style.cssText = 'padding:4px 8px;border:1px solid #cbd5e1;border-radius:4px;font-size:0.8rem;width:220px';
  const addBtn = mkBtn('Add', 'ghost');
  const errSpan = document.createElement('span');
  errSpan.style.cssText = 'color:#c00;font-size:0.75rem;display:none';
  addRow.appendChild(input);
  addRow.appendChild(addBtn);
  addRow.appendChild(errSpan);
  container.appendChild(addRow);

  addBtn.addEventListener('click', async () => {
    const uid = input.value.trim();
    if (!uid) return;
    errSpan.style.display = 'none';
    addBtn.disabled = true;
    try {
      await addGroupMember(groupId, uid);
      input.value = '';
      await loadMembers(container, groupId, me);
    } catch (e: unknown) {
      errSpan.textContent = e instanceof Error ? e.message : String(e);
      errSpan.style.display = 'inline';
      addBtn.disabled = false;
    }
  });
}

function groupForm(onSave: (name: string) => Promise<void>, onCancel: () => void): HTMLElement {
  const wrap = document.createElement('div');
  wrap.style.cssText = 'background:#f1f5f9;border:1px solid #e2e8f0;border-radius:8px;padding:20px;margin-bottom:16px';

  const label = document.createElement('label');
  label.style.cssText = 'font-family:system-ui,sans-serif;font-size:0.8rem;font-weight:500;color:#374151';
  label.textContent = 'Group name';
  const input = document.createElement('input');
  input.placeholder = 'e.g. Engineering';
  input.style.cssText = 'display:block;width:100%;margin-top:4px;padding:6px 10px;border:1px solid #cbd5e1;border-radius:4px;font-size:0.875rem';
  label.appendChild(input);
  wrap.appendChild(label);

  const footer = document.createElement('div');
  footer.style.cssText = 'margin-top:12px;display:flex;gap:8px;align-items:center';
  const saveBtn = mkBtn('Create', 'primary');
  const cancelBtn = mkBtn('Cancel', 'ghost');
  const errSpan = document.createElement('span');
  errSpan.style.cssText = 'color:#c00;font-size:0.8rem;display:none';
  footer.appendChild(saveBtn);
  footer.appendChild(cancelBtn);
  footer.appendChild(errSpan);
  wrap.appendChild(footer);

  cancelBtn.addEventListener('click', onCancel);
  saveBtn.addEventListener('click', async () => {
    const name = input.value.trim();
    if (!name) return;
    errSpan.style.display = 'none';
    saveBtn.disabled = true;
    saveBtn.textContent = 'Creating…';
    try {
      await onSave(name);
    } catch (e: unknown) {
      errSpan.textContent = e instanceof Error ? e.message : String(e);
      errSpan.style.display = 'inline';
      saveBtn.disabled = false;
      saveBtn.textContent = 'Create';
    }
  });

  return wrap;
}

function applyToggleStyle(b: HTMLButtonElement, on: boolean): void {
  b.style.cssText = on
    ? 'padding:2px 8px;background:#d1fae5;color:#065f46;border:1px solid #6ee7b7;border-radius:4px;cursor:pointer;font-size:0.75rem'
    : 'padding:2px 8px;background:#f1f5f9;color:#64748b;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer;font-size:0.75rem';
}

function mkBtn(text: string, variant: 'primary' | 'ghost' | 'link' | 'link-danger' | 'toggle-on' | 'toggle-off'): HTMLButtonElement {
  const b = document.createElement('button');
  b.textContent = text;
  if (variant === 'toggle-on' || variant === 'toggle-off') {
    applyToggleStyle(b, variant === 'toggle-on');
    return b;
  }
  const styles: Record<string, string> = {
    primary: 'padding:8px 16px;background:#5B6B8E;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:0.875rem',
    ghost: 'padding:8px 16px;background:none;border:1px solid #cbd5e1;border-radius:6px;cursor:pointer;font-size:0.875rem',
    link: 'background:none;border:none;cursor:pointer;color:#5B6B8E;text-decoration:underline;font-size:0.8rem;padding:0',
    'link-danger': 'background:none;border:none;cursor:pointer;color:#dc2626;text-decoration:underline;font-size:0.8rem;padding:0',
  };
  b.style.cssText = styles[variant];
  return b;
}
```

- [ ] **Step 2: Build**

```bash
cd frontend && npm run build 2>&1 | head -20
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/views/groups.ts
git commit -m "feat: add groups management view for admin users"
```

---

### Task 14: Final integration build

- [ ] **Step 1: Run full backend test suite**

```bash
cd backend && go test ./... -count=1
```

Expected: all packages pass with no cached results.

- [ ] **Step 2: Build frontend bundle**

```bash
cd frontend && npm run build
```

Expected: clean build, no errors.

- [ ] **Step 3: Rebuild server binaries**

```bash
cd backend && make build 2>/dev/null || go build -o bin/pubobs-$(go env GOOS)-$(go env GOARCH) ./cmd/server
```

Expected: binary produced without errors.

- [ ] **Step 4: Final commit**

```bash
git add backend/bin/ frontend/dist/ 2>/dev/null; git status
git commit -m "build: rebuild binaries and frontend bundle for admin role feature" --allow-empty
```
