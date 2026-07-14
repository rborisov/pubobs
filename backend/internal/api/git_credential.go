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

func handleVerifyGitCredential(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")
		if err := requireRepoRole(r.Context(), deps, claims, repoID, "editor"); err != nil {
			writeError(w, http.StatusForbidden, "editor role required")
			return
		}
		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}
		enc, ok, err := deps.Store.GetUserCredentialSecret(r.Context(), repoID, claims.UserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "lookup failed")
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "no credential configured")
			return
		}
		cred, err := auth.DecryptCreds(deps.Config.SecretKey, enc)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "decrypt failed")
			return
		}
		// The clone/fetch that VerifyWriteCredential needs uses the owner
		// service credential, never the user's own token.
		ownerCred, err := decryptCreds(deps, repo.EncryptedCreds)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "owner credential unavailable")
			return
		}
		status, errMsg := "verified", ""
		if verr := deps.Cache.VerifyWriteCredential(repo, ownerCred, cred); verr != nil {
			status = "auth_failed"
			errMsg = "could not authenticate or push to the remote" // never echo the token/raw error verbatim
		}
		if serr := deps.Store.SetUserCredentialVerification(r.Context(), repoID, claims.UserID, status, errMsg); serr != nil {
			writeError(w, http.StatusInternalServerError, "status write failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": status})
	}
}

// handleGetGitCredential reports whether the caller has their own credential
// configured for this repo and its last verification status. It never returns
// the secret — the token is write-only from the client's perspective.
func handleGetGitCredential(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")
		if err := requireRepoRole(r.Context(), deps, claims, repoID, "editor"); err != nil {
			writeError(w, http.StatusForbidden, "editor role required")
			return
		}
		_, configured, err := deps.Store.GetUserCredentialSecret(r.Context(), repoID, claims.UserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "lookup failed")
			return
		}
		verifyStatus := ""
		if configured {
			if creds, lerr := deps.Store.ListUserCredentials(r.Context(), repoID); lerr == nil {
				for _, c := range creds {
					if c.UserID == claims.UserID {
						verifyStatus = c.VerifyStatus
						break
					}
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"configured":    configured,
			"verify_status": verifyStatus,
		})
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
