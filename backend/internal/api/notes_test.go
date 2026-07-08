package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/stretchr/testify/require"
)

// TestPubListNotes_prunesOrphanDBRows verifies that handlePubListNotes only
// returns notes whose paths still exist in git, and deletes stale DB rows for
// paths that were removed from the repo (e.g. after a rename/delete sync that
// previously failed to clean up the notes table).
func TestPubListNotes_prunesOrphanDBRows(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "syncer@x.com", "Syncer")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", true))

	// hello.md exists in git (seedBareRepo); ghost.md is a stale DB-only row.
	hello, err := deps.Store.UpsertNote(ctx, "r1", "hello.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, hello.ID, "", "{}", "u1", "sha1"))

	ghost, err := deps.Store.UpsertNote(ctx, "r1", "ghost.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, ghost.ID, "", "{}", "u1", "sha2"))

	req := httptest.NewRequest("GET", "/pub/r1", nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp struct {
		Notes []struct {
			Path string `json:"path"`
		} `json:"notes"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Len(t, resp.Notes, 1)
	require.Equal(t, "hello.md", resp.Notes[0].Path)

	gotGhost, err := deps.Store.GetNote(ctx, "r1", "ghost.md")
	require.NoError(t, err)
	require.Nil(t, gotGhost, "orphan note row must be pruned on list")
}

// TestHandleListNotes_prunesOrphanDBRows is the authenticated wiki-list
// counterpart: same git-vs-DB reconciliation as the pub reader list.
func TestHandleListNotes_prunesOrphanDBRows(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader")

	hello, err := deps.Store.UpsertNote(ctx, "r1", "hello.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, hello.ID, "", "{}", "u1", "sha1"))

	ghost, err := deps.Store.UpsertNote(ctx, "r1", "ghost.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, ghost.ID, "", "{}", "u1", "sha2"))

	req := httptest.NewRequest("GET", "/api/repos/r1/notes", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var notes []struct {
		Path string `json:"path"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&notes))
	require.Len(t, notes, 1)
	require.Equal(t, "hello.md", notes[0].Path)

	gotGhost, err := deps.Store.GetNote(ctx, "r1", "ghost.md")
	require.NoError(t, err)
	require.Nil(t, gotGhost, "orphan note row must be pruned on list")
}

// TestPubListNotes_deletedViaSyncNotListed is the end-to-end regression test
// for deleted notes still appearing in the reader list: sync with deleted_paths
// must remove the file from git and the DB, and a subsequent list must not
// include it.
func TestPubListNotes_deletedViaSyncNotListed(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", true))

	// Sync the note in.
	createPayload := `{"files":[{"path":"progress-2026-06-17.md","md_content":"# Progress","encrypted_html":"dGVzdCBlbmNyeXB0ZWQgaHRtbA==","frontmatter":{}}]}`
	req := httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(createPayload))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	// Confirm it appears in the reader list.
	listReq := httptest.NewRequest("GET", "/pub/r1", nil)
	listRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(listRR, listReq)
	require.Equal(t, http.StatusOK, listRR.Code, listRR.Body.String())
	var listBefore struct {
		Notes []struct {
			Path string `json:"path"`
		} `json:"notes"`
	}
	require.NoError(t, json.NewDecoder(listRR.Body).Decode(&listBefore))
	require.Len(t, listBefore.Notes, 1)
	require.Equal(t, "progress-2026-06-17.md", listBefore.Notes[0].Path)

	// Delete via sync.
	deletePayload := `{"files":[],"deleted_paths":["progress-2026-06-17.md"]}`
	req = httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(deletePayload))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr = httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	// Must no longer appear in the reader list.
	listReq = httptest.NewRequest("GET", "/pub/r1", nil)
	listRR = httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(listRR, listReq)
	require.Equal(t, http.StatusOK, listRR.Code, listRR.Body.String())
	var listAfter struct {
		Notes []struct {
			Path string `json:"path"`
		} `json:"notes"`
	}
	require.NoError(t, json.NewDecoder(listRR.Body).Decode(&listAfter))
	require.Empty(t, listAfter.Notes, "deleted note must not appear in reader list")

	gotNote, err := deps.Store.GetNote(ctx, "r1", "progress-2026-06-17.md")
	require.NoError(t, err)
	require.Nil(t, gotNote, "deleted note DB row must be gone")
}

