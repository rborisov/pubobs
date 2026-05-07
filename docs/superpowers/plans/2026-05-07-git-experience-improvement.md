# Git Experience Improvement — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve git sync UX with shallow clones, per-note plugin metadata in frontmatter, a unified sync command, and stale comment marking.

**Architecture:** Backend gets shallow clone/fetch, stale-comment SHA tracking, and drops the history endpoint. The Obsidian plugin gains frontmatter plugin injection, a pull-compatibility check with local-copy fallback, and a unified pull-then-push sync flow. The frontend renders outdated comments with a visual badge.

**Tech Stack:** Go (backend), TypeScript (Obsidian plugin + vanilla-TS frontend), SQLite, git CLI.

---

## File Map

| File | Change |
|---|---|
| `backend/internal/gitcache/git.go` | Add `FetchReset`; add `--depth=1` to `Clone` |
| `backend/internal/gitcache/cache.go` | Use `FetchReset`; remove `History` |
| `backend/internal/gitcache/comments.go` | Add SHA field to format/parse |
| `backend/internal/api/wiki.go` | Remove history handler; staleness in list-comments; SHA in add-comment |
| `backend/internal/api/pub.go` | Staleness in public list-comments |
| `backend/internal/gitcache/git_test.go` | Add `FetchReset` test |
| `backend/internal/gitcache/comments_test.go` | Update for new SHA field |
| `backend/internal/api/wiki_test.go` | Remove history test; add staleness test |
| `frontend/src/api.ts` | Add `is_outdated` to `PubComment`; `note_commit_sha` in `addComment` |
| `frontend/src/views/reader-note.ts` | Render outdated badge; pass SHA on post |
| `obsidian-plugin/src/sync.ts` | Plugin frontmatter injection; compat check; unified sync |
| `obsidian-plugin/src/main.ts` | Remove "Pull all repos" command |
| `obsidian-plugin/tests/sync.test.ts` | Tests for new helpers and unified sync |

---

## Task 1: Shallow clone — git.go

**Files:**
- Modify: `backend/internal/gitcache/git.go`
- Test: `backend/internal/gitcache/git_test.go`

- [ ] **Step 1: Write the failing test for FetchReset**

Add to `backend/internal/gitcache/git_test.go`:

```go
func TestFetchReset(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	cloneDir := t.TempDir()
	g := gitcache.NewGitRunner()
	require.NoError(t, g.Clone(cloneDir, bareURL, "", "main"))

	// Push a second commit to the remote
	work := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@x.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@x.com",
		)
		require.NoError(t, cmd.Run())
	}
	run("clone", bareURL, ".")
	os.WriteFile(filepath.Join(work, "second.md"), []byte("# Two"), 0644)
	run("add", ".")
	run("commit", "-m", "second")
	run("push", "origin", "HEAD:main")

	// FetchReset should bring the new file into the clone
	require.NoError(t, g.FetchReset(cloneDir, bareURL, ""))
	_, err := os.Stat(filepath.Join(cloneDir, "second.md"))
	require.NoError(t, err, "second.md should exist after FetchReset")
}
```

- [ ] **Step 2: Run test — confirm it fails**

```
cd backend && go test ./internal/gitcache/... -run TestFetchReset -v
```

Expected: `FAIL` — `g.FetchReset undefined`.

- [ ] **Step 3: Add FetchReset and update Clone in git.go**

In `backend/internal/gitcache/git.go`, replace `Clone` and `Pull` with:

```go
// Clone clones remoteURL into dir using a shallow single-branch clone.
func (g *GitRunner) Clone(dir, remoteURL, credJSON, branch string) error {
	authedURL := credentialedURL(remoteURL, credJSON)
	_, err := g.run("", "clone", "--depth=1", "--branch", branch, "--single-branch", authedURL, dir)
	if err != nil {
		// If branch doesn't exist yet (fresh bare repo), clone without --branch
		_, err = g.run("", "clone", "--depth=1", authedURL, dir)
	}
	return err
}

// FetchReset fetches the latest commit (depth=1) and hard-resets to it.
// It replaces the old Pull method.
func (g *GitRunner) FetchReset(dir, remoteURL, credJSON string) error {
	authedURL := credentialedURL(remoteURL, credJSON)
	if _, err := g.run(dir, "fetch", "--depth=1", authedURL); err != nil {
		return err
	}
	_, err := g.run(dir, "reset", "--hard", "FETCH_HEAD")
	return err
}
```

Remove the `Pull` method entirely.

- [ ] **Step 4: Run test — confirm it passes**

```
cd backend && go test ./internal/gitcache/... -run TestFetchReset -v
```

Expected: `PASS`.

- [ ] **Step 5: Run full gitcache tests**

```
cd backend && go test ./internal/gitcache/... -v
```

Expected: all pass (TestLogFile may fail — it tests the History path; ignore for now, it will be removed in Task 2).

- [ ] **Step 6: Commit**

```bash
git add backend/internal/gitcache/git.go backend/internal/gitcache/git_test.go
git commit -m "feat: shallow clone with --depth=1 and FetchReset replacing Pull"
```

---

## Task 2: Wire FetchReset in cache.go; remove History

**Files:**
- Modify: `backend/internal/gitcache/cache.go`
- Modify: `backend/internal/api/wiki.go`
- Modify: `backend/internal/gitcache/git_test.go` (remove TestLogFile)
- Modify: `backend/internal/api/wiki_test.go` (remove history test)

- [ ] **Step 1: Update getOrClone in cache.go**

In `backend/internal/gitcache/cache.go`, change `getOrClone`:

