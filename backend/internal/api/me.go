package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/model"
)

func handleMe(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		user, err := deps.Store.GetUserByID(r.Context(), claims.UserID)
		if err != nil || user == nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":                user.ID,
			"email":             user.Email,
			"name":              user.Name,
			"is_instance_admin": user.IsInstanceAdmin,
			"is_admin":          user.IsAdmin,
		})
	}
}

func handleListFolderMappings(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		mappings, err := deps.Store.ListUserFolderMappings(r.Context(), claims.UserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list mappings failed")
			return
		}
		if mappings == nil {
			mappings = []*model.FolderMapping{}
		}
		writeJSON(w, http.StatusOK, mappings)
	}
}

func handleUpsertFolderMapping(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "repoID")
		var body struct {
			VaultFolder   string `json:"vault_folder"`
			RepoSubfolder string `json:"repo_subfolder"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := deps.Store.UpsertFolderMapping(r.Context(), claims.UserID, repoID, body.VaultFolder, body.RepoSubfolder); err != nil {
			writeError(w, http.StatusInternalServerError, "upsert failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
