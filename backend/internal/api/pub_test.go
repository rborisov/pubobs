package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/storageresolver"
	"github.com/stretchr/testify/require"
)

// newTestResolver builds a StorageResolver backed by throwaway local dirs.
// Repos default to local storage (StorageDestinationID nil), so
// RenderStoreFor/AssetStoreFor resolve to these local stores.
func newTestResolver(t *testing.T, deps *api.Deps) *storageresolver.Resolver {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	r, err := storageresolver.New(deps.Store, t.TempDir(), t.TempDir(), key)
	require.NoError(t, err)
	return r
}

func newTestDepsForPub(t *testing.T) (deps *api.Deps, cacheDir string) {
	t.Helper()
	deps = newTestDeps(t)
	cacheDir = t.TempDir()
	deps.Cache = gitcache.NewCache(cacheDir)
	deps.Resolver = newTestResolver(t, deps)
	return deps, cacheDir
}

func TestHandlePubGetAsset_servesFromAssetStoreWhenPresent(t *testing.T) {
	deps, _ := newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", true))

	astore, err := deps.Resolver.AssetStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, astore.Write("r1", "img.png", []byte("from-asset-store")))

	req := httptest.NewRequest("GET", "/pub/r1/assets/img.png", nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Equal(t, []byte("from-asset-store"), rr.Body.Bytes())
}

// setupPubAccessTest seeds repo r1 (allow_guest=allowGuest) with reader
// access for user u1, and note docs/intro.md whose shared_publicly is set
// to shared with a fixed test key. A render blob is written for the note so
// "shown" cases can assert a real 200 with real bytes, not just a
// content-less 404-vs-not-404 distinction.
func setupPubAccessTest(t *testing.T, allowGuest, shared bool) (deps *api.Deps, key string) {
	t.Helper()
	deps, _ = newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "reader@x.com", "Reader")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", allowGuest))
	require.NoError(t, deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader"))

	note, err := deps.Store.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "<h1>Hi</h1>", "{}", "u1", "sha1"))

	key = "test-note-key-0123456789AB"
	require.NoError(t, deps.Store.SetNoteShared(ctx, note.ID, shared, key))

	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", "docs/intro.md", []byte("encrypted-blob-bytes")))

	return deps, key
}

func getPubNote(deps *api.Deps, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md"+query, nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	return rr
}

func getPubRender(deps *api.Deps, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/pub/r1/render/docs/intro.md"+query, nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	return rr
}

func TestPubAccess_guestOpen_noKey_shown(t *testing.T) {
	deps, _ := setupPubAccessTest(t, true, false)
	require.Equal(t, http.StatusOK, getPubNote(deps, "").Code)
	require.Equal(t, http.StatusOK, getPubRender(deps, "").Code)
}

func TestPubAccess_guestOpen_someKeyPresent_shownKeyIgnored(t *testing.T) {
	deps, _ := setupPubAccessTest(t, true, false)
	require.Equal(t, http.StatusOK, getPubNote(deps, "?key=totally-wrong-key").Code)
	require.Equal(t, http.StatusOK, getPubRender(deps, "?key=totally-wrong-key").Code)
}

func TestPubAccess_guestClosedNotShared_noKey_hidden(t *testing.T) {
	deps, _ := setupPubAccessTest(t, false, false)
	require.Equal(t, http.StatusNotFound, getPubNote(deps, "").Code)
	require.Equal(t, http.StatusNotFound, getPubRender(deps, "").Code)
}

func TestPubAccess_guestClosedNotShared_correctKey_stillHidden(t *testing.T) {
	deps, key := setupPubAccessTest(t, false, false)
	rr := getPubNote(deps, "?key="+key)
	require.Equal(t, http.StatusNotFound, rr.Code, "key alone must never be sufficient when the note isn't shared")
	rr2 := getPubRender(deps, "?key="+key)
	require.Equal(t, http.StatusNotFound, rr2.Code, "key alone must never be sufficient when the note isn't shared")
}

func TestPubAccess_guestClosedShared_noKey_hidden(t *testing.T) {
	deps, _ := setupPubAccessTest(t, false, true)
	require.Equal(t, http.StatusNotFound, getPubNote(deps, "").Code)
	require.Equal(t, http.StatusNotFound, getPubRender(deps, "").Code)
}

func TestPubAccess_guestClosedShared_correctKey_shown(t *testing.T) {
	deps, key := setupPubAccessTest(t, false, true)
	rr := getPubNote(deps, "?key="+key)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	rr2 := getPubRender(deps, "?key="+key)
	require.Equal(t, http.StatusOK, rr2.Code, rr2.Body.String())
	require.Equal(t, "encrypted-blob-bytes", rr2.Body.String())
}

