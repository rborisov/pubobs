package api_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/model"
	"github.com/stretchr/testify/require"
)

func newBareRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bare := filepath.Join(dir, "remote.git")
	require.NoError(t, exec.Command("git", "init", "--bare", bare).Run())
	return bare
}

func seedBareRepo(t *testing.T, bareURL string) {
	t.Helper()
	seedBareRepoWithFiles(t, bareURL, map[string]string{"hello.md": "# Hello"})
}

func seedBareRepoWithFiles(t *testing.T, bareURL string, files map[string]string) {
	t.Helper()
	work := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@x.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@x.com",
		)
		require.NoError(t, cmd.Run())
	}
	run("clone", bareURL, ".")
	for path, content := range files {
		full := filepath.Join(work, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	run("add", ".")
	run("commit", "-m", "initial")
	run("push", "origin", "HEAD:main")
}

func newTestDepsWithCache(t *testing.T) *api.Deps {
	t.Helper()
	deps := newTestDeps(t)
	deps.Cache = gitcache.NewCache(t.TempDir())
	deps.Resolver = newTestResolver(t, deps)
	return deps
}

func TestHandleSync(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")

	payload := `{"files":[{"path":"notes/hello.md","md_content":"# Hello","encrypted_html":"dGVzdCBlbmNyeXB0ZWQgaHRtbA==","frontmatter":{"tags":["test"]}}]}`
	req := httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(payload))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.NotEmpty(t, resp["commit_sha"])
	noteKeys, _ := resp["note_keys"].(map[string]any)
	require.NotEmpty(t, noteKeys["notes/hello.md"])
}

func TestHandleSync_emptyRepo(t *testing.T) {
	bareURL := newBareRepo(t) // bare repo with NO initial commit

	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Empty Repo", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")

	payload := `{"files":[{"path":"notes/hello.md","md_content":"# Hello","encrypted_html":"dGVzdCBlbmNyeXB0ZWQgaHRtbA==","frontmatter":{}}]}`
	req := httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(payload))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.NotEmpty(t, resp["commit_sha"])
}

// TestHandleSync_staleCritCacheDoesNotBlockHealthySync is the core
// regression test for a real incident: the periodic eviction job's cached
// system_health row said "crit" from a stale prior computation (it only
// refreshes once per PUBOBS_CACHE_CHECK_INTERVAL, default 1h), but the disk
// is actually healthy right now (the test cache dir is a normal temp dir).
// handleSync must check disk space live rather than trust that stale cached
// row, so the sync must succeed here, not be rejected with 507.
func TestHandleSync_staleCritCacheDoesNotBlockHealthySync(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")

	// Simulate a stale "crit" reading left behind by a prior eviction cycle,
	// from back when disk really was low — nothing has refreshed it since.
	require.NoError(t, deps.Store.UpsertHealth(ctx, 1.0, 100, "crit", nil))
	h, err := deps.Store.GetHealth(ctx)
	require.NoError(t, err)
	require.Equal(t, "crit", h.DiskStatus, "sanity check: stale cache must actually say crit")

	payload := `{"files":[{"path":"notes/hello.md","md_content":"# Hello","encrypted_html":"dGVzdCBlbmNyeXB0ZWQgaHRtbA==","frontmatter":{}}]}`
	req := httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(payload))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "sync must not be rejected on stale cached crit status when live disk is healthy: %s", rr.Body.String())
}

func TestHandleSync_insufficientRole(t *testing.T) {
	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", "https://x.com/r.git", "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader")

	req := httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(`{"files":[]}`))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

// TestHandleSync_noteKeysIdempotent verifies handleSync returns a note_keys
// entry for each synced file, and that syncing the same file again returns
// the SAME key rather than rotating it — GetOrCreateNoteKey is idempotent
// per-note, and the sync response must reflect that so the plugin can safely
// cache and reuse the key across syncs.
func TestHandleSync_noteKeysIdempotent(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")

	payload := `{"files":[{"path":"notes/hello.md","md_content":"# Hello","encrypted_html":"dGVzdCBlbmNyeXB0ZWQgaHRtbA==","frontmatter":{}}]}`

	doSync := func() map[string]any {
		req := httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(payload))
		req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
		rr := httptest.NewRecorder()
		api.BuildRouter(deps).ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		var resp map[string]any
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
		return resp
	}

	resp1 := doSync()
	noteKeys1, _ := resp1["note_keys"].(map[string]any)
	key1, _ := noteKeys1["notes/hello.md"].(string)
	require.NotEmpty(t, key1)

	resp2 := doSync()
	noteKeys2, _ := resp2["note_keys"].(map[string]any)
	key2, _ := noteKeys2["notes/hello.md"].(string)
	require.Equal(t, key1, key2, "repeated syncs of the same note must return the same key")
}

