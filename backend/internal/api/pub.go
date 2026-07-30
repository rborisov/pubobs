package api

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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
			CommentCount   int      `json:"comment_count"`
		}

		commentCounts, _ := deps.Cache.CommentCounts(repoID)

		items := make([]noteItem, 0, len(notes))
		for _, n := range notes {
			// Defensively hide companion files (comment files, _pubobs/) that
			// leaked into the DB via an ingestion path that predates the
			// isNonNotePath filter — so stale rows disappear from the list
			// without needing a re-sync or DB cleanup.
			if isNonNotePath(n.Path) {
				continue
			}
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
				CommentCount:   commentCounts[n.Path],
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
			// Comment read now follows note-read access (pubNoteAccess): repo
			// members, guest-open visitors, AND share-link visitors with a
			// valid ?key= can read comments — see the open-comments design.
			notePath = strings.TrimSuffix(notePath, "/comments")
			note, _ := deps.Store.GetNote(r.Context(), repoID, notePath)
			if note == nil || !pubNoteAccess(r, deps, repo, note) {
				writeError(w, http.StatusNotFound, "not found")
				return
			}
			handlePubComments(w, r, deps, repoID, notePath)
			return
		}

		note, err := deps.Store.GetNote(r.Context(), repoID, notePath)
		if note == nil && err == nil {
			if healed, _ := ensureNoteFromGit(r.Context(), deps, repo, notePath, syncedByFromRequest(r, deps)); healed != nil {
				note = healed
			}
		}
		if err != nil || note == nil {
			writeError(w, http.StatusNotFound, "note not found")
			return
		}
		if !pubNoteAccess(r, deps, repo, note) {
			writeError(w, http.StatusNotFound, "note not found")
			return
		}
		snap, snapErr := deps.Store.GetSnapshot(r.Context(), note.ID)
		if snap == nil {
			if healed, _ := ensureNoteFromGit(r.Context(), deps, repo, notePath, syncedByFromRequest(r, deps)); healed != nil {
				note = healed
				snap, snapErr = deps.Store.GetSnapshot(r.Context(), note.ID)
			}
		}
		if snapErr != nil || snap == nil {
			writeError(w, http.StatusNotFound, "snapshot not found")
			return
		}

		var meta struct {
			Tags        []string       `json:"tags"`
			Frontmatter map[string]any `json:"frontmatter"`
		}
		_ = json.Unmarshal([]byte(snap.MetadataJSON), &meta)

		hasRepoAccess := pubRepoAccess(r, deps, repoID) != nil

		type backlinkItem struct {
			Path  string `json:"path"`
			Title string `json:"title"`
		}
		// Backlinks are repo-scoped metadata (paths + titles of OTHER notes)
		// and must never reach a share-link-only visitor, who is authorized
		// for just this one shared note via its ?key=. Unlike per-note
		// comments (gated by pubNoteAccess, which a share-link visitor
		// satisfies), backlinks are deliberately held to the stricter
		// repo-level gate below because they expose OTHER notes' paths and
		// would let such a visitor enumerate a guest-closed repo. Guest-open
		// repos and real repo members both have hasRepoAccess, so they keep
		// backlinks unchanged.
		bl := make([]backlinkItem, 0)
		if hasRepoAccess {
			backlinks, _ := deps.Store.GetBacklinks(r.Context(), repoID, notePath)
			for _, b := range backlinks {
				bsnap, _ := deps.Store.GetSnapshot(r.Context(), b.ID)
				bl = append(bl, backlinkItem{Path: b.Path, Title: noteTitle(b.Path, bsnap)})
			}
		}

		render := resolveNoteHTML(r.Context(), deps, repo, notePath, note, snap, hasRepoAccess)

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
			// Whether the caller can browse the whole repo (guest-open repo or a
			// real member) vs. only this one shared note via ?key=. The reader
			// uses it to decide whether "Back" goes to the repo note list or the
			// login screen. Distinct from role, which is "" for guest-open too.
			"has_repo_access": hasRepoAccess,
		}

		// Every asset URL the reader will hit needs a signature, since the
		// browser can't put a bearer token on an <img src>. Minted here, after
		// pubNoteAccess has already authorized this caller for this note.
		assetExp := time.Now().Add(assetSigTTL).Unix()
		secret := deps.Config.SecretKey

		if render.HTMLContent != "" {
			resp["html_content"] = signAssetURLsInHTML(secret, repoID, rewriteRepoID(render.HTMLContent, repoID), assetExp)
		} else if !hasRepoAccess {
			// Share-link-only visitor: their note HTML stays encrypted until the
			// browser decrypts it, so the signatures its <img> tags need can't be
			// baked in above. Decrypt the blob here purely to enumerate which
			// asset paths this one note references, and return a signature for
			// each. No new trust boundary: the backend already owns this note's
			// key and already decrypts the same blob for repo members.
			if sigs := shareVisitorAssetSigs(r.Context(), deps, repoID, notePath, note, assetExp); len(sigs) > 0 {
				resp["asset_sigs"] = sigs
			}
		}
		if render.Pending {
			resp["render_pending"] = true
		}

		// The repo's Obsidian theme is an asset too, and is referenced by the
		// reader rather than by the note HTML — so it gets its own signed URL,
		// or a shared note on a private repo would render unstyled.
		resp["theme_css_url"] = "/pub/" + repoID + "/assets/" + themeCSSPath +
			"?" + assetSigQuery(secret, repoID, themeCSSPath, assetExp)

		writeJSON(w, http.StatusOK, resp)
	}
}

