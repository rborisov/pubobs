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
