package api_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/storageresolver"
	"github.com/stretchr/testify/require"
)

// newTestResolver builds a StorageResolver backed by throwaway local dirs.
// Repos default to local storage (StorageDestinationID nil), so
// RenderStoreFor/AssetStoreFor resolve to these local stores.
func newTestResolver(t *testing.T, deps *api.Deps) *storageresolver.Resolver {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	r, err := storageresolver.New(deps.Store, t.TempDir(), t.TempDir(), key)
	require.NoError(t, err)
	return r
}

func newTestDepsForPub(t *testing.T) (deps *api.Deps, cacheDir string) {
	t.Helper()
	deps = newTestDeps(t)
	cacheDir = t.TempDir()
	deps.Cache = gitcache.NewCache(cacheDir)
	deps.Resolver = newTestResolver(t, deps)
	return deps, cacheDir
}

func TestHandlePubGetAsset_servesFromAssetStoreWhenPresent(t *testing.T) {
	deps, _ := newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", true))

	astore, err := deps.Resolver.AssetStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, astore.Write("r1", "img.png", []byte("from-asset-store")))

	req := httptest.NewRequest("GET", "/pub/r1/assets/img.png", nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Equal(t, []byte("from-asset-store"), rr.Body.Bytes())
}

// setupPubAccessTest seeds repo r1 (allow_guest=allowGuest) with reader
// access for user u1, and note docs/intro.md whose shared_publicly is set
// to shared with a fixed test key. A render blob is written for the note so
// "shown" cases can assert a real 200 with real bytes, not just a
// content-less 404-vs-not-404 distinction.
func setupPubAccessTest(t *testing.T, allowGuest, shared bool) (deps *api.Deps, key string) {
	t.Helper()
	deps, _ = newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "reader@x.com", "Reader")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", allowGuest))
	require.NoError(t, deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader"))

	note, err := deps.Store.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "<h1>Hi</h1>", "{}", "u1", "sha1"))

	key = "test-note-key-0123456789AB"
	require.NoError(t, deps.Store.SetNoteShared(ctx, note.ID, shared, key))

	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", "docs/intro.md", []byte("encrypted-blob-bytes")))

	return deps, key
}

func getPubNote(deps *api.Deps, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md"+query, nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	return rr
}

func getPubRender(deps *api.Deps, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/pub/r1/render/docs/intro.md"+query, nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	return rr
}

func TestPubAccess_guestOpen_noKey_shown(t *testing.T) {
	deps, _ := setupPubAccessTest(t, true, false)
	require.Equal(t, http.StatusOK, getPubNote(deps, "").Code)
	require.Equal(t, http.StatusOK, getPubRender(deps, "").Code)
}

func TestPubAccess_guestOpen_someKeyPresent_shownKeyIgnored(t *testing.T) {
	deps, _ := setupPubAccessTest(t, true, false)
	require.Equal(t, http.StatusOK, getPubNote(deps, "?key=totally-wrong-key").Code)
	require.Equal(t, http.StatusOK, getPubRender(deps, "?key=totally-wrong-key").Code)
}

func TestPubAccess_guestClosedNotShared_noKey_hidden(t *testing.T) {
	deps, _ := setupPubAccessTest(t, false, false)
	require.Equal(t, http.StatusNotFound, getPubNote(deps, "").Code)
	require.Equal(t, http.StatusNotFound, getPubRender(deps, "").Code)
}

func TestPubAccess_guestClosedNotShared_correctKey_stillHidden(t *testing.T) {
	deps, key := setupPubAccessTest(t, false, false)
	rr := getPubNote(deps, "?key="+key)
	require.Equal(t, http.StatusNotFound, rr.Code, "key alone must never be sufficient when the note isn't shared")
	rr2 := getPubRender(deps, "?key="+key)
	require.Equal(t, http.StatusNotFound, rr2.Code, "key alone must never be sufficient when the note isn't shared")
}

func TestPubAccess_guestClosedShared_noKey_hidden(t *testing.T) {
	deps, _ := setupPubAccessTest(t, false, true)
	require.Equal(t, http.StatusNotFound, getPubNote(deps, "").Code)
	require.Equal(t, http.StatusNotFound, getPubRender(deps, "").Code)
}

