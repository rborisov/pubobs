package api

import (
	"encoding/json"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/model"
)

func handlePubListNotes(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := chi.URLParam(r, "repoId")
		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}
		notes, err := deps.Store.ListNotes(r.Context(), repoID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list notes failed")
			return
		}

		type noteItem struct {
			ID       string `json:"id"`
			Path     string `json:"path"`
			Title    string `json:"title"`
			SyncedAt string `json:"synced_at"`
		}

		items := make([]noteItem, 0, len(notes))
		for _, n := range notes {
			snap, _ := deps.Store.GetSnapshot(r.Context(), n.ID)
			syncedAt := ""
			if snap != nil {
				syncedAt = snap.SyncedAt.UTC().Format("2006-01-02T15:04:05Z")
			}
			items = append(items, noteItem{
				ID:       n.ID,
				Path:     n.Path,
				Title:    noteTitle(n.Path, snap),
				SyncedAt: syncedAt,
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"repo":  map[string]string{"id": repo.ID, "name": repo.Name},
			"notes": items,
		})
	}
}

func handlePubGetNote(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := chi.URLParam(r, "repoId")
		notePath := chi.URLParam(r, "*")

		if strings.HasSuffix(notePath, "/comments") {
			notePath = strings.TrimSuffix(notePath, "/comments")
			handlePubComments(w, r, deps, repoID, notePath)
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

		var meta struct {
			Tags        []string       `json:"tags"`
			Frontmatter map[string]any `json:"frontmatter"`
		}
		_ = json.Unmarshal([]byte(snap.MetadataJSON), &meta)

		backlinks, _ := deps.Store.GetBacklinks(r.Context(), repoID, notePath)
		type backlinkItem struct {
			Path  string `json:"path"`
			Title string `json:"title"`
		}
		bl := make([]backlinkItem, 0, len(backlinks))
		for _, b := range backlinks {
			bsnap, _ := deps.Store.GetSnapshot(r.Context(), b.ID)
			bl = append(bl, backlinkItem{Path: b.Path, Title: noteTitle(b.Path, bsnap)})
		}

		htmlContent, _ := deps.Cache.ReadRenderedHTML(repoID, notePath)
		if htmlContent == "" {
			htmlContent = snap.HTMLContent // old notes synced before git rendering
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"id":             note.ID,
			"path":           note.Path,
			"title":          noteTitle(notePath, snap),
			"html_content":   htmlContent,
			"tags":           meta.Tags,
			"frontmatter":    meta.Frontmatter,
			"git_commit_sha": snap.GitCommitSHA,
			"synced_at":      snap.SyncedAt.UTC().Format("2006-01-02T15:04:05Z"),
			"backlinks":      bl,
		})
	}
}

func handlePubComments(w http.ResponseWriter, r *http.Request, deps *Deps, repoID, notePath string) {
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

func handlePubGetAsset(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := chi.URLParam(r, "repoId")
		assetPath := chi.URLParam(r, "*")

		data, err := deps.Cache.ReadAsset(repoID, assetPath)
		if err != nil {
			writeError(w, http.StatusNotFound, "asset not found")
			return
		}

		ct := mime.TypeByExtension(filepath.Ext(assetPath))
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write(data)
	}
}

func noteTitle(path string, snap *model.NoteSnapshot) string {
	if snap != nil && snap.MetadataJSON != "" {
		var meta struct {
			Headings []string `json:"headings"`
		}
		if err := json.Unmarshal([]byte(snap.MetadataJSON), &meta); err == nil && len(meta.Headings) > 0 {
			return meta.Headings[0]
		}
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
