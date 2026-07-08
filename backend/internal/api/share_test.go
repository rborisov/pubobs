package api_test

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/stretchr/testify/require"
)

// testGCMEncrypt/testGCMDecrypt replicate the AES-256-GCM,
// nonce-prepended-to-ciphertext layout the handlers under test use (see
// share.go's aesGCMEncrypt/aesGCMDecrypt and renderstore.EncryptingStore),
// so tests can simulate a client that already encrypted a render blob with
// a note's key.
func testGCMEncrypt(t *testing.T, key, plaintext []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	nonce := make([]byte, gcm.NonceSize())
	_, err = io.ReadFull(rand.Reader, nonce)
	require.NoError(t, err)
	return gcm.Seal(nonce, nonce, plaintext, nil)
}

func testGCMDecrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(ciphertext) < ns {
		return nil, errors.New("ciphertext too short")
	}
	return gcm.Open(nil, ciphertext[:ns], ciphertext[ns:], nil)
}

func newTestDepsForShare(t *testing.T) *api.Deps {
	t.Helper()
	deps := newTestDeps(t)
	deps.Resolver = newTestResolver(t, deps)
	return deps
}

// seedNoteForShare creates repo r1 + note docs/intro.md, and grants userID
// the given role on r1 (creating the user first).
func seedNoteForShare(t *testing.T, deps *api.Deps, userID, role string) {
	t.Helper()
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, userID, userID+"@x.com", "User")
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", userID, role)
	deps.Store.UpsertNote(ctx, "r1", "docs/intro.md")
}

func doShareRequest(t *testing.T, deps *api.Deps, userID, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/repos/r1/notes/docs/intro.md/"+path, strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, userID, userID+"@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	return rr
}

func TestShareNote_public_mintsLink(t *testing.T) {
	deps := newTestDepsForShare(t)
	seedNoteForShare(t, deps, "editor1", "editor")

	rr := doShareRequest(t, deps, "editor1", "share", `{"mode":"public"}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, true, resp["shared"])
	require.Equal(t, "r1/docs/intro.md", resp["path"])
	key, _ := resp["key"].(string)
	require.NotEmpty(t, key)

	note, err := deps.Store.GetNote(context.Background(), "r1", "docs/intro.md")
	require.NoError(t, err)
	require.True(t, note.SharedPublicly)
	require.Equal(t, key, note.EncryptionKey)

	// Calling /share public again is idempotent: same key, still shared.
	rr2 := doShareRequest(t, deps, "editor1", "share", `{"mode":"public"}`)
	require.Equal(t, http.StatusOK, rr2.Code)
	var resp2 map[string]any
	require.NoError(t, json.NewDecoder(rr2.Body).Decode(&resp2))
	require.Equal(t, key, resp2["key"])
}

func TestShareNote_restricted_doesNotShare(t *testing.T) {
	deps := newTestDepsForShare(t)
	seedNoteForShare(t, deps, "editor1", "editor")

	rr := doShareRequest(t, deps, "editor1", "share", `{"mode":"restricted"}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, false, resp["shared"])
	require.Equal(t, "r1/docs/intro.md", resp["path"])
	require.NotContains(t, resp, "key")

	note, err := deps.Store.GetNote(context.Background(), "r1", "docs/intro.md")
	require.NoError(t, err)
	require.False(t, note.SharedPublicly)
	require.Empty(t, note.EncryptionKey)
}

func TestShareNote_noteNotFound(t *testing.T) {
	deps := newTestDepsForShare(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "editor1", "editor1@x.com", "Editor")
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "editor1", "editor")
	// Note deliberately never created.

	rr := doShareRequest(t, deps, "editor1", "share", `{"mode":"public"}`)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestUnshareNote_alreadyUnshared_isNoop(t *testing.T) {
	deps := newTestDepsForShare(t)
	seedNoteForShare(t, deps, "editor1", "editor")

	rr := doShareRequest(t, deps, "editor1", "unshare", "")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, false, resp["shared"])

	note, err := deps.Store.GetNote(context.Background(), "r1", "docs/intro.md")
	require.NoError(t, err)
	require.False(t, note.SharedPublicly)
}