func TestPubAccess_guestClosedShared_correctKey_shown(t *testing.T) {
	deps, key := setupPubAccessTest(t, false, true)
	rr := getPubNote(deps, "?key="+key)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	rr2 := getPubRender(deps, "?key="+key)
	require.Equal(t, http.StatusOK, rr2.Code, rr2.Body.String())
	require.Equal(t, "encrypted-blob-bytes", rr2.Body.String())
}

// TestPubAccess_guestClosedShared_nestedPath_correctKey_shown covers the
// reported production URL shape (#/read/:repoId/3gpp/foo.md?key=...) where
// the note lives in a subfolder — chi must receive the full path and
// pubNoteAccess must honour the share key.
func TestPubAccess_guestClosedShared_nestedPath_correctKey_shown(t *testing.T) {
	deps, _ := newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "reader@x.com", "Reader")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", false))

	notePath := "3gpp/38211-konspekt.md"
	note, err := deps.Store.UpsertNote(ctx, "r1", notePath)
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "", "{}", "u1", "sha1"))

	key := "test-note-key-0123456789AB"
	require.NoError(t, deps.Store.SetNoteShared(ctx, note.ID, true, key))

	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", notePath, []byte("encrypted-blob-bytes")))

	req := httptest.NewRequest("GET", "/pub/r1/notes/"+notePath+"?key="+key, nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	renderReq := httptest.NewRequest("GET", "/pub/r1/render/"+notePath+"?key="+key, nil)
	renderRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(renderRR, renderReq)
	require.Equal(t, http.StatusOK, renderRR.Code, renderRR.Body.String())
}

func TestPubAccess_guestClosedShared_wrongKey_hidden(t *testing.T) {
	deps, _ := setupPubAccessTest(t, false, true)
	require.Equal(t, http.StatusNotFound, getPubNote(deps, "?key=totally-wrong-key").Code)
	require.Equal(t, http.StatusForbidden, getPubRender(deps, "?key=totally-wrong-key").Code)
}

func TestPubAccess_guestClosed_validBearerToken_shownRegardlessOfSharedState(t *testing.T) {
	for _, shared := range []bool{false, true} {
		deps, _ := setupPubAccessTest(t, false, shared)

		req := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md", nil)
		req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "reader@x.com", false))
		rr := httptest.NewRecorder()
		api.BuildRouter(deps).ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

		req2 := httptest.NewRequest("GET", "/pub/r1/render/docs/intro.md", nil)
		req2.Header.Set("Authorization", bearerHeader(t, deps, "u1", "reader@x.com", false))
		rr2 := httptest.NewRecorder()
		api.BuildRouter(deps).ServeHTTP(rr2, req2)
		require.Equal(t, http.StatusOK, rr2.Code, rr2.Body.String())
	}
}

// TestPubListNotes_rejectsShareKeyOnlyRequest verifies the explicit
// invariant in handlePubListNotes: a note-scoped share key must never grant
// access to the repo-level list endpoint, even when it's a valid key for
// one of the repo's notes.
func TestPubListNotes_rejectsShareKeyOnlyRequest(t *testing.T) {
	deps, key := setupPubAccessTest(t, false, true)
	req := httptest.NewRequest("GET", "/pub/r1?key="+key, nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code, rr.Body.String())
}

// TestPubGetNote_authenticatedSeesRealRole verifies an authenticated repo
// member sees their real role in the response, and that a note which isn't
// shared is never misreported as shared_publicly.
func TestPubGetNote_authenticatedSeesRealRole(t *testing.T) {
	deps, _ := setupPubAccessTest(t, true, false)
	req := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "reader@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "reader", resp["role"])
	require.Equal(t, false, resp["shared_publicly"])
}

// TestPubGetNote_shareKeyOnlyVisitorSeesGuestRole verifies a visitor who
// only supplied a valid note-level share key (no bearer token, no real repo
// role) sees an empty/guest role marker rather than any real role string —
// they must never be shown editor-only sharing controls.
func TestPubGetNote_shareKeyOnlyVisitorSeesGuestRole(t *testing.T) {
	deps, key := setupPubAccessTest(t, false, true)
	rr := getPubNote(deps, "?key="+key)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "", resp["role"])
	require.Equal(t, true, resp["shared_publicly"])
}

