package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
)

func handleAdminSetRepoStrictCredentials(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		id := chi.URLParam(r, "id")
		if !requireRepoManage(r.Context(), deps, claims, id, w) {
			return
		}
		var body struct {
			Strict bool `json:"strict"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := deps.Store.SetRepoStrictCredentials(r.Context(), id, body.Strict); err != nil {
			writeError(w, http.StatusInternalServerError, "update failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
