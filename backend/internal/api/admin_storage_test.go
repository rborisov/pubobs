// backend/internal/api/admin_storage_test.go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/gitcache"
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

// TestAdminStorageSettings_putS3BlankSecretValidatesPreservedSecret guards
// against a regression where the handler validated the raw request body
// (whose s3_secret_key is blank when the admin means "keep existing")
// instead of the merged candidate that actually gets persisted/swapped in.
// There's no live S3 fixture in this repo, so instead of hitting a real
// endpoint we swap api.S3ValidateFunc for a fake that just records the
// secret it was called with — this proves the *effective* secret reaching
// validation is the preserved one, not "", without needing network access.
func TestAdminStorageSettings_putS3BlankSecretValidatesPreservedSecret(t *testing.T) {
	deps := newTestDepsForStorage(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.UpsertStorageSettings(ctx, &model.StorageSettings{
		StoreType:          "s3",
		S3Endpoint:         "s3.example.com:9000",
		S3Bucket:           "old-bucket",
		S3AccessKey:        "AKIAOLD",
		S3SecretKey:        "super-secret-existing-key",
		S3Region:           "us-east-1",
		S3UseSSL:           false,
		MigrationStatus:    "idle",
		AssetEncryptionKey: strings.Repeat("00", 32),
	})

	var capturedSecret string
	var capturedBucket string
	origValidate := api.S3ValidateFunc
	api.S3ValidateFunc = func(s *model.StorageSettings) error {
		capturedSecret = s.S3SecretKey
		capturedBucket = s.S3Bucket
		return nil // stand-in for a successful round-trip against a real endpoint
	}
	defer func() { api.S3ValidateFunc = origValidate }()

	// Change the bucket but leave s3_secret_key blank — the documented way
	// to keep the existing secret.
	body := `{"store_type":"s3","s3_endpoint":"s3.example.com:9000","s3_bucket":"new-bucket","s3_access_key":"AKIAOLD","s3_secret_key":"","s3_region":"us-east-1","s3_use_ssl":false}`
	req := httptest.NewRequest("PUT", "/api/admin/storage-settings", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Equal(t, "super-secret-existing-key", capturedSecret, "validation must run against the preserved existing secret, not an empty string")
	require.Equal(t, "new-bucket", capturedBucket, "validation must see the newly requested bucket")

	saved, err := deps.Store.GetStorageSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "new-bucket", saved.S3Bucket)
	require.Equal(t, "super-secret-existing-key", saved.S3SecretKey, "existing secret must be preserved in the persisted config")
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

func TestAdminStorageUsage_reportsLocalBreakdown(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.UpsertStorageSettings(ctx, &model.StorageSettings{StoreType: "local", MigrationStatus: "idle"})

	deps.Config.RepoCacheDir = t.TempDir()
	deps.Config.RenderDir = t.TempDir()
	deps.Config.AssetDir = t.TempDir()
	deps.Cache = gitcache.NewCache(deps.Config.RepoCacheDir)
	deps.RenderStore = renderstore.NewSwappableStore(renderstore.NewLocal(deps.Config.RenderDir))
	deps.AssetStore = renderstore.NewSwappableStore(renderstore.NewLocal(deps.Config.AssetDir))

	require.NoError(t, os.WriteFile(filepath.Join(deps.Config.RenderDir, "x.enc"), []byte("12345"), 0644))

	req := httptest.NewRequest("GET", "/api/admin/storage-usage", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, float64(5), body["local_renders_bytes"])
	require.Equal(t, float64(0), body["s3_renders_bytes"], "no S3 configured in this test")
}