func TestPubAccess_guestClosedShared_wrongKey_hidden(t *testing.T) {
	deps, _ := setupPubAccessTest(t, false, true)
	require.Equal(t, http.StatusNotFound, getPubNote(deps, "?key=totally-wrong-key").Code)
	require.Equal(t, http.StatusForbidden, getPubRender(deps, "?key=totally-wrong-key").Code)
}

func TestPubAccess_guestClosed_validBearerToken_shownRegardlessOfSharedState(t *testing.T) {
	for _, shared := range []bool{false, true} {
		deps, _ := setupPubAccessTest(t, false, shared)

		req := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md", nil)
		req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "reader@x.com", false))
		rr := httptest.NewRecorder()
		api.BuildRouter(deps).ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

		req2 := httptest.NewRequest("GET", "/pub/r1/render/docs/intro.md", nil)
		req2.Header.Set("Authorization", bearerHeader(t, deps, "u1", "reader@x.com", false))
		rr2 := httptest.NewRecorder()
		api.BuildRouter(deps).ServeHTTP(rr2, req2)
		require.Equal(t, http.StatusOK, rr2.Code, rr2.Body.String())
	}
}

// TestPubListNotes_rejectsShareKeyOnlyRequest verifies the explicit
// invariant in handlePubListNotes: a note-scoped share key must never grant
// access to the repo-level list endpoint, even when it's a valid key for
// one of the repo's notes.
func TestPubListNotes_rejectsShareKeyOnlyRequest(t *testing.T) {
	deps, key := setupPubAccessTest(t, false, true)
	req := httptest.NewRequest("GET", "/pub/r1?key="+key, nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code, rr.Body.String())
}

// TestPubGetNote_authenticatedSeesRealRole verifies an authenticated repo
// member sees their real role in the response, and that a note which isn't
// shared is never misreported as shared_publicly.
func TestPubGetNote_authenticatedSeesRealRole(t *testing.T) {
	deps, _ := setupPubAccessTest(t, true, false)
	req := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "reader@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "reader", resp["role"])
	require.Equal(t, false, resp["shared_publicly"])
}

// TestPubGetNote_shareKeyOnlyVisitorSeesGuestRole verifies a visitor who
// only supplied a valid note-level share key (no bearer token, no real repo
// role) sees an empty/guest role marker rather than any real role string —
// they must never be shown editor-only sharing controls.
func TestPubGetNote_shareKeyOnlyVisitorSeesGuestRole(t *testing.T) {
	deps, key := setupPubAccessTest(t, false, true)
	rr := getPubNote(deps, "?key="+key)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "", resp["role"])
	require.Equal(t, true, resp["shared_publicly"])
}

// TestPubListNotes_reportsRoleAndPerNoteSharedState verifies
// handlePubListNotes surfaces the caller's real role and each note's
// shared_publicly state without misreporting an unshared note as shared.
func TestPubListNotes_reportsRoleAndPerNoteSharedState(t *testing.T) {
	deps, _ := setupPubAccessTest(t, true, true)
	req := httptest.NewRequest("GET", "/pub/r1", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "reader@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp struct {
		Role  string `json:"role"`
		Notes []struct {
			Path           string `json:"path"`
			SharedPublicly bool   `json:"shared_publicly"`
		} `json:"notes"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "reader", resp.Role)
	require.Len(t, resp.Notes, 1)
	require.Equal(t, "docs/intro.md", resp.Notes[0].Path)
	require.True(t, resp.Notes[0].SharedPublicly)
}

// TestPubListNotes_anonymousGuestSeesEmptyRole verifies an anonymous guest
// on a guest-open repo (no bearer token at all) sees the empty/guest role
// marker, not a real role string.
func TestPubListNotes_anonymousGuestSeesEmptyRole(t *testing.T) {
	deps, _ := setupPubAccessTest(t, true, false)
	req := httptest.NewRequest("GET", "/pub/r1", nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "", resp["role"])
}

func TestHandlePubGetAsset_fallsBackToGitCheckoutAndBackfills(t *testing.T) {
	deps, cacheDir := newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", true))

	// Simulate a note published before this feature shipped: the asset only
	// exists in the git checkout, not yet in AssetStore.
	repoDir := filepath.Join(cacheDir, "r1")
	require.NoError(t, os.MkdirAll(repoDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "legacy.png"), []byte("from-git-checkout"), 0644))

	req := httptest.NewRequest("GET", "/pub/r1/assets/legacy.png", nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Equal(t, []byte("from-git-checkout"), rr.Body.Bytes())

	// Backfilled: the next read no longer needs the checkout.
	astore, err := deps.Resolver.AssetStoreFor(ctx, "r1")
	require.NoError(t, err)
	backfilled, err := astore.Read("r1", "legacy.png")
	require.NoError(t, err)
	require.Equal(t, []byte("from-git-checkout"), backfilled)
}
