# Open Comments + Comment-Count Badge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let anyone who can read a note read and post comments on it (members, guest-open, and share-link visitors), show a per-note comment count in the reader list, and add a parent-note wikilink to comment files for automatic Obsidian backlinks.

**Architecture:** Comment read + write become gated on `pubNoteAccess` (the note-read check) instead of repo-only access. A new `POST /pub/{repoId}/notes/*/comments` endpoint attributes comments to the logged-in account when a token is present, otherwise to a sanitized display name defaulting to `anonym`. The reader-list count comes from a local walk of `*-comments.md` files. Comment files gain a `[[parent-note]]` wikilink.

**Tech Stack:** Go (chi router, `net/http/httptest`, testify) backend; TypeScript (esbuild bundle, `tsc --noEmit` typecheck) frontend. No frontend test runner — frontend tasks are verified by `npm run build` (which typechecks) plus manual checks.

## Global Constraints

- Comment records are one `### name | RFC3339 | email | sha` block per comment, split on `\n### ` (`gitcache.ParseComments`). Any user-supplied name must never contain `|`, newlines, or a leading `### `.
- `app.js` is `go:embed`'d into the server binary the Dockerfile copies. Shipping requires: commit source → `make build` (in `backend/`) → commit rebuilt `backend/bin/pubobs-linux-*` → push. A source-only commit ships stale JS.
- Backend module path: `github.com/pubobs/backend`. Run backend tests from `backend/` with `go test ./...`.
- Follow existing pub-handler access conventions: return `404 not found` (never `403`) for unauthorized note access, matching `handlePubGetNote`.

---

### Task 1: Sanitize commenter display names

**Files:**
- Modify: `backend/internal/gitcache/comments.go`
- Test: `backend/internal/gitcache/comments_test.go`