// TestPubGetNote_shareKeyOnlyVisitor_backlinksHidden verifies backlinks
// (paths + titles of OTHER notes) are withheld from a share-link-only
// visitor — who must not be able to enumerate the rest of a guest-closed
// repo — while a real repo member still receives them.
func TestPubGetNote_shareKeyOnlyVisitor_backlinksHidden(t *testing.T) {
	deps, key := setupPubAccessTest(t, false, true)
	ctx := context.Background()

	// Seed another note linking to docs/intro.md, producing one backlink.
	other, err := deps.Store.UpsertNote(ctx, "r1", "docs/other.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, other.ID, "<h1>Other</h1>", "{}", "u1", "sha2"))
	require.NoError(t, deps.Store.UpsertNoteLinks(ctx, other.ID, []string{"docs/intro.md"}))

	// Share-link-only visitor (valid ?key=, no repo role): no backlinks.
	rr := getPubNote(deps, "?key="+key)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var guestResp struct {
		Backlinks []map[string]any `json:"backlinks"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&guestResp))
	require.Empty(t, guestResp.Backlinks, "share-link-only visitor must not receive backlinks")

	// Real repo member (bearer token): backlinks present, unchanged.
	req := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "reader@x.com", false))
	memberRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(memberRR, req)
	require.Equal(t, http.StatusOK, memberRR.Code, memberRR.Body.String())
	var memberResp struct {
		Backlinks []map[string]any `json:"backlinks"`
	}
	require.NoError(t, json.NewDecoder(memberRR.Body).Decode(&memberResp))
	require.Len(t, memberResp.Backlinks, 1, "repo member must still receive backlinks")
	require.Equal(t, "docs/other.md", memberResp.Backlinks[0]["path"])
}

// TestPubListNotes_reportsRoleAndPerNoteSharedState verifies
// handlePubListNotes surfaces the caller's real role and each note's
// shared_publicly state without misreporting an unshared note as shared.
func TestPubListNotes_reportsRoleAndPerNoteSharedState(t *testing.T) {
	deps, _ := setupPubAccessTest(t, true, true)
	req := httptest.NewRequest("GET", "/pub/r1", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "reader@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp struct {
		Role  string `json:"role"`
		Notes []struct {
			Path           string `json:"path"`
			SharedPublicly bool   `json:"shared_publicly"`
		} `json:"notes"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "reader", resp.Role)
	require.Len(t, resp.Notes, 1)
	require.Equal(t, "docs/intro.md", resp.Notes[0].Path)
	require.True(t, resp.Notes[0].SharedPublicly)
}

// TestPubListNotes_anonymousGuestSeesEmptyRole verifies an anonymous guest
// on a guest-open repo (no bearer token at all) sees the empty/guest role
// marker, not a real role string.
func TestPubListNotes_anonymousGuestSeesEmptyRole(t *testing.T) {
	deps, _ := setupPubAccessTest(t, true, false)
	req := httptest.NewRequest("GET", "/pub/r1", nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "", resp["role"])
}

// TestPubGetNote_authenticatedRealAccess_decryptsRenderWithoutKey pins down
// the reported regression: an authenticated user with a REAL repo role
// (reader, here — the weakest real role) opening a bare /pub note URL with
// no ?key= at all must still see the note's actual decrypted content, not
// the "open via a shared link" placeholder. Before the fix, handlePubGetNote
// only ever inlined html_content for legacy (never-encrypted) notes, so any
// note synced under the render-encryption scheme was unreadable by anyone
// who didn't separately hold a share-link key — even a real repo member.
// TestPubGetNote_authenticatedStaleShareKeyStillReturnsHTML verifies repo
// members opening an outdated ?key= share URL still receive server-decrypted
// html_content. The frontend must prefer that over client-side /render decrypt
// when the caller has a real repo role — otherwise a post-unshare stale key
// in the bookmarked URL breaks the page even for authenticated readers.
func TestPubGetNote_authenticatedStaleShareKeyStillReturnsHTML(t *testing.T) {
	deps, _ := newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "reader@x.com", "Reader")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", false))
	require.NoError(t, deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader"))

	note, err := deps.Store.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "", "{}", "u1", "sha1"))

	key, err := deps.Store.GetOrCreateNoteKey(ctx, note.ID)
	require.NoError(t, err)
	keyBytes, err := base64.RawURLEncoding.DecodeString(key)
	require.NoError(t, err)

	plaintext := []byte("<h1>Real content</h1>")
	ciphertext := testGCMEncrypt(t, keyBytes, plaintext)
	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", "docs/intro.md", ciphertext))

	req := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md?key=totally-stale-share-key", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "reader@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, string(plaintext), resp["html_content"],
		"repo access must win over a stale ?key= in the URL")
	require.Equal(t, "reader", resp["role"])
}

