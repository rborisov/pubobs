// backend/internal/api/admin_storage.go
package api

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/jobs"
	"github.com/pubobs/backend/internal/model"
	"github.com/pubobs/backend/internal/renderstore"
)

var errValidationMismatch = errors.New("validation object mismatch")

// storageSettingsResponse omits the secrets (s3_secret_key, asset_encryption_key)
// that should never round-trip back to the browser.
type storageSettingsResponse struct {
	StoreType       string `json:"store_type"`
	S3Endpoint      string `json:"s3_endpoint"`
	S3Bucket        string `json:"s3_bucket"`
	S3AccessKey     string `json:"s3_access_key"`
	S3Region        string `json:"s3_region"`
	S3UseSSL        bool   `json:"s3_use_ssl"`
	MigrationStatus string `json:"migration_status"`
	MigrationTotal  int    `json:"migration_total"`
	MigrationDone   int    `json:"migration_done"`
}

func toStorageSettingsResponse(s *model.StorageSettings) storageSettingsResponse {
	return storageSettingsResponse{
		StoreType:       s.StoreType,
		S3Endpoint:      s.S3Endpoint,
		S3Bucket:        s.S3Bucket,
		S3AccessKey:     s.S3AccessKey,
		S3Region:        s.S3Region,
		S3UseSSL:        s.S3UseSSL,
		MigrationStatus: s.MigrationStatus,
		MigrationTotal:  s.MigrationTotal,
		MigrationDone:   s.MigrationDone,
	}
}

func handleAdminGetStorageSettings(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		settings, err := deps.Store.GetStorageSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "load storage settings failed")
			return
		}
		writeJSON(w, http.StatusOK, toStorageSettingsResponse(settings))
	}
}

type updateStorageSettingsBody struct {
	StoreType   string `json:"store_type"`
	S3Endpoint  string `json:"s3_endpoint"`
	S3Bucket    string `json:"s3_bucket"`
	S3AccessKey string `json:"s3_access_key"`
	S3SecretKey string `json:"s3_secret_key"`
	S3Region    string `json:"s3_region"`
	S3UseSSL    bool   `json:"s3_use_ssl"`
}

func handleAdminUpdateStorageSettings(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		var body updateStorageSettingsBody
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.StoreType != "local" && body.StoreType != "s3" {
			writeError(w, http.StatusBadRequest, "store_type must be \"local\" or \"s3\"")
			return
		}

		current, err := deps.Store.GetStorageSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "load storage settings failed")
			return
		}
		current.StoreType = body.StoreType
		current.S3Endpoint = body.S3Endpoint
		current.S3Bucket = body.S3Bucket
		current.S3AccessKey = body.S3AccessKey
		if body.S3SecretKey != "" {
			current.S3SecretKey = body.S3SecretKey // blank means "keep existing" — never overwrite with empty
		}
		current.S3Region = body.S3Region
		current.S3UseSSL = body.S3UseSSL

		// Validate the merged candidate (current), not the raw request body:
		// when s3_secret_key is left blank to "keep existing", body.S3SecretKey
		// is empty but current.S3SecretKey holds the real preserved secret —
		// validating body directly would spuriously fail auth against "".
		if current.StoreType == "s3" {
			if err := S3ValidateFunc(current); err != nil {
				writeError(w, http.StatusBadRequest, "S3 settings invalid: "+err.Error())
				return
			}
		}

		newRenderStore, err := renderstore.New(
			current.StoreType, deps.Config.RenderDir,
			current.S3Endpoint, current.S3Bucket, current.S3AccessKey, current.S3SecretKey,
			current.S3Region, current.S3UseSSL, "renders/",
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "build new render store failed")
			return
		}
		newAssetBase, err := renderstore.New(
			current.StoreType, deps.Config.AssetDir,
			current.S3Endpoint, current.S3Bucket, current.S3AccessKey, current.S3SecretKey,
			current.S3Region, current.S3UseSSL, "assets/",
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "build new asset store failed")
			return
		}
		assetKeyBytes, err := hex.DecodeString(current.AssetEncryptionKey)
		if err != nil || len(assetKeyBytes) != 32 {
			writeError(w, http.StatusInternalServerError, "invalid asset encryption key")
			return
		}
		newAssetStore, err := renderstore.NewEncryptingStore(newAssetBase, assetKeyBytes)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "build new encrypting asset store failed")
			return
		}

		if err := deps.Store.UpsertStorageSettings(r.Context(), current); err != nil {
			writeError(w, http.StatusInternalServerError, "save storage settings failed")
			return
		}

		// Swap only after the save succeeded — applies immediately, no
		// restart, no downtime.
		deps.RenderStore.Swap(newRenderStore)
		deps.AssetStore.Swap(newAssetStore)

		writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
	}
}

