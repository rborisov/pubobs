package api

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

// callerRepoRole returns the caller's real repo role ("reader" | "commentator"
// | "editor" | "admin"), independent of whether the request's access to the
// current resource was actually granted via that role. It returns "" for any
// caller without a real repo membership: no bearer token at all, an invalid
// one, or a valid one with no access on this repo. That "" is deliberately
// used as the "guest" sentinel throughout the pub handlers below — it covers
// both true anonymous guest-open visitors AND share-link-only visitors (who
// may hold no token, or an unrelated valid token that just doesn't grant them
// a role here), so the frontend can use a single check ("role is editor or
// admin") to decide whether to show sharing controls, without ever mistaking
// a share-link visit for real repo membership.
func callerRepoRole(r *http.Request, deps *Deps, repoID string) string {
	tokenStr := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tokenStr == "" {
		return ""
	}
	claims, err := auth.VerifyAccessToken(deps.Config.SecretKey, tokenStr)
	if err != nil {
		return ""
	}
	if claims.IsAdmin {
		return "admin"
	}
	role, err := deps.Store.GetUserRole(r.Context(), claims.UserID, repoID)
	if err != nil {
		return ""
	}
	return role
}

// pubNoteAccess returns true if the request may access this specific note's
// content: either because pubRepoAccess already grants repo-level access
// (guest-open repo, or a valid bearer token with reader+ role — unchanged),
// or because the note itself is shared_publicly and the request supplies a
// ?key= query parameter matching note.EncryptionKey. The key comparison
// uses crypto/subtle.ConstantTimeCompare rather than == since this is a
// security-sensitive credential check.
//
// A repo-level grant always supersedes; a note's key only ever matters when
// the repo is guest-closed AND that specific note is shared_publicly — see
// the access-control table in the design doc for the full matrix.
func pubNoteAccess(r *http.Request, deps *Deps, repo *model.Repo, note *model.Note) bool {
	if pubRepoAccess(r, deps, repo.ID) != nil {
		return true
	}
	if !note.SharedPublicly {
		return false
	}
	providedKey := r.URL.Query().Get("key")
	if providedKey == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(providedKey), []byte(note.EncryptionKey)) == 1
}

func handlePubListNotes(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := chi.URLParam(r, "repoId")
		// INVARIANT: this endpoint must ONLY ever use repo-level
		// pubRepoAccess — NEVER a per-note share key, even if one happens to
		// be supplied as a ?key= query param. A share-link visitor for one
		// specific note must never be able to enumerate the rest of the
		// repo's notes via this endpoint. Do not change this to accept a
		// note-scoped key without re-reading the access-control design.
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
		notes, err = reconcileNotesWithGit(r.Context(), deps, repo, notes)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list notes failed")
			return
		}

		type noteItem struct {
			ID             string   `json:"id"`
			Path           string   `json:"path"`
			Title          string   `json:"title"`
			Tags           []string `json:"tags"`
			SyncedAt       string   `json:"synced_at"`
			SharedPublicly bool     `json:"shared_publicly"`
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
				ID:             n.ID,
				Path:           n.Path,
				Title:          noteTitle(n.Path, snap),
				Tags:           tags,
				SyncedAt:       syncedAt,
				SharedPublicly: n.SharedPublicly,
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"repo":  map[string]string{"id": repo.ID, "name": repo.Name},
			"notes": items,
			// "" (guest) whenever the caller isn't a real repo member — see
			// callerRepoRole. This endpoint is only ever reached via
			// pubRepoAccess (repo-level access), never a note-scoped share
			// key, so "guest" here always means "anonymous on a guest-open
			// repo", never "share-link-only visitor" (see the invariant note
			// above on repoID/repo access).
			"role": callerRepoRole(r, deps, repoID),
		})
	}
}

