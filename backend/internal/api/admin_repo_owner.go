package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
)

func handleAdminSetRepoOwner(deps *Deps) http.HandlerFunc {
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
		var body struct {
			OwnerUserID string `json:"owner_user_id"`
		}
		if err := readJSON(r, &body); err != nil || body.OwnerUserID == "" {
			writeError(w, http.StatusBadRequest, "owner_user_id required")
			return
		}
		if u, err := deps.Store.GetUserByID(r.Context(), body.OwnerUserID); err != nil || u == nil {
			writeError(w, http.StatusBadRequest, "unknown user")
			return
		}
		if err := deps.Store.SetRepoOwner(r.Context(), repoID, body.OwnerUserID); err != nil {
			writeError(w, http.StatusInternalServerError, "set owner failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