func TestUnshareNote_revokesAndReencrypts(t *testing.T) {
	deps := newTestDepsForShare(t)
	seedNoteForShare(t, deps, "editor1", "editor")
	ctx := context.Background()

	// Mint a public link.
	rr := doShareRequest(t, deps, "editor1", "share", `{"mode":"public"}`)
	require.Equal(t, http.StatusOK, rr.Code)
	var shareResp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&shareResp))
	oldKeyB64 := shareResp["key"].(string)
	oldKey, err := base64.RawURLEncoding.DecodeString(oldKeyB64)
	require.NoError(t, err)

	// Simulate a previously-synced render blob encrypted with that key.
	plaintext := []byte("<h1>secret content</h1>")
	ciphertext := testGCMEncrypt(t, oldKey, plaintext)
	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", "docs/intro.md", ciphertext))

	// Revoke.
	rr2 := doShareRequest(t, deps, "editor1", "unshare", "")
	require.Equal(t, http.StatusOK, rr2.Code, rr2.Body.String())
	var unshareResp map[string]any
	require.NoError(t, json.NewDecoder(rr2.Body).Decode(&unshareResp))
	require.Equal(t, false, unshareResp["shared"])

	note, err := deps.Store.GetNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.False(t, note.SharedPublicly)
	require.NotEqual(t, oldKeyB64, note.EncryptionKey)
	newKey, err := base64.RawURLEncoding.DecodeString(note.EncryptionKey)
	require.NoError(t, err)

	newCiphertext, err := rstore.Read("r1", "docs/intro.md")
	require.NoError(t, err)
	require.NotEqual(t, ciphertext, newCiphertext)

	// Old key can no longer decrypt the stored blob.
	_, err = testGCMDecrypt(oldKey, newCiphertext)
	require.Error(t, err)

	// New key decrypts it back to the exact same plaintext.
	decrypted, err := testGCMDecrypt(newKey, newCiphertext)
	require.NoError(t, err)
	require.Equal(t, plaintext, decrypted)
}

func TestUnshareNote_noRenderBlob_justRotatesAndFlips(t *testing.T) {
	deps := newTestDepsForShare(t)
	seedNoteForShare(t, deps, "editor1", "editor")

	rr := doShareRequest(t, deps, "editor1", "share", `{"mode":"public"}`)
	require.Equal(t, http.StatusOK, rr.Code)

	// No render blob ever written for this note.
	rr2 := doShareRequest(t, deps, "editor1", "unshare", "")
	require.Equal(t, http.StatusOK, rr2.Code, rr2.Body.String())

	note, err := deps.Store.GetNote(context.Background(), "r1", "docs/intro.md")
	require.NoError(t, err)
	require.False(t, note.SharedPublicly)
	require.NotEmpty(t, note.EncryptionKey)
}