// TestPubListNotes_sharedNoteSurvivesReconcileWithoutGitPath verifies that
// reconcileNotesWithGit hides a shared note from the list when its path is
// absent from git, but does NOT delete the DB row — a direct share-link
// visitor must still reach handlePubGetNote afterward. Before this guard,
// loading the reader list could prune the note and permanently break an
// already-issued ?key= link.
func TestPubListNotes_sharedNoteSurvivesReconcileWithoutGitPath(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "syncer@x.com", "Syncer")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", true))

	// hello.md is in git; shared.md exists only in the DB (stale row).
	hello, err := deps.Store.UpsertNote(ctx, "r1", "hello.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, hello.ID, "", "{}", "u1", "sha1"))

	sharedPath := "3gpp/38211-konspekt.md"
	shared, err := deps.Store.UpsertNote(ctx, "r1", sharedPath)
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, shared.ID, "", "{}", "u1", "sha2"))
	key := "test-note-key-0123456789AB"
	require.NoError(t, deps.Store.SetNoteShared(ctx, shared.ID, true, key))

	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", sharedPath, []byte("encrypted-blob-bytes")))

	listReq := httptest.NewRequest("GET", "/pub/r1", nil)
	listRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(listRR, listReq)
	require.Equal(t, http.StatusOK, listRR.Code, listRR.Body.String())

	var listResp struct {
		Notes []struct {
			Path string `json:"path"`
		} `json:"notes"`
	}
	require.NoError(t, json.NewDecoder(listRR.Body).Decode(&listResp))
	require.Len(t, listResp.Notes, 1, "shared note missing from git must be hidden from list")
	require.Equal(t, "hello.md", listResp.Notes[0].Path)

	gotShared, err := deps.Store.GetNote(ctx, "r1", sharedPath)
	require.NoError(t, err)
	require.NotNil(t, gotShared, "shared note row must survive list reconcile")

	noteReq := httptest.NewRequest("GET", "/pub/r1/notes/"+sharedPath+"?key="+key, nil)
	noteRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(noteRR, noteReq)
	require.Equal(t, http.StatusOK, noteRR.Code, noteRR.Body.String())
}

// TestPubListNotes_unsharedEncryptedNoteSurvivesReconcileWithoutGitPath
// verifies that after "Stop sharing", a note with a tracked encryption key
// is NOT pruned from the DB when its path is absent from git — repo members
// must still be able to open it (or at least not lose the row on list load).
func TestPubListNotes_unsharedEncryptedNoteSurvivesReconcileWithoutGitPath(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "editor@x.com", "Editor")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", true))
	require.NoError(t, deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor"))

	hello, err := deps.Store.UpsertNote(ctx, "r1", "hello.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, hello.ID, "", "{}", "u1", "sha1"))

	sharedPath := "3gpp/38211-konspekt.md"
	shared, err := deps.Store.UpsertNote(ctx, "r1", sharedPath)
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, shared.ID, "", "{}", "u1", "sha2"))
	key, err := deps.Store.GetOrCreateNoteKey(ctx, shared.ID)
	require.NoError(t, err)
	require.NoError(t, deps.Store.SetNoteShared(ctx, shared.ID, true, key))

	// Stop sharing — shared_publicly=false but encryption_key remains.
	unshareReq := httptest.NewRequest("POST", "/api/repos/r1/notes/"+sharedPath+"/unshare", strings.NewReader(""))
	unshareReq.Header.Set("Authorization", bearerHeader(t, deps, "u1", "editor@x.com", false))
	unshareRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(unshareRR, unshareReq)
	require.Equal(t, http.StatusOK, unshareRR.Code, unshareRR.Body.String())

	listReq := httptest.NewRequest("GET", "/pub/r1", nil)
	listRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(listRR, listReq)
	require.Equal(t, http.StatusOK, listRR.Code, listRR.Body.String())

	gotShared, err := deps.Store.GetNote(ctx, "r1", sharedPath)
	require.NoError(t, err)
	require.NotNil(t, gotShared, "unshared encrypted note must survive list reconcile")
	require.False(t, gotShared.SharedPublicly)
	require.NotEmpty(t, gotShared.EncryptionKey)
}
