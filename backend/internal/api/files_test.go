package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/auth"
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

// gitToken is shaped like a real forge token so a substring match on the
// response body is a meaningful assertion.
const gitToken = "ghp_AbCdEf0123456789SecretTokenValue"

// newURLEchoingGitServer stands in for a git host (or a proxy in front of one)
// whose error body quotes back the URL it was asked for. git relays a failing
// request's response body verbatim as "remote: …" lines on stderr, and
// GitRunner.runCtx folds that stderr into the returned error — so this is the
// route by which the credentialed remote URL (see credentialedURL) ends up
// inside err.Error() and, unless redacted, inside the handler's 502 body.
//
// Note the deliberate choice not to rely on git's own "fatal: unable to access
// '<url>'" line: current git anonymizes the userinfo there itself, so a test
// resting on that alone would pass with or without the handler's Redact call.
// Text that came from the *remote* is never anonymized by git.
func newURLEchoingGitServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "error: refused upstream request for http://svc:%s@%s/org/notes.git\n", gitToken, r.Host)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// credLeakDeps registers a repo pointing at that server, with a stored git
// credential, and grants u1 only the READER role — the whole point being that a
// reader is deliberately less privileged than the owner who supplied the
// credential, and must never be handed it back in an error message.
func credLeakDeps(t *testing.T) *api.Deps {
	t.Helper()
	srv := newURLEchoingGitServer(t)
	deps := newTestDepsWithCache(t)
	ctx := context.Background()
	encCreds, err := auth.EncryptCreds(deps.Config.SecretKey,
		`{"username":"svc","password":"`+gitToken+`"}`)
	require.NoError(t, err)
	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	_, err = deps.Store.CreateRepo(ctx, "r1", "Repo", srv.URL+"/org/notes.git", encCreds, "main")
	require.NoError(t, err)
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader")
	return deps
}

func TestHandleListFiles_gitFailureDoesNotLeakCredential(t *testing.T) {
	deps := credLeakDeps(t)

	req := httptest.NewRequest("GET", "/api/repos/r1/files", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadGateway, rr.Code, rr.Body.String())
	body := rr.Body.String()
	require.Contains(t, body, "list files failed", "the failure is still reported")
	require.Contains(t, body, "refused upstream request",
		"precondition: the remote's own text really does reach the response")
	require.NotContains(t, body, gitToken, "the owner's git token must never reach a reader")
	require.NotContains(t, body, "svc:")
}

func TestHandleListDataFiles_gitFailureDoesNotLeakCredential(t *testing.T) {
	deps := credLeakDeps(t)

	rr := dataFilesReq(t, deps, "?ext=csv")

	require.Equal(t, http.StatusBadGateway, rr.Code, rr.Body.String())
	body := rr.Body.String()
	require.Contains(t, body, "list data files failed", "the failure is still reported")
	require.Contains(t, body, "refused upstream request",
		"precondition: the remote's own text really does reach the response")
	require.NotContains(t, body, gitToken, "the owner's git token must never reach a reader")
	require.NotContains(t, body, "svc:")
}
