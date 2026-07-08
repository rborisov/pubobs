// backend/internal/api/share.go
package api

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/store"
)

// serveShareNote handles POST /api/repos/{id}/notes/*/share, gated on
// editor-or-higher role for the repo. Two modes:
//
//   - "restricted": no DB mutation at all — just resolves the note (404 if
//     missing) and returns its key-less canonical path.
//   - "public": mints the note's key on first use (GetOrCreateNoteKey) and
//     marks it shared. Calling this again while already shared is
//     idempotent — it reuses the existing key rather than rotating it;
//     only /unshare rotates.
func serveShareNote(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	repo, err := deps.Store.GetRepo(r.Context(), repoID)
	if err != nil || repo == nil {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "editor"); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	note, err := deps.Store.GetNote(r.Context(), repoID, notePath)
	if err != nil || note == nil {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}

	var body struct {
		Mode string `json:"mode"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	switch body.Mode {
	case "restricted":
		// Deliberately does not touch shared_publicly/encryption_key.
		writeJSON(w, http.StatusOK, map[string]any{
			"shared": false,
			"path":   repoID + "/" + notePath,
		})
	case "public":
		key, err := deps.Store.GetOrCreateNoteKey(r.Context(), note.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "key generation failed")
			return
		}
		if err := deps.Store.SetNoteShared(r.Context(), note.ID, true, key); err != nil {
			writeError(w, http.StatusInternalServerError, "share failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"shared": true,
			"path":   repoID + "/" + notePath,
			"key":    key,
		})
	default:
		writeError(w, http.StatusBadRequest, `mode must be "restricted" or "public"`)
	}
}

// serveNoteKey handles POST /api/repos/{id}/notes/*/key, gated on
// editor-or-higher role for the repo. It exists purely so the Obsidian
// plugin can learn a brand-new note's encryption key BEFORE it can send that
// note's (already client-side-encrypted) content in the main sync request —
// a chicken-and-egg problem /share can't solve, since its "restricted" mode
// deliberately never returns a key, and its "public" mode would incorrectly
// flip shared_publicly on just to learn a key. This endpoint does neither:
// it only ever mints/returns the key, never touching shared_publicly.
//
// Unlike /share and /unshare, the note may not exist yet — this is called
// for a note's very first sync, before any note row exists for it — so this
// upserts the note (by path) rather than 404ing if it's missing.
func serveNoteKey(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	repo, err := deps.Store.GetRepo(r.Context(), repoID)
	if err != nil || repo == nil {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "editor"); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	note, err := deps.Store.UpsertNote(r.Context(), repoID, notePath)
	if err != nil || note == nil {
		writeError(w, http.StatusInternalServerError, "note upsert failed")
		return
	}

	key, err := deps.Store.GetOrCreateNoteKey(r.Context(), note.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "key generation failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"key": key})
}

// serveUnshareNote handles POST /api/repos/{id}/notes/*/unshare, gated on
// editor-or-higher role for the repo. Revocation is instant and
// unconditional (no grace period): it always mints a brand new key, and if
// the note has a stored render blob it re-encrypts that same plaintext
// under the new key before flipping shared_publicly off — so a previously
// issued link (correct old key or not) stops working the moment this
// returns, rather than just becoming un-listed while the old ciphertext
// remains decryptable with the old key.
//
// Design choice: this rotates the key even when the note is already
// unshared, rather than treating that as a pure no-op. That's more work
// than strictly necessary (nothing is currently using the old key), but it
// defends against any edge case where a key ended up persisted without
// shared_publicly also being set, at negligible cost — so we prefer it.
func serveUnshareNote(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	repo, err := deps.Store.GetRepo(r.Context(), repoID)
	if err != nil || repo == nil {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "editor"); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	note, err := deps.Store.GetNote(r.Context(), repoID, notePath)
	if err != nil || note == nil {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}

	newKey, err := store.GenerateNoteKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "key generation failed")
		return
	}

	// Only attempt to re-encrypt if the note ever actually had a key — an
	// empty encryption_key means nothing was ever encrypted under a key we
	// know, so there's nothing to decrypt (this also covers "note was never
	// synced/rendered" and "note was never shared" gracefully, without a
	// spurious decrypt failure on an empty key).
	if note.EncryptionKey != "" {
		if err := reencryptRenderBlob(r.Context(), deps, repoID, notePath, note.EncryptionKey, newKey); err != nil {
			writeError(w, http.StatusInternalServerError, "re-encrypt failed: "+err.Error())
			return
		}
	}

	if err := deps.Store.SetNoteShared(r.Context(), note.ID, false, newKey); err != nil {
		writeError(w, http.StatusInternalServerError, "unshare failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"shared": false})
}

// reencryptRenderBlob decrypts a note's stored render blob with oldKeyB64
// and re-encrypts the identical plaintext with newKeyB64, overwriting it at
// the same location. Both keys are base64url-encoded 32-byte AES keys (see
// store.GenerateNoteKey). A missing blob (never synced/rendered) is a no-op,
// not an error. Mirrors the exact resolve-store-then-read pattern
// handlePubGetRender uses in pub.go, plus a decrypt/re-encrypt/write step.
func reencryptRenderBlob(ctx context.Context, deps *Deps, repoID, notePath, oldKeyB64, newKeyB64 string) error {
	rstore, err := deps.Resolver.RenderStoreFor(ctx, repoID)
	if err != nil {
		return fmt.Errorf("resolve render store: %w", err)
	}
	ciphertext, err := rstore.Read(repoID, notePath)
	if err != nil {
		return fmt.Errorf("read render blob: %w", err)
	}
	if ciphertext == nil {
		return nil
	}

	oldKey, err := base64.RawURLEncoding.DecodeString(oldKeyB64)
	if err != nil {
		return fmt.Errorf("decode old key: %w", err)
	}
	plaintext, err := aesGCMDecrypt(oldKey, ciphertext)
	if err != nil {
		return fmt.Errorf("decrypt with old key: %w", err)
	}

	newKey, err := base64.RawURLEncoding.DecodeString(newKeyB64)
	if err != nil {
		return fmt.Errorf("decode new key: %w", err)
	}
	newCiphertext, err := aesGCMEncrypt(newKey, plaintext)
	if err != nil {
		return fmt.Errorf("encrypt with new key: %w", err)
	}

	if err := rstore.Write(repoID, notePath, newCiphertext); err != nil {
		return fmt.Errorf("write re-encrypted blob: %w", err)
	}
	return nil
}

// aesGCMEncrypt/aesGCMDecrypt/newGCM replicate the exact AES-256-GCM,
// nonce-prepended-to-ciphertext scheme used by
// renderstore.EncryptingStore.gcm(), so blobs re-encrypted here stay
// interoperable with anything else (the plugin, the frontend) that assumes
// this layout. That helper is unexported to its own package, so the
// algorithm is duplicated here verbatim rather than imported.
func aesGCMEncrypt(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func aesGCMDecrypt(key, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ct, nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
