package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/stretchr/testify/require"
)

func TestHandleListFiles(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Repo", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader")

	req := httptest.NewRequest("GET", "/api/repos/r1/files", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var files []map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&files))
	require.NotEmpty(t, files)

	var paths []string
	for _, f := range files {
		paths = append(paths, f["path"])
	}
	require.Contains(t, paths, "hello.md")
}