```go
func (c *Cache) getOrClone(repo *model.Repo, credJSON string) (string, error) {
	dir := c.repoDir(repo.ID)
	if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", dir, err)
		}
		if err := c.git.Clone(dir, repo.RemoteURL, credJSON, repo.DefaultBranch); err != nil {
			os.RemoveAll(dir)
			return "", fmt.Errorf("clone %s: %w", repo.RemoteURL, err)
		}
	} else {
		if err := c.git.FetchReset(dir, repo.RemoteURL, credJSON); err != nil {
			return "", fmt.Errorf("fetch-reset %s: %w", repo.ID, err)
		}
	}
	return dir, nil
}
```

- [ ] **Step 2: Remove History method from cache.go**

Delete the entire `History` method from `cache.go`. It runs `git log` which requires history depth.

- [ ] **Step 3: Remove serveHistory from wiki.go**

In `backend/internal/api/wiki.go`:

1. Delete the entire `serveHistory` function (lines starting `func serveHistory`).
2. In `handleNoteGet`, remove the history case:

```go
// Before (remove this case):
case strings.HasSuffix(notePath, "/history"):
    serveHistory(w, r, deps, claims, repoID, strings.TrimSuffix(notePath, "/history"))
```

The switch becomes:

```go
switch {
case strings.HasSuffix(notePath, "/backlinks"):
    serveBacklinks(w, r, deps, claims, repoID, strings.TrimSuffix(notePath, "/backlinks"))
case strings.HasSuffix(notePath, "/comments"):
    serveListComments(w, r, deps, claims, repoID, strings.TrimSuffix(notePath, "/comments"))
default:
    serveNoteView(w, r, deps, claims, repoID, notePath)
}
```

- [ ] **Step 4: Remove TestLogFile from git_test.go**

Delete `TestLogFile` entirely from `backend/internal/gitcache/git_test.go`.

- [ ] **Step 5: Remove history test from wiki_test.go**

Search `backend/internal/api/wiki_test.go` for any test function that calls the `/history` route (e.g. `TestHandleHistory`) and delete it.

- [ ] **Step 6: Build and test**

```
cd backend && go build ./... && go test ./internal/gitcache/... ./internal/api/... -v
```

Expected: all pass, no compile errors.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/gitcache/cache.go backend/internal/api/wiki.go \
        backend/internal/gitcache/git_test.go backend/internal/api/wiki_test.go
git commit -m "feat: use FetchReset in cache; drop history endpoint (incompatible with shallow)"
```

---

## Task 3: Stale comment format — comments.go

**Files:**
- Modify: `backend/internal/gitcache/comments.go`
- Modify: `backend/internal/gitcache/comments_test.go`

- [ ] **Step 1: Write failing tests for SHA format**

Replace the entire content of `backend/internal/gitcache/comments_test.go` with:

```go
package gitcache

import (
	"testing"
	"time"
)

func TestParseComments_empty(t *testing.T) {
	got := ParseComments("")
	if len(got) != 0 {
		t.Fatalf("expected 0, got %d", len(got))
	}
}

func TestParseComments_noComments(t *testing.T) {
	content := "---\ntype: comments\nnote: foo.md\n---\n\n"
	got := ParseComments(content)
	if len(got) != 0 {
		t.Fatalf("expected 0, got %d", len(got))
	}
}

func TestParseComments_oneComment(t *testing.T) {
	content := "---\ntype: comments\nnote: foo.md\n---\n\n" +
		"### Alice | 2026-05-04T10:00:00Z | alice@example.com\n\nHello world\n"
	got := ParseComments(content)
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	c := got[0]
	if c.AuthorName != "Alice" {
		t.Errorf("name: got %q", c.AuthorName)
	}
	if c.AuthorEmail != "alice@example.com" {
		t.Errorf("email: got %q", c.AuthorEmail)
	}
	if c.Body != "Hello world" {
		t.Errorf("body: got %q", c.Body)
	}
	if c.NoteCommitSHA != "" {
		t.Errorf("NoteCommitSHA should be empty for legacy comment, got %q", c.NoteCommitSHA)
	}
	wantTS := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	if !c.CreatedAt.Equal(wantTS) {
		t.Errorf("ts: got %v, want %v", c.CreatedAt, wantTS)
	}
}

func TestParseComments_withSHA(t *testing.T) {
	content := "---\ntype: comments\nnote: foo.md\n---\n\n" +
		"### Alice | 2026-05-04T10:00:00Z | alice@example.com | abc123de\n\nHello world\n"
	got := ParseComments(content)
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].NoteCommitSHA != "abc123de" {
		t.Errorf("NoteCommitSHA: got %q, want %q", got[0].NoteCommitSHA, "abc123de")
	}
}

func TestParseComments_noFrontmatter(t *testing.T) {
	content := "### Alice | 2026-05-04T10:00:00Z | alice@example.com\n\nHello\n"
	got := ParseComments(content)
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].Body != "Hello" {
		t.Errorf("body: got %q", got[0].Body)
	}
}

func TestParseComments_twoComments(t *testing.T) {
	content := "---\ntype: comments\nnote: foo.md\n---\n\n" +
		"### Alice | 2026-05-04T10:00:00Z | alice@example.com | sha1\n\nFirst\n" +
		"### Bob | 2026-05-04T11:00:00Z | bob@example.com | sha2\n\nSecond\n"
	got := ParseComments(content)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].NoteCommitSHA != "sha1" {
		t.Errorf("first SHA: got %q", got[0].NoteCommitSHA)
	}
	if got[1].NoteCommitSHA != "sha2" {
		t.Errorf("second SHA: got %q", got[1].NoteCommitSHA)
	}
}

