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

	// A DB file (7 bytes) plus a -wal sidecar (3 bytes) → db_bytes should sum to 10.
	dbPath := filepath.Join(t.TempDir(), "pubobs.db")
	require.NoError(t, os.WriteFile(dbPath, []byte("dbbytes"), 0644))
	require.NoError(t, os.WriteFile(dbPath+"-wal", []byte("wal"), 0644))
	deps.Config.DBPath = dbPath

	req := httptest.NewRequest("GET", "/api/admin/storage-usage", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var body struct {
		Local struct {
			RendersBytes float64 `json:"renders_bytes"`
			DBBytes      float64 `json:"db_bytes"`
		} `json:"local"`
		Destinations []any `json:"destinations"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, float64(5), body.Local.RendersBytes)
	require.Equal(t, float64(10), body.Local.DBBytes, "db_bytes = db file (7) + -wal sidecar (3)")
	require.Empty(t, body.Destinations, "no S3 destinations configured in this test")
}
