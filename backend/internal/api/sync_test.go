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
	os.WriteFile(filepath.Join(work, "hello.md"), []byte("# Hello"), 0644)
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