func TestFormatComment_roundtrip_withSHA(t *testing.T) {
	ts := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	formatted := FormatComment("Alice", "alice@example.com", "Hello world", "abc123de", ts)
	got := ParseComments("---\ntype: comments\nnote: foo.md\n---\n\n" + formatted)
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].Body != "Hello world" {
		t.Errorf("body: got %q", got[0].Body)
	}
	if got[0].NoteCommitSHA != "abc123de" {
		t.Errorf("SHA: got %q", got[0].NoteCommitSHA)
	}
}

func TestCommentsFilePath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"note.md", "note-comments.md"},
		{"Daily Notes/2026-05-04.md", "Daily Notes/2026-05-04-comments.md"},
		{"path/to/deep/note.md", "path/to/deep/note-comments.md"},
	}
	for _, c := range cases {
		got := CommentsFilePath(c.in)
		if got != c.want {
			t.Errorf("CommentsFilePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run tests — confirm failures**

```
cd backend && go test ./internal/gitcache/... -run TestParseComments_withSHA -v
```

Expected: `FAIL` — `NoteCommitSHA` undefined.

- [ ] **Step 3: Update comments.go**

Replace `backend/internal/gitcache/comments.go` with:

```go
package gitcache

import (
	"fmt"
	"strings"
	"time"
)

// ParsedComment is a single comment parsed from a comments markdown file.
type ParsedComment struct {
	AuthorName    string
	AuthorEmail   string
	CreatedAt     time.Time
	Body          string
	NoteCommitSHA string // empty for comments written before SHA tracking was added
}

// CommentsFilePath derives the comments file path from a note path.
// "path/to/note.md" → "path/to/note-comments.md"
func CommentsFilePath(notePath string) string {
	return strings.TrimSuffix(notePath, ".md") + "-comments.md"
}

func commentsFileHeader(notePath string) string {
	return fmt.Sprintf("---\ntype: comments\nnote: %s\n---\n\n", notePath)
}

// FormatComment formats a single comment block for appending to a comments file.
// noteCommitSHA is the git_commit_sha of the note at the time of posting.
func FormatComment(name, email, body, noteCommitSHA string, ts time.Time) string {
	return fmt.Sprintf("### %s | %s | %s | %s\n\n%s\n",
		name, ts.UTC().Format(time.RFC3339), email, noteCommitSHA, strings.TrimSpace(body))
}

// ParseComments parses the contents of a comments markdown file into structured comments.
// The 4th header field (note commit SHA) is optional — legacy comments without it
// have NoteCommitSHA == "".
func ParseComments(content string) []ParsedComment {
	parts := strings.Split(content, "\n### ")
	start := 1
	if strings.HasPrefix(strings.TrimLeft(parts[0], "\r\n"), "### ") {
		parts[0] = strings.TrimPrefix(strings.TrimLeft(parts[0], "\r\n"), "### ")
		start = 0
	}
	var out []ParsedComment
	for _, part := range parts[start:] {
		nl := strings.Index(part, "\n")
		if nl == -1 {
			continue
		}
		header := strings.TrimSpace(part[:nl])
		body := strings.TrimSpace(part[nl+1:])

		fields := strings.SplitN(header, " | ", 4)
		if len(fields) < 3 {
			continue
		}
		ts, err := time.Parse(time.RFC3339, strings.TrimSpace(fields[1]))
		if err != nil {
			continue
		}
		var sha string
		if len(fields) == 4 {
			sha = strings.TrimSpace(fields[3])
		}
		out = append(out, ParsedComment{
			AuthorName:    strings.TrimSpace(fields[0]),
			AuthorEmail:   strings.TrimSpace(fields[2]),
			CreatedAt:     ts,
			Body:          body,
			NoteCommitSHA: sha,
		})
	}
	return out
}
```

- [ ] **Step 4: Run all comments tests**

```
cd backend && go test ./internal/gitcache/... -v
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/gitcache/comments.go backend/internal/gitcache/comments_test.go
git commit -m "feat: add note commit SHA field to comment format for staleness tracking"
```

---

## Task 4: AppendComment + API handlers — wiki.go, pub.go

**Files:**
- Modify: `backend/internal/gitcache/cache.go`
- Modify: `backend/internal/api/wiki.go`
- Modify: `backend/internal/api/pub.go`
- Modify: `backend/internal/api/wiki_test.go`

- [ ] **Step 1: Write failing test for stale comments**

Add to `backend/internal/api/wiki_test.go`:

```go
func TestServeListComments_isOutdated(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDeps(t)
	deps.Cache = gitcache.NewCache(t.TempDir())
	seedNoteForWiki(t, deps) // seeds snapshot with git_commit_sha = "abc123"

	ctx := context.Background()
	deps.Store.UpdateRepo(ctx, "r1", "R", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a2", "r1", "user", "u1", "commentator")

	// Post a comment stamped with SHA "oldsha" (different from snapshot SHA "abc123")
	postReq := httptest.NewRequest("POST",
		"/api/repos/r1/notes/docs/intro.md/comments",
		strings.NewReader(`{"body":"hello","note_commit_sha":"oldsha"}`))
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
```

- [ ] **Step 2: Update AppendComment signature in cache.go**

In `backend/internal/gitcache/cache.go`, update the `AppendComment` method:

```go
// AppendComment appends a comment to the note's companion comments file,
// commits the change, and pushes to the remote.
// noteCommitSHA is the git_commit_sha of the note at the time of posting.
func (c *Cache) AppendComment(ctx context.Context, repo *model.Repo, credJSON, notePath, authorName, authorEmail, body, noteCommitSHA string) error {
	lock := c.repoLock(repo.ID)
	lock.Lock()
	defer lock.Unlock()

	dir, err := c.getOrClone(repo, credJSON)
	if err != nil {
		return err
	}

	commentsPath := CommentsFilePath(notePath)
	fullPath := filepath.Join(dir, commentsPath)

	existing, err := os.ReadFile(fullPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	block := FormatComment(authorName, authorEmail, body, noteCommitSHA, time.Now().UTC())

	var content string
	if len(existing) == 0 {
		content = commentsFileHeader(notePath) + block
	} else {
		content = string(existing) + block
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return err
	}

	_, err = c.git.AddCommitPush(dir, repo.RemoteURL, credJSON, repo.DefaultBranch,
		fmt.Sprintf("pubobs: comment on %s", notePath))
	return err
}
```

- [ ] **Step 3: Update serveAddComment in wiki.go**

Replace `serveAddComment` in `backend/internal/api/wiki.go`:

```go
func serveAddComment(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "reader"); err != nil {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	var body struct {
		Body          string `json:"body"`
		NoteCommitSHA string `json:"note_commit_sha"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Body) == "" {
		writeError(w, http.StatusBadRequest, "body required")
		return
	}

	repo, err := deps.Store.GetRepo(r.Context(), repoID)
	if err != nil || repo == nil {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}

	credJSON, err := decryptCreds(deps, repo.EncryptedCreds)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cred decrypt failed")
		return
	}

	user, err := deps.Store.GetUserByID(r.Context(), claims.UserID)
	if err != nil || user == nil {
		writeError(w, http.StatusInternalServerError, "user not found")
		return
	}

	if err := deps.Cache.AppendComment(r.Context(), repo, credJSON, notePath, user.Name, user.Email, body.Body, body.NoteCommitSHA); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save comment")
		return
	}

	w.WriteHeader(http.StatusCreated)
}
```

- [ ] **Step 4: Update serveListComments in wiki.go**

Replace `serveListComments` in `backend/internal/api/wiki.go`:

```go
func serveListComments(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "reader"); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
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
```

- [ ] **Step 5: Update handlePubComments in pub.go**

Replace `handlePubComments` in `backend/internal/api/pub.go`:

```go
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
```

- [ ] **Step 6: Build and test**

```
cd backend && go build ./... && go test ./internal/... -v
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/gitcache/cache.go backend/internal/api/wiki.go \
        backend/internal/api/pub.go backend/internal/api/wiki_test.go
