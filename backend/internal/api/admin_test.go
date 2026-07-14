package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/stretchr/testify/require"
)

func TestAdminCreateRepo(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.SetInstanceAdmin(ctx, "admin1", true)

	body := `{"name":"My Repo","remote_url":"https://github.com/org/repo.git","username":"x-access-token","password":"ghp_test","default_branch":"main"}`
	req := httptest.NewRequest("POST", "/api/admin/repos", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.NotEmpty(t, resp["id"])
}

func TestAdminHealth(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.SetInstanceAdmin(ctx, "admin1", true)

	req := httptest.NewRequest("GET", "/api/admin/health", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestAdminEndpoints_nonAdmin(t *testing.T) {
	deps := newTestDeps(t)
	deps.Store.UpsertUser(context.Background(), "u1", "user@x.com", "User")

	req := httptest.NewRequest("POST", "/api/admin/repos", strings.NewReader("{}"))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "user@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

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

func TestAdminSetRepoOwner(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.SetInstanceAdmin(ctx, "admin1", true)
	deps.Store.UpsertUser(ctx, "u2", "u2@x.com", "User Two")
	repo, _ := deps.Store.CreateRepo(ctx, "r1", "R", "https://x/r1.git", "", "main")
	require.NoError(t, deps.Store.GrantAccess(ctx, "ga1", repo.ID, "user", "u2", "admin"))

	body := strings.NewReader(`{"owner_user_id":"u2"}`)
	req := httptest.NewRequest("POST", "/api/admin/repos/"+repo.ID+"/owner", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	retrievedRepo, _ := deps.Store.GetRepo(ctx, repo.ID)
	require.NotNil(t, retrievedRepo.OwnerUserID)
	require.Equal(t, "u2", *retrievedRepo.OwnerUserID)
}

func TestAdminCreateRepo_setsOwnerToCreator(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.SetInstanceAdmin(ctx, "admin1", true)

	body := `{"name":"My Repo","remote_url":"https://github.com/org/repo.git","username":"x-access-token","password":"ghp_test","default_branch":"main"}`
	req := httptest.NewRequest("POST", "/api/admin/repos", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	repoID := resp["id"]
	require.NotEmpty(t, repoID)

	repo, _ := deps.Store.GetRepo(ctx, repoID)
	require.NotNil(t, repo.OwnerUserID)
	require.Equal(t, "admin1", *repo.OwnerUserID)
}

func TestAdminListRepoAccess_includesGitCredentialStatus(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()

	// Setup: admin user, repo, and two users
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.SetInstanceAdmin(ctx, "admin1", true)
	deps.Store.UpsertUser(ctx, "u1", "u1@x.com", "User One")
	deps.Store.UpsertUser(ctx, "u2", "u2@x.com", "User Two")

	repo, _ := deps.Store.CreateRepo(ctx, "r1", "Repo", "https://x.com/r1.git", "", "main")

	// Grant u1 editor access and u2 reader access
	deps.Store.GrantAccess(ctx, "acc1", repo.ID, "user", "u1", "editor")
	deps.Store.GrantAccess(ctx, "acc2", repo.ID, "user", "u2", "reader")

	// Upsert and verify a credential for u1
	deps.Store.UpsertUserCredential(ctx, repo.ID, "u1", "enc_creds", "User One", "u1@x.com")
	deps.Store.SetUserCredentialVerification(ctx, repo.ID, "u1", "verified", "")

	// Call the endpoint
	req := httptest.NewRequest("GET", "/api/admin/repos/"+repo.ID+"/access", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	// Parse response
	var entries []map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&entries))
	require.Len(t, entries, 2, "should have 2 access entries")

	// Find u1 and u2 entries
	var u1Entry, u2Entry map[string]interface{}
	for _, e := range entries {
		if e["principal_id"] == "u1" {
			u1Entry = e
		} else if e["principal_id"] == "u2" {
			u2Entry = e
		}
	}

	require.NotNil(t, u1Entry, "u1 entry not found")
	require.NotNil(t, u2Entry, "u2 entry not found")

	// Assert git_credential status
	require.Equal(t, "verified", u1Entry["git_credential"], "u1 should have verified credential")
	require.Equal(t, "none", u2Entry["git_credential"], "u2 should have no credential")
}

func TestAdminSetRepoStrictCredentials(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x/r1.git", "", "main")

	body := strings.NewReader(`{"strict":true}`)
	req := httptest.NewRequest("PUT", "/api/admin/repos/r1/strict-credentials", body)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	repo, _ := deps.Store.GetRepo(ctx, "r1")
	require.True(t, repo.StrictCredentials)
}

func TestListRepos_includesOwnerAndStrict(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x/r1.git", "", "main")
	deps.Store.SetRepoOwner(ctx, "r1", "admin1")
	deps.Store.SetRepoStrictCredentials(ctx, "r1", true)

	req := httptest.NewRequest("GET", "/api/repos", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), `"owner_user_id":"admin1"`)
	require.Contains(t, rr.Body.String(), `"strict_credentials":true`)
}

func TestAdminSetRepoOwner_rejectsNonAdminTarget(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.UpsertUser(ctx, "u2", "u2@x.com", "Plain") // not an admin
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x/r1.git", "", "main")

	req := httptest.NewRequest("POST", "/api/admin/repos/r1/owner", strings.NewReader(`{"owner_user_id":"u2"}`))
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "admin")
}
