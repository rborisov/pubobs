package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

// TestVerifyGitCredential exercises handleVerifyGitCredential against a real
// gitcache.Cache. newAuthRequiredGitServer (gitcache_test) isn't importable
// here, so instead of an HTTP auth-gated remote this uses two local repos:
// r1's remote is a real no-auth bare repo (git ls-remote succeeds regardless
// of the credential, so it exercises the "verified" path), and r2's remote
// is a syntactically valid but nonexistent local path (git ls-remote fails
// fast, with no network round trip, so it exercises "auth_failed" without a
// slow/flaky timeout).
func TestVerifyGitCredential(t *testing.T) {
	deps, _ := newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "u1@x.com", "One")

	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)
	deps.Store.CreateRepo(ctx, "r1", "R1", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")

	badURL := filepath.Join(t.TempDir(), "does-not-exist.git")
	deps.Store.CreateRepo(ctx, "r2", "R2", badURL, "", "main")
	deps.Store.GrantAccess(ctx, "a2", "r2", "user", "u1", "editor")

	setCred := func(repoID string) {
		body := strings.NewReader(`{"username":"u1","token":"tok"}`)
		req := httptest.NewRequest("PUT", "/api/repos/"+repoID+"/git-credential", body)
		req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "u1@x.com", false))
		rr := httptest.NewRecorder()
		api.BuildRouter(deps).ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	}
	verifyAs := func(repoID, userID, email string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/repos/"+repoID+"/git-credential/verify", nil)
		req.Header.Set("Authorization", bearerHeader(t, deps, userID, email, false))
		rr := httptest.NewRecorder()
		api.BuildRouter(deps).ServeHTTP(rr, req)
		return rr
	}
	verify := func(repoID string) *httptest.ResponseRecorder {
		return verifyAs(repoID, "u1", "u1@x.com")
	}

	// no credential stored yet -> 404
	rrNoCred := verify("r1")
	require.Equal(t, http.StatusNotFound, rrNoCred.Code)

	setCred("r1")
	rr := verify("r1")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "verified")
	list, _ := deps.Store.ListUserCredentials(ctx, "r1")
	require.Equal(t, "verified", list[0].VerifyStatus)

	setCred("r2")
	rr2 := verify("r2")
	require.Equal(t, http.StatusOK, rr2.Code, rr2.Body.String())
	require.Contains(t, rr2.Body.String(), "auth_failed")
	list2, _ := deps.Store.ListUserCredentials(ctx, "r2")
	require.Equal(t, "auth_failed", list2[0].VerifyStatus)
	require.NotContains(t, list2[0].VerifyError, "tok") // never echo the raw credential/error

	// a reader (no editor role) is forbidden
	deps.Store.UpsertUser(ctx, "u2", "u2@x.com", "Two")
	deps.Store.GrantAccess(ctx, "a3", "r1", "user", "u2", "reader")
	rr3 := verifyAs("r1", "u2", "u2@x.com")
	require.Equal(t, http.StatusForbidden, rr3.Code)
}
