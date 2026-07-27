package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/stretchr/testify/require"
)

// rejectPushes installs a pre-receive hook on a bare repo that refuses every
// push with the given message on stderr, so a test can reproduce a
// server-side rejection (the shape a git host uses for a credential without
// write access, a protected branch, or a policy hook) without needing a real
// remote or real credentials.
func rejectPushes(t *testing.T, bareURL, remoteMessage string) {
	t.Helper()
	hook := filepath.Join(bareURL, "hooks", "pre-receive")
	require.NoError(t, os.MkdirAll(filepath.Dir(hook), 0755))
	script := "#!/bin/sh\necho \"" + remoteMessage + "\" >&2\nexit 1\n"
	require.NoError(t, os.WriteFile(hook, []byte(script), 0755))
}

func syncOnce(t *testing.T, deps *api.Deps) *httptest.ResponseRecorder {
	t.Helper()
	payload := `{"files":[{"path":"notes/hello.md","md_content":"# Hello"}]}`
	req := httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(payload))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	return rr
}

func newSyncableRepo(t *testing.T, bareURL string) *api.Deps {
	t.Helper()
	deps := newTestDepsWithCache(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")
	return deps
}

// A push the remote refuses on permission grounds must be reported as exactly
// that. It used to come back as `409 push rejected: pull first, then sync`,
// which is wrong twice over: nothing needs pulling (Sync hard-resets to the
// remote tip before every commit), and no amount of syncing fixes a
// credential that may not write to the repo. This is the regression test for
// the "new repo can't sync — pull first, then sync" report.
func TestHandleSync_permissionRejectionIsNotReportedAsAConflict(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)
	rejectPushes(t, bareURL, "Gitea: User permission denied for writing.")

	rr := syncOnce(t, newSyncableRepo(t, bareURL))
	body := rr.Body.String()

	require.Equal(t, http.StatusForbidden, rr.Code, body)
	require.NotContains(t, body, "pull first")
	require.Contains(t, body, "write access")
	require.Contains(t, body, "User permission denied for writing.",
		"the remote's own explanation is the only actionable part — it must reach the client")
}

// The genuine conflict still reports as a retryable conflict, so narrowing the
// classification didn't simply reclassify everything as a permission problem.
func TestHandleSync_nonFastForwardStillReportsAsConflict(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)
	rejectPushes(t, bareURL, "error: denying non-fast-forward refs/heads/main")

	rr := syncOnce(t, newSyncableRepo(t, bareURL))
	body := rr.Body.String()

	require.Equal(t, http.StatusConflict, rr.Code, body)
	require.Contains(t, body, "sync again")
}

// Whatever the failure, the remote URL git echoes back carries the injected
// credentials — none of it may reach the client.
func TestHandleSync_errorNeverLeaksCredentials(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)
	rejectPushes(t, bareURL, "denied for https://bob:s3cr3t-token@git.example.com/u/r.git")

	rr := syncOnce(t, newSyncableRepo(t, bareURL))
	require.NotContains(t, rr.Body.String(), "s3cr3t-token")
}

// A brand-new, still-empty remote takes a different push path: the clone
// finds no HEAD, so gitcache pushes an initial commit (InitializeIfEmpty)
// before the sync's own push. A refusal there must classify the same way —
// this is the exact shape of "new repo can't sync".
func TestHandleSync_emptyRepoInitialPushRejection(t *testing.T) {
	bareURL := newBareRepo(t) // no commits at all
	rejectPushes(t, bareURL, "remote: Write access to repository not granted.")

	rr := syncOnce(t, newSyncableRepo(t, bareURL))
	body := rr.Body.String()

	require.Equal(t, http.StatusForbidden, rr.Code, body)
	require.NotContains(t, body, "pull first")
	require.Contains(t, body, "write access")
}