// TestShareNote_public_healsPreExistingKeyMismatchedBlob pins down the
// exact production bug: a note synced before encryption_key tracking
// existed (or otherwise left with a render blob under a key the DB no
// longer knows) has an empty encryption_key and a render blob encrypted
// under some other, unrelated key. Minting a public share link for it must
// not hand out a URL that decrypts nothing forever — the mismatched blob
// must be healed (deleted) so the note falls back to "not available" and
// self-heals on the next real sync, instead of permanently 404/decrypt-
// failing under a key the DB claims is correct.
func TestShareNote_public_healsPreExistingKeyMismatchedBlob(t *testing.T) {
	deps := newTestDepsForShare(t)
	seedNoteForShare(t, deps, "editor1", "editor")
	ctx := context.Background()

	// Simulate a note synced under the plugin's old client-generated-key
	// scheme, before encryption_key existed: a render blob is present, but
	// note.EncryptionKey is still "" (as migration 059113f leaves existing
	// rows), encrypted under some key the DB has no record of.
	legacyKey := make([]byte, 32)
	for i := range legacyKey {
		legacyKey[i] = byte(0xAA)
	}
	ciphertext := testGCMEncrypt(t, legacyKey, []byte("<h1>old content</h1>"))
	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", "docs/intro.md", ciphertext))

	note, err := deps.Store.GetNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.Empty(t, note.EncryptionKey, "precondition: note must not have a tracked key yet")

	rr := doShareRequest(t, deps, "editor1", "share", `{"mode":"public"}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, true, resp["shared"])
	newKeyB64, _ := resp["key"].(string)
	require.NotEmpty(t, newKeyB64)

	// The freshly-minted key can never have encrypted the pre-existing
	// blob, so it must have been healed away rather than left mismatched.
	remaining, err := rstore.Read("r1", "docs/intro.md")
	require.NoError(t, err)
	require.Nil(t, remaining, "stale, undecryptable render blob must be deleted by /share")

	note, err = deps.Store.GetNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.True(t, note.SharedPublicly)
	require.Equal(t, newKeyB64, note.EncryptionKey)
}

// TestShareNote_public_preservesValidMatchingBlob is the regression guard
// for the fix above: a render blob that DOES decrypt correctly with the
// note's key (the normal, healthy case) must never be touched by /share.
func TestShareNote_public_preservesValidMatchingBlob(t *testing.T) {
	deps := newTestDepsForShare(t)
	seedNoteForShare(t, deps, "editor1", "editor")
	ctx := context.Background()

	// First share mints the key normally.
	rr := doShareRequest(t, deps, "editor1", "share", `{"mode":"public"}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	keyB64 := resp["key"].(string)
	key, err := base64.RawURLEncoding.DecodeString(keyB64)
	require.NoError(t, err)

	// Simulate a real sync writing a correctly-encrypted blob under that key.
	plaintext := []byte("<h1>real content</h1>")
	ciphertext := testGCMEncrypt(t, key, plaintext)
	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", "docs/intro.md", ciphertext))

	// Re-sharing (idempotent) must not disturb a blob that already
	// decrypts correctly under the current key.
	rr2 := doShareRequest(t, deps, "editor1", "share", `{"mode":"public"}`)
	require.Equal(t, http.StatusOK, rr2.Code, rr2.Body.String())

	remaining, err := rstore.Read("r1", "docs/intro.md")
	require.NoError(t, err)
	require.Equal(t, ciphertext, remaining, "a valid, correctly-keyed blob must survive /share untouched")
}

// TestServeNoteKey_healsPreExistingKeyMismatchedBlob mirrors the /share
// heal test above for the /key endpoint, since it mints keys through the
// exact same code path for the plugin's pre-sync key fetch.
func TestServeNoteKey_healsPreExistingKeyMismatchedBlob(t *testing.T) {
	deps := newTestDepsForShare(t)
	seedNoteForShare(t, deps, "editor1", "editor")
	ctx := context.Background()

	legacyKey := make([]byte, 32)
	for i := range legacyKey {
		legacyKey[i] = byte(0x55)
	}
	ciphertext := testGCMEncrypt(t, legacyKey, []byte("<h1>old content</h1>"))
	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", "docs/intro.md", ciphertext))

	rr := doShareRequest(t, deps, "editor1", "key", "")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	remaining, err := rstore.Read("r1", "docs/intro.md")
	require.NoError(t, err)
	require.Nil(t, remaining, "stale, undecryptable render blob must be deleted by /key")
}

// TestUnshareNote_staleRenderBlob_doesNotFail guards against a related
// failure mode: revoking a share on a note whose stored blob doesn't
// actually decrypt with its recorded key (the same underlying mismatch)
// must not 500 the whole request. It should gracefully drop the
// undecryptable blob and still complete the rotation/unshare.
func TestUnshareNote_staleRenderBlob_doesNotFail(t *testing.T) {
	deps := newTestDepsForShare(t)
	seedNoteForShare(t, deps, "editor1", "editor")
	ctx := context.Background()

	rr := doShareRequest(t, deps, "editor1", "share", `{"mode":"public"}`)
	require.Equal(t, http.StatusOK, rr.Code)
	var shareResp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&shareResp))
	recordedKeyB64 := shareResp["key"].(string)

	// Overwrite the render blob directly with bytes encrypted under a key
	// that does NOT match note.EncryptionKey, simulating the exact
	// mismatch this bug produces (recorded key present, but wrong for the
	// bytes actually on disk).
	wrongKey := make([]byte, 32)
	for i := range wrongKey {
		wrongKey[i] = byte(0xEE)
	}
	ciphertext := testGCMEncrypt(t, wrongKey, []byte("<h1>mismatched</h1>"))
	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", "docs/intro.md", ciphertext))

	rr2 := doShareRequest(t, deps, "editor1", "unshare", "")
	require.Equal(t, http.StatusOK, rr2.Code, rr2.Body.String())

	note, err := deps.Store.GetNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.False(t, note.SharedPublicly)
	require.NotEqual(t, recordedKeyB64, note.EncryptionKey)

	remaining, err := rstore.Read("r1", "docs/intro.md")
	require.NoError(t, err)
	require.Nil(t, remaining, "undecryptable blob must be dropped rather than left mismatched under the rotated key")
}

