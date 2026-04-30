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
