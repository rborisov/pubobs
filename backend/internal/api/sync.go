package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
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

		// Validate every client-supplied path before anything is written.
		// One rejection fails the whole request: a sync is a single commit,
		// and silently dropping one bad path would produce a commit the
		// client believes is complete but isn't.
		for _, f := range payload.Files {
			if !validRepoPath(f.Path) {
				writeError(w, http.StatusBadRequest, "invalid path: "+f.Path)
				return
			}
		}
		for _, a := range payload.Assets {
			if !validRepoPath(a.Path) {
				writeError(w, http.StatusBadRequest, "invalid path: "+a.Path)
				return
			}
		}
		for _, p := range payload.DeletedPaths {
			if !validRepoPath(p) {
				writeError(w, http.StatusBadRequest, "invalid path: "+p)
				return
			}
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

		user, err := deps.Store.GetUserByID(r.Context(), claims.UserID)
		if err != nil || user == nil {
			writeError(w, http.StatusInternalServerError, "user lookup failed")
			return
		}
		commitMsg := fmt.Sprintf("pubobs: sync %s by %s", time.Now().UTC().Format(time.RFC3339), user.Email)

		// Owner service credential (clone/reads + fallback push).
		ownerCred := credJSON

		// Push credential = the syncing user's own (user, repo) credential.
		pushCred := ""
		encUserCred, ok, uerr := deps.Store.GetUserCredentialSecret(r.Context(), repoID, claims.UserID)
		if uerr != nil {
			writeError(w, http.StatusInternalServerError, "credential lookup failed")
			return
		}
		if ok {
			if pushCred, err = auth.DecryptCreds(deps.Config.SecretKey, encUserCred); err != nil {
				writeError(w, http.StatusInternalServerError, "credential decrypt failed")
				return
			}
		} else if repo.StrictCredentials {
			writeError(w, http.StatusForbidden, "configure your git credential for this repo before publishing")
			return
		}
		// Legacy mode (not strict) with no user cred: pushCred stays "" → Cache.Sync
		// falls back to the owner cred.

		authorName := user.Name
		authorEmail := user.Email
		// Prefer the syncing user's stored (repo, user) git credential identity
		// (git_name/git_email, set via PUT .../git-credential) over their plain
		// account name/email when present — lets a user commit under a distinct
		// git identity (e.g. matching their GitHub account) without changing
		// their pubobs account name/email. Falls back to the account identity
		// per-field: a credential with only one of git_name/git_email set keeps
		// the account value for the other.
		if gn, ge, ok, _ := deps.Store.GetUserCredentialGitIdentity(r.Context(), repoID, claims.UserID); ok {
			if gn != "" {
				authorName = gn
			}
			if ge != "" {
				authorEmail = ge
			}
		}

		sha, err := deps.Cache.Sync(r.Context(), repo, ownerCred, pushCred, cacheFiles, cacheAssets, payload.DeletedPaths, commitMsg, authorName, authorEmail)
		if err != nil {
			writeSyncError(w, repo.DefaultBranch, err)
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
			// Comment files and _pubobs/ metadata are companion data, not
			// notes — never create note rows for them (they'd otherwise leak
			// into the notes list). Same rule as the import path.
			if isNonNotePath(f.Path) {
				continue
			}
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

// writeSyncError maps a failed Cache.Sync onto a status and a message the
// person syncing can act on.
//
// The previous implementation matched the substring "rejected" anywhere in
// git's output and reported every hit as `push rejected: pull first, then
// sync`. That single line collapsed three unrelated failures — a token
// without write access, a protected/hook-guarded branch, and a genuine
// non-fast-forward — into the one diagnosis that is almost never the real
// cause here: Sync fetches and hard-resets to the remote tip immediately
// before it commits (getOrClone → FetchReset), so the local clone cannot be
// *persistently* behind the remote. It also advised a remedy that does not
// exist in this product — there is no user-facing "pull" of git history to
// perform — which is what made a brand-new repo whose credential simply
// could not write look like a merge conflict.
//
// Reasons are taken from gitcache.PushRejectionReason, which is scrubbed of
// the credentials embedded in the remote URL; raw err.Error() is never sent
// to the client for the same reason.
func writeSyncError(w http.ResponseWriter, branch string, err error) {
	if branch == "" {
		branch = "the default branch"
	}
	reason := gitcache.PushRejectionReason(err)

	switch {
	case errors.Is(err, gitcache.ErrGitNonFastForward):
		msg := fmt.Sprintf("the remote %s moved while this sync was in flight — sync again", branch)
		if reason != "" {
			msg += " (" + reason + ")"
		}
		writeError(w, http.StatusConflict, msg)

	case errors.Is(err, gitcache.ErrGitPushRejected):
		msg := fmt.Sprintf("the git remote refused the push to %s — check that the git credential used for this repo has write access to it", branch)
		if reason != "" {
			msg += ". Remote said: " + reason
		}
		writeError(w, http.StatusForbidden, msg)

	case errors.Is(err, gitcache.ErrGitAuthFailed):
		writeError(w, http.StatusForbidden, "the git remote rejected the credential for this repo — check the stored git credential")

	case errors.Is(err, gitcache.ErrGitOpTimedOut):
		writeError(w, http.StatusGatewayTimeout, "the git remote did not respond in time — try again")

	default:
		writeError(w, http.StatusBadGateway, "sync failed: "+gitcache.Redact(err.Error()))
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