func TestServeNoteKey_mintsWithoutSharing(t *testing.T) {
	deps := newTestDepsForShare(t)
	seedNoteForShare(t, deps, "editor1", "editor")

	rr := doShareRequest(t, deps, "editor1", "key", "")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	key, _ := resp["key"].(string)
	require.NotEmpty(t, key)

	note, err := deps.Store.GetNote(context.Background(), "r1", "docs/intro.md")
	require.NoError(t, err)
	require.Equal(t, key, note.EncryptionKey)
	require.False(t, note.SharedPublicly, "/key must never flip shared_publicly on")

	// Idempotent: calling again returns the same key.
	rr2 := doShareRequest(t, deps, "editor1", "key", "")
	require.Equal(t, http.StatusOK, rr2.Code)
	var resp2 map[string]any
	require.NoError(t, json.NewDecoder(rr2.Body).Decode(&resp2))
	require.Equal(t, key, resp2["key"])
}

// TestServeNoteKey_createsNoteIfMissing exercises the very-first-sync case
// the plugin relies on: a note that has never been synced (no row in
// `notes` yet) still gets a usable key rather than a 404, since /key must be
// callable BEFORE the main sync request creates the note.
func TestServeNoteKey_createsNoteIfMissing(t *testing.T) {
	deps := newTestDepsForShare(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "editor1", "editor1@x.com", "Editor")
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "editor1", "editor")
	// Note deliberately never created (simulates a brand-new file).

	rr := doShareRequest(t, deps, "editor1", "key", "")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.NotEmpty(t, resp["key"])

	note, err := deps.Store.GetNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.NotNil(t, note)
	require.False(t, note.SharedPublicly)
}

func TestServeNoteKey_roleEnforcement(t *testing.T) {
	for _, tc := range []struct {
		role       string
		wantStatus int
	}{
		{"reader", http.StatusForbidden},
		{"commentator", http.StatusForbidden},
		{"editor", http.StatusOK},
		{"admin", http.StatusOK},
	} {
		t.Run(tc.role, func(t *testing.T) {
			deps := newTestDepsForShare(t)
			seedNoteForShare(t, deps, "u1", tc.role)

			rr := doShareRequest(t, deps, "u1", "key", "")
			require.Equal(t, tc.wantStatus, rr.Code, rr.Body.String())
		})
	}
}

func TestShareUnshare_roleEnforcement(t *testing.T) {
	for _, tc := range []struct {
		role       string
		wantStatus int
	}{
		{"reader", http.StatusForbidden},
		{"commentator", http.StatusForbidden},
		{"editor", http.StatusOK},
		{"admin", http.StatusOK},
	} {
		t.Run(tc.role, func(t *testing.T) {
			deps := newTestDepsForShare(t)
			seedNoteForShare(t, deps, "u1", tc.role)

			rr := doShareRequest(t, deps, "u1", "share", `{"mode":"public"}`)
			require.Equal(t, tc.wantStatus, rr.Code, rr.Body.String())

			rr2 := doShareRequest(t, deps, "u1", "unshare", "")
			require.Equal(t, tc.wantStatus, rr2.Code, rr2.Body.String())
		})
	}
}