// shareVisitorAssetSigs decrypts a shared note's render blob server-side for the
// sole purpose of listing the asset URLs that note embeds, returning a signed
// URL for each (see signedAssetURLs). Only called for share-link-only visitors,
// who never receive the decrypted HTML itself.
//
// Returns nil whenever the blob is missing or undecryptable: a note with no
// usable signatures still reads fine apart from its images, which is not worth
// failing the whole note response over.
func shareVisitorAssetSigs(ctx context.Context, deps *Deps, repoID, notePath string, note *model.Note, exp int64) map[string]string {
	if note.EncryptionKey == "" {
		return nil
	}
	rstore, err := deps.Resolver.RenderStoreFor(ctx, repoID)
	if err != nil {
		return nil
	}
	data, err := rstore.Read(repoID, notePath)
	if err != nil || data == nil {
		return nil
	}
	html, err := decryptNoteRenderHTML(note.EncryptionKey, data)
	if err != nil {
		return nil
	}
	return signedAssetURLs(deps.Config.SecretKey, repoID, html, exp)
}

// noteHTMLResult is the resolved HTML for handlePubGetNote, plus a flag when
// a repo member has access but no render is available yet (blob missing or
// deleted during share/unshare heal — needs an Obsidian re-sync).
type noteHTMLResult struct {
	HTMLContent string
	Pending     bool
}

// syncedByFromRequest returns the authenticated user's ID when the request
// carries a valid bearer token, for git-heal snapshot writes.
func syncedByFromRequest(r *http.Request, deps *Deps) string {
	tokenStr := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tokenStr == "" {
		return ""
	}
	claims, err := auth.VerifyAccessToken(deps.Config.SecretKey, tokenStr)
	if err != nil {
		return ""
	}
	return claims.UserID
}

// resolveNoteHTML picks the best available HTML for handlePubGetNote.
//
// Share-link-only visitors never get server-side decryption here — they use
// /render + client-side ?key= decryption. Repo members (hasRepoAccess) always
// get server-side decrypt when a matching blob exists; when the blob is
// missing or stale (key mismatch after share/unshare), we heal it away and
// fall back to legacy snapshot HTML, then git markdown rendered server-side,
// or signal render_pending only when the source file is not in git.
func resolveNoteHTML(ctx context.Context, deps *Deps, repo *model.Repo, notePath string, note *model.Note, snap *model.NoteSnapshot, hasRepoAccess bool) noteHTMLResult {
	repoID := repo.ID
	rstore, rerr := deps.Resolver.RenderStoreFor(ctx, repoID)
	if rerr == nil {
		if data, _ := rstore.Read(repoID, notePath); data != nil {
			if note.EncryptionKey != "" && hasRepoAccess {
				keyBytes, keyErr := base64.RawURLEncoding.DecodeString(note.EncryptionKey)
				if keyErr == nil && len(keyBytes) == 32 {
					plaintext, decryptErr := decryptNoteRenderHTML(note.EncryptionKey, data)
					if decryptErr == nil {
						return noteHTMLResult{HTMLContent: plaintext}
					}
					// Valid key but blob mismatch — heal away undecryptable bytes.
					fmt.Printf("decrypt render blob %s/%s: %v\n", repoID, notePath, decryptErr)
					if delErr := rstore.Delete(repoID, notePath); delErr != nil {
						fmt.Printf("heal stale render blob delete %s/%s: %v\n", repoID, notePath, delErr)
					}
				}
			} else if !hasRepoAccess {
				// Share-link visitor: no server-side HTML; client decrypts.
				return noteHTMLResult{}
			}
		}
	}

	// Legacy fallback: pre-encryption notes, or notes whose render blob was
	// deleted during share/unshare heal and not yet re-synced.
	htmlContent := ""
	if deps.Cache != nil {
		htmlContent, _ = deps.Cache.ReadRenderedHTML(repoID, notePath)
	}
	if htmlContent == "" {
		htmlContent = snap.HTMLContent
	}
	if htmlContent != "" {
		return noteHTMLResult{HTMLContent: htmlContent}
	}
	if hasRepoAccess {
		if md, err := readNoteMarkdownFromGit(ctx, deps, repo, notePath); err == nil && md != "" {
			return noteHTMLResult{HTMLContent: renderMarkdown(md)}
		}
		return noteHTMLResult{Pending: true}
	}
	return noteHTMLResult{}
}