func TestPubGetNote_authenticatedRealAccess_decryptsRenderWithoutKey(t *testing.T) {
	deps, _ := newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "reader@x.com", "Reader")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", false))
	require.NoError(t, deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader"))

	note, err := deps.Store.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "", "{}", "u1", "sha1"))

	// A note synced under the encryption scheme, but never shared publicly
	// (mirrors what a normal sync does for every note regardless of share
	// state: GetOrCreateNoteKey always mints a key and the render blob is
	// always written encrypted).
	key, err := deps.Store.GetOrCreateNoteKey(ctx, note.ID)
	require.NoError(t, err)
	keyBytes, err := base64.RawURLEncoding.DecodeString(key)
	require.NoError(t, err)

	plaintext := []byte("<h1>Real content</h1>")
	ciphertext := testGCMEncrypt(t, keyBytes, plaintext)
	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", "docs/intro.md", ciphertext))

	req := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "reader@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, string(plaintext), resp["html_content"],
		"an authenticated user with a real repo role must see decrypted content with no ?key=")
	require.Equal(t, "reader", resp["role"])
}

// TestPubGetNote_instanceAdmin_decryptsRenderWithoutKey covers the exact
// adjacent scenario from the bug report: an instance admin (who has no
// explicit per-repo access grant at all — admins bypass the repo ACL
// entirely) opens a bare reader link for a repo/note with no ?key=, and
// must see the real content exactly as before the sharing feature existed.
func TestPubGetNote_instanceAdmin_decryptsRenderWithoutKey(t *testing.T) {
	deps, _ := newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", false))
	// Deliberately NO GrantAccess call — the admin has no per-repo role row
	// at all, only claims.IsAdmin.

	note, err := deps.Store.UpsertNote(ctx, "r1", "3gpp/38211-konspekt.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "", "{}", "admin1", "sha1"))

	key, err := deps.Store.GetOrCreateNoteKey(ctx, note.ID)
	require.NoError(t, err)
	keyBytes, err := base64.RawURLEncoding.DecodeString(key)
	require.NoError(t, err)

	plaintext := []byte("<h1>3GPP TS 38.211</h1>")
	ciphertext := testGCMEncrypt(t, keyBytes, plaintext)
	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", "3gpp/38211-konspekt.md", ciphertext))

	req := httptest.NewRequest("GET", "/pub/r1/notes/3gpp/38211-konspekt.md", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, string(plaintext), resp["html_content"],
		"an instance admin must see decrypted content via a bare reader URL, exactly like any other real repo access")
	require.Equal(t, "admin", resp["role"])
}

// TestPubGetNote_shareKeyOnlyVisitor_noServerSideDecrypt verifies the
// server-side decrypt path introduced above never fires for a share-link-
// only visitor (valid key, no real repo role): they must still go through
// /render + client-side decryption with their own key, exactly as before —
// the server must never decrypt render blobs on behalf of a caller who
// isn't a real repo member.
func TestPubGetNote_shareKeyOnlyVisitor_noServerSideDecrypt(t *testing.T) {
	deps, key := setupPubAccessTest(t, false, true)
	rr := getPubNote(deps, "?key="+key)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.NotContains(t, resp, "html_content",
		"a share-link-only visitor must decrypt client-side, never via server-inlined html_content")
}

// TestPubShareThenRender_healsPreExistingLegacyBlob reproduces the reported
// production bug end-to-end: a note synced before encryption_key tracking
// existed has a render blob encrypted under an unknown legacy key, while
// its DB encryption_key column is still empty. An editor mints a "public"
// share link via POST /share (exactly what the reported repro did), then an
// anonymous visitor opens the resulting URL. Before the fix, /share handed
// back a key that could never decrypt the pre-existing blob, so /render
// happily served the undecryptable bytes and the client-side decrypt failed
// with a generic "could not decrypt" — indistinguishable from a real crypto
// bug. After the fix, /share heals (deletes) the stale blob, so the
// anonymous visitor instead gets a clean 404 from /render (note "not yet
// available"), and the note fully repairs itself the next time it's synced.
func TestPubShareThenRender_healsPreExistingLegacyBlob(t *testing.T) {
	deps, _ := newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "editor1", "editor@x.com", "Editor")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", false))
	require.NoError(t, deps.Store.GrantAccess(ctx, "a1", "r1", "user", "editor1", "editor"))

	note, err := deps.Store.UpsertNote(ctx, "r1", "3gpp/38213-konspekt.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "", "{}", "editor1", "sha1"))
	require.Empty(t, note.EncryptionKey, "precondition: pre-migration note has no tracked key yet")

	// The note's render blob was written long ago under the plugin's old
	// client-generated key scheme — the DB has no idea what key this is.
	legacyKey := make([]byte, 32)
	for i := range legacyKey {
		legacyKey[i] = byte(0x7B)
	}
	legacyCiphertext := testGCMEncrypt(t, legacyKey, []byte("<h1>old 3GPP notes</h1>"))
	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", "3gpp/38213-konspekt.md", legacyCiphertext))

	// Editor mints a public share link, exactly like the reported repro.
	shareReq := httptest.NewRequest("POST", "/api/repos/r1/notes/3gpp/38213-konspekt.md/share", strings.NewReader(`{"mode":"public"}`))
	shareReq.Header.Set("Authorization", bearerHeader(t, deps, "editor1", "editor@x.com", false))
	shareRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(shareRR, shareReq)
	require.Equal(t, http.StatusOK, shareRR.Code, shareRR.Body.String())

	var shareResp map[string]any
	require.NoError(t, json.NewDecoder(shareRR.Body).Decode(&shareResp))
	mintedKey, _ := shareResp["key"].(string)
	require.NotEmpty(t, mintedKey)

	// Anonymous visitor opens the resulting share link.
	renderReq := httptest.NewRequest("GET", "/pub/r1/render/3gpp/38213-konspekt.md?key="+mintedKey, nil)
	renderRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(renderRR, renderReq)

	require.Equal(t, http.StatusNotFound, renderRR.Code,
		"a healed stale blob must read as 'not available' — never serve undecryptable bytes for the client to fail on")
}

// TestPubShareUnshareThenOpen_authenticatedStillWorks is the core share→open→
// unshare→open regression: a repo member must always open a note without ?key=
// regardless of share history, as long as the render blob is healthy.
func TestPubShareUnshareThenOpen_authenticatedStillWorks(t *testing.T) {
	deps, _ := newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "editor1", "editor@x.com", "Editor")
	deps.Store.UpsertUser(ctx, "reader1", "reader@x.com", "Reader")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", false))
	require.NoError(t, deps.Store.GrantAccess(ctx, "a1", "r1", "user", "editor1", "editor"))
	require.NoError(t, deps.Store.GrantAccess(ctx, "a2", "r1", "user", "reader1", "reader"))

	note, err := deps.Store.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "", "{}", "editor1", "sha1"))

	key, err := deps.Store.GetOrCreateNoteKey(ctx, note.ID)
	require.NoError(t, err)
	keyBytes, err := base64.RawURLEncoding.DecodeString(key)
	require.NoError(t, err)
	plaintext := []byte("<h1>Share cycle content</h1>")
	ciphertext := testGCMEncrypt(t, keyBytes, plaintext)
	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", "docs/intro.md", ciphertext))

	// Share publicly.
	shareReq := httptest.NewRequest("POST", "/api/repos/r1/notes/docs/intro.md/share", strings.NewReader(`{"mode":"public"}`))
	shareReq.Header.Set("Authorization", bearerHeader(t, deps, "editor1", "editor@x.com", false))
	shareRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(shareRR, shareReq)
	require.Equal(t, http.StatusOK, shareRR.Code, shareRR.Body.String())

	// Reader opens without key — must see content.
	openAsReader := func() string {
		req := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md", nil)
		req.Header.Set("Authorization", bearerHeader(t, deps, "reader1", "reader@x.com", false))
		rr := httptest.NewRecorder()
		api.BuildRouter(deps).ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		var resp map[string]any
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
		html, _ := resp["html_content"].(string)
		return html
	}
	require.Equal(t, string(plaintext), openAsReader())

	// Stop sharing.
	unshareReq := httptest.NewRequest("POST", "/api/repos/r1/notes/docs/intro.md/unshare", strings.NewReader(""))
	unshareReq.Header.Set("Authorization", bearerHeader(t, deps, "editor1", "editor@x.com", false))
	unshareRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(unshareRR, unshareReq)
	require.Equal(t, http.StatusOK, unshareRR.Code, unshareRR.Body.String())

	// Reader still opens without key — must still see content (re-encrypted blob).
	require.Equal(t, string(plaintext), openAsReader())

	// Old share key must no longer grant guest access.
	guestReq := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md?key="+key, nil)
	guestRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(guestRR, guestReq)
	require.Equal(t, http.StatusNotFound, guestRR.Code, "revoked share key must not grant access after unshare")
}

