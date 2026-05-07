package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/stretchr/testify/require"
)

func seedNoteForWiki(t *testing.T, deps *api.Deps) {
	t.Helper()
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader")
	note, _ := deps.Store.UpsertNote(ctx, "r1", "docs/intro.md")
	deps.Store.UpsertSnapshot(ctx, note.ID, "<h1>Intro</h1>", `{"links":["other.md"]}`, "u1", "abc123")
	other, _ := deps.Store.UpsertNote(ctx, "r1", "other.md")
	deps.Store.UpsertNoteLinks(ctx, note.ID, []string{"other.md"})
	_ = other
}

func TestHandleListNotes(t *testing.T) {
	deps := newTestDeps(t)
	seedNoteForWiki(t, deps)

	req := httptest.NewRequest("GET", "/api/repos/r1/notes", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var notes []map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&notes))
	require.Len(t, notes, 2)
}

func TestHandleNoteView(t *testing.T) {
	deps := newTestDeps(t)
	seedNoteForWiki(t, deps)

	req := httptest.NewRequest("GET", "/api/repos/r1/notes/docs/intro.md", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, "<h1>Intro</h1>", resp["html_content"])
}

func TestHandleBacklinks(t *testing.T) {
	deps := newTestDeps(t)
	seedNoteForWiki(t, deps)

	req := httptest.NewRequest("GET", "/api/repos/r1/notes/other.md/backlinks", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var notes []map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&notes))
	require.Len(t, notes, 1)
}

func TestHandleAddComment(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDeps(t)
	deps.Cache = gitcache.NewCache(t.TempDir())
	seedNoteForWiki(t, deps)

	ctx := context.Background()
	// Update repo to use the bare git URL
	deps.Store.UpdateRepo(ctx, "r1", "R", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a2", "r1", "user", "u1", "commentator")

	body := `{"body":"Great note!","note_commit_sha":"abc123"}`
	req := httptest.NewRequest("POST", "/api/repos/r1/notes/docs/intro.md/comments",
		strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
}

func TestServeListComments_isOutdated(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDeps(t)
	deps.Cache = gitcache.NewCache(t.TempDir())
	seedNoteForWiki(t, deps)

	ctx := context.Background()
	deps.Store.UpdateRepo(ctx, "r1", "R", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a2", "r1", "user", "u1", "commentator")

	// Post a comment stamped with a SHA different from the snapshot's ("abc123")
	postBody := `{"body":"hello","note_commit_sha":"oldsha"}`
	postReq := httptest.NewRequest("POST", "/api/repos/r1/notes/docs/intro.md/comments",
		strings.NewReader(postBody))
	postReq.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	postReq.Header.Set("Content-Type", "application/json")
	postRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(postRR, postReq)
	require.Equal(t, http.StatusCreated, postRR.Code, postRR.Body.String())

	// List comments — "oldsha" != "abc123" → is_outdated: true
	listReq := httptest.NewRequest("GET", "/api/repos/r1/notes/docs/intro.md/comments", nil)
	listReq.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	listRR := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(listRR, listReq)
	require.Equal(t, http.StatusOK, listRR.Code)

	var comments []map[string]any
	require.NoError(t, json.NewDecoder(listRR.Body).Decode(&comments))
	require.Len(t, comments, 1)
	require.Equal(t, true, comments[0]["is_outdated"])
}