git commit -m "feat: track note commit SHA in comments; mark outdated comments in API response"
```

---

## Task 5: Frontend — stale comment badge

**Files:**
- Modify: `frontend/src/api.ts`
- Modify: `frontend/src/views/reader-note.ts`

- [ ] **Step 1: Update PubComment and addComment in api.ts**

In `frontend/src/api.ts`, update `PubComment` and `addComment`:

```typescript
export interface PubComment {
  body: string;
  created_at: string;
  author_email: string;
  author_name: string;
  is_outdated: boolean;
}

export async function addComment(repoId: string, notePath: string, body: string, noteCommitSha: string): Promise<void> {
  const resp = await authedFetch(`/api/repos/${repoId}/notes/${notePath}/comments`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ body, note_commit_sha: noteCommitSha }),
  });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
}
```

- [ ] **Step 2: Update reader-note.ts to pass SHA and render outdated badge**

In `frontend/src/views/reader-note.ts`:

1. Find the `addComment` call in the post-comment handler and add the note's `git_commit_sha`. The `note` object (type `PubNoteDetail`) already has `git_commit_sha: string`. Pass it:

```typescript
await addComment(repoId, notePath, body, note.git_commit_sha ?? '');
```

2. In `loadComments`, update the comment card rendering to show an outdated badge when `c.is_outdated` is true:

```typescript
for (const c of comments) {
  const card = document.createElement('div');
  card.className = 'r-comment-card';
  if (c.is_outdated) card.style.opacity = '0.6';

  const meta = document.createElement('div');
  meta.style.cssText = 'display:flex;gap:8px;align-items:baseline;margin-bottom:6px;flex-wrap:wrap';
  meta.innerHTML = `
    <span class="r-comment-author">${esc(c.author_name || c.author_email)}</span>
    <span class="r-comment-date">${new Date(c.created_at).toLocaleDateString(undefined, { year:'numeric', month:'short', day:'numeric' })}</span>
    ${c.is_outdated ? '<span style="font-size:0.75rem;color:var(--r-faint,#999);font-style:italic">added before last edit</span>' : ''}
  `;

  const body = document.createElement('div');
  body.className = 'r-comment-body';
  body.innerHTML = renderCommentBody(c.body);

  card.appendChild(meta);
  card.appendChild(body);
  list.appendChild(card);
}
```

- [ ] **Step 3: Build frontend**

```
cd frontend && npm run build
```

Expected: no TypeScript errors, build succeeds.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/api.ts frontend/src/views/reader-note.ts
git commit -m "feat: render outdated badge on comments posted before last note edit"
```

---

## Task 6: Plugin — inject pubobs-plugins into note frontmatter

**Files:**
- Modify: `obsidian-plugin/src/sync.ts`
- Modify: `obsidian-plugin/tests/sync.test.ts`

- [ ] **Step 1: Write failing tests for injectPluginFrontmatter**

Add to `obsidian-plugin/tests/sync.test.ts`:

