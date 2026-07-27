package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/model"
)

func handleListFiles(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")

		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}
		role, _ := deps.Store.GetUserRole(r.Context(), claims.UserID, repoID)
		if !claims.IsAdmin && !model.RoleAtLeast(role, "reader") {
			writeError(w, http.StatusForbidden, "reader role required")
			return
		}

		credJSON, err := decryptCreds(deps, repo.EncryptedCreds)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cred decrypt failed")
			return
		}

		entries, err := deps.Cache.ListFiles(r.Context(), repo, credJSON)
		if err != nil {
			// Redacted for the same reason as handleListDataFiles: this error
			// carries git's raw stderr, which quotes the credentialed remote
			// URL on any clone/fetch failure, and reader role is all it takes
			// to reach this handler.
			writeError(w, http.StatusBadGateway, "list files failed: "+gitcache.Redact(err.Error()))
			return
		}
		if entries == nil {
			entries = []model.FileEntry{}
		}
		deps.Store.TouchLastUsedAt(r.Context(), repoID)
		writeJSON(w, http.StatusOK, entries)
	}
}