// TestPubShareUnshareThenOpen_blobHealedSignalsPending covers the case where
// /share deletes a stale render blob: repo members must not get a blank page
// or the guest-only placeholder — they get render_pending instead.
func TestPubShareUnshareThenOpen_blobHealedSignalsPending(t *testing.T) {
	deps, _ := newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "editor1", "editor@x.com", "Editor")
	deps.Store.UpsertUser(ctx, "reader1", "reader@x.com", "Reader")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", false))
	require.NoError(t, deps.Store.GrantAccess(ctx, "a1", "r1", "user", "editor1", "editor"))
	require.NoError(t, deps.Store.GrantAccess(ctx, "a2", "r1", "user", "reader1", "reader"))

	note, err := deps.Store.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "", "{}", "editor1", "sha1"))

	// Legacy blob under an unknown key — /share will heal (delete) it.
	legacyKey := make([]byte, 32)
	for i := range legacyKey {
		legacyKey[i] = byte(0xCC)
	}
	ciphertext := testGCMEncrypt(t, legacyKey, []byte("<h1>legacy</h1>"))
	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", "docs/intro.md", ciphertext))

	shareReq := httptest.NewRequest("POST", "/api/repos/r1/notes/docs/intro.md/share", strings.NewReader(`{"mode":"public"}`))
	shareReq.Header.Set("Authorization", bearerHeader(t, deps, "editor1", "editor@x.com", false))
	shareRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(shareRR, shareReq)
	require.Equal(t, http.StatusOK, shareRR.Code, shareRR.Body.String())

	unshareReq := httptest.NewRequest("POST", "/api/repos/r1/notes/docs/intro.md/unshare", strings.NewReader(""))
	unshareReq.Header.Set("Authorization", bearerHeader(t, deps, "editor1", "editor@x.com", false))
	unshareRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(unshareRR, unshareReq)
	require.Equal(t, http.StatusOK, unshareRR.Code, unshareRR.Body.String())

	req := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "reader1", "reader@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.NotContains(t, resp, "html_content", "no content until re-sync")
	require.Equal(t, true, resp["render_pending"])
	require.Equal(t, "reader", resp["role"])
}

