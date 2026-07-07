// backend/internal/api/admin_repo_storage.go
package api

import (
	"context"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/jobs"
)

type assignStorageBody struct {
	StorageDestinationID *string `json:"storage_destination_id"` // null = local
}

// sameDestination reports whether two storage-destination pointers refer to the
// same destination by VALUE: equal when both are nil (local) or both non-nil
// with equal ids.
func sameDestination(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func handleAdminAssignRepoStorage(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		repoID := chi.URLParam(r, "id")
		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}
		if repo.MigrationStatus == "running" {
			writeError(w, http.StatusConflict, "a migration is already running for this repo")
			return
		}
		var b assignStorageBody
		if err := readJSON(r, &b); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		// Validate the target destination exists (nil = local is always valid).
		if b.StorageDestinationID != nil {
			if _, err := deps.Store.GetStorageDestination(r.Context(), *b.StorageDestinationID); err != nil {
				writeError(w, http.StatusBadRequest, "unknown destination")
				return
			}
		}

		oldDest := repo.StorageDestinationID
		newDest := b.StorageDestinationID

		// No-op reassignment: the requested destination equals the current one.
		// Source and destination stores would alias the SAME objects, so a
		// migration would copy each key onto itself and then DELETE it — wiping
		// the repo's renders/assets. Short-circuit before touching anything.
		if sameDestination(oldDest, newDest) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "unchanged"})
			return
		}

		// Resolve source (old) and dest (new) store pairs BEFORE reassigning,
		// so the source still points at where the data currently lives.
		srcRender, srcAssetPlain, ok1 := deps.Resolver.StoresForDestination(oldDest)
		dstRender, dstAssetPlain, ok2 := deps.Resolver.StoresForDestination(newDest)
		if !ok1 || !ok2 {
			writeError(w, http.StatusInternalServerError, "resolve storage failed")
			return
		}

		// Persist the new assignment immediately (reads/writes go to the new
		// destination from now on; migration moves the existing data over).
		if err := deps.Store.SetRepoStorageDestination(r.Context(), repoID, newDest); err != nil {
			writeError(w, http.StatusInternalServerError, "assign destination failed")
			return
		}
		if err := deps.Store.SetRepoMigrationStatus(r.Context(), repoID, "running", 0, 0); err != nil {
			writeError(w, http.StatusInternalServerError, "set migration status failed")
			return
		}

		go func() {
			ctx := context.Background()
			migrated, failed, merr := jobs.RunRepoMigration(ctx, repoID, srcRender, dstRender, srcAssetPlain, dstAssetPlain)
			status := "done"
			if merr != nil || failed > 0 {
				status = "failed"
			}
			if err := deps.Store.SetRepoMigrationStatus(ctx, repoID, status, migrated+failed, migrated); err != nil {
				log.Printf("repo-migration: final status write for %s failed: %v", repoID, err)
			}
		}()

		writeJSON(w, http.StatusAccepted, map[string]string{"status": "running"})
	}
}
