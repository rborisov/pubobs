package api_test

import (
	"context"
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
