package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pubobs/backend/internal/auth"
)

func requireAdmin(claims *auth.AccessClaims, w http.ResponseWriter) bool {
	if !claims.IsAdmin {
		writeError(w, http.StatusForbidden, "instance admin required")
		return false
	}
	return true
}

func handleAdminHealth(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		h, err := deps.Store.GetHealth(r.Context())
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"disk_status": "ok", "disk_free_pct": 100})
			return
		}
		writeJSON(w, http.StatusOK, h)
	}
}

func handleAdminCreateRepo(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		var body struct {
			Name          string `json:"name"`
			RemoteURL     string `json:"remote_url"`
			Username      string `json:"username"`
			Password      string `json:"password"`
			DefaultBranch string `json:"default_branch"`
		}
		if err := readJSON(r, &body); err != nil || body.Name == "" || body.RemoteURL == "" {
			writeError(w, http.StatusBadRequest, "name and remote_url are required")
			return
		}
		if body.DefaultBranch == "" {
			body.DefaultBranch = "main"
		}
		credJSON, _ := json.Marshal(map[string]string{"username": body.Username, "password": body.Password})
		encCreds, err := auth.EncryptCreds(deps.Config.SecretKey, string(credJSON))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encrypt creds failed")
			return
		}
		repo, err := deps.Store.CreateRepo(r.Context(), uuid.NewString(), body.Name, body.RemoteURL, encCreds, body.DefaultBranch)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "create repo failed")
			return
		}
		userID := claims.UserID
		go func() {
			n, err := importRepoFromGit(context.Background(), deps, repo.ID, userID)
			if err != nil {
				log.Printf("[pubobs] background import for %s failed: %v", repo.ID, err)
			} else if n > 0 {
				log.Printf("[pubobs] imported %d note(s) from existing repo %s", n, repo.ID)
			}
		}()
		writeJSON(w, http.StatusCreated, map[string]string{"id": repo.ID, "name": repo.Name})
	}
}

func handleAdminImportRepo(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		id := chi.URLParam(r, "id")
		n, err := importRepoFromGit(r.Context(), deps, id, claims.UserID)
		if err != nil {
			writeError(w, http.StatusBadGateway, "import failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"imported": n})
	}
}

func importRepoFromGit(ctx context.Context, deps *Deps, repoID, syncedBy string) (int, error) {
	repo, err := deps.Store.GetRepo(ctx, repoID)
	if err != nil || repo == nil {
		return 0, fmt.Errorf("repo not found")
	}
	credJSON, err := decryptCreds(deps, repo.EncryptedCreds)
	if err != nil {
		return 0, err
	}
	files, err := deps.Cache.ListFiles(ctx, repo, credJSON)
	if err != nil {
		return 0, err
	}
	headSHA, _ := deps.Cache.HeadSHA(repoID)

	imported := 0
	for _, f := range files {
		if strings.HasSuffix(f.Path, "-comments.md") || strings.HasPrefix(f.Path, "_pubobs/") {
			continue
		}
		note, err := deps.Store.UpsertNote(ctx, repoID, f.Path)
		if err != nil {
			continue
		}
		meta := extractMetadata(f.Content, map[string]any{})
		metaJSON, _ := json.Marshal(meta)
		deps.Store.UpsertSnapshot(ctx, note.ID, "", string(metaJSON), syncedBy, headSHA)
		deps.Store.UpsertNoteLinks(ctx, note.ID, meta.Links)
		imported++
	}
	return imported, nil
}

func handleAdminUpdateRepo(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		id := chi.URLParam(r, "id")
		var body struct {
			Name          string `json:"name"`
			RemoteURL     string `json:"remote_url"`
			Username      string `json:"username"`
			Password      string `json:"password"`
			DefaultBranch string `json:"default_branch"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		credJSON, _ := json.Marshal(map[string]string{"username": body.Username, "password": body.Password})
		encCreds, err := auth.EncryptCreds(deps.Config.SecretKey, string(credJSON))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encrypt creds failed")
			return
		}
		if err := deps.Store.UpdateRepo(r.Context(), id, body.Name, body.RemoteURL, encCreds, body.DefaultBranch); err != nil {
			writeError(w, http.StatusInternalServerError, "update repo failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleAdminDeleteRepo(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		id := chi.URLParam(r, "id")
		if deps.Cache != nil {
			deps.Cache.Evict(id)
		}
		if err := deps.Store.DeleteRepo(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "delete repo failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleAdminGrantAccess(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		repoID := chi.URLParam(r, "id")
		var body struct {
			PrincipalType string `json:"principal_type"`
			PrincipalID   string `json:"principal_id"`
			Role          string `json:"role"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := deps.Store.GrantAccess(r.Context(), uuid.NewString(), repoID, body.PrincipalType, body.PrincipalID, body.Role); err != nil {
			writeError(w, http.StatusInternalServerError, "grant access failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleAdminRevokeAccess(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		accessID := chi.URLParam(r, "accessID")
		if err := deps.Store.RevokeAccess(r.Context(), accessID); err != nil {
			writeError(w, http.StatusInternalServerError, "revoke access failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleAdminListRepoAccess(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		repoID := chi.URLParam(r, "id")
		entries, err := deps.Store.ListRepoAccess(r.Context(), repoID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list access failed")
			return
		}
		type accessResp struct {
			ID            string `json:"id"`
			RepoID        string `json:"repo_id"`
			PrincipalType string `json:"principal_type"`
			PrincipalID   string `json:"principal_id"`
			Role          string `json:"role"`
		}
		out := make([]accessResp, len(entries))
		for i, e := range entries {
			out[i] = accessResp{e.ID, e.RepoID, e.PrincipalType, e.PrincipalID, e.Role}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func handleAdminListUsers(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		users, err := deps.Store.ListUsers(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list users failed")
			return
		}
		writeJSON(w, http.StatusOK, users)
	}
}

func handleAdminCreateGroup(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := readJSON(r, &body); err != nil || body.Name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		g, err := deps.Store.CreateGroup(r.Context(), uuid.NewString(), body.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "create group failed")
			return
		}
		writeJSON(w, http.StatusCreated, g)
	}
}

func handleAdminAddGroupMember(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		groupID := chi.URLParam(r, "id")
		var body struct {
			UserID string `json:"user_id"`
		}
		if err := readJSON(r, &body); err != nil || body.UserID == "" {
			writeError(w, http.StatusBadRequest, "user_id is required")
			return
		}
		if err := deps.Store.AddGroupMember(r.Context(), groupID, body.UserID); err != nil {
			writeError(w, http.StatusInternalServerError, "add member failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
