package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/model"
)

func handleListNotes(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")
		if err := requireRepoRole(r.Context(), deps, claims, repoID, "reader"); err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		notes, err := deps.Store.ListNotes(r.Context(), repoID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list notes failed")
			return
		}
		if notes == nil {
			notes = []*model.Note{}
		}
		type noteResp struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		}
		out := make([]noteResp, len(notes))
		for i, n := range notes {
			out[i] = noteResp{ID: n.ID, Path: n.Path}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// handleNoteGet dispatches GET /api/repos/{id}/notes/* based on path suffix.
func handleNoteGet(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")
		notePath := chi.URLParam(r, "*")

		switch {
		case strings.HasSuffix(notePath, "/backlinks"):
			serveBacklinks(w, r, deps, claims, repoID, strings.TrimSuffix(notePath, "/backlinks"))
		case strings.HasSuffix(notePath, "/history"):
			serveHistory(w, r, deps, claims, repoID, strings.TrimSuffix(notePath, "/history"))
		case strings.HasSuffix(notePath, "/comments"):
			serveListComments(w, r, deps, claims, repoID, strings.TrimSuffix(notePath, "/comments"))
		default:
			serveNoteView(w, r, deps, claims, repoID, notePath)
		}
	}
}

// handleNotePost dispatches POST /api/repos/{id}/notes/* (only /comments supported).
func handleNotePost(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")
		notePath := chi.URLParam(r, "*")

		if strings.HasSuffix(notePath, "/comments") {
			serveAddComment(w, r, deps, claims, repoID, strings.TrimSuffix(notePath, "/comments"))
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func serveNoteView(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "reader"); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	note, err := deps.Store.GetNote(r.Context(), repoID, notePath)
	if err != nil || note == nil {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}
	snap, err := deps.Store.GetSnapshot(r.Context(), note.ID)
	if err != nil || snap == nil {
		writeError(w, http.StatusNotFound, "snapshot not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":             note.ID,
		"path":           note.Path,
		"html_content":   snap.HTMLContent,
		"metadata_json":  snap.MetadataJSON,
		"git_commit_sha": snap.GitCommitSHA,
		"synced_at":      snap.SyncedAt,
	})
}

func serveHistory(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "reader"); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	repo, _ := deps.Store.GetRepo(r.Context(), repoID)
	if repo == nil {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}
	credJSON, _ := decryptCreds(deps, repo.EncryptedCreds)
	commits, err := deps.Cache.History(r.Context(), repo, credJSON, notePath)
	if err != nil {
		writeError(w, http.StatusBadGateway, "history failed: "+err.Error())
		return
	}
	if commits == nil {
		commits = []model.Commit{}
	}
	writeJSON(w, http.StatusOK, commits)
}

func serveBacklinks(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "reader"); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	notes, err := deps.Store.GetBacklinks(r.Context(), repoID, notePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "backlinks failed")
		return
	}
	if notes == nil {
		notes = []*model.Note{}
	}
	writeJSON(w, http.StatusOK, notes)
}

func serveListComments(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "reader"); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	note, _ := deps.Store.GetNote(r.Context(), repoID, notePath)
	if note == nil {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}
	comments, err := deps.Store.ListComments(r.Context(), note.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list comments failed")
		return
	}
	if comments == nil {
		comments = []*model.Comment{}
	}
	writeJSON(w, http.StatusOK, comments)
}

func serveAddComment(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "commentator"); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	note, _ := deps.Store.GetNote(r.Context(), repoID, notePath)
	if note == nil {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}
	var body struct {
		ParentID *string `json:"parent_id"`
		Body     string  `json:"body"`
	}
	if err := readJSON(r, &body); err != nil || body.Body == "" {
		writeError(w, http.StatusBadRequest, "body is required")
		return
	}
	comment, err := deps.Store.CreateComment(r.Context(), uuid.NewString(), note.ID, claims.UserID, body.ParentID, body.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create comment failed")
		return
	}
	writeJSON(w, http.StatusCreated, comment)
}