```typescript
import { injectPluginFrontmatter, semverGte } from '../src/sync';

describe('semverGte', () => {
  test('equal versions', () => expect(semverGte('1.2.3', '1.2.3')).toBe(true));
  test('installed higher patch', () => expect(semverGte('1.2.4', '1.2.3')).toBe(true));
  test('installed lower patch', () => expect(semverGte('1.2.2', '1.2.3')).toBe(false));
  test('installed higher minor', () => expect(semverGte('1.3.0', '1.2.9')).toBe(true));
  test('installed lower minor', () => expect(semverGte('1.1.9', '1.2.0')).toBe(false));
  test('installed higher major', () => expect(semverGte('2.0.0', '1.9.9')).toBe(true));
  test('installed lower major', () => expect(semverGte('1.9.9', '2.0.0')).toBe(false));
});

describe('injectPluginFrontmatter', () => {
  test('adds pubobs-plugins to existing frontmatter', () => {
    const content = '---\ntitle: Test\n---\n\n# Hello';
    const plugins = [{ id: 'dataview', version: '0.5.55' }];
    const result = injectPluginFrontmatter(content, plugins);
    expect(result).toContain('pubobs-plugins:');
    expect(result).toContain('dataview');
    expect(result).toContain('# Hello');
  });

  test('creates frontmatter when absent', () => {
    const content = '# Hello\nNo frontmatter';
    const plugins = [{ id: 'dataview', version: '0.5.55' }];
    const result = injectPluginFrontmatter(content, plugins);
    expect(result.startsWith('---\n')).toBe(true);
    expect(result).toContain('pubobs-plugins:');
    expect(result).toContain('# Hello');
  });

  test('removes pubobs-plugins when no plugins detected', () => {
    const content = '---\ntitle: Test\npubobs-plugins:\n  - id: dataview\n    version: 0.5.55\n---\n\n# Hello';
    const result = injectPluginFrontmatter(content, []);
    expect(result).not.toContain('pubobs-plugins');
    expect(result).toContain('title: Test');
  });

  test('no-op when no plugins and no existing pubobs-plugins', () => {
    const content = '---\ntitle: Test\n---\n\n# Hello';
    const result = injectPluginFrontmatter(content, []);
    expect(result).toBe(content);
  });
});
```

- [ ] **Step 2: Run tests — confirm failures**

```
cd obsidian-plugin && npx jest --testPathPattern=sync -t "injectPluginFrontmatter|semverGte" 2>&1 | tail -20
```

Expected: `FAIL` — exports not found.

- [ ] **Step 3: Add helpers and update buildSyncFile in sync.ts**

In `obsidian-plugin/src/sync.ts`:

1. Add imports at the top:

```typescript
import { App, TFile, Notice, Modal, parseYaml, stringifyYaml } from 'obsidian';
```

2. Export `semverGte` helper (add near the bottom of the file, before the `fnv1a` function):

```typescript
export function semverGte(installed: string, required: string): boolean {
  const parse = (v: string) => v.split('.').map(n => parseInt(n, 10) || 0);
  const [iMaj, iMin, iPat] = parse(installed);
  const [rMaj, rMin, rPat] = parse(required);
  if (iMaj !== rMaj) return iMaj > rMaj;
  if (iMin !== rMin) return iMin > rMin;
  return iPat >= rPat;
}
```

3. Export `injectPluginFrontmatter` helper:

```typescript
export function injectPluginFrontmatter(
  content: string,
  plugins: Array<{ id: string; version: string }>,
): string {
  const fmMatch = content.match(/^---\n([\s\S]*?)\n---\n?/);
  if (fmMatch) {
    let fm: Record<string, unknown>;
    try { fm = (parseYaml(fmMatch[1]) as Record<string, unknown>) ?? {}; }
    catch { fm = {}; }

    if (plugins.length > 0) {
      fm['pubobs-plugins'] = plugins;
    } else {
      delete fm['pubobs-plugins'];
      // If nothing changed, return original to avoid noise
      if (!fmMatch[1].includes('pubobs-plugins')) return content;
    }
    const fmStr = stringifyYaml(fm);
    return `---\n${fmStr}---\n${content.slice(fmMatch[0].length)}`;
  }
  if (plugins.length === 0) return content;
  const fmStr = stringifyYaml({ 'pubobs-plugins': plugins });
  return `---\n${fmStr}---\n${content}`;
}
```

4. Update `buildSyncFile` to accept and inject detected plugins:

```typescript
private async buildSyncFile(
  file: TFile,
  content: string,
  vaultFolder: string,
  subfolder: string,
  repoId: string,
  detectedPluginIds: string[],
): Promise<{ syncFile: SyncFile; assets: Map<string, ArrayBuffer> }> {
  const cache = this.app.metadataCache.getFileCache(file);
  const { position: _pos, ...frontmatter } = ((cache?.frontmatter ?? {}) as Record<string, unknown>);

  // Build plugin metadata from detected IDs + installed versions
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const manifests = (this.app as any).plugins?.manifests ?? {};
  const pluginsMeta = detectedPluginIds.map(id => ({
    id,
    version: (manifests[id]?.version as string | undefined) ?? '0.0.0',
  }));

  // Inject into frontmatter object (stored in DB metadata_json)
  if (pluginsMeta.length > 0) {
    frontmatter['pubobs-plugins'] = pluginsMeta;
  } else {
    delete frontmatter['pubobs-plugins'];
  }

  // Inject into raw markdown (stored in git)
  const mdContent = injectPluginFrontmatter(content, pluginsMeta);

  let relative = file.path;
  if (vaultFolder && relative.startsWith(vaultFolder + '/')) {
    relative = relative.slice(vaultFolder.length + 1);
  }
  const repoPath = subfolder ? `${subfolder.replace(/\/$/, '')}/${relative}` : relative;

  const { html, assets } = await renderNoteToHTML(this.app, content, file.path, repoId, vaultFolder, subfolder);

  return {
    syncFile: { path: repoPath, md_content: mdContent, html_content: html, frontmatter },
    assets,
  };
}
```