**Interfaces:**
- Produces: `func SanitizeCommentName(name string) string` — strips CR/LF, neutralizes `|`, removes a leading `#`/space run, caps at 100 runes; returns `""` for empty/whitespace-only input (caller applies the `anonym` default). `FormatComment` applies it to `name` defensively.

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/gitcache/comments_test.go`:

```go
func TestSanitizeCommentName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Alice", "Alice"},
		{"  Bob  ", "Bob"},
		{"", ""},
		{"   ", ""},
		{"a | b", "a  b"},                       // pipe neutralized to space
		{"line1\nline2", "line1 line2"},          // newline -> space
		{"line1\r\nline2", "line1 line2"},         // CRLF -> single space
		{"### hi", "hi"},                          // leading ### stripped
		{"#### still hi", "still hi"},
		{"###", ""},
	}
	for _, c := range cases {
		if got := SanitizeCommentName(c.in); got != c.want {
			t.Errorf("SanitizeCommentName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// length cap
	long := strings.Repeat("x", 250)
	if got := SanitizeCommentName(long); len([]rune(got)) != 100 {
		t.Errorf("expected 100-rune cap, got %d", len([]rune(got)))
	}
}

func TestFormatComment_sanitizesName(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	// A malicious name must not forge extra pipe-fields or start a new "### "
	// record. FormatComment's own "|" separators are expected; assert on the
	// PARSED name and comment count instead of the raw formatted string.
	formatted := FormatComment("evil | name\n### fake", "", "hi", "", ts)
	got := ParseComments("---\ntype: comments\nnote: foo.md\n---\n\n" + formatted)
	if len(got) != 1 {
		t.Fatalf("expected exactly one parsed comment, got %d: %+v", len(got), got)
	}
	if strings.Contains(got[0].AuthorName, "|") {
		t.Errorf("parsed author name leaks a pipe: %q", got[0].AuthorName)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/gitcache/ -run 'SanitizeCommentName|FormatComment_sanitizes' -v`
Expected: FAIL — `undefined: SanitizeCommentName`.

- [ ] **Step 3: Write minimal implementation**

In `backend/internal/gitcache/comments.go`, add the function and apply it in `FormatComment`:

```go
// SanitizeCommentName makes a user-supplied display name safe to store in the
// pipe-delimited, "\n### "-record comments file: it strips line breaks,
// neutralizes the "|" field separator, removes a leading "#"/space run so the
// name can't start a "### " comment header, trims, and caps length. Returns ""
// for empty/whitespace-only input; callers apply their own default (e.g.
// "anonym").
func SanitizeCommentName(name string) string {
	name = strings.ReplaceAll(name, "\r\n", " ")
	name = strings.NewReplacer("\r", " ", "\n", " ", "|", " ").Replace(name)
	name = strings.TrimLeft(name, "# \t")
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 100 {
		name = strings.TrimSpace(string(r[:100]))
	}
	return name
}
```

Change the first line of `FormatComment` to sanitize the name:

```go
func FormatComment(name, email, body, noteCommitSHA string, ts time.Time) string {
	name = SanitizeCommentName(name)
	return fmt.Sprintf("### %s | %s | %s | %s\n\n%s\n",
		name, ts.UTC().Format(time.RFC3339), email, noteCommitSHA, strings.TrimSpace(body))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/gitcache/ -v`
Expected: PASS (all existing comment tests plus the two new ones).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/gitcache/comments.go backend/internal/gitcache/comments_test.go
git commit -m "feat: sanitize commenter display names for the comments file format"
```

---

### Task 2: Parent-note wikilink in comment files

**Files:**
- Modify: `backend/internal/gitcache/comments.go`
- Modify: `backend/internal/gitcache/cache.go` (`AppendComment`, ~line 479)
- Test: `backend/internal/gitcache/comments_test.go`

**Interfaces:**
- Produces: `func ensureParentLink(content, notePath string) string` — returns `content` with a `Comments on [[<notePath without .md>]]` line in the preamble if not already present (idempotent). `commentsFileHeader` includes that line for newly created files.

- [ ] **Step 1: Write the failing test**

Add to `comments_test.go`:

```go
func TestCommentsHeader_hasParentWikilink(t *testing.T) {
	h := commentsFileHeader("docs/intro.md")
	if !strings.Contains(h, "[[docs/intro]]") {
		t.Errorf("header missing parent wikilink: %q", h)
	}
}

func TestEnsureParentLink_addsWhenMissingIdempotently(t *testing.T) {
	legacy := "---\ntype: comments\nnote: docs/intro.md\n---\n\n### Alice | 2026-01-01T00:00:00Z | a@x.com | sha\n\nhi\n"
	once := ensureParentLink(legacy, "docs/intro.md")
	if !strings.Contains(once, "[[docs/intro]]") {
		t.Fatalf("expected wikilink added: %q", once)
	}
	twice := ensureParentLink(once, "docs/intro.md")
	if once != twice {
		t.Errorf("ensureParentLink not idempotent:\n%q\n%q", once, twice)
	}
	// must not corrupt existing comment records
	if got := ParseComments(once); len(got) != 1 {
		t.Errorf("expected 1 comment preserved, got %d", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/gitcache/ -run 'ParentWikilink|EnsureParentLink' -v`
Expected: FAIL — `undefined: ensureParentLink` and header assertion fails.

- [ ] **Step 3: Write minimal implementation**

In `comments.go`, update `commentsFileHeader` and add `ensureParentLink`:

```go
func commentsFileHeader(notePath string) string {
	link := "[[" + strings.TrimSuffix(notePath, ".md") + "]]"
	return fmt.Sprintf("---\ntype: comments\nnote: %s\n---\n\nComments on %s\n\n", notePath, link)
}

// ensureParentLink inserts a "Comments on [[parent]]" line into an existing
// comments file that predates the wikilink (so Obsidian surfaces the comments
// file as a backlink on the note). Idempotent: a no-op if the link is already
// present. The line goes in the preamble, before the first "### " record.
func ensureParentLink(content, notePath string) string {
	link := "[[" + strings.TrimSuffix(notePath, ".md") + "]]"
	if strings.Contains(content, link) {
		return content
	}
	line := "Comments on " + link + "\n\n"
	if strings.HasPrefix(content, "---\n") {
		if idx := strings.Index(content[4:], "\n---\n"); idx != -1 {
			end := 4 + idx + len("\n---\n")
			rest := strings.TrimLeft(content[end:], "\n")
			return content[:end] + "\n" + line + rest
		}
	}
	return line + content
}
```

In `cache.go` `AppendComment`, change the content-building branch so existing files get backfilled:

```go
	var content string
	if len(existing) == 0 {
		content = commentsFileHeader(notePath) + block
	} else {
		content = ensureParentLink(string(existing), notePath) + block
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/gitcache/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/gitcache/comments.go backend/internal/gitcache/cache.go backend/internal/gitcache/comments_test.go
git commit -m "feat: add parent-note wikilink to comment files for Obsidian backlinks"
```

---

### Task 3: Open comment READ to all note-readers

**Files:**
- Modify: `backend/internal/api/pub.go` (`handlePubGetNote` dispatcher, ~lines 195–206)
- Test: `backend/internal/api/pub_test.go`

**Interfaces:**
- Consumes: `pubNoteAccess(r, deps, repo, note)` (pub.go:94), `deps.Store.GetNote` (returns `*model.Note, error`).

- [ ] **Step 1: Write the failing test**

Add to `pub_test.go` (mirrors `setupPubAccessTest`, which seeds repo `r1`, user `u1` reader, note `docs/intro.md`, shared with `key`, and returns `cacheDir` via `newTestDepsForPub`). Write a comments file directly into the local clone dir so the read returns content:

```go
func writeCommentsFile(t *testing.T, cacheDir, repoID, notePath, contents string) {
	t.Helper()
	p := filepath.Join(cacheDir, repoID, gitcache.CommentsFilePath(notePath))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(contents), 0o644))
}

func TestPubComments_shareLinkVisitorCanReadWithKey(t *testing.T) {
	deps, cacheDir := newTestDepsForPub(t)
	ctx := context.Background()
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run TestPubComments_shareLinkVisitorCanReadWithKey -v`
Expected: FAIL — the `?key=` request returns 404 (current code gates on `pubRepoAccess`, which a share-link visitor fails).

- [ ] **Step 3: Write minimal implementation**

In `pub.go` `handlePubGetNote`, replace the `/comments` dispatch block (currently gating on `pubRepoAccess`) with note-scoped access:

```go
		if strings.HasSuffix(notePath, "/comments") {
			// Comment read now follows note-read access (pubNoteAccess): repo
			// members, guest-open visitors, AND share-link visitors with a
			// valid ?key= can read comments — see the open-comments design.
			notePath = strings.TrimSuffix(notePath, "/comments")
			note, _ := deps.Store.GetNote(r.Context(), repoID, notePath)
			if note == nil || !pubNoteAccess(r, deps, repo, note) {
				writeError(w, http.StatusNotFound, "not found")
				return
			}
			handlePubComments(w, r, deps, repoID, notePath)
			return
		}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/api/ -run 'TestPubComments|TestHandlePub' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/pub.go backend/internal/api/pub_test.go
git commit -m "feat: allow note-readers (incl. share-link) to read comments"
```

---

### Task 4: Public comment WRITE endpoint

**Files:**
- Modify: `backend/internal/api/pub.go` (new handler + `resolveCommenter` helper)
- Modify: `backend/internal/api/router.go` (register POST route, after line 132)
- Test: `backend/internal/api/pub_test.go`

**Interfaces:**
- Consumes: `pubNoteAccess`, `decryptCreds(deps, repo.EncryptedCreds)` (used in wiki.go:196), `deps.Store.GetUserByID`, `deps.Cache.AppendComment(ctx, repo, credJSON, notePath, name, email, body, sha)`, `gitcache.SanitizeCommentName` (Task 1), `auth.VerifyAccessToken(deps.Config.SecretKey, token)`.
- Produces: `handlePubPostComment(deps *Deps) http.HandlerFunc`; `resolveCommenter(r *http.Request, deps *Deps, clientName string) (name, email string)`.
- Test helpers `newBareRepo(t)`, `seedBareRepo(t, url)` exist in the api_test package (used by `wiki_test.go:TestHandleAddComment`).

- [ ] **Step 1: Write the failing test**

Add to `pub_test.go` (mirrors `wiki_test.go:TestHandleAddComment`, which points `r1` at a real bare repo so `AppendComment` can push):

```go
func TestPubPostComment_anonymousUsesDisplayNameThenAnonym(t *testing.T) {
	deps, _ := newTestDepsForPub(t)
	ctx := context.Background()
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)
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

func TestPubPostComment_deniedWithoutAccess(t *testing.T) {
	deps, _ := newTestDepsForPub(t)
	ctx := context.Background()
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run TestPubPostComment -v`
Expected: FAIL — POST to `/pub/...` has no route (chi returns 405/404 and no comment is stored).

- [ ] **Step 3: Write minimal implementation**

In `pub.go`, add the handler and helper:

```go
// resolveCommenter decides how to attribute a comment. A caller holding a valid
// bearer token comments under their account identity (client-supplied name is
// ignored — no spoofing). Everyone else is anonymous: the sanitized client name,
// or "anonym" when blank.
func resolveCommenter(r *http.Request, deps *Deps, clientName string) (name, email string) {
	tokenStr := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tokenStr != "" {
		if claims, err := auth.VerifyAccessToken(deps.Config.SecretKey, tokenStr); err == nil {
			if user, uerr := deps.Store.GetUserByID(r.Context(), claims.UserID); uerr == nil && user != nil {
				return user.Name, user.Email
			}
		}
	}
	name = gitcache.SanitizeCommentName(clientName)
	if name == "" {
		name = "anonym"
	}
	return name, ""
}

func handlePubPostComment(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := chi.URLParam(r, "repoId")
		notePath := chi.URLParam(r, "*")
		if !strings.HasSuffix(notePath, "/comments") {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		notePath = strings.TrimSuffix(notePath, "/comments")

		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}
		note, _ := deps.Store.GetNote(r.Context(), repoID, notePath)
		if note == nil || !pubNoteAccess(r, deps, repo, note) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}

		var body struct {
			Body          string `json:"body"`
			NoteCommitSHA string `json:"note_commit_sha"`
			AuthorName    string `json:"author_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Body) == "" {
			writeError(w, http.StatusBadRequest, "body required")
			return
		}

		name, email := resolveCommenter(r, deps, body.AuthorName)

		credJSON, err := decryptCreds(deps, repo.EncryptedCreds)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cred decrypt failed")
			return
		}
		if err := deps.Cache.AppendComment(r.Context(), repo, credJSON, notePath, name, email, body.Body, body.NoteCommitSHA); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save comment")
			return
		}
		w.WriteHeader(http.StatusCreated)
	}
}
```

In `router.go`, after the pub GET routes (line 132 `r.Get("/pub/{repoId}/notes/*", ...)`), add:

```go
		r.Post("/pub/{repoId}/notes/*", handlePubPostComment(deps))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/api/ -run 'TestPubPostComment|TestPubComments' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/pub.go backend/internal/api/router.go backend/internal/api/pub_test.go
