package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/model"
)

type syncFilePayload struct {
	Path        string         `json:"path"`
	MDContent   string         `json:"md_content"`
	HTMLContent string         `json:"html_content"`
	Frontmatter map[string]any `json:"frontmatter"`
}

func handleSync(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")

		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}
		role, err := deps.Store.GetUserRole(r.Context(), claims.UserID, repoID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "role check failed")
			return
		}
		if !claims.IsAdmin && !model.RoleAtLeast(role, "editor") {
			writeError(w, http.StatusForbidden, "editor role required")
			return
		}

		if h, err := deps.Store.GetHealth(r.Context()); err == nil && h.DiskStatus == "crit" {
			writeError(w, http.StatusInsufficientStorage, "disk critically low — sync rejected")
			return
		}

		var payload struct {
			Files []syncFilePayload `json:"files"`
		}
		if err := readJSON(r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		credJSON, err := decryptCreds(deps, repo.EncryptedCreds)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cred decrypt failed")
			return
		}

		cacheFiles := make([]gitcache.SyncFile, len(payload.Files))
		for i, f := range payload.Files {
			cacheFiles[i] = gitcache.SyncFile{
				Path:        f.Path,
				MDContent:   f.MDContent,
				HTMLContent: f.HTMLContent,
			}
		}

		user, _ := deps.Store.GetUserByID(r.Context(), claims.UserID)
		commitMsg := fmt.Sprintf("pubobs: sync %s by %s", time.Now().UTC().Format(time.RFC3339), user.Email)

		sha, err := deps.Cache.Sync(r.Context(), repo, credJSON, cacheFiles, commitMsg)
		if err != nil {
			if strings.Contains(err.Error(), "non-fast-forward") || strings.Contains(err.Error(), "rejected") {
				writeError(w, http.StatusConflict, "push rejected: pull first, then sync")
				return
			}
			writeError(w, http.StatusBadGateway, "sync failed: "+err.Error())
			return
		}

		for _, f := range payload.Files {
			note, err := deps.Store.UpsertNote(r.Context(), repoID, f.Path)
			if err != nil {
				continue
			}
			meta := extractMetadata(f.MDContent, f.Frontmatter)
			metaJSON, _ := json.Marshal(meta)
			deps.Store.UpsertSnapshot(r.Context(), note.ID, f.HTMLContent, string(metaJSON), claims.UserID, sha)
			deps.Store.UpsertNoteLinks(r.Context(), note.ID, meta.Links)
		}
		deps.Store.TouchLastUsedAt(r.Context(), repoID)

		writeJSON(w, http.StatusOK, map[string]string{"commit_sha": sha})
	}
}

type noteMetadata struct {
	Headings    []string       `json:"headings"`
	Links       []string       `json:"links"`
	Tags        []string       `json:"tags"`
	Frontmatter map[string]any `json:"frontmatter"`
}

var (
	headingRE  = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	wikilinkRE = regexp.MustCompile(`\[\[([^\]|]+)`)
)

func extractMetadata(md string, frontmatter map[string]any) noteMetadata {
	var meta noteMetadata
	meta.Frontmatter = frontmatter
	for _, m := range headingRE.FindAllStringSubmatch(md, -1) {
		meta.Headings = append(meta.Headings, strings.TrimSpace(m[1]))
	}
	seen := map[string]bool{}
	for _, m := range wikilinkRE.FindAllStringSubmatch(md, -1) {
		link := strings.TrimSpace(m[1])
		if !seen[link] {
			meta.Links = append(meta.Links, link)
			seen[link] = true
		}
	}
	if tags, ok := frontmatter["tags"].([]any); ok {
		for _, t := range tags {
			if s, ok := t.(string); ok {
				meta.Tags = append(meta.Tags, s)
			}
		}
	}
	return meta
}

func decryptCreds(deps *Deps, encCreds string) (string, error) {
	if encCreds == "" {
		return "", nil
	}
	return auth.DecryptCreds(deps.Config.SecretKey, encCreds)
}
