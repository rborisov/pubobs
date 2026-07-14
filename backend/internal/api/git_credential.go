package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
)

func handleSetGitCredential(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")
		if err := requireRepoRole(r.Context(), deps, claims, repoID, "editor"); err != nil {
			writeError(w, http.StatusForbidden, "editor role required")
			return
		}
		var body struct {
			Username string `json:"username"`
			Token    string `json:"token"`
			GitName  string `json:"git_name"`
			GitEmail string `json:"git_email"`
		}
		if err := readJSON(r, &body); err != nil || body.Username == "" || body.Token == "" {
			writeError(w, http.StatusBadRequest, "username and token required")
			return
		}
		credPlain, _ := json.Marshal(map[string]string{"username": body.Username, "password": body.Token})
		enc, err := auth.EncryptCreds(deps.Config.SecretKey, string(credPlain))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encrypt failed")
			return
		}
		if err := deps.Store.UpsertUserCredential(r.Context(), repoID, claims.UserID, enc, body.GitName, body.GitEmail); err != nil {
			writeError(w, http.StatusInternalServerError, "save failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
	}
}

func handleDeleteGitCredential(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")
		if err := requireRepoRole(r.Context(), deps, claims, repoID, "editor"); err != nil {
			writeError(w, http.StatusForbidden, "editor role required")
			return
		}
		if err := deps.Store.DeleteUserCredential(r.Context(), repoID, claims.UserID); err != nil {
			writeError(w, http.StatusInternalServerError, "delete failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}