git commit -m "feat: public comment write endpoint gated on note access"
```

---

### Task 5: Comment count in the notes list

**Files:**
- Modify: `backend/internal/gitcache/cache.go` (new `CommentCounts` method)
- Modify: `backend/internal/api/pub.go` (`handlePubListNotes`, ~lines 133–168)
- Test: `backend/internal/gitcache/cache_test.go`, `backend/internal/api/pub_test.go`

**Interfaces:**
- Produces: `func (c *Cache) CommentCounts(repoID string) (map[string]int, error)` — maps note path (`.md`) → number of comments, from a local walk of `*-comments.md`; empty map when the repo isn't cloned. `noteItem` gains `CommentCount int json:"comment_count"`.

- [ ] **Step 1: Write the failing test**

Add to `cache_test.go`:

```go
func TestCache_CommentCounts(t *testing.T) {
	cacheDir := t.TempDir()
	cache := gitcache.NewCache(cacheDir)
	base := filepath.Join(cacheDir, "r1", "docs")
	require.NoError(t, os.MkdirAll(base, 0o755))
	// two comments on docs/intro.md
	require.NoError(t, os.WriteFile(filepath.Join(base, "intro-comments.md"),
		[]byte("---\ntype: comments\n---\n\n### A | 2026-01-01T00:00:00Z |  | s\n\nx\n### B | 2026-01-02T00:00:00Z |  | s\n\ny\n"), 0o644))

	counts, err := cache.CommentCounts("r1")
	require.NoError(t, err)
	require.Equal(t, 2, counts["docs/intro.md"])
	require.Equal(t, 0, counts["docs/other.md"])

	// missing repo -> empty map, no error
	empty, err := cache.CommentCounts("nope")
	require.NoError(t, err)
	require.Empty(t, empty)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/gitcache/ -run TestCache_CommentCounts -v`
Expected: FAIL — `undefined: cache.CommentCounts`.

- [ ] **Step 3: Write minimal implementation**

In `cache.go` (imports `io/fs`, `os`, `path/filepath`, `strings` are already present — WalkDir is used at ~line 276), add:

```go
// CommentCounts walks the repo's local clone for "*-comments.md" files and
// returns a map of note path (".md") -> number of comments. Only files that
// exist are read, so cost scales with the number of commented notes. Returns
// an empty map (no error) when the repo isn't cloned locally yet.
func (c *Cache) CommentCounts(repoID string) (map[string]int, error) {
	counts := map[string]int{}
	root := c.repoDir(repoID)
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return counts, nil
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "-comments.md") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		notePath := strings.TrimSuffix(filepath.ToSlash(rel), "-comments.md") + ".md"
		counts[notePath] = len(ParseComments(string(data)))
		return nil
	})
	return counts, err
}
```

In `pub.go` `handlePubListNotes`, add the count field to `noteItem` and populate it. Add `CommentCount int json:"comment_count"` to the struct, then before the loop:

```go
		commentCounts, _ := deps.Cache.CommentCounts(repoID)
