# Git-Based Comments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace DB-stored comments with comments stored as Markdown files committed directly to the git repository, parsed and served by the backend.

**Architecture:** Each note `path/to/note.md` gets a companion `path/to/note-comments.md` file in the same git repo. The backend reads this file from the local clone (no credentials needed for read if clone is warm), parses structured comment blocks, and returns JSON. To post a comment the backend acquires the per-repo lock, clones/pulls if needed, appends a new block, commits, and pushes — reusing the existing `Cache` git machinery. The DB comment tables are left in place but stop receiving new data.

**Tech Stack:** Go 1.22, chi router, existing `gitcache` package (`Cache`, `GitRunner`, per-repo `sync.Mutex`), TypeScript frontend with no external markdown library (inline regex renderer).

---

## Comments File Format

```markdown
---
type: comments
note: path/to/note.md
---

### John Doe | 2026-05-04T10:30:00Z | john@example.com

Great article! Really helpful.

### Jane Smith | 2026-05-05T09:00:00Z | jane@example.com

Thanks for sharing this.
```

- Frontmatter header block (created once when first comment is posted)
- Each comment is a `### Name | RFC3339 | email` line followed by body text
- Parsed by splitting on `\n### `
- Human-readable in Obsidian; the file is a real note in the vault

---

## File Map

| Action | File |
|--------|------|
| **Create** | `backend/internal/gitcache/comments.go` |
| **Create** | `backend/internal/gitcache/comments_test.go` |
| **Modify** | `backend/internal/gitcache/cache.go` |
| **Modify** | `backend/internal/api/pub.go` |
| **Modify** | `backend/internal/api/wiki.go` |
| **Modify** | `frontend/src/views/reader-note.ts` |

---

### Task 1: Comments file parser

**Files:**
- Create: `backend/internal/gitcache/comments.go`
- Create: `backend/internal/gitcache/comments_test.go`

- [ ] **Step 1: Create the test file**

```go
// backend/internal/gitcache/comments_test.go
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
	if c.CreatedAt.Year() != 2026 {
		t.Errorf("ts: got %v", c.CreatedAt)
	}
}

func TestParseComments_twoComments(t *testing.T) {
	content := "---\ntype: comments\nnote: foo.md\n---\n\n" +
		"### Alice | 2026-05-04T10:00:00Z | alice@example.com\n\nFirst\n" +
		"### Bob | 2026-05-04T11:00:00Z | bob@example.com\n\nSecond\n"
	got := ParseComments(content)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].Body != "First" {
		t.Errorf("first body: got %q", got[0].Body)
	}
	if got[1].AuthorName != "Bob" {
		t.Errorf("second name: got %q", got[1].AuthorName)
	}
}

func TestFormatComment_roundtrip(t *testing.T) {
	ts := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	formatted := FormatComment("Alice", "alice@example.com", "Hello world", ts)
	got := ParseComments("---\ntype: comments\nnote: foo.md\n---\n\n" + formatted)
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].Body != "Hello world" {
		t.Errorf("body: got %q", got[0].Body)
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

- [ ] **Step 2: Run tests — expect compile failure**

```bash
cd /Volumes/docvol/pubobs/backend && go test ./internal/gitcache/... 2>&1 | head -20
```

Expected: `undefined: ParseComments` (or similar compile error).

- [ ] **Step 3: Create comments.go**

```go
// backend/internal/gitcache/comments.go
package gitcache

import (
	"fmt"
	"strings"
	"time"
)

// ParsedComment is a single comment parsed from a comments markdown file.
type ParsedComment struct {
	AuthorName  string
	AuthorEmail string
	CreatedAt   time.Time
	Body        string
}

// CommentsFilePath derives the comments file path from a note path.
// "path/to/note.md" → "path/to/note-comments.md"
func CommentsFilePath(notePath string) string {
	return strings.TrimSuffix(notePath, ".md") + "-comments.md"
}

