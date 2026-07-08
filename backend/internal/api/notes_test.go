package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// TestPubListNotes_excludesNoteWithoutSnapshot verifies that a DB row whose
// path still exists in git but has no snapshot (e.g. created by serveNoteKey
// before the first sync) is hidden from the reader list and pruned, since
// handlePubGetNote returns 404 "snapshot not found" for such notes.
func TestPubListNotes_excludesNoteWithoutSnapshot(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepoWithFiles(t, bareURL, map[string]string{
		"hello.md":                  "# Hello",
		"progress-2026-06-17.md":    "# Progress",
	})

	deps := newTestDepsWithCache(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "syncer@x.com", "Syncer")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", true))

	hello, err := deps.Store.UpsertNote(ctx, "r1", "hello.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, hello.ID, "", "{}", "u1", "sha1"))

	// Path exists in git but has no snapshot — the broken state reported in prod.
	_, err = deps.Store.UpsertNote(ctx, "r1", "progress-2026-06-17.md")
	require.NoError(t, err)

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

	gotBroken, err := deps.Store.GetNote(ctx, "r1", "progress-2026-06-17.md")
	require.NoError(t, err)
	require.Nil(t, gotBroken, "note without snapshot must be pruned on list")

	openReq := httptest.NewRequest("GET", "/pub/r1/notes/progress-2026-06-17.md", nil)
	openRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(openRR, openReq)
	require.Equal(t, http.StatusNotFound, openRR.Code)
}