```

and in the `items = append(...)` literal add:

```go
			CommentCount: commentCounts[n.Path],
```

- [ ] **Step 4: Write the list handler test + run**

Add to `pub_test.go`:

```go
func TestPubListNotes_includesCommentCount(t *testing.T) {
	deps, cacheDir := newTestDepsForPub(t)
	ctx := context.Background()
	deps.Store.CreateRepo(ctx, "r1", "R", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", true))
	note, err := deps.Store.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "<h1>Hi</h1>", "{}", "u1", "sha1"))
	writeCommentsFile(t, cacheDir, "r1", "docs/intro.md",
		"---\ntype: comments\n---\n\n### A | 2026-01-01T00:00:00Z |  | s\n\nx\n")

	req := httptest.NewRequest("GET", "/pub/r1", nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "\"comment_count\":1")
}
```

Run: `cd backend && go test ./internal/gitcache/ ./internal/api/ -run 'CommentCounts|includesCommentCount' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/gitcache/cache.go backend/internal/gitcache/cache_test.go backend/internal/api/pub.go backend/internal/api/pub_test.go
git commit -m "feat: include per-note comment_count in the pub notes list"
```

---

### Task 6: Frontend API — key passthrough + public comment write

**Files:**
- Modify: `frontend/src/api.ts`

**Interfaces:**
- Produces: `PubNote.comment_count: number`; `pubListComments(repoId, notePath, key?)`; `pubAddComment(repoId, notePath, body, noteCommitSha, opts?: { authorName?: string; key?: string })`; `pubFetch` extended to accept an optional `RequestInit`.

- [ ] **Step 1: Extend `pubFetch` to accept init**

Replace `pubFetch` (lines 359–365):

```ts
async function pubFetch(input: string, init?: RequestInit): Promise<Response> {
  const t = tokenStore.get();
  const headers = new Headers(init?.headers);
  if (t && !tokenStore.isExpired()) headers.set('Authorization', `Bearer ${t.accessToken}`);
  return fetchWithTimeout(input, { ...init, headers });
}
```

- [ ] **Step 2: Add `comment_count` to `PubNote`**

In the `PubNote` interface (line 322), add:

```ts
  comment_count: number;