// S3ValidateFunc performs the S3 round-trip validation used by
// handleAdminUpdateStorageSettings. It's a variable (rather than a plain
// function call) so tests can substitute a fake round-trip and assert on
// the effective settings that reached validation, without needing a live
// S3-compatible endpoint.
var S3ValidateFunc = validateS3Settings

// validateS3Settings does a write+read+delete round-trip against the
// candidate settings before they're persisted or swapped in, so a bad
// bucket/credential is caught immediately rather than silently breaking
// reads/writes after the swap. Callers must pass the merged/effective
// settings (e.g. current after applying the request body), not the raw
// request body, so that a blank s3_secret_key ("keep existing") validates
// against the real preserved secret rather than an empty string.
func validateS3Settings(s *model.StorageSettings) error {
	testStore, err := renderstore.NewS3(
		s.S3Endpoint, s.S3Bucket, s.S3AccessKey, s.S3SecretKey,
		s.S3Region, s.S3UseSSL, "pubobs-validate/",
	)
	if err != nil {
		return err
	}
	const testKey = "connectivity-check"
	payload := []byte("pubobs storage settings validation")
	if err := testStore.Write("_validate", testKey, payload); err != nil {
		return err
	}
	got, err := testStore.Read("_validate", testKey)
	if err != nil {
		return err
	}
	if string(got) != string(payload) {
		return errValidationMismatch
	}
	return testStore.Delete("_validate", testKey)
}

func dirSizeBytes(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
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

		usage := map[string]any{
			"local_free_bytes":    freeBytes,
			"local_repos_bytes":   dirSizeBytes(deps.Config.RepoCacheDir),
			"local_renders_bytes": dirSizeBytes(deps.Config.RenderDir),
			"local_assets_bytes":  dirSizeBytes(deps.Config.AssetDir),
			"s3_renders_bytes":    int64(0),
			"s3_assets_bytes":     int64(0),
		}

		if s3Renders, ok := deps.RenderStore.Current().(*renderstore.S3RenderStore); ok {
			if objects, err := s3Renders.ListAllObjects(); err == nil {
				usage["s3_renders_bytes"] = renderstore.SumObjectSizesWithPrefix(objects, "renders/")
				usage["s3_assets_bytes"] = renderstore.SumObjectSizesWithPrefix(objects, "assets/")
			}
		}

		writeJSON(w, http.StatusOK, usage)
	}
}

func handleAdminMigrateStorage(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}

		settings, err := deps.Store.GetStorageSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "load storage settings failed")
			return
		}
		if settings.StoreType == "local" {
			writeError(w, http.StatusBadRequest, "configure S3 storage before migrating")
			return
		}
		if settings.MigrationStatus == "running" {
			writeError(w, http.StatusConflict, "migration already running")
			return
		}

		settings.MigrationStatus = "running"
		settings.MigrationTotal = 0
		settings.MigrationDone = 0
		if err := deps.Store.UpsertStorageSettings(r.Context(), settings); err != nil {
			writeError(w, http.StatusInternalServerError, "save storage settings failed")
			return
		}

		go runMigrationInBackground(deps)

		writeJSON(w, http.StatusAccepted, map[string]string{"status": "running"})
	}
}

// runMigrationInBackground migrates from the currently-configured local
// render/asset stores to whatever deps.RenderStore/deps.AssetStore already
// point at (Task 7's handler already called Swap() when the admin saved
// these settings, so this is live immediately, no restart) — i.e. it's
// always "local → whatever's configured now", matching this feature's
// actual use case of moving *off* local disk.
func runMigrationInBackground(deps *Deps) {
	ctx := context.Background()
	localRenders := renderstore.NewLocal(deps.Config.RenderDir)
	localAssets := renderstore.NewLocal(deps.Config.AssetDir)

	migrated, failed, err := jobs.RunMigrationCycle(ctx, localRenders, deps.RenderStore, localAssets, deps.AssetStore)

	settings, gerr := deps.Store.GetStorageSettings(ctx)
	if gerr != nil {
		fmt.Printf("migration: reload settings after run: %v\n", gerr)
		return
	}
	settings.MigrationTotal = migrated + failed
	settings.MigrationDone = migrated
	if err != nil {
		settings.MigrationStatus = "failed"
	} else {
		settings.MigrationStatus = "done"
	}
	if uerr := deps.Store.UpsertStorageSettings(ctx, settings); uerr != nil {
		fmt.Printf("migration: save final status: %v\n", uerr)
	}
}
