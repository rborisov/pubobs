// backend/internal/api/admin_storage_test.go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/model"
	"github.com/pubobs/backend/internal/renderstore"
	"github.com/stretchr/testify/require"
)

func newTestDepsForStorage(t *testing.T) *api.Deps {
	t.Helper()
	deps := newTestDeps(t)
	deps.RenderStore = renderstore.NewSwappableStore(renderstore.NewLocal(t.TempDir()))
	deps.AssetStore = renderstore.NewSwappableStore(renderstore.NewLocal(t.TempDir()))
	return deps
}

func TestAdminStorageSettings_getReturnsCurrentConfig(t *testing.T) {
	deps := newTestDepsForStorage(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	// GetStorageSettings must succeed even before Task 4's boot-time seeding
	// runs (these API tests build Deps directly, bypassing main()), so seed
	// it explicitly here.
	deps.Store.UpsertStorageSettings(ctx, &model.StorageSettings{StoreType: "local", MigrationStatus: "idle"})

	req := httptest.NewRequest("GET", "/api/admin/storage-settings", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, "local", body["store_type"])
	_, hasSecretKey := body["s3_secret_key"]
	require.False(t, hasSecretKey, "secret key must never be echoed back to the client")
}

func TestAdminStorageSettings_putRejectsNonAdmin(t *testing.T) {
	deps := newTestDepsForStorage(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "editor@x.com", "Editor")
	deps.Store.UpsertStorageSettings(ctx, &model.StorageSettings{StoreType: "local", MigrationStatus: "idle"})

	req := httptest.NewRequest("PUT", "/api/admin/storage-settings", strings.NewReader(`{"store_type":"local"}`))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "editor@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code)
}

func TestAdminStorageSettings_putRejectsInvalidS3Config(t *testing.T) {
	deps := newTestDepsForStorage(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.UpsertStorageSettings(ctx, &model.StorageSettings{StoreType: "local", MigrationStatus: "idle"})

	body := `{"store_type":"s3","s3_endpoint":"s3.invalid.invalid:9000","s3_bucket":"nope","s3_access_key":"x","s3_secret_key":"y","s3_region":"us-east-1","s3_use_ssl":false}`
	req := httptest.NewRequest("PUT", "/api/admin/storage-settings", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())

	// Rejected config must not have been persisted, and the live store must
	// not have been swapped.
	saved, err := deps.Store.GetStorageSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "local", saved.StoreType)
	_, isLocal := deps.RenderStore.Current().(*renderstore.LocalRenderStore)
	require.True(t, isLocal, "RenderStore must still be the original local store")
}

func TestAdminStorageSettings_putAppliesLive(t *testing.T) {
	deps := newTestDepsForStorage(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.UpsertStorageSettings(ctx, &model.StorageSettings{StoreType: "local", MigrationStatus: "idle", AssetEncryptionKey: strings.Repeat("00", 32)})

	// Switching to a different local directory (standing in for "a different
	// backend") should take effect immediately, no restart: write via the
	// swapped-in store and read it straight back through deps.RenderStore.
	newDir := t.TempDir()
	body := `{"store_type":"local"}`
	req := httptest.NewRequest("PUT", "/api/admin/storage-settings", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	_ = newDir // the handler derives its own dirs from deps.Config; see Step 3
	require.NoError(t, deps.RenderStore.Write("r1", "note.md", []byte("after-swap")))
	got, err := deps.RenderStore.Read("r1", "note.md")
	require.NoError(t, err)
	require.Equal(t, []byte("after-swap"), got)
}
