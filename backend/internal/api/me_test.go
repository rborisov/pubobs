package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/auth"
	"github.com/stretchr/testify/require"
)

func bearerHeader(t *testing.T, deps *api.Deps, userID, email string, isAdmin bool) string {
	t.Helper()
	token, err := auth.IssueAccessToken(deps.Config.SecretKey, userID, email, isAdmin, time.Hour)
	require.NoError(t, err)
	return "Bearer " + token
}

func TestHandleMe(t *testing.T) {
	deps := newTestDeps(t)
	deps.Store.UpsertUser(context.Background(), "u1", "alice@x.com", "Alice")

	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "alice@x.com", resp["email"])
}

func TestHandleListRepos_empty(t *testing.T) {
	deps := newTestDeps(t)
	deps.Store.UpsertUser(context.Background(), "u1", "alice@x.com", "Alice")

	req := httptest.NewRequest("GET", "/api/repos", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var repos []any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&repos))
	require.Len(t, repos, 0)
}

func TestHandleUpsertFolderMapping(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "c", "main")

	body := `{"vault_folder":"MyNotes","repo_subfolder":"docs"}`
	req := httptest.NewRequest("PUT", "/api/me/folder-mappings/r1", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)
}