// commentsFileHeader returns the frontmatter for a new comments file.
func commentsFileHeader(notePath string) string {
	return fmt.Sprintf("---\ntype: comments\nnote: %s\n---\n\n", notePath)
}

// FormatComment formats a single comment block for appending to a comments file.
func FormatComment(name, email, body string, ts time.Time) string {
	return fmt.Sprintf("### %s | %s | %s\n\n%s\n",
		name, ts.UTC().Format(time.RFC3339), email, strings.TrimSpace(body))
}

// ParseComments parses the contents of a comments markdown file into structured comments.
func ParseComments(content string) []ParsedComment {
	parts := strings.Split(content, "\n### ")
	var out []ParsedComment
	for _, part := range parts[1:] { // parts[0] is the frontmatter preamble
		nl := strings.Index(part, "\n")
		if nl == -1 {
			continue
		}
		header := strings.TrimSpace(part[:nl])
		body := strings.TrimSpace(part[nl+1:])

		fields := strings.SplitN(header, " | ", 3)
		if len(fields) != 3 {
			continue
		}
		ts, err := time.Parse(time.RFC3339, strings.TrimSpace(fields[1]))
		if err != nil {
			continue
		}
		out = append(out, ParsedComment{
			AuthorName:  strings.TrimSpace(fields[0]),
			AuthorEmail: strings.TrimSpace(fields[2]),
			CreatedAt:   ts,
			Body:        body,
		})
	}
	return out
}
```

- [ ] **Step 4: Run tests — expect all pass**

```bash
cd /Volumes/docvol/pubobs/backend && go test ./internal/gitcache/... -run TestParse -v 2>&1
go test ./internal/gitcache/... -run TestFormat -v 2>&1
go test ./internal/gitcache/... -run TestComments -v 2>&1
```

Expected: all `PASS`.

- [ ] **Step 5: Commit**

```bash
cd /Volumes/docvol/pubobs/backend
git add internal/gitcache/comments.go internal/gitcache/comments_test.go
git commit -m "feat: add comments file parser (CommentsFilePath, ParseComments, FormatComment)"
```

---

### Task 2: Add ReadRawFile and AppendComment to Cache

**Files:**
- Modify: `backend/internal/gitcache/cache.go`

- [ ] **Step 1: Add ReadRawFile method**

Add at the end of `cache.go`, before the closing line:

```go
// ReadRawFile reads the raw content of a file from the local clone.
// Returns ("", nil) when the file does not exist.
func (c *Cache) ReadRawFile(repoID, filePath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(c.repoDir(repoID), filePath))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// AppendComment appends a comment to the note's companion comments file,
// commits the change, and pushes to the remote.
func (c *Cache) AppendComment(ctx context.Context, repo *model.Repo, credJSON, notePath, authorName, authorEmail, body string) error {
	lock := c.repoLock(repo.ID)
	lock.Lock()
	defer lock.Unlock()

	dir, err := c.getOrClone(repo, credJSON)
	if err != nil {
		return err
	}

	commentsPath := CommentsFilePath(notePath)
	fullPath := filepath.Join(dir, commentsPath)

	existing, _ := os.ReadFile(fullPath)
	block := FormatComment(authorName, authorEmail, body, time.Now().UTC())

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

Make sure `time` is imported — add it to the import block at the top of `cache.go` if not already there:

```go
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"   // add this if missing

	"github.com/pubobs/backend/internal/model"
)
```

- [ ] **Step 2: Build — expect clean**

```bash
cd /Volumes/docvol/pubobs/backend && go build ./... 2>&1
```

Expected: no output (clean build).

- [ ] **Step 3: Commit**

```bash
git add internal/gitcache/cache.go
git commit -m "feat: add Cache.ReadRawFile and Cache.AppendComment for git-backed comments"
```

---

### Task 3: Replace public comments endpoint to read from git

**Files:**
- Modify: `backend/internal/api/pub.go`

Currently `handlePubComments` reads from `deps.Store.ListCommentsWithAuthor`. Replace with git file read + parse.

- [ ] **Step 1: Replace handlePubComments**

Replace the entire `handlePubComments` function in `pub.go`:

```go
func handlePubComments(w http.ResponseWriter, r *http.Request, deps *Deps, repoID, notePath string) {
	raw, err := deps.Cache.ReadRawFile(repoID, gitcache.CommentsFilePath(notePath))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read comments failed")
		return
	}

	type item struct {
		AuthorName  string `json:"author_name"`
		AuthorEmail string `json:"author_email"`
		CreatedAt   string `json:"created_at"`
		Body        string `json:"body"`
	}
	parsed := gitcache.ParseComments(raw)
	out := make([]item, 0, len(parsed))
	for _, c := range parsed {
		out = append(out, item{
			AuthorName:  c.AuthorName,
			AuthorEmail: c.AuthorEmail,
			CreatedAt:   c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			Body:        c.Body,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
```

- [ ] **Step 2: Add gitcache import to pub.go**

The import block in `pub.go` currently has:

```go
import (
	"encoding/json"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/model"
)
```

Replace with:

```go
import (
	"encoding/json"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/model"
)
```

- [ ] **Step 3: Build — expect clean**

```bash
cd /Volumes/docvol/pubobs/backend && go build ./... 2>&1
```

Expected: no output. If `model` import is now unused, remove it from pub.go.

- [ ] **Step 4: Commit**

```bash
git add internal/api/pub.go
git commit -m "feat: serve public comments from git file instead of DB"
```

---

### Task 4: Replace authenticated comment endpoints to use git

**Files:**
- Modify: `backend/internal/api/wiki.go`

Two functions need changing: `serveListComments` (reads) and `serveAddComment` (writes).

- [ ] **Step 1: Replace serveListComments**

Find and replace the `serveListComments` function:

```go
func serveListComments(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	raw, err := deps.Cache.ReadRawFile(repoID, gitcache.CommentsFilePath(notePath))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read comments failed")
		return
	}

	type item struct {
		AuthorName  string `json:"author_name"`
		AuthorEmail string `json:"author_email"`
		CreatedAt   string `json:"created_at"`
		Body        string `json:"body"`
	}
	parsed := gitcache.ParseComments(raw)
	out := make([]item, 0, len(parsed))
	for _, c := range parsed {
		out = append(out, item{
			AuthorName:  c.AuthorName,
			AuthorEmail: c.AuthorEmail,
			CreatedAt:   c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			Body:        c.Body,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
```

- [ ] **Step 2: Replace serveAddComment**

Find and replace the `serveAddComment` function:

```go
func serveAddComment(w http.ResponseWriter, r *http.Request, deps *Deps, claims *auth.AccessClaims, repoID, notePath string) {
	if err := requireRepoRole(r.Context(), deps, claims, repoID, "commentator"); err != nil {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	var body struct {
		Body string `json:"body"`
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

	if err := deps.Cache.AppendComment(r.Context(), repo, credJSON, notePath, user.Name, user.Email, body.Body); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save comment")
		return
	}

	w.WriteHeader(http.StatusCreated)
}
```

- [ ] **Step 3: Add gitcache import to wiki.go**

Find the import block in `wiki.go` and add the gitcache package. The existing imports include something like:

```go
import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
	// ... other imports
)
```

Add:

```go
	"github.com/pubobs/backend/internal/gitcache"
```

- [ ] **Step 4: Build — expect clean**

```bash
cd /Volumes/docvol/pubobs/backend && go build ./... 2>&1
```

Expected: no output. Remove any now-unused imports (e.g., `model` package if no longer referenced in these functions).

- [ ] **Step 5: Commit**

```bash
git add internal/api/wiki.go
git commit -m "feat: post and list comments via git file instead of DB"
```

---

### Task 5: Frontend — simple markdown renderer for comment bodies

**Files:**
- Modify: `frontend/src/views/reader-note.ts`

Currently comment bodies are rendered as plain text (`body.textContent = c.body`). Replace with a simple inline markdown renderer.

- [ ] **Step 1: Add renderCommentBody helper function**

Add this function at the bottom of `reader-note.ts` (after the existing `esc` and `patchObsidianCSS` functions):

```typescript
function renderCommentBody(text: string): string {
  const safe = esc(text);
  const html = safe
    .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
    .replace(/\*(.+?)\*/g, '<em>$1</em>')
    .replace(/_(.+?)_/g, '<em>$1</em>')
    .replace(/`([^`]+)`/g, '<code style="background:#f1f5f9;padding:1px 4px;border-radius:3px;font-size:0.85em">$1</code>');
  return html
    .split(/\n\n+/)
    .map(p => `<p style="margin:0 0 6px">${p.replace(/\n/g, '<br>')}</p>`)
    .join('');
}
```

- [ ] **Step 2: Use renderCommentBody in loadComments**

In the `loadComments` function, find the body element creation:

```typescript
    const body = document.createElement('p');
    body.style.cssText = 'margin:0;font-size:0.875rem;color:#1a1a1a;white-space:pre-wrap';
    body.textContent = c.body;
```

Replace with:

```typescript
    const body = document.createElement('div');
    body.style.cssText = 'font-size:0.875rem;color:#1a1a1a';
    body.innerHTML = renderCommentBody(c.body);
```

- [ ] **Step 3: Build frontend**

```bash
cd /Volumes/docvol/pubobs/frontend && npm run build 2>&1
```

Expected: `Done in <Xms>` with no errors.

- [ ] **Step 4: Build backend (embeds the new frontend)**

```bash
cd /Volumes/docvol/pubobs/backend && go build ./... 2>&1
```

Expected: no output.

- [ ] **Step 5: Commit**

```bash
cd /Volumes/docvol/pubobs
git add frontend/src/views/reader-note.ts backend/frontend/static/app.js
git commit -m "feat: render comment bodies as simple markdown"
```

---

## Self-Review

### Spec coverage

| Requirement | Task |
|-------------|------|
| Comments stored as `.md` files in git | Tasks 1–4 |
| `{note-name}-comments.md` path convention | Task 1 (`CommentsFilePath`) |
| Frontmatter header on creation | Task 2 (`commentsFileHeader`) |
| `### Name \| timestamp \| email` block format | Tasks 1–2 |
| Backend reads without credentials (warm cache) | Task 3 (`ReadRawFile`) |
| Backend writes via lock → pull → append → push | Task 2 (`AppendComment`) |
| Public reader endpoint returns structured JSON | Task 3 |
| Authenticated post endpoint writes to git | Task 4 |
| Reader renders comment bodies as markdown | Task 5 |

### Type consistency

- `ParsedComment.AuthorName`, `ParsedComment.AuthorEmail`, `ParsedComment.CreatedAt`, `ParsedComment.Body` — used consistently in Tasks 1, 3, 4
- `CommentsFilePath(notePath)` — called identically in Tasks 2, 3, 4
- `Cache.AppendComment(ctx, repo, credJSON, notePath, authorName, authorEmail, body)` — defined in Task 2, called in Task 4
- `Cache.ReadRawFile(repoID, filePath)` — defined in Task 2, called in Tasks 3 and 4

### Placeholder scan

No TBDs, TODOs, or "similar to Task N" references found.

### Edge cases covered

- Empty comments file → `ParseComments("")` returns nil slice → JSON returns `[]`
- File not found → `ReadRawFile` returns `("", nil)` → same as empty
- First comment ever → `AppendComment` creates frontmatter header before appending
- Concurrent comment attempts on same repo → per-repo `sync.Mutex` in `Cache.repoLock` serializes them
