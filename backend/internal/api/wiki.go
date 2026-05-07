package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/gitcache"
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
	raw, err := deps.Cache.ReadRawFile(repoID, gitcache.CommentsFilePath(notePath))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read comments failed")
		return
	}

	type item struct {
		AuthorName  string `json:"author_name"`
		AuthorEmail string `json:"author_email"`
		CreatedAt   string `json:"created_at"`
		Body        string `json:"body"`
	}
	parsed := gitcache.ParseComments(raw)
	out := make([]item, 0, len(parsed))
	for _, c := range parsed {
		out = append(out, item{
			AuthorName:  c.AuthorName,
			AuthorEmail: c.AuthorEmail,
			CreatedAt:   c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			Body:        c.Body,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func serveAddComment(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "reader"); err != nil {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	var body struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Body) == "" {
		writeError(w, http.StatusBadRequest, "body required")
		return
	}

	repo, err := deps.Store.GetRepo(r.Context(), repoID)
	if err != nil || repo == nil {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}

	credJSON, err := decryptCreds(deps, repo.EncryptedCreds)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cred decrypt failed")
		return
	}

	user, err := deps.Store.GetUserByID(r.Context(), claims.UserID)
	if err != nil || user == nil {
		writeError(w, http.StatusInternalServerError, "user not found")
		return
	}

	if err := deps.Cache.AppendComment(r.Context(), repo, credJSON, notePath, user.Name, user.Email, body.Body); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save comment")
		return
	}

	w.WriteHeader(http.StatusCreated)
}