5. In `syncRepo`, update the call to `buildSyncFile` to pass `used`:

```typescript
const used = detectPlugins(content);
const { syncFile, assets } = await this.buildSyncFile(f, content, vaultFolder, subfolder, repoId, used);
```

6. Remove the `_pubobs/note-plugins.json` asset — delete these lines from `syncRepo`:

```typescript
// DELETE these lines:
syncAssets.push({
  path: '_pubobs/note-plugins.json',
  content: bufferToBase64(new TextEncoder().encode(notePluginsJson(notePlugins)).buffer),
});
```

Also delete the `notePlugins` variable declaration and the `if (used.length > 0) notePlugins[repoPath] = used;` line.

Delete the `notePluginsJson` function at the bottom of the file.

- [ ] **Step 4: Run plugin tests**

```
cd obsidian-plugin && npx jest --testPathPattern=sync 2>&1 | tail -30
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add obsidian-plugin/src/sync.ts obsidian-plugin/tests/sync.test.ts
git commit -m "feat: inject pubobs-plugins into note frontmatter on sync; drop note-plugins.json"
```

---

## Task 7: Plugin — compatibility check on pull

**Files:**
- Modify: `obsidian-plugin/src/sync.ts`
- Modify: `obsidian-plugin/tests/sync.test.ts`

- [ ] **Step 1: Write failing tests for parseFrontmatterPlugins**

Add to `obsidian-plugin/tests/sync.test.ts`:

```typescript
import { parseFrontmatterPlugins } from '../src/sync';

describe('parseFrontmatterPlugins', () => {
  test('returns empty for note without frontmatter', () => {
    expect(parseFrontmatterPlugins('# Hello')).toEqual([]);
  });

  test('returns empty for frontmatter without pubobs-plugins', () => {
    expect(parseFrontmatterPlugins('---\ntitle: Test\n---\n# Hello')).toEqual([]);
  });

  test('returns plugins from frontmatter', () => {
    const content = '---\npubobs-plugins:\n  - id: dataview\n    version: "0.5.55"\n---\n# Hello';
    expect(parseFrontmatterPlugins(content)).toEqual([{ id: 'dataview', version: '0.5.55' }]);
  });

  test('returns multiple plugins', () => {
    const content = '---\npubobs-plugins:\n  - id: dataview\n    version: "0.5.55"\n  - id: templater-obsidian\n    version: "1.16.0"\n---\n# Hello';
    const result = parseFrontmatterPlugins(content);
    expect(result).toHaveLength(2);
    expect(result[0].id).toBe('dataview');
    expect(result[1].id).toBe('templater-obsidian');
  });
});
```

- [ ] **Step 2: Run tests — confirm failure**

```
cd obsidian-plugin && npx jest --testPathPattern=sync -t "parseFrontmatterPlugins" 2>&1 | tail -10
```

Expected: `FAIL` — export not found.

- [ ] **Step 3: Add parseFrontmatterPlugins to sync.ts**

Add near the other exported helpers in `obsidian-plugin/src/sync.ts`:

```typescript
export function parseFrontmatterPlugins(content: string): Array<{ id: string; version: string }> {
  const fmMatch = content.match(/^---\n([\s\S]*?)\n---/);
  if (!fmMatch) return [];
  try {
    const fm = parseYaml(fmMatch[1]) as Record<string, unknown>;
    const plugins = fm['pubobs-plugins'];
    if (!Array.isArray(plugins)) return [];
    return plugins.filter(
      (p): p is { id: string; version: string } =>
        typeof p === 'object' && p !== null &&
        'id' in p && typeof (p as Record<string, unknown>).id === 'string' &&
        'version' in p && typeof (p as Record<string, unknown>).version === 'string',
    );
  } catch {
    return [];
  }
}
```

- [ ] **Step 4: Run tests — confirm pass**

```
cd obsidian-plugin && npx jest --testPathPattern=sync -t "parseFrontmatterPlugins" 2>&1 | tail -10
```

Expected: `PASS`.

- [ ] **Step 5: Add IncompatibleNotesModal to sync.ts**

Add the modal class inside `obsidian-plugin/src/sync.ts` (after imports, before `SyncManager`):

```typescript
interface IncompatibleNote {
  path: string;
  missing: string[];
  content: string;
  sha: string;
}

class IncompatibleNotesModal extends Modal {
  constructor(
    app: App,
    private notes: IncompatibleNote[],
    private onCreateCopies: (notes: IncompatibleNote[]) => Promise<void>,
    private onSkip: () => void,
  ) {
    super(app);
  }

  onOpen() {
    const { contentEl } = this;
    contentEl.createEl('h2', { text: 'Plugin Compatibility' });
    contentEl.createEl('p', {
      text: `${this.notes.length} note(s) require plugin(s) that are not installed:`,
    });
    const ul = contentEl.createEl('ul');
    for (const n of this.notes) {
      ul.createEl('li', { text: `${n.path} — needs: ${n.missing.join(', ')}` });
    }
    contentEl.createEl('p', { text: 'Create local copies linked to the originals instead of pulling?' });
    const row = contentEl.createDiv({ cls: 'modal-button-container' });
    const copyBtn = row.createEl('button', { text: 'Create Copies', cls: 'mod-cta' });
    copyBtn.onclick = () => { this.close(); void this.onCreateCopies(this.notes); };
    const skipBtn = row.createEl('button', { text: 'Skip' });
    skipBtn.onclick = () => { this.close(); this.onSkip(); };
  }

  onClose() { this.contentEl.empty(); }
}
```

