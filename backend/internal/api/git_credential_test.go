package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/stretchr/testify/require"
)

func TestSetAndDeleteGitCredential(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "u1@x.com", "User One")
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x/r1.git", "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")

	// set
	body := strings.NewReader(`{"username":"u1","token":"tok","git_name":"One","git_email":"one@x.com"}`)
	req := httptest.NewRequest("PUT", "/api/repos/r1/git-credential", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "u1@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	enc, ok, _ := deps.Store.GetUserCredentialSecret(ctx, "r1", "u1")
	require.True(t, ok)
	require.NotEmpty(t, enc)
	require.NotContains(t, enc, "tok") // stored encrypted, not plaintext
	name, email, ok, _ := deps.Store.GetUserCredentialGitIdentity(ctx, "r1", "u1")
	require.True(t, ok)
	require.Equal(t, "One", name)
	require.Equal(t, "one@x.com", email)

	// a reader (no editor role) is forbidden
	deps.Store.UpsertUser(ctx, "u2", "u2@x.com", "Two")
	deps.Store.GrantAccess(ctx, "a2", "r1", "user", "u2", "reader")
	req2 := httptest.NewRequest("PUT", "/api/repos/r1/git-credential", strings.NewReader(`{"username":"x","token":"y"}`))
	req2.Header.Set("Authorization", bearerHeader(t, deps, "u2", "u2@x.com", false))
	rr2 := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr2, req2)
	require.Equal(t, http.StatusForbidden, rr2.Code)

	// delete
	del := httptest.NewRequest("DELETE", "/api/repos/r1/git-credential", nil)
	del.Header.Set("Authorization", bearerHeader(t, deps, "u1", "u1@x.com", false))
	dr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(dr, del)
	require.Equal(t, http.StatusOK, dr.Code)
	_, ok, _ = deps.Store.GetUserCredentialSecret(ctx, "r1", "u1")
	require.False(t, ok)
}