// TestPubGetNote_healsPrunedNoteFromGit verifies lazy recovery when reconcile
// incorrectly deleted a DB row that still exists in git.
func TestPubGetNote_healsPrunedNoteFromGit(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepoWithFiles(t, bareURL, map[string]string{"healed.md": "# Healed\n\n[[other]]"})

	deps := newTestDepsWithCache(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "reader@x.com", "Reader")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", true))
	require.NoError(t, deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader"))

	// Simulate incorrect prune: note exists in git but DB row was deleted.
	req := httptest.NewRequest("GET", "/pub/r1/notes/healed.md", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "reader@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "Healed", resp["title"])
	html, _ := resp["html_content"].(string)
	require.Contains(t, html, "Healed", "pruned note must render from git markdown without re-sync")

	got, err := deps.Store.GetNote(ctx, "r1", "healed.md")
	require.NoError(t, err)
	require.NotNil(t, got, "open must recreate the pruned DB row from git")
}

// TestPubGetNote_sharedPrunedBlobRendersFromGit reproduces the reported
// production failure: a note was shared (render blob existed), then
// healStaleRenderBlob deleted the blob during share/unshare, leaving repo
// members with no html_content. Signed-in users must still read the note from
// git markdown without an Obsidian re-sync.
func TestPubGetNote_sharedPrunedBlobRendersFromGit(t *testing.T) {
	bareURL := newBareRepo(t)
	notePath := "3gpp/38211-konspekt.md"
	seedBareRepoWithFiles(t, bareURL, map[string]string{
		notePath: "# 3GPP TS 38.211\n\nPhysical channels.",
	})

	deps := newTestDepsWithCache(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", false))

	note, err := deps.Store.UpsertNote(ctx, "r1", notePath)
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "", "{}", "admin1", "sha1"))
	key, err := deps.Store.GetOrCreateNoteKey(ctx, note.ID)
	require.NoError(t, err)
	require.NoError(t, deps.Store.SetNoteShared(ctx, note.ID, true, key))

	// Simulate share heal deleting the only render blob.
	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Delete("r1", notePath))

	req := httptest.NewRequest("GET", "/pub/r1/notes/"+notePath, nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "admin", resp["role"])
	html, _ := resp["html_content"].(string)
	require.Contains(t, html, "3GPP TS 38.211", "signed-in user must read note from git when render blob is gone")
	require.NotContains(t, resp, "render_pending")
}