- [ ] **Step 6: Add createLocalCopy helper to sync.ts**

Add inside `SyncManager` class:

```typescript
private async createLocalCopy(
  vaultFolder: string,
  subfolder: string,
  note: IncompatibleNote,
): Promise<void> {
  const vaultPath = repoPathToVaultPath(note.path, vaultFolder, subfolder);
  const ext = '.md';
  const base = vaultPath.slice(0, -ext.length);

  // Find a non-colliding name
  let copyPath = `${base}-local-copy${ext}`;
  let suffix = 2;
  while (this.app.vault.getAbstractFileByPath(copyPath)) {
    copyPath = `${base}-local-copy-${suffix}${ext}`;
    suffix++;
  }

  // Inject pubobs-parent into content frontmatter
  const fmMatch = note.content.match(/^---\n([\s\S]*?)\n---\n?/);
  let copyContent: string;
  if (fmMatch) {
    let fm: Record<string, unknown>;
    try { fm = (parseYaml(fmMatch[1]) as Record<string, unknown>) ?? {}; } catch { fm = {}; }
    fm['pubobs-parent'] = note.path;
    const fmStr = stringifyYaml(fm);
    copyContent = `---\n${fmStr}---\n${note.content.slice(fmMatch[0].length)}`;
  } else {
    const fmStr = stringifyYaml({ 'pubobs-parent': note.path });
    copyContent = `---\n${fmStr}---\n${note.content}`;
  }

  const dir = copyPath.split('/').slice(0, -1).join('/');
  if (dir && !this.app.vault.getAbstractFileByPath(dir)) {
    await this.app.vault.createFolder(dir);
  }

  const existing = this.app.vault.getAbstractFileByPath(copyPath);
  if (existing instanceof TFile) {
    await this.app.vault.modify(existing, copyContent);
  } else {
    await this.app.vault.create(copyPath, copyContent);
  }
}
```

- [ ] **Step 7: Run all plugin tests**