```

- [ ] **Step 3: Add key passthrough to `pubListComments` and a public `pubAddComment`**

Replace `pubListComments` and add `pubAddComment`:

```ts
export async function pubListComments(repoId: string, notePath: string, key?: string): Promise<PubComment[]> {
  const qs = key ? `?key=${encodeURIComponent(key)}` : '';
  return json(await pubFetch(`/pub/${repoId}/notes/${encodeNotePath(notePath)}/comments${qs}`));
}

export async function pubAddComment(
  repoId: string,
  notePath: string,
  body: string,
  noteCommitSha: string,
  opts?: { authorName?: string; key?: string },
): Promise<void> {
  const qs = opts?.key ? `?key=${encodeURIComponent(opts.key)}` : '';
  const resp = await pubFetch(`/pub/${repoId}/notes/${encodeNotePath(notePath)}/comments${qs}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ body, note_commit_sha: noteCommitSha, author_name: opts?.authorName ?? '' }),
  });
  if (!resp.ok) throw new Error((await resp.text().catch(() => '')) || 'failed to post comment');
}
```

- [ ] **Step 4: Typecheck**

Run: `cd frontend && npm run build`
Expected: builds with no TypeScript errors (existing `addComment`/`PubNote` consumers still compile — `comment_count` is only read where added in Task 8).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/api.ts
git commit -m "feat: frontend API for public comment write and key passthrough"
```

---

### Task 7: Frontend reader-note — open comment form to everyone

**Files:**
- Modify: `frontend/src/views/reader-note.ts`

**Interfaces:**
- Consumes: `pubAddComment`, `pubListComments(…, key?)` (Task 6). `effectiveKey` is available in `readerNoteView` (line 69).

- [ ] **Step 1: Update imports**

In the top import from `../api` (lines 1–4), add `pubAddComment` and drop the old `addComment`:

```ts
import {
  pubGetNote, pubListComments, pubAddComment, mintShareLink, revokeShareLink,
  type PubNoteDetail, type PubComment, type ShareMode,
} from '../api';
```

- [ ] **Step 2: Thread the share key into the comments section**

At the call site (lines 177–181), pass `effectiveKey`:

```ts
  const commentsSection = buildCommentsSection(repoId, notePath, note, effectiveKey);
  wrap.appendChild(commentsSection);
  const commentsList = commentsSection.querySelector(`#comments-list-${note.id}`) as HTMLElement;
  loadComments(repoId, notePath, commentsList, effectiveKey);
```

- [ ] **Step 3: Rewrite `buildCommentsSection` to always show the form**

Replace `buildCommentsSection`'s signature and the `formWrap` block (lines 287–349) so the form always renders, with an `anonym`-prefilled name field for anonymous users:

```ts
function buildCommentsSection(repoId: string, notePath: string, note: PubNoteDetail, key?: string): HTMLElement {
  const section = document.createElement('section');
  section.style.cssText = 'margin-top:48px;padding-top:24px;border-top:1px solid var(--r-border)';

  const h = document.createElement('h2');
  h.className = 'r-section-heading';
  h.style.marginBottom = '16px';
  h.textContent = 'Comments';
  section.appendChild(h);

  const list = document.createElement('div');
  list.id = `comments-list-${note.id}`;
  list.className = 'r-faint';
  list.style.fontSize = '0.875rem';
  list.appendChild(loadingRow('Loading comments…', 14));
  section.appendChild(list);

  const formWrap = document.createElement('div');
  formWrap.style.marginTop = '20px';

  const anonymous = !isAuthenticated();
  let nameInput: HTMLInputElement | null = null;
  if (anonymous) {
    nameInput = document.createElement('input');
    nameInput.type = 'text';
    nameInput.value = 'anonym';
    nameInput.placeholder = 'Your name';
    nameInput.className = 'r-form-input';
    nameInput.style.cssText = 'min-height:auto;margin-bottom:8px';
    formWrap.appendChild(nameInput);
  }

  const ta = document.createElement('textarea');
  ta.placeholder = 'Write a comment…';
  ta.className = 'r-form-input';
  formWrap.appendChild(ta);

  const row = document.createElement('div');
  row.style.cssText = 'display:flex;gap:8px;align-items:center;margin-top:8px';

  const btn = document.createElement('button');
  btn.textContent = 'Post comment';
  btn.className = 'r-btn-primary';
  row.appendChild(btn);

  const err = document.createElement('span');
  err.className = 'r-error';
  err.style.fontSize = '0.8rem';
  row.appendChild(err);
  formWrap.appendChild(row);

  btn.addEventListener('click', async () => {
    const body = ta.value.trim();
    if (!body) return;
    btn.disabled = true;
    err.textContent = '';
    try {
      const authorName = anonymous ? (nameInput!.value.trim() || 'anonym') : undefined;
      await pubAddComment(repoId, notePath, body, note.git_commit_sha ?? '', { authorName, key });
      ta.value = '';
      await loadComments(repoId, notePath, list, key);
    } catch (e: unknown) {
      err.textContent = e instanceof Error ? e.message : String(e);
    } finally {
      btn.disabled = false;
    }
  });

  section.appendChild(formWrap);
  return section;
}
```

- [ ] **Step 4: Pass the key through `loadComments`**

Change `loadComments` (line 354) signature and its `pubListComments` call:

```ts
async function loadComments(repoId: string, notePath: string, list: HTMLElement, key?: string): Promise<void> {
  let comments: PubComment[];
  try {
    comments = await pubListComments(repoId, notePath, key);
```

(The rest of `loadComments` is unchanged. Any other internal `loadComments(repoId, notePath, list)` call — e.g. after posting — must pass `key`; the Step 3 handler already does.)

- [ ] **Step 5: Typecheck**

Run: `cd frontend && npm run build`
Expected: no TypeScript errors. Confirm `isAuthenticated` is still imported (line 5) and `addComment` is no longer referenced anywhere.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/views/reader-note.ts
git commit -m "feat: show comment form to all note-readers with anonymous name field"
```

---

### Task 8: Frontend reader-list — comment count badge

**Files:**
- Modify: `frontend/src/views/reader-list.ts`

**Interfaces:**
- Consumes: `PubNote.comment_count` (Task 6).

- [ ] **Step 1: Add the badge in the note row**

In `renderNoteList`, in the `right` block (after the `dateSpan` append, before the `canManageSharing` check, ~line 403), add:

```ts
      if (note.comment_count > 0) {
        const commentBadge = document.createElement('span');
        commentBadge.style.cssText = 'color:var(--r-text-faint);font-size:0.75rem;white-space:nowrap';
        commentBadge.textContent = `💬 ${note.comment_count}`;
        commentBadge.title = `${note.comment_count} comment${note.comment_count !== 1 ? 's' : ''}`;
        right.appendChild(commentBadge);
      }
```

- [ ] **Step 2: Typecheck**

Run: `cd frontend && npm run build`
Expected: no TypeScript errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/views/reader-list.ts
git commit -m "feat: show comment count badge on reader note list rows"
```

---

### Task 9: Full verification + deploy build

**Files:**
- Modify: `backend/frontend/static/app.js` (regenerated), `backend/bin/pubobs-linux-amd64`, `backend/bin/pubobs-linux-arm64` (rebuilt)

- [ ] **Step 1: Run the full backend test suite**

Run: `cd backend && go test ./...`
Expected: PASS (all packages).

- [ ] **Step 2: Build the frontend (typecheck + bundle)**

Run: `cd frontend && npm run build`
Expected: emits `../backend/frontend/static/app.js` with no errors.

- [ ] **Step 3: Manual end-to-end check (record result)**

Drive the reader in a browser (see the `run`/`verify` skills). Verify:
- Reader list shows `💬 N` on notes that have comments.
- Opening a shared note as an anonymous visitor (share link with `?key=`) shows existing comments and a comment form with an `anonym`-prefilled name field.
- Posting as anonymous (custom name and default `anonym`) succeeds and the comment appears.
- Logged-in posting attributes to the account (name field hidden).
- In Obsidian, the parent note lists its `-comments.md` file under backlinks after a comment is added.

- [ ] **Step 4: Commit the regenerated frontend bundle (source commit)**

```bash
git add backend/frontend/static/app.js
git commit -m "build: rebuild frontend bundle for open-comments feature"
```

- [ ] **Step 5: Rebuild and commit the deployed binaries**

```bash
cd backend && make build
cd /Volumes/docvol/pubobs
git add backend/bin/pubobs-linux-amd64 backend/bin/pubobs-linux-arm64
git commit -m "build: rebuild deployed binaries with open-comments feature"
```

- [ ] **Step 6: Verify the binary embeds the new bundle + push**

```bash
strings backend/bin/pubobs-linux-amd64 | grep -c "comment_count"   # expect >= 1
git push origin main
```

Then run `install.sh --update` on the VPS and hard-reload the reader.