func handlePubGetNote(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := chi.URLParam(r, "repoId")
		notePath := chi.URLParam(r, "*")

		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}

		if strings.HasSuffix(notePath, "/comments") {
			// Comments stay gated on plain repo-level access, unchanged — a
			// share-link-only visitor never sees comments/backlinks, per
			// project decision. Do NOT switch this to pubNoteAccess.
			if pubRepoAccess(r, deps, repoID) == nil {
				writeError(w, http.StatusNotFound, "repo not found")
				return
			}
			notePath = strings.TrimSuffix(notePath, "/comments")
			handlePubComments(w, r, deps, repoID, notePath)
			return
		}

		note, err := deps.Store.GetNote(r.Context(), repoID, notePath)
		if err != nil || note == nil {
			writeError(w, http.StatusNotFound, "note not found")
			return
		}
		if !pubNoteAccess(r, deps, repo, note) {
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

		// hasRender reflects whether this note has an encrypted render blob.
		// A share-link-only visitor (no real repo role, just a matching
		// note key) must fetch that blob separately from /render and
		// decrypt it client-side with their own ?key= — the server never
		// decrypts on their behalf.
		//
		// A caller with real repo-level access (pubRepoAccess grants it —
		// guest-open repo, or an authenticated session with a role here)
		// already has full standing rights to this note regardless of any
		// share/key state, exactly as before this feature existed. For
		// them, the server decrypts the render blob right here and inlines
		// the plaintext below, just like it does for a legacy pre-encryption
		// note — this is the ONLY content path for a logged-in repo member
		// who opens a bare /read/:repoId/:path URL with no ?key=, so it must
		// never be skipped for them.
		hasRender := false
		decryptedHTML, hasDecrypted := "", false
		if rstore, rerr := deps.Resolver.RenderStoreFor(r.Context(), repoID); rerr == nil {
			if data, _ := rstore.Read(repoID, notePath); data != nil {
				hasRender = true
				if note.EncryptionKey != "" && pubRepoAccess(r, deps, repoID) != nil {
					if plaintext, derr := decryptNoteRenderHTML(note.EncryptionKey, data); derr == nil {
						decryptedHTML, hasDecrypted = plaintext, true
					} else {
						fmt.Printf("decrypt render blob %s/%s: %v\n", repoID, notePath, derr)
					}
				}
			}
		}

		resp := map[string]any{
			"id":              note.ID,
			"path":            note.Path,
			"title":           noteTitle(notePath, snap),
			"tags":            meta.Tags,
			"frontmatter":     meta.Frontmatter,
			"git_commit_sha":  snap.GitCommitSHA,
			"synced_at":       snap.SyncedAt.UTC().Format("2006-01-02T15:04:05Z"),
			"backlinks":       bl,
			"shared_publicly": note.SharedPublicly,
			// "" (guest) here covers BOTH an anonymous guest-open visitor AND
			// a share-link-only visitor with no real repo role — either way,
			// the frontend must not show editor-only sharing controls. See
			// callerRepoRole's doc comment for the full rationale.
			"role": callerRepoRole(r, deps, repoID),
		}

		switch {
		case hasDecrypted:
			resp["html_content"] = rewriteRepoID(decryptedHTML, repoID)
		case !hasRender:
			// Legacy fallback: notes not yet synced with encryption.
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

		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "render not found")
			return
		}
		note, err := deps.Store.GetNote(r.Context(), repoID, notePath)
		if err != nil || note == nil {
			writeError(w, http.StatusNotFound, "render not found")
			return
		}

		if !pubNoteAccess(r, deps, repo, note) {
			// A key was supplied and checked against a note that IS shared,
			// but didn't match: 403 (a real credential was rejected). Every
			// other denial (not shared at all, or no key/token at all) is a
			// plain 404 instead, so as not to leak whether the note is
			// currently being shared.
			if note.SharedPublicly && r.URL.Query().Get("key") != "" {
				writeError(w, http.StatusForbidden, "invalid key")
			} else {
				writeError(w, http.StatusNotFound, "render not found")
			}
			return
		}

		rstore, rerr := deps.Resolver.RenderStoreFor(r.Context(), repoID)
		if rerr != nil {
			writeError(w, http.StatusInternalServerError, "resolve render store failed")
			return
		}
		data, err := rstore.Read(repoID, notePath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "render read failed")
			return
		}
		if data == nil {
			writeError(w, http.StatusNotFound, "render not found")
			return
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
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

		astore, aerr := deps.Resolver.AssetStoreFor(r.Context(), repoID)
		if aerr != nil {
			writeError(w, http.StatusInternalServerError, "resolve asset store failed")
			return
		}

		data, err := astore.Read(repoID, assetPath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "asset read failed")
			return
		}
		if data == nil {
			// Not in the asset store yet — likely synced before this feature
			// shipped. Fall back to the git checkout and backfill so the next
			// request hits the fast path.
			data, err = deps.Cache.ReadAsset(repoID, assetPath)
			if err != nil {
				writeError(w, http.StatusNotFound, "asset not found")
				return
			}
			if werr := astore.Write(repoID, assetPath, data); werr != nil {
				fmt.Printf("assetstore backfill %s/%s: %v\n", repoID, assetPath, werr)
			}
		}

		ct := mime.TypeByExtension(filepath.Ext(assetPath))
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	}
}

// decryptNoteRenderHTML decrypts a note's stored render blob using its
// backend-owned encryption key, for inlining into handlePubGetNote's
// response to a caller with real repo-level access. keyB64 is the same
// base64url-encoded 32-byte AES key format used throughout this feature
// (see store.GenerateNoteKey); ciphertext uses the nonce-prepended AES-256-GCM
// layout shared with reencryptRenderBlob in share.go.
func decryptNoteRenderHTML(keyB64 string, ciphertext []byte) (string, error) {
	key, err := base64.RawURLEncoding.DecodeString(keyB64)
	if err != nil {
		return "", fmt.Errorf("decode key: %w", err)
	}
	plaintext, err := aesGCMDecrypt(key, ciphertext)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
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