```
cd obsidian-plugin && npx jest 2>&1 | tail -20
```

Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add obsidian-plugin/src/sync.ts obsidian-plugin/tests/sync.test.ts
git commit -m "feat: plugin compat check on pull with local-copy fallback"
```

---

## Task 8: Plugin — unified sync command

**Files:**
- Modify: `obsidian-plugin/src/sync.ts`
- Modify: `obsidian-plugin/src/main.ts`
- Modify: `obsidian-plugin/tests/sync.test.ts`

- [ ] **Step 1: Merge pull phase into syncRepo in sync.ts**

Replace `syncRepo` in `SyncManager` to add a pull phase at the start, before the existing push phase. Insert this pull phase immediately after the `notice` setup and before the file iteration loop:

```typescript
async syncRepo(repoId: string): Promise<void> {
  const mapping = this.settings.repoMappings[repoId];
  if (!mapping) throw new Error(`No folder mapping for repo ${repoId}`);
  const { vaultFolder, subfolder } = mapping;

  // ── Pull phase ────────────────────────────────────────────────────────────
  const notice = new Notice('PubObs: checking for remote changes…', 0);
  try {
    const remoteFiles = await this.client.listFiles(repoId);
    const storedPullSHAs: Record<string, string> = { ...(this.settings.pullSHAs[repoId] ?? {}) };
    const noteFiles = remoteFiles.filter(f => f.path.endsWith('.md') && !f.path.startsWith('_pubobs/'));

    const incompatible: IncompatibleNote[] = [];
    const toPull: typeof noteFiles = [];

    for (const file of noteFiles) {
      if (storedPullSHAs[file.path] === file.sha) continue;
      const required = parseFrontmatterPlugins(file.content);
      if (required.length > 0) {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const manifests = (this.app as any).plugins?.manifests ?? {};
        const missing = required
          .filter(p => {
            const installedVersion: string | undefined = manifests[p.id]?.version;
            return !installedVersion || !semverGte(installedVersion, p.version);
          })
          .map(p => {
            const entry = PLUGIN_PATTERNS.find(pp => pp.id === p.id);
            return entry ? `${entry.name} v${p.version}` : `${p.id} v${p.version}`;
          });
        if (missing.length > 0) {
          incompatible.push({ path: file.path, missing, content: file.content, sha: file.sha });
          continue;
        }
      }
      toPull.push(file);
    }

    // Pull compatible files
    let pulled = 0;
    for (const file of toPull) {
      const vaultPath = repoPathToVaultPath(file.path, vaultFolder, subfolder);
      const dir = vaultPath.split('/').slice(0, -1).join('/');
      if (dir && !this.app.vault.getAbstractFileByPath(dir)) {
        await this.app.vault.createFolder(dir);
      }
      const existing = this.app.vault.getAbstractFileByPath(vaultPath);
      if (existing instanceof TFile) {
        await this.app.vault.modify(existing, file.content);
      } else {
        await this.app.vault.create(vaultPath, file.content);
      }
      storedPullSHAs[file.path] = file.sha;
      pulled++;
    }
    this.settings.pullSHAs[repoId] = storedPullSHAs;
    await this.saveSettings();

    // Handle incompatible notes
    if (incompatible.length > 0) {
      await new Promise<void>(resolve => {
        new IncompatibleNotesModal(
          this.app,
          incompatible,
          async (notes) => {
            for (const n of notes) {
              await this.createLocalCopy(vaultFolder, subfolder, n);
            }
            resolve();
          },
          resolve,
        ).open();
      });
    }

    if (pulled > 0) notice.setMessage(`PubObs: pulled ${pulled} note(s), pushing local changes…`);
  } catch (e) {
    console.error('[PubObs] pull phase failed:', e);
  }

  // ── Push phase (existing syncRepo logic) ──────────────────────────────────
  const vaultFiles = this.app.vault
    .getFiles()
    .filter((f: TFile) => f.extension === 'md' && (vaultFolder === '' || f.path.startsWith(vaultFolder + '/')));

  const storedHashes: Record<string, string> = { ...(this.settings.syncHashes[repoId] ?? {}) };
  const newHashes: Record<string, string> = {};
  const currentRepoPaths = new Set<string>();

  const syncFiles: SyncFile[] = [];
  const assetMap = new Map<string, ArrayBuffer>();
  let skipped = 0;

  for (let i = 0; i < vaultFiles.length; i++) {
    const f = vaultFiles[i];
    try {
      const content = await this.app.vault.read(f);
      let relative = f.path;
      if (vaultFolder && relative.startsWith(vaultFolder + '/')) {
        relative = relative.slice(vaultFolder.length + 1);
      }
      const repoPath = subfolder ? `${subfolder.replace(/\/$/, '')}/${relative}` : relative;
      currentRepoPaths.add(repoPath);

      const hash = fnv1a(content);
      newHashes[repoPath] = hash;

      if (storedHashes[repoPath] === hash) { skipped++; continue; }

      notice.setMessage(`PubObs: rendering ${syncFiles.length + 1}: ${f.basename}…`);
      const used = detectPlugins(content);
      const { syncFile, assets } = await this.buildSyncFile(f, content, vaultFolder, subfolder, repoId, used);
      syncFiles.push(syncFile);
      for (const [vaultPath, buf] of assets) assetMap.set(vaultPath, buf);
    } catch (e) {
      console.error(`[PubObs] render failed for ${f.path}: ${e}`);
    }
  }
  notice.hide();

  const deletedPaths = Object.keys(storedHashes).filter(p => !currentRepoPaths.has(p));
  const syncAssets: SyncAsset[] = Array.from(assetMap.entries()).map(([vaultPath, buf]) => ({
    path: this.assetRepoPath(vaultPath, vaultFolder, subfolder),
    content: bufferToBase64(buf),
  }));

  const css = extractStyles();
  syncAssets.push({
    path: '_pubobs/obsidian.css',
    content: bufferToBase64(new TextEncoder().encode(css).buffer),
  });

  if (syncFiles.length === 0 && deletedPaths.length === 0) {
    new Notice(`PubObs: nothing to push (${skipped} note(s) up to date)`);
    return;
  }

  const result = await this.client.sync(repoId, syncFiles, syncAssets, deletedPaths);
  for (const p of deletedPaths) delete newHashes[p];
  this.settings.syncHashes[repoId] = newHashes;
  await this.saveSettings();

  new Notice(`PubObs: ${syncFiles.length} pushed, ${deletedPaths.length} deleted, ${skipped} unchanged — ${result.commit_sha.slice(0, 7)}`);
}
```

- [ ] **Step 2: Remove pullRepo method from SyncManager**

Delete the entire `pullRepo` method from the `SyncManager` class.

Delete the `warnMissingPlugins` standalone function at the bottom of the file (it is now replaced by the inline logic using `IncompatibleNotesModal`).

- [ ] **Step 3: Update main.ts — remove "Pull all repos" command**

In `obsidian-plugin/src/main.ts`, remove the command registration for `pullRepo`. It will look like:

```typescript
// DELETE this block:
this.addCommand({
  id: 'pull-all-repos',
  name: 'Pull all repos',
  callback: async () => {
    for (const repoId of Object.keys(this.settings.repoMappings)) {
      await this.syncManager.pullRepo(repoId);
    }
  },
});
```

- [ ] **Step 4: Update sync.test.ts — remove pullRepo tests, update syncRepo tests**

In `obsidian-plugin/tests/sync.test.ts`, remove the `describe('SyncManager.pullRepo', ...)` block entirely.

Update any existing `SyncManager.syncRepo` tests to account for the pull phase. If the mock client doesn't have a `listFiles` mock, add it returning an empty array:

```typescript
const mockClient = {
  listFiles: jest.fn().mockResolvedValue([]),
  sync: jest.fn().mockResolvedValue({ commit_sha: 'abc1234567890' }),
};
```

- [ ] **Step 5: Run all plugin tests**

```
cd obsidian-plugin && npx jest 2>&1 | tail -30
```

Expected: all pass.

- [ ] **Step 6: Build plugin**

```
cd obsidian-plugin && npm run build
```

Expected: no TypeScript errors.

- [ ] **Step 7: Commit**

```bash
git add obsidian-plugin/src/sync.ts obsidian-plugin/src/main.ts obsidian-plugin/tests/sync.test.ts
git commit -m "feat: unified sync command (pull-then-push); remove separate Pull command"
```

---

## Task 9: Final build and integration check

- [ ] **Step 1: Full backend build and test**

```
cd backend && go build ./... && go test ./... -v 2>&1 | grep -E "PASS|FAIL|ok|---"
```

Expected: all `ok`, no `FAIL`.

- [ ] **Step 2: Full plugin build and test**

```
cd obsidian-plugin && npm run build && npx jest 2>&1 | tail -20
```

Expected: build succeeds, all tests pass.

- [ ] **Step 3: Full frontend build**

```
cd frontend && npm run build
```

Expected: no errors.

- [ ] **Step 4: Final commit**

```bash
git add -A
git commit -m "chore: final build artifacts for git experience improvement (Track 1)"
```
