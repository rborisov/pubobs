// backend/internal/api/admin_storage_usage_test.go
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
	"github.com/stretchr/testify/require"
)

func TestAdminStorageUsage_localBreakdown(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")

	deps.Config.RepoCacheDir = t.TempDir()
	deps.Config.RenderDir = t.TempDir()
	deps.Config.AssetDir = t.TempDir()
	deps.Cache = gitcache.NewCache(deps.Config.RepoCacheDir)
	deps.Resolver = newTestResolver(t, deps)
	require.NoError(t, os.WriteFile(filepath.Join(deps.Config.RenderDir, "x.enc"), []byte("12345"), 0644))

	req := httptest.NewRequest("GET", "/api/admin/storage-usage", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var body struct {
		Local struct {
			RendersBytes float64 `json:"renders_bytes"`
		} `json:"local"`
		Destinations []any `json:"destinations"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, float64(5), body.Local.RendersBytes)
	require.Empty(t, body.Destinations, "no S3 destinations configured in this test")
}
