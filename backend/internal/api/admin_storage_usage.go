// backend/internal/api/admin_storage_usage.go
package api

import (
	"net/http"
	"os"

	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/renderstore"
)

// dbSizeBytes returns the total on-disk size of the SQLite database: the main
// file plus its -wal and -shm sidecars (present when the DB is in WAL mode).
// Missing files count as 0.
func dbSizeBytes(dbPath string) int64 {
	var total int64
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			total += info.Size()
		}
	}
	return total
}

type destUsage struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RendersBytes int64  `json:"renders_bytes"`
	AssetsBytes  int64  `json:"assets_bytes"`
	Error        string `json:"error,omitempty"`
}

func handleAdminStorageUsage(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		freeBytes, _, err := deps.Cache.DiskUsage()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "disk usage check failed")
			return
		}

		dests, err := deps.Store.ListStorageDestinations(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list destinations failed")
			return
		}
		destUsages := make([]destUsage, 0, len(dests))
		for _, d := range dests {
			du := destUsage{ID: d.ID, Name: d.Name}
			rstore, ok := deps.Resolver.DestinationRenderStore(d.ID)
			if s3, isS3 := rstore.(*renderstore.S3RenderStore); ok && isS3 {
				objects, lerr := s3.ListAllObjects()
				if lerr != nil {
					du.Error = lerr.Error()
				} else {
					du.RendersBytes = renderstore.SumObjectSizesWithPrefix(objects, "renders/")
					du.AssetsBytes = renderstore.SumObjectSizesWithPrefix(objects, "assets/")
				}
			}
			destUsages = append(destUsages, du)
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"local": map[string]any{
				"free_bytes":    freeBytes,
				"db_bytes":      dbSizeBytes(deps.Config.DBPath),
				"repos_bytes":   dirSizeBytes(deps.Config.RepoCacheDir),
				"renders_bytes": dirSizeBytes(deps.Config.RenderDir),
				"assets_bytes":  dirSizeBytes(deps.Config.AssetDir),
			},
			"destinations": destUsages,
		})
	}
}
