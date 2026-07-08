package api

import (
	"encoding/base64"
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

type syncAssetPayload struct {
	Path    string `json:"path"`
	Content string `json:"content"` // base64-encoded binary
}

type syncFilePayload struct {
	Path          string         `json:"path"`
	MDContent     string         `json:"md_content"`
	EncryptedHTML string         `json:"encrypted_html"`
	Frontmatter   map[string]any `json:"frontmatter"`
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

		// Check disk space live rather than trusting the periodically-cached
		// health row (refreshed at most once per PUBOBS_CACHE_CHECK_INTERVAL,
		// default 1h): a stale "crit" reading would otherwise keep rejecting
		// syncs for up to an hour after an operator has already freed up
		// space. A single statfs call is cheap enough to do on every sync, so
		// there's no need to trade correctness for cost here. The cached
		// value is still fine for the admin dashboard's informational health
		// display, which isn't gating any action.
		if deps.Cache != nil {
			if status, _, _, err := deps.Cache.DiskStatus(deps.Config.DiskWarnPct, deps.Config.DiskCritPct); err == nil && status == "crit" {
				writeError(w, http.StatusInsufficientStorage, "disk critically low — sync rejected")
				return
			}
		}

		var payload struct {
			Files        []syncFilePayload  `json:"files"`
			Assets       []syncAssetPayload `json:"assets"`
			DeletedPaths []string           `json:"deleted_paths"`
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
				Path:      f.Path,
				MDContent: f.MDContent,
			}
		}

		cacheAssets := make([]gitcache.SyncAsset, 0, len(payload.Assets))
		for _, a := range payload.Assets {
			data, err := base64.StdEncoding.DecodeString(a.Content)
			if err != nil {
				continue // skip malformed assets
			}
			cacheAssets = append(cacheAssets, gitcache.SyncAsset{Path: a.Path, Content: data})
		}

		user, _ := deps.Store.GetUserByID(r.Context(), claims.UserID)
		commitMsg := fmt.Sprintf("pubobs: sync %s by %s", time.Now().UTC().Format(time.RFC3339), user.Email)

		sha, err := deps.Cache.Sync(r.Context(), repo, credJSON, cacheFiles, cacheAssets, commitMsg)
		if err != nil {
			if strings.Contains(err.Error(), "non-fast-forward") || strings.Contains(err.Error(), "rejected") {
				writeError(w, http.StatusConflict, "push rejected: pull first, then sync")
				return
			}
			writeError(w, http.StatusBadGateway, "sync failed: "+err.Error())
			return
		}

		// noteKeys carries each synced note's backend-authoritative encryption
		// key back to the plugin, keyed by the same vault-relative path the
		// plugin sent — so it always encrypts (and links to) notes using the
		// key the backend actually has on file, never one it invented
		// locally. GetOrCreateNoteKey is idempotent, so repeated syncs of the
		// same note always yield the same key here.
		noteKeys := make(map[string]string, len(payload.Files))
		for _, f := range payload.Files {
			note, err := deps.Store.UpsertNote(r.Context(), repoID, f.Path)
			if err != nil {
				continue
			}
			if note == nil {
				continue
			}
			meta := extractMetadata(f.MDContent, f.Frontmatter)
			metaJSON, _ := json.Marshal(meta)
			deps.Store.UpsertSnapshot(r.Context(), note.ID, "", string(metaJSON), claims.UserID, sha)
			deps.Store.UpsertNoteLinks(r.Context(), note.ID, meta.Links)
			if encBytes, err := base64.StdEncoding.DecodeString(f.EncryptedHTML); err == nil && len(encBytes) > 0 {
				if rstore, rerr := deps.Resolver.RenderStoreFor(r.Context(), repoID); rerr == nil {
					if werr := rstore.Write(repoID, f.Path, encBytes); werr != nil {
						fmt.Printf("renderstore write %s/%s: %v\n", repoID, f.Path, werr)
					}
				} else {
					fmt.Printf("resolve render store %s: %v\n", repoID, rerr)
				}
			}
			if key, kerr := deps.Store.GetOrCreateNoteKey(r.Context(), note.ID); kerr == nil {
				noteKeys[f.Path] = key
			} else {
				fmt.Printf("get-or-create note key %s/%s: %v\n", repoID, f.Path, kerr)
			}
		}

		for _, a := range cacheAssets {
			if astore, aerr := deps.Resolver.AssetStoreFor(r.Context(), repoID); aerr == nil {
				if werr := astore.Write(repoID, a.Path, a.Content); werr != nil {
					fmt.Printf("assetstore write %s/%s: %v\n", repoID, a.Path, werr)
				}
			} else {
				fmt.Printf("resolve asset store %s: %v\n", repoID, aerr)
			}
		}

		for _, p := range payload.DeletedPaths {
			deps.Store.DeleteNote(r.Context(), repoID, p)
			if rstore, rerr := deps.Resolver.RenderStoreFor(r.Context(), repoID); rerr == nil {
				if derr := rstore.Delete(repoID, p); derr != nil {
					fmt.Printf("renderstore delete %s/%s: %v\n", repoID, p, derr)
				}
			} else {
				fmt.Printf("resolve render store %s: %v\n", repoID, rerr)
			}
		}
		deps.Store.TouchLastUsedAt(r.Context(), repoID)

		writeJSON(w, http.StatusOK, map[string]any{
			"commit_sha": sha,
			"note_keys":  noteKeys,
		})
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