// TestPubGetNote_sharedPrunedRowAndBlobRendersFromGit simulates the old
// reconcile bug that deleted both the DB row and render blob for a previously
// shared note. A signed-in repo member opening the bare URL must get 200 with
// content rendered from git, not 404.
func TestPubGetNote_sharedPrunedRowAndBlobRendersFromGit(t *testing.T) {
	bareURL := newBareRepo(t)
	notePath := "3gpp/38213-konspekt.md"
	seedBareRepoWithFiles(t, bareURL, map[string]string{
		notePath: "# 3GPP TS 38.213\n\nControl procedures.",
	})

	deps := newTestDepsWithCache(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", false))

	note, err := deps.Store.UpsertNote(ctx, "r1", notePath)
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "", "{}", "admin1", "sha1"))
	key, err := deps.Store.GetOrCreateNoteKey(ctx, note.ID)
	require.NoError(t, err)
	require.NoError(t, deps.Store.SetNoteShared(ctx, note.ID, true, key))

	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", notePath, []byte("was-here")))
	require.NoError(t, deps.Store.DeleteNote(ctx, "r1", notePath))
	require.NoError(t, rstore.Delete("r1", notePath))

	req := httptest.NewRequest("GET", "/pub/r1/notes/"+notePath, nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	html, _ := resp["html_content"].(string)
	require.Contains(t, html, "3GPP TS 38.213")
	got, err := deps.Store.GetNote(ctx, "r1", notePath)
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestHandlePubGetAsset_fallsBackToGitCheckoutAndBackfills(t *testing.T) {
	deps, cacheDir := newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", true))

	// Simulate a note published before this feature shipped: the asset only
	// exists in the git checkout, not yet in AssetStore.
	repoDir := filepath.Join(cacheDir, "r1")
	require.NoError(t, os.MkdirAll(repoDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "legacy.png"), []byte("from-git-checkout"), 0644))

	req := httptest.NewRequest("GET", "/pub/r1/assets/legacy.png", nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Equal(t, []byte("from-git-checkout"), rr.Body.Bytes())

	// Backfilled: the next read no longer needs the checkout.
	astore, err := deps.Resolver.AssetStoreFor(ctx, "r1")
	require.NoError(t, err)
	backfilled, err := astore.Read("r1", "legacy.png")
	require.NoError(t, err)
	require.Equal(t, []byte("from-git-checkout"), backfilled)
}

func writeCommentsFile(t *testing.T, cacheDir, repoID, notePath, contents string) {
	t.Helper()
	p := filepath.Join(cacheDir, repoID, gitcache.CommentsFilePath(notePath))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(contents), 0o644))
}

