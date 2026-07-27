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

func dataFilesReq(t *testing.T, deps *api.Deps, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/repos/r1/data-files"+query, nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	return rr
}

func TestHandleListDataFiles(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepoWithFiles(t, bareURL, map[string]string{
		"hello.md":  "# Hello",
		"table.csv": "a,b\n1,2\n",
		"skip.txt":  "no",
	})
	deps := newTestDepsWithCache(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "R", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader")

	rr := dataFilesReq(t, deps, "?ext=csv,json")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp struct {
		Files []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			SHA     string `json:"sha"`
			Size    int64  `json:"size"`
		} `json:"files"`
		Skipped []struct {
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"skipped"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Len(t, resp.Files, 1)
	require.Equal(t, "table.csv", resp.Files[0].Path)
	require.Equal(t, "a,b\n1,2\n", resp.Files[0].Content)
	require.NotEmpty(t, resp.Files[0].SHA)
	require.NotNil(t, resp.Skipped, "skipped must serialize as [], never null")
}

func TestHandleListDataFiles_rejectsBadExt(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)
	deps := newTestDepsWithCache(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "R", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader")

	for _, q := range []string{"", "?ext=", "?ext=md", "?ext=../etc", "?ext=csv,*", "?ext=CSV!"} {
		rr := dataFilesReq(t, deps, q)
		require.Equal(t, http.StatusBadRequest, rr.Code, "query %q must be rejected: %s", q, rr.Body.String())
	}
}

func TestHandleListDataFiles_requiresReaderRole(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)
	deps := newTestDepsWithCache(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "R", bareURL, "", "main")
	// no GrantAccess — u1 has no role on r1

	rr := dataFilesReq(t, deps, "?ext=csv")
	require.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())
}