// emailLocalPart returns the portion of an email address before "@", so a
// non-member viewer never sees a member's full address/domain. Empty or
// non-email values pass through unchanged.
func emailLocalPart(email string) string {
	if i := strings.Index(email, "@"); i >= 0 {
		return email[:i]
	}
	return email
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
	// Non-members (anonymous guest-open or share-link visitors — no repo role)
	// must not see members' full email addresses. Expose only the local part
	// (before "@") to them; real members still get the full address.
	isMember := callerRepoRole(r, deps, repoID) != ""
	parsed := gitcache.ParseComments(raw)
	out := make([]item, 0, len(parsed))
	for _, c := range parsed {
		isOutdated := c.NoteCommitSHA != "" && currentSHA != "" && c.NoteCommitSHA != currentSHA
		email := c.AuthorEmail
		if !isMember {
			email = emailLocalPart(email)
		}
		out = append(out, item{
			AuthorName:  c.AuthorName,
			AuthorEmail: email,
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

		// A browser cannot put an Authorization header on an <img src>, so
		// repo access alone would make every image in every note on a
		// guest-closed repo unreachable. A signature minted by the note-read
		// handler for this exact repo+path stands in for it — see assetsig.go.
		q := r.URL.Query()
		if pubRepoAccess(r, deps, repoID) == nil &&
			!validAssetSig(deps.Config.SecretKey, repoID, assetPath, q.Get("exp"), q.Get("sig")) {
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

// resolveCommenter decides how to attribute a comment. A caller holding a valid
// bearer token comments under their account identity (client-supplied name is
// ignored — no spoofing). Everyone else is anonymous: the sanitized client name,
// or "anonym" when blank.
func resolveCommenter(r *http.Request, deps *Deps, clientName string) (name, email string) {
	tokenStr := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tokenStr != "" {
		if claims, err := auth.VerifyAccessToken(deps.Config.SecretKey, tokenStr); err == nil {
			if user, uerr := deps.Store.GetUserByID(r.Context(), claims.UserID); uerr == nil && user != nil {
				return user.Name, user.Email
			}
		}
	}
	name = gitcache.SanitizeCommentName(clientName)
	if name == "" {
		name = "anonym"
	}
	return name, ""
}

func handlePubPostComment(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := chi.URLParam(r, "repoId")
		notePath := chi.URLParam(r, "*")
		if !strings.HasSuffix(notePath, "/comments") {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		notePath = strings.TrimSuffix(notePath, "/comments")

		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}
		note, _ := deps.Store.GetNote(r.Context(), repoID, notePath)
		if note == nil || !pubNoteAccess(r, deps, repo, note) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}

		var body struct {
			Body          string `json:"body"`
			NoteCommitSHA string `json:"note_commit_sha"`
			AuthorName    string `json:"author_name"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Body) == "" {
			writeError(w, http.StatusBadRequest, "body required")
			return
		}

		name, email := resolveCommenter(r, deps, body.AuthorName)

		credJSON, err := decryptCreds(deps, repo.EncryptedCreds)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cred decrypt failed")
			return
		}
		if err := deps.Cache.AppendComment(r.Context(), repo, credJSON, credJSON, notePath, name, email, body.Body, body.NoteCommitSHA); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save comment")
			return
		}
		w.WriteHeader(http.StatusCreated)
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