func TestPubComments_shareLinkVisitorCanReadWithKey(t *testing.T) {
	deps, cacheDir := newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "reader@x.com", "Reader")
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", false)) // guest-closed
	note, err := deps.Store.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "<h1>Hi</h1>", "{}", "u1", "sha1"))
	key := "test-note-key-0123456789AB"
	require.NoError(t, deps.Store.SetNoteShared(ctx, note.ID, true, key))
	writeCommentsFile(t, cacheDir, "r1", "docs/intro.md",
		"---\ntype: comments\nnote: docs/intro.md\n---\n\n### Al | 2026-01-01T00:00:00Z |  | sha1\n\nhi\n")

	// with key -> 200
	req := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md/comments?key="+key, nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "\"author_name\":\"Al\"")

	// without key on a guest-closed repo -> 404
	req2 := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md/comments", nil)
	rr2 := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr2, req2)
	require.Equal(t, http.StatusNotFound, rr2.Code)
}

func TestPubPostComment_anonymousUsesDisplayNameThenAnonym(t *testing.T) {
	deps, _ := newTestDepsForPub(t)
	ctx := context.Background()
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)
	deps.Store.UpsertUser(ctx, "u1", "reader@x.com", "Reader")
	deps.Store.CreateRepo(ctx, "r1", "R", bareURL, "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", true)) // guest-open -> anonymous can read/write
	note, err := deps.Store.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "<h1>Hi</h1>", "{}", "u1", "sha1"))

	// named anonymous comment
	body := strings.NewReader(`{"body":"hello","note_commit_sha":"sha1","author_name":"Bob"}`)
	req := httptest.NewRequest("POST", "/pub/r1/notes/docs/intro.md/comments", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	// blank name -> anonym
	body2 := strings.NewReader(`{"body":"again","note_commit_sha":"sha1","author_name":"  "}`)
	req2 := httptest.NewRequest("POST", "/pub/r1/notes/docs/intro.md/comments", body2)
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr2, req2)
	require.Equal(t, http.StatusCreated, rr2.Code, rr2.Body.String())

	// read back
	get := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md/comments", nil)
	gr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(gr, get)
	require.Contains(t, gr.Body.String(), "\"author_name\":\"Bob\"")
	require.Contains(t, gr.Body.String(), "\"author_name\":\"anonym\"")
}

// TestPubPostComment_authenticatedUsesAccountIdentityNotClientName verifies
// that when a valid bearer token is presented, the endpoint attributes the
// comment to the caller's real account identity (display name), ignoring
// any client-supplied author_name — a spoofed name must never be stored.
func TestPubPostComment_authenticatedUsesAccountIdentityNotClientName(t *testing.T) {
	deps, _ := newTestDepsForPub(t)
	ctx := context.Background()
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)
	deps.Store.UpsertUser(ctx, "u1", "reader@x.com", "Reader")
	deps.Store.CreateRepo(ctx, "r1", "R", bareURL, "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", true)) // guest-open
	require.NoError(t, deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader"))
	note, err := deps.Store.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "<h1>Hi</h1>", "{}", "u1", "sha1"))

	body := strings.NewReader(`{"body":"hello","note_commit_sha":"sha1","author_name":"Spoofed"}`)
	req := httptest.NewRequest("POST", "/pub/r1/notes/docs/intro.md/comments", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "reader@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	get := httptest.NewRequest("GET", "/pub/r1/notes/docs/intro.md/comments", nil)
	gr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(gr, get)
	require.Equal(t, http.StatusOK, gr.Code, gr.Body.String())
	require.Contains(t, gr.Body.String(), "\"author_name\":\"Reader\"")
	require.NotContains(t, gr.Body.String(), "Spoofed")
}

func TestPubPostComment_deniedWithoutAccess(t *testing.T) {
	deps, _ := newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "reader@x.com", "Reader")
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", false)) // guest-closed, note not shared
	note, err := deps.Store.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "<h1>Hi</h1>", "{}", "u1", "sha1"))

	body := strings.NewReader(`{"body":"x","note_commit_sha":"sha1","author_name":"Bob"}`)
	req := httptest.NewRequest("POST", "/pub/r1/notes/docs/intro.md/comments", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)
}
