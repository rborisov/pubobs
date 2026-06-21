package api

import (
	"encoding/json"
	"mime"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/model"
)

var repoIDInHTML = regexp.MustCompile(`(/pub/|#/read/)([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/`)

func rewriteRepoID(html, repoID string) string {
	return repoIDInHTML.ReplaceAllString(html, "${1}"+repoID+"/")
}

// pubRepoAccess returns the repo if the request is allowed to access it.
// Access is granted when allow_guest is true, or when the request carries a
// valid auth token with at least the reader role for this repo.
func pubRepoAccess(r *http.Request, deps *Deps, repoID string) *model.Repo {
	repo, err := deps.Store.GetRepo(r.Context(), repoID)
	if err != nil || repo == nil {
		return nil
	}
	if repo.AllowGuest {
		return repo
	}
	// Try bearer token for authenticated readers of private repos.
	tokenStr := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tokenStr == "" {
		return nil
	}
	claims, err := auth.VerifyAccessToken(deps.Config.SecretKey, tokenStr)
	if err != nil {
		return nil
	}
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "reader"); err != nil {
		return nil
	}
	return repo
}

func handlePubListNotes(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := chi.URLParam(r, "repoId")
		repo := pubRepoAccess(r, deps, repoID)
		if repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}
		notes, err := deps.Store.ListNotes(r.Context(), repoID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list notes failed")
			return
		}

		type noteItem struct {
			ID       string   `json:"id"`
			Path     string   `json:"path"`
			Title    string   `json:"title"`
			Tags     []string `json:"tags"`
			SyncedAt string   `json:"synced_at"`
		}

		items := make([]noteItem, 0, len(notes))
		for _, n := range notes {
			snap, _ := deps.Store.GetSnapshot(r.Context(), n.ID)
			syncedAt := ""
			var tags []string
			if snap != nil {
				syncedAt = snap.SyncedAt.UTC().Format("2006-01-02T15:04:05Z")
				var meta struct {
					Tags []string `json:"tags"`
				}
				_ = json.Unmarshal([]byte(snap.MetadataJSON), &meta)
				if meta.Tags != nil {
					tags = meta.Tags
				}
			}
			if tags == nil {
				tags = []string{}
			}
			items = append(items, noteItem{
				ID:       n.ID,
				Path:     n.Path,
				Title:    noteTitle(n.Path, snap),
				Tags:     tags,
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

		if pubRepoAccess(r, deps, repoID) == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}

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

		// Extract render key: new format embeds it in pubobs-url after last '&';
		// fall back to legacy pubobs-render-key field for notes not yet re-synced.
		var renderKey string
		if pubobsURL, _ := meta.Frontmatter["pubobs-url"].(string); pubobsURL != "" {
			if i := strings.LastIndex(pubobsURL, "&"); i != -1 {
				renderKey = pubobsURL[i+1:]
			}
		}
		if renderKey == "" {
			renderKey, _ = meta.Frontmatter["pubobs-render-key"].(string)
		}

		resp := map[string]any{
			"id":             note.ID,
			"path":           note.Path,
			"title":          noteTitle(notePath, snap),
			"tags":           meta.Tags,
			"frontmatter":    meta.Frontmatter,
			"git_commit_sha": snap.GitCommitSHA,
			"synced_at":      snap.SyncedAt.UTC().Format("2006-01-02T15:04:05Z"),
			"backlinks":      bl,
		}

		if renderKey != "" {
			resp["render_url"] = "/pub/" + repoID + "/render/" + notePath
			resp["render_key"] = renderKey
		} else {
			// Legacy fallback for notes not yet re-synced
			htmlContent, _ := deps.Cache.ReadRenderedHTML(repoID, notePath)
			if htmlContent == "" {
				htmlContent = snap.HTMLContent
			}
			htmlContent = rewriteRepoID(htmlContent, repoID)
			resp["html_content"] = htmlContent
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

func handlePubComments(w http.ResponseWriter, r *http.Request, deps *Deps, repoID, notePath string) {
	raw, err := deps.Cache.ReadRawFile(repoID, gitcache.CommentsFilePath(notePath))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read comments failed")
		return
	}

	var currentSHA string
	if note, _ := deps.Store.GetNote(r.Context(), repoID, notePath); note != nil {
		if snap, _ := deps.Store.GetSnapshot(r.Context(), note.ID); snap != nil {
			currentSHA = snap.GitCommitSHA
		}
	}

	type item struct {
		AuthorName  string `json:"author_name"`
		AuthorEmail string `json:"author_email"`
		CreatedAt   string `json:"created_at"`
		Body        string `json:"body"`
		IsOutdated  bool   `json:"is_outdated"`
	}
	parsed := gitcache.ParseComments(raw)
	out := make([]item, 0, len(parsed))
	for _, c := range parsed {
		isOutdated := c.NoteCommitSHA != "" && currentSHA != "" && c.NoteCommitSHA != currentSHA
		out = append(out, item{
			AuthorName:  c.AuthorName,
			AuthorEmail: c.AuthorEmail,
			CreatedAt:   c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			Body:        c.Body,
			IsOutdated:  isOutdated,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func handlePubGetRender(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := chi.URLParam(r, "repoId")
		notePath := chi.URLParam(r, "*")

		if pubRepoAccess(r, deps, repoID) == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}

		data, err := deps.RenderStore.Read(repoID, notePath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "render read failed")
			return
		}
		if data == nil {
			writeError(w, http.StatusNotFound, "render not found")
			return
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write(data)
	}
}

func handlePubGetAsset(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := chi.URLParam(r, "repoId")
		assetPath := chi.URLParam(r, "*")

		if pubRepoAccess(r, deps, repoID) == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}

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
