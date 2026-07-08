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