// TestHandleSync_deletedPathsRemovedFromGitAndDB is the end-to-end
// regression test for a bug where a sync's deleted_paths only removed the
// note's DB row and render-store blob, never the underlying .md file in the
// repo's git storage — so a "deleted" note kept reappearing to every client
// that pulled, because it was never actually gone from the remote.
func TestHandleSync_deletedPathsRemovedFromGitAndDB(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")

	// First sync the note in so it has a DB row to delete.
	createPayload := `{"files":[{"path":"hello.md","md_content":"# Hello","encrypted_html":"dGVzdCBlbmNyeXB0ZWQgaHRtbA==","frontmatter":{}}]}`
	req := httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(createPayload))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	note, err := deps.Store.UpsertNote(ctx, "r1", "hello.md")
	require.NoError(t, err)
	require.NotNil(t, note)

	// Now sync again with hello.md listed as deleted, and no files.
	deletePayload := `{"files":[],"deleted_paths":["hello.md"]}`
	req = httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(deletePayload))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr = httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	entries, err := deps.Cache.ListFiles(ctx, mustGetRepo(t, deps, "r1"), "")
	require.NoError(t, err)
	var paths []string
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	require.NotContains(t, paths, "hello.md", "deleted_paths must be removed from the repo's git storage")

	gotNote, err := deps.Store.GetNote(ctx, "r1", "hello.md")
	require.NoError(t, err)
	require.Nil(t, gotNote, "deleted_paths must remove the note's DB row")
}

// TestHandleSync_strictRejectsWhenNoUserCredential verifies that when a repo
// has strict_credentials enabled, an editor who has not configured their own
// (user, repo) git credential is rejected with 403 rather than silently
// falling back to the owner's service credential.
func TestHandleSync_strictRejectsWhenNoUserCredential(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)
	deps.Store.UpsertUser(ctx, "u1", "u1@x.com", "User One")
	deps.Store.CreateRepo(ctx, "r1", "R", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")
	require.NoError(t, deps.Store.SetRepoStrictCredentials(ctx, "r1", true)) // strict, no user cred

	body := strings.NewReader(`{"files":[{"path":"n.md","md_content":"# hi"}],"assets":[],"deleted_paths":[]}`)
	req := httptest.NewRequest("POST", "/api/repos/r1/sync", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "u1@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "credential")
}

// TestHandleSync_usesStoredGitIdentityAsAuthor verifies handleSync prefers
// the syncing user's stored (repo, user) git credential identity
// (git_name/git_email) as the commit author over their plain account
// name/email, once one has been configured via PUT .../git-credential.
// Mirrors the Phase-1 attribution-test approach (TestCache_Sync_authoredByUser
// in gitcache/cache_test.go): read the pushed commit's %an/%ae, here off the
// bare remote handleSync actually pushed to.
func TestHandleSync_usesStoredGitIdentityAsAuthor(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")

	// Configure a git credential for u1 on r1 with a distinct git identity
	// (username/token are irrelevant here — the bare-repo remote has no auth
	// gate — only git_name/git_email matter for this test).
	credBody := strings.NewReader(`{"username":"u1","token":"tok","git_name":"Custom","git_email":"custom@x.com"}`)
	credReq := httptest.NewRequest("PUT", "/api/repos/r1/git-credential", credBody)
	credReq.Header.Set("Content-Type", "application/json")
	credReq.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	credRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(credRR, credReq)
	require.Equal(t, http.StatusOK, credRR.Code, credRR.Body.String())

	payload := `{"files":[{"path":"notes/hello.md","md_content":"# Hello","encrypted_html":"dGVzdCBlbmNyeXB0ZWQgaHRtbA==","frontmatter":{}}]}`
	req := httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(payload))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	an, err := exec.Command("git", "-C", bareURL, "log", "-1", "--format=%an").Output()
	require.NoError(t, err)
	ae, err := exec.Command("git", "-C", bareURL, "log", "-1", "--format=%ae").Output()
	require.NoError(t, err)
	require.Equal(t, "Custom", strings.TrimSpace(string(an)), "commit author must use the stored git credential's git_name, not the account name")
	require.Equal(t, "custom@x.com", strings.TrimSpace(string(ae)), "commit author email must use the stored git credential's git_email, not the account email")
}

func mustGetRepo(t *testing.T, deps *api.Deps, repoID string) *model.Repo {
	t.Helper()
	repo, err := deps.Store.GetRepo(context.Background(), repoID)
	require.NoError(t, err)
	require.NotNil(t, repo)
	return repo
}

func TestHandleSync_writesAssetsToAssetStore(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")

	payload := `{"files":[{"path":"notes/hello.md","md_content":"# Hello","encrypted_html":"dGVzdCBlbmNyeXB0ZWQgaHRtbA==","frontmatter":{}}],` +
		`"assets":[{"path":"img.png","content":"` + base64.StdEncoding.EncodeToString([]byte("pngbytes")) + `"}]}`
	req := httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(payload))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	astore, err := deps.Resolver.AssetStoreFor(ctx, "r1")
	require.NoError(t, err)
	data, err := astore.Read("r1", "img.png")
	require.NoError(t, err)
	require.Equal(t, []byte("pngbytes"), data)
}
