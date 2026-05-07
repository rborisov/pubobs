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
