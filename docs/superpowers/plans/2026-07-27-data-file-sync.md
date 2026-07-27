# Data File Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sync `.base`, `.csv`, `.json` and `.yaml` files bidirectionally between a PubObs repo and the Obsidian vault, without them becoming notes.

**Architecture:** A third file category ("data files") alongside notes and assets. The backend gains `Cache.ListDataFiles` plus a `GET /api/repos/{id}/data-files` endpoint for the pull direction, and a `data_files` field on the sync payload for the push direction. Data files are written into the same git commit as notes but never enter the note pipeline (no note row, note key, snapshot or render blob), so they never appear in the reader or notes list. A shared `validRepoPath` guard is added to every client-supplied path the sync handler writes.

**Tech Stack:** Go 1.25 (chi router, testify), TypeScript (esbuild, jest), Obsidian plugin API.

**Spec:** [docs/superpowers/specs/2026-07-27-data-file-sync-design.md](../specs/2026-07-27-data-file-sync-design.md)

## Global Constraints

- Extension allowlist default: `base, csv, json, yaml, yml`. Per-file cap default 5 MB (plugin setting `dataFileMaxMB`), hard server ceiling `gitcache.MaxDataFileBytes = 25 << 20`.
- `md` is never a valid data-file extension — notes have their own path.
- `_pubobs/` paths are never data files.
- `ListFiles` and `ListFilePaths` keep their `*.md` glob and must not be modified. Note ingestion, admin import and `reconcileNotesWithGit` behave exactly as before.
- Data-file content is read from the **working tree** (`os.ReadFile`), never through `GitRunner.ReadFile` — `runCtx` returns `strings.TrimSpace(out.String())` ([git.go:362](../../../backend/internal/gitcache/git.go)), which would strip trailing newlines and make every round-trip dirty the repo.
- Backend tests run with `cd backend && go test ./...`; plugin tests with `cd obsidian-plugin && npx jest`.
- Commit messages follow the repo's existing convention (`feat:`, `fix:`, `test:`, `build:`).
- Do NOT run `make build` or commit binaries except in Task 8.

## Task Order Rationale

Task 6 (push) deliberately precedes Task 7 (pull). The spec's deletion trap —
data files present in `syncHashes`/`pullSHAs` but absent from the push phase's
`currentRepoPaths`, causing every data file to be reported in `deleted_paths`
and removed from the repo — only exists if the pull side lands first. Building
the push enumeration first means `currentRepoPaths` already contains data files
before any code writes a data-file entry into the shared maps, so the dangerous
intermediate state never exists on the branch.

---

### Task 1: Path traversal guard

**Files:**
- Create: `backend/internal/api/repopath.go`
- Create: `backend/internal/api/repopath_test.go`
- Modify: `backend/internal/api/sync.go` (after the `readJSON` block, ~line 70)

**Interfaces:**
- Consumes: nothing
- Produces: `func validRepoPath(p string) bool` — used by Task 4 to validate `data_files` paths.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/api/repopath_test.go`:

```go
package api

import "testing"

func TestValidRepoPath(t *testing.T) {
	valid := []string{
		"note.md",
		"notes/sub/note.md",
		"attachments/diagram.png",
		"данные/файл.csv",
		"a b/c d.json",
		"_pubobs/obsidian.css",
	}
	for _, p := range valid {
		if !validRepoPath(p) {
			t.Errorf("validRepoPath(%q) = false, want true", p)
		}
	}

	invalid := []string{
		"",                         // empty
		"/etc/passwd",              // absolute
		"../outside.md",            // escapes the clone
		"notes/../../outside.md",   // escapes after descending
		"..",                       // bare parent
		"./notes/a.md",             // dot segment
		"notes//a.md",              // empty segment
		".git/config",              // git internals
		"notes/.git/hooks/pre-push",
		"C:\\Windows\\x.md",        // windows absolute
		"notes\\a.md",              // backslash separator
		"notes/a\x00.md",           // NUL byte
	}
	for _, p := range invalid {
		if validRepoPath(p) {
			t.Errorf("validRepoPath(%q) = true, want false", p)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run TestValidRepoPath`
Expected: FAIL — `undefined: validRepoPath`

- [ ] **Step 3: Write the implementation**

Create `backend/internal/api/repopath.go`:

```go
package api

import "strings"

// maxRepoPathLen bounds a client-supplied path. Well beyond any real vault
// path, but short enough that a pathological payload can't be used to build
// enormous filesystem paths.
const maxRepoPathLen = 1024

// validRepoPath reports whether a client-supplied, repo-relative path is safe
// to join onto a repo's local clone directory.
//
// Every path in a sync payload is passed to filepath.Join against the clone
// dir and then written or removed (gitcache.Sync). filepath.Join *cleans* its
// result, so a path like "../../../etc/cron.d/x" resolves to an absolute path
// outside the clone entirely — and the backend container runs as root. Nothing
// upstream constrains these paths: they arrive verbatim from the plugin's JSON.
//
// Rejecting rather than sanitizing is deliberate. A silently-rewritten path
// would write the user's note to a location they didn't ask for; a rejection
// tells them their payload was wrong.
func validRepoPath(p string) bool {
	if p == "" || len(p) > maxRepoPathLen {
		return false
	}
	if strings.ContainsRune(p, 0) {
		return false
	}
	// Backslashes are never a separator here (repo paths are always
	// slash-separated) and would otherwise smuggle traversal past the
	// segment checks below on a Windows host.
	if strings.Contains(p, `\`) {
		return false
	}
	if strings.HasPrefix(p, "/") {
		return false
	}
	// Windows drive-absolute ("C:\...") and drive-relative ("C:x") paths.
	if len(p) >= 2 && p[1] == ':' {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".", "..", ".git":
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/api/ -run TestValidRepoPath -v`
Expected: PASS

- [ ] **Step 5: Write the failing handler test**

Append to `backend/internal/api/sync_pusherr_test.go`:

```go
// Every path in a sync payload is joined onto the repo's clone dir and then
// written or removed. A payload that escapes the clone must be refused
// outright rather than sanitized — see validRepoPath.
func TestHandleSync_rejectsTraversalPaths(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	payloads := map[string]string{
		"file":    `{"files":[{"path":"../../escaped.md","md_content":"x"}]}`,
		"asset":   `{"files":[],"assets":[{"path":"../../escaped.png","content":"eA=="}]}`,
		"deleted": `{"files":[],"deleted_paths":["../../escaped.md"]}`,
		"gitdir":  `{"files":[{"path":".git/hooks/pre-push","md_content":"x"}]}`,
	}
	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			deps := newSyncableRepo(t, bareURL)
			req := httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(payload))
			req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
			rr := httptest.NewRecorder()
			api.BuildRouter(deps).ServeHTTP(rr, req)

			require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
			require.Contains(t, rr.Body.String(), "invalid path")
		})
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run TestHandleSync_rejectsTraversalPaths`
Expected: FAIL — the handler returns 200, not 400

- [ ] **Step 7: Wire the guard into handleSync**

In `backend/internal/api/sync.go`, immediately after the `readJSON` error block (the `writeError(w, http.StatusBadRequest, "invalid JSON")` return), insert:

```go
		// Validate every client-supplied path before anything is written.
		// One rejection fails the whole request: a sync is a single commit,
		// and silently dropping one bad path would produce a commit the
		// client believes is complete but isn't.
		for _, f := range payload.Files {
			if !validRepoPath(f.Path) {
				writeError(w, http.StatusBadRequest, "invalid path: "+f.Path)
				return
			}
		}
		for _, a := range payload.Assets {
			if !validRepoPath(a.Path) {
				writeError(w, http.StatusBadRequest, "invalid path: "+a.Path)
				return
			}
		}
		for _, p := range payload.DeletedPaths {
			if !validRepoPath(p) {
				writeError(w, http.StatusBadRequest, "invalid path: "+p)
				return
			}
		}
```

- [ ] **Step 8: Run the full api suite**

Run: `cd backend && go test ./internal/api/`
Expected: PASS (all pre-existing tests still pass — legitimate paths are unaffected)

- [ ] **Step 9: Commit**

```bash
git add backend/internal/api/repopath.go backend/internal/api/repopath_test.go \
        backend/internal/api/sync.go backend/internal/api/sync_pusherr_test.go
git commit -m "fix: reject path-traversal in sync payload paths"
```

---

### Task 2: List data files from a clone

**Files:**
- Modify: `backend/internal/gitcache/git.go` (add `ListFilesByExt`, extract `splitNUL` from `ListFiles`)
- Create: `backend/internal/gitcache/datafiles.go`
- Create: `backend/internal/gitcache/datafiles_test.go`

**Interfaces:**
- Consumes: `GitRunner.run`, `Cache.getOrClone`, `Cache.repoLock`, `GitRunner.BlobSHA` (all existing)
- Produces:
  - `const MaxDataFileBytes = 25 << 20`
  - `type DataFile struct { Path, Content, SHA string; Size int64 }` (JSON: `path`, `content`, `sha`, `size`)
  - `type SkippedDataFile struct { Path string; Size int64; Reason string }` (JSON: `path`, `size`, `reason`; reason is `"too_large"` or `"not_utf8"`)
  - `type DataFileList struct { Files []DataFile; Skipped []SkippedDataFile }` (JSON: `files`, `skipped`)
  - `func (c *Cache) ListDataFiles(ctx context.Context, repo *model.Repo, credJSON string, exts []string, maxBytes int64) (DataFileList, error)`
  - `func (g *GitRunner) ListFilesByExt(dir string, exts []string) ([]string, error)`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/gitcache/datafiles_test.go`:

```go
package gitcache_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/model"
	"github.com/stretchr/testify/require"
)

// seedDataRepo builds a bare remote containing a mix of notes, data files,
// metadata and a binary, and returns its URL.
func seedDataRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	bare := filepath.Join(dir, "remote.git")
	require.NoError(t, exec.Command("git", "init", "--bare", bare).Run())

	work := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@x.com",
			"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@x.com")
		require.NoError(t, cmd.Run(), strings.Join(args, " "))
	}
	run("clone", bare, ".")
	for path, content := range files {
		full := filepath.Join(work, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	run("add", ".")
	run("commit", "-m", "seed")
	run("push", "origin", "HEAD:main")
	return bare
}

func TestListDataFiles(t *testing.T) {
	bare := seedDataRepo(t, map[string]string{
		"note.md":              "# Note",
		"data/table.csv":       "a,b\n1,2\n",
		"data/config.json":     "{\"k\":1}\n",
		"views/tasks.base":     "filters:\n  - done\n",
		"stack.yaml":           "key: value\n",
		"_pubobs/obsidian.css": "body{}",
		"readme.txt":           "not requested",
	})
	repo := &model.Repo{ID: "r1", RemoteURL: bare, DefaultBranch: "main"}
	c := gitcache.NewCache(t.TempDir())

	got, err := c.ListDataFiles(context.Background(), repo, "",
		[]string{"csv", "json", "base", "yaml", "yml"}, 5<<20)
	require.NoError(t, err)

	paths := map[string]string{}
	for _, f := range got.Files {
		paths[f.Path] = f.Content
	}
	require.Len(t, paths, 4)
	require.Contains(t, paths, "data/table.csv")
	require.Contains(t, paths, "data/config.json")
	require.Contains(t, paths, "views/tasks.base")
	require.Contains(t, paths, "stack.yaml")
	require.NotContains(t, paths, "note.md", "notes are not data files")
	require.NotContains(t, paths, "readme.txt", "unrequested extensions are excluded")
	require.NotContains(t, paths, "_pubobs/obsidian.css", "_pubobs metadata is never a data file")

	require.Equal(t, "a,b\n1,2\n", paths["data/table.csv"],
		"content must be byte-exact, including the trailing newline")

	for _, f := range got.Files {
		require.NotEmpty(t, f.SHA)
		require.Greater(t, f.Size, int64(0))
	}
}

func TestListDataFiles_skipsOversized(t *testing.T) {
	big := strings.Repeat("x", 2048)
	bare := seedDataRepo(t, map[string]string{
		"small.csv": "a,b\n",
		"big.csv":   big,
	})
	repo := &model.Repo{ID: "r1", RemoteURL: bare, DefaultBranch: "main"}
	c := gitcache.NewCache(t.TempDir())

	got, err := c.ListDataFiles(context.Background(), repo, "", []string{"csv"}, 1024)
	require.NoError(t, err)

	require.Len(t, got.Files, 1)
	require.Equal(t, "small.csv", got.Files[0].Path)
	require.Len(t, got.Skipped, 1)
	require.Equal(t, "big.csv", got.Skipped[0].Path)
	require.Equal(t, "too_large", got.Skipped[0].Reason)
	require.Equal(t, int64(2048), got.Skipped[0].Size)
}

func TestListDataFiles_skipsNonUTF8(t *testing.T) {
	bare := seedDataRepo(t, map[string]string{
		"ok.csv":  "a,b\n",
		"bad.csv": "\xff\xfe\x00binary",
	})
	repo := &model.Repo{ID: "r1", RemoteURL: bare, DefaultBranch: "main"}
	c := gitcache.NewCache(t.TempDir())

	got, err := c.ListDataFiles(context.Background(), repo, "", []string{"csv"}, 5<<20)
	require.NoError(t, err)

	require.Len(t, got.Files, 1)
	require.Equal(t, "ok.csv", got.Files[0].Path)
	require.Len(t, got.Skipped, 1)
	require.Equal(t, "bad.csv", got.Skipped[0].Path)
	require.Equal(t, "not_utf8", got.Skipped[0].Reason)
}

// maxBytes is clamped to the hard ceiling: a client cannot raise its own limit
// past what the server is willing to read into memory.
func TestListDataFiles_clampsMaxBytes(t *testing.T) {
	bare := seedDataRepo(t, map[string]string{"a.csv": "x\n"})
	repo := &model.Repo{ID: "r1", RemoteURL: bare, DefaultBranch: "main"}
	c := gitcache.NewCache(t.TempDir())

	for _, maxBytes := range []int64{0, -1, gitcache.MaxDataFileBytes * 10} {
		got, err := c.ListDataFiles(context.Background(), repo, "", []string{"csv"}, maxBytes)
		require.NoError(t, err)
		require.Len(t, got.Files, 1, "maxBytes=%d must fall back to the ceiling", maxBytes)
	}
}

func TestListDataFiles_noExtensionsReturnsNothing(t *testing.T) {
	bare := seedDataRepo(t, map[string]string{"a.csv": "x\n"})
	repo := &model.Repo{ID: "r1", RemoteURL: bare, DefaultBranch: "main"}
	c := gitcache.NewCache(t.TempDir())

	got, err := c.ListDataFiles(context.Background(), repo, "", nil, 5<<20)
	require.NoError(t, err)
	require.Empty(t, got.Files)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/gitcache/ -run TestListDataFiles`
Expected: FAIL — `undefined: c.ListDataFiles`, `undefined: gitcache.MaxDataFileBytes`

- [ ] **Step 3: Add `ListFilesByExt` to GitRunner**

In `backend/internal/gitcache/git.go`, replace the body of `ListFiles` (which currently parses NUL-delimited output inline) with a call to a shared helper, and add the new lister. Keep `ListFiles`'s existing doc comment as-is:

```go
func (g *GitRunner) ListFiles(dir string) ([]string, error) {
	out, err := g.run(dir, "ls-files", "-z", "--", "*.md")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// ListFilesByExt returns tracked file paths whose extension is in exts (given
// without a leading dot, e.g. "csv"). Uses the same -z NUL-delimited output as
// ListFiles, for the same reason: git otherwise C-quotes and octal-escapes any
// path containing non-ASCII bytes.
func (g *GitRunner) ListFilesByExt(dir string, exts []string) ([]string, error) {
	if len(exts) == 0 {
		return nil, nil
	}
	args := []string{"ls-files", "-z", "--"}
	for _, e := range exts {
		args = append(args, "*."+e)
	}
	out, err := g.run(dir, args...)
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// splitNUL parses git's -z output into paths, dropping the trailing empty
// element left by the final NUL terminator.
func splitNUL(out string) []string {
	out = strings.TrimSuffix(out, "\x00")
	if out == "" {
		return nil
	}
	parts := strings.Split(out, "\x00")
	files := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			files = append(files, p)
		}
	}
	return files
}
```

- [ ] **Step 4: Write the Cache method**

Create `backend/internal/gitcache/datafiles.go`:

```go
package gitcache

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/pubobs/backend/internal/model"
)

// MaxDataFileBytes is the hard per-file ceiling for data files, in either
// direction. A client may request a smaller limit; it may not raise this one.
// It bounds how much a single request can make the server read into memory
// (the list endpoint holds every matched file's content at once) and how large
// a file a sync payload can cause to be written into a clone.
const MaxDataFileBytes = 25 << 20 // 25 MB

// DataFile is a non-note repo file synced verbatim to the vault.
type DataFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA     string `json:"sha"`
	Size    int64  `json:"size"`
}

// SkippedDataFile records a file that matched the extension allowlist but was
// deliberately not returned. Reported rather than dropped silently, so the
// plugin can tell the user why a file they expected never appeared.
type SkippedDataFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Reason string `json:"reason"` // "too_large" | "not_utf8"
}

type DataFileList struct {
	Files   []DataFile        `json:"files"`
	Skipped []SkippedDataFile `json:"skipped"`
}

// ListDataFiles returns the repo's tracked files whose extension is in exts,
// excluding notes and _pubobs/ metadata.
//
// Content is read from the working tree rather than through GitRunner.ReadFile
// (`git show HEAD:<path>`) because every GitRunner invocation returns
// strings.TrimSpace'd output. For markdown that only costs a trailing newline;
// for a CSV or JSON file it means the vault copy never matches the repo copy
// byte-for-byte, so every sync round-trip would report a spurious change. The
// working tree is hard-reset to the remote tip by getOrClone immediately
// above, so it is exactly HEAD's content.
func (c *Cache) ListDataFiles(ctx context.Context, repo *model.Repo, credJSON string, exts []string, maxBytes int64) (DataFileList, error) {
	out := DataFileList{Files: []DataFile{}, Skipped: []SkippedDataFile{}}
	if len(exts) == 0 {
		return out, nil
	}
	if maxBytes <= 0 || maxBytes > MaxDataFileBytes {
		maxBytes = MaxDataFileBytes
	}

	lock := c.repoLock(repo.ID)
	lock.Lock()
	defer lock.Unlock()

	dir, err := c.getOrClone(repo, credJSON)
	if err != nil {
		return DataFileList{}, err
	}
	paths, err := c.git.ListFilesByExt(dir, exts)
	if err != nil {
		return DataFileList{}, err
	}

	for _, p := range paths {
		if strings.HasPrefix(p, "_pubobs/") || strings.HasSuffix(p, ".md") {
			continue
		}
		full := filepath.Join(dir, p)
		info, err := os.Stat(full)
		if err != nil {
			continue // tracked but absent from the working tree; nothing to send
		}
		if info.Size() > maxBytes {
			out.Skipped = append(out.Skipped, SkippedDataFile{Path: p, Size: info.Size(), Reason: "too_large"})
			continue
		}
		content, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		if !utf8.Valid(content) {
			out.Skipped = append(out.Skipped, SkippedDataFile{Path: p, Size: info.Size(), Reason: "not_utf8"})
			continue
		}
		sha, _ := c.git.BlobSHA(dir, p)
		out.Files = append(out.Files, DataFile{Path: p, Content: string(content), SHA: sha, Size: info.Size()})
	}
	return out, nil
}
```

- [ ] **Step 5: Run the tests**

Run: `cd backend && go test ./internal/gitcache/ -run TestListDataFiles -v`
Expected: PASS (all five)

- [ ] **Step 6: Run the whole gitcache suite**

Run: `cd backend && go test ./internal/gitcache/`
Expected: PASS — confirms the `ListFiles` refactor didn't change note listing

- [ ] **Step 7: Commit**

```bash
git add backend/internal/gitcache/datafiles.go backend/internal/gitcache/datafiles_test.go \
        backend/internal/gitcache/git.go
git commit -m "feat: list repo data files by extension from the local clone"
```

---

### Task 3: `GET /api/repos/{id}/data-files`

**Files:**
- Create: `backend/internal/api/data_files.go`
- Create: `backend/internal/api/data_files_test.go`
- Modify: `backend/internal/api/router.go:97` (register the route next to `/files`)

**Interfaces:**
- Consumes: `gitcache.DataFileList`, `Cache.ListDataFiles`, `gitcache.MaxDataFileBytes` (Task 2)
- Produces: `func parseDataFileExts(raw string) ([]string, error)`, `handleListDataFiles(deps *Deps) http.HandlerFunc`, and the route `GET /api/repos/{id}/data-files?ext=<csv-list>&max_bytes=<int>`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/api/data_files_test.go`:

```go
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

func dataFilesReq(t *testing.T, deps *api.Deps, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/repos/r1/data-files"+query, nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	return rr
}

func TestHandleListDataFiles(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepoWithFiles(t, bareURL, map[string]string{
		"hello.md":   "# Hello",
		"table.csv":  "a,b\n1,2\n",
		"skip.txt":   "no",
	})
	deps := newTestDepsWithCache(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "R", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader")

	rr := dataFilesReq(t, deps, "?ext=csv,json")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp struct {
		Files []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			SHA     string `json:"sha"`
			Size    int64  `json:"size"`
		} `json:"files"`
		Skipped []struct {
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"skipped"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Len(t, resp.Files, 1)
	require.Equal(t, "table.csv", resp.Files[0].Path)
	require.Equal(t, "a,b\n1,2\n", resp.Files[0].Content)
	require.NotEmpty(t, resp.Files[0].SHA)
	require.NotNil(t, resp.Skipped, "skipped must serialize as [], never null")
}

func TestHandleListDataFiles_rejectsBadExt(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)
	deps := newTestDepsWithCache(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "R", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader")

	for _, q := range []string{"", "?ext=", "?ext=md", "?ext=../etc", "?ext=csv,*", "?ext=CSV!"} {
		rr := dataFilesReq(t, deps, q)
		require.Equal(t, http.StatusBadRequest, rr.Code, "query %q must be rejected: %s", q, rr.Body.String())
	}
}

func TestHandleListDataFiles_requiresReaderRole(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)
	deps := newTestDepsWithCache(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "R", bareURL, "", "main")
	// no GrantAccess — u1 has no role on r1

	rr := dataFilesReq(t, deps, "?ext=csv")
	require.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())
}
```

Create the parser test in the same package as the parser — append to `backend/internal/api/repopath_test.go`:

```go
func TestParseDataFileExts(t *testing.T) {
	got, err := parseDataFileExts(" base, .CSV ,json,, yaml,yaml ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"base", "csv", "json", "yaml"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}

	for _, bad := range []string{"", "   ", ",,,", "md", "csv,md", "../etc", "c*v", "toolongextension"} {
		if _, err := parseDataFileExts(bad); err == nil {
			t.Errorf("parseDataFileExts(%q) = nil error, want error", bad)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/api/ -run 'TestHandleListDataFiles|TestParseDataFileExts'`
Expected: FAIL — `undefined: parseDataFileExts`, and the route 404s

- [ ] **Step 3: Write the handler**

Create `backend/internal/api/data_files.go`:

```go
package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/model"
)

// dataFileExtRE constrains an extension to something that can only ever be a
// file suffix. The values reach `git ls-files -- *.<ext>`, so anything with a
// path separator, a glob metacharacter or a leading dash is refused rather
// than passed through.
var dataFileExtRE = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

const maxDataFileExts = 20

// parseDataFileExts parses the `ext` query parameter — a comma-separated list
// of extensions, with or without leading dots, in any case. Returns an error
// rather than silently dropping bad entries: a typo'd extension that quietly
// syncs nothing is indistinguishable from a repo that has no such files.
func parseDataFileExts(raw string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		e := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(part), "."))
		if e == "" {
			continue
		}
		if e == "md" {
			return nil, fmt.Errorf("md files sync as notes, not data files")
		}
		if !dataFileExtRE.MatchString(e) {
			return nil, fmt.Errorf("invalid extension %q", e)
		}
		if !seen[e] {
			out = append(out, e)
			seen[e] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ext parameter is required")
	}
	if len(out) > maxDataFileExts {
		return nil, fmt.Errorf("at most %d extensions", maxDataFileExts)
	}
	return out, nil
}

// handleListDataFiles returns the repo's non-note data files for the plugin's
// pull phase. Mirrors handleListFiles (reader role, same cache/credential
// path); the extension allowlist and size cap arrive as query parameters so
// they can be a plugin setting rather than server configuration.
func handleListDataFiles(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")

		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}
		role, _ := deps.Store.GetUserRole(r.Context(), claims.UserID, repoID)
		if !claims.IsAdmin && !model.RoleAtLeast(role, "reader") {
			writeError(w, http.StatusForbidden, "reader role required")
			return
		}

		exts, err := parseDataFileExts(r.URL.Query().Get("ext"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// An unparseable or absent max_bytes means "no client preference";
		// ListDataFiles clamps 0 to the server ceiling.
		maxBytes, _ := strconv.ParseInt(r.URL.Query().Get("max_bytes"), 10, 64)

		credJSON, err := decryptCreds(deps, repo.EncryptedCreds)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cred decrypt failed")
			return
		}

		list, err := deps.Cache.ListDataFiles(r.Context(), repo, credJSON, exts, maxBytes)
		if err != nil {
			writeError(w, http.StatusBadGateway, "list data files failed: "+err.Error())
			return
		}
		deps.Store.TouchLastUsedAt(r.Context(), repoID)
		writeJSON(w, http.StatusOK, list)
	}
}
```

- [ ] **Step 4: Register the route**

In `backend/internal/api/router.go`, directly below the existing `/files` line:

```go
		r.Get("/api/repos/{id}/files", handleListFiles(deps))
		r.Get("/api/repos/{id}/data-files", handleListDataFiles(deps))
```

- [ ] **Step 5: Run the tests**

Run: `cd backend && go test ./internal/api/ -run 'TestHandleListDataFiles|TestParseDataFileExts' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add backend/internal/api/data_files.go backend/internal/api/data_files_test.go \
        backend/internal/api/repopath_test.go backend/internal/api/router.go
git commit -m "feat: add data-files endpoint for the plugin pull phase"
```

---

### Task 4: Accept `data_files` on sync

**Files:**
- Modify: `backend/internal/api/sync.go` (payload struct, path validation, cacheFiles assembly)
- Modify: `backend/internal/api/sync_test.go` (append tests)

**Interfaces:**
- Consumes: `validRepoPath` (Task 1), `gitcache.MaxDataFileBytes` (Task 2)
- Produces: sync request field `data_files: [{path, content}]`

**Design note for the implementer:** `Cache.Sync` is NOT changed. At the git layer a data file and a note are the same operation — write text to a path in the clone — so data files are appended to the existing `cacheFiles` slice. The separation that matters is in this handler: only `payload.Files` runs the note pipeline (`UpsertNote`, `UpsertSnapshot`, `UpsertNoteLinks`, `GetOrCreateNoteKey`, render-store write), so data files never become notes.

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/api/sync_test.go`:

```go
// A data file must reach git in the same commit as the notes, while creating
// none of the note-side state: no note row (it would appear in the notes list
// and the reader), no note key, no render blob.
func TestHandleSync_dataFilesReachGitButAreNotNotes(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDepsWithCache(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")

	payload := `{"files":[{"path":"note.md","md_content":"# Note","encrypted_html":"dGVzdA=="}],` +
		`"data_files":[{"path":"data/table.csv","content":"a,b\n1,2\n"},` +
		`{"path":"views/tasks.base","content":"filters:\n  - done\n"}]}`
	req := httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(payload))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp struct {
		CommitSHA string            `json:"commit_sha"`
		NoteKeys  map[string]string `json:"note_keys"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.NotEmpty(t, resp.CommitSHA)
	require.Contains(t, resp.NoteKeys, "note.md")
	require.NotContains(t, resp.NoteKeys, "data/table.csv", "data files never get note keys")
	require.NotContains(t, resp.NoteKeys, "views/tasks.base")

	// Present in git, with byte-exact content.
	list, err := deps.Cache.ListDataFiles(ctx, mustGetRepo(t, deps, "r1"), "",
		[]string{"csv", "base"}, 5<<20)
	require.NoError(t, err)
	got := map[string]string{}
	for _, f := range list.Files {
		got[f.Path] = f.Content
	}
	require.Equal(t, "a,b\n1,2\n", got["data/table.csv"])
	require.Equal(t, "filters:\n  - done\n", got["views/tasks.base"])

	// Absent from the note tables.
	for _, p := range []string{"data/table.csv", "views/tasks.base"} {
		note, err := deps.Store.GetNote(ctx, "r1", p)
		require.NoError(t, err)
		require.Nil(t, note, "%s must not have a note row", p)
	}
}

// deleted_paths already covers data files: the working-tree removal is shared,
// and the note-row/render-blob deletions are no-ops for a path that never had
// either.
func TestHandleSync_deletesDataFiles(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepoWithFiles(t, bareURL, map[string]string{
		"hello.md":  "# Hello",
		"table.csv": "a,b\n",
	})

	deps := newTestDepsWithCache(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")

	payload := `{"files":[],"deleted_paths":["table.csv"]}`
	req := httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(payload))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	list, err := deps.Cache.ListDataFiles(ctx, mustGetRepo(t, deps, "r1"), "", []string{"csv"}, 5<<20)
	require.NoError(t, err)
	require.Empty(t, list.Files, "the data file must be gone from git")
}

// A data file path is validated exactly like a note path.
func TestHandleSync_rejectsTraversalInDataFiles(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	deps := newTestDepsWithCache(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "u1", "alice@x.com", "Alice")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", bareURL, "", "main")
	deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "editor")

	payload := `{"files":[],"data_files":[{"path":"../../escaped.csv","content":"x"}]}`
	req := httptest.NewRequest("POST", "/api/repos/r1/sync", strings.NewReader(payload))
	req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "alice@x.com", false))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "invalid path")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/api/ -run 'TestHandleSync_dataFiles|TestHandleSync_deletesDataFiles|TestHandleSync_rejectsTraversalInDataFiles'`
Expected: FAIL — `data_files` is ignored, so the CSV never reaches git

- [ ] **Step 3: Add the payload type**

In `backend/internal/api/sync.go`, next to `syncAssetPayload`:

```go
// syncDataFilePayload is a non-note text file (CSV/JSON/YAML/base) synced
// verbatim. Unlike syncFilePayload it carries no rendered HTML and no
// frontmatter: it is never rendered, never encrypted, and never becomes a note.
type syncDataFilePayload struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
```

- [ ] **Step 4: Extend the request struct and validation**

In `handleSync`, add the field to the anonymous payload struct:

```go
		var payload struct {
			Files        []syncFilePayload     `json:"files"`
			Assets       []syncAssetPayload    `json:"assets"`
			DataFiles    []syncDataFilePayload `json:"data_files"`
			DeletedPaths []string              `json:"deleted_paths"`
		}
```

and add a fourth validation loop alongside the three from Task 1:

```go
		for _, d := range payload.DataFiles {
			if !validRepoPath(d.Path) {
				writeError(w, http.StatusBadRequest, "invalid path: "+d.Path)
				return
			}
		}
```

- [ ] **Step 5: Merge data files into the git write**

Replace the `cacheFiles` assembly with:

```go
		// Data files join the same commit as the notes. At the git layer the
		// two are the same operation (write text to a path), so they share
		// cacheFiles; the distinction lives in the note-pipeline loop below,
		// which iterates payload.Files only — that is what keeps a .csv from
		// becoming a note.
		cacheFiles := make([]gitcache.SyncFile, 0, len(payload.Files)+len(payload.DataFiles))
		for _, f := range payload.Files {
			cacheFiles = append(cacheFiles, gitcache.SyncFile{
				Path:      f.Path,
				MDContent: f.MDContent,
			})
		}
		for _, d := range payload.DataFiles {
			// Re-enforced server-side: the client's own cap is a setting, not
			// a guarantee.
			if len(d.Content) > gitcache.MaxDataFileBytes {
				fmt.Printf("sync %s: skipping oversized data file %s (%d bytes)\n", repoID, d.Path, len(d.Content))
				continue
			}
			cacheFiles = append(cacheFiles, gitcache.SyncFile{
				Path:      d.Path,
				MDContent: d.Content,
			})
		}
```

- [ ] **Step 6: Run the tests**

Run: `cd backend && go test ./internal/api/ -run 'TestHandleSync' -v`
Expected: PASS (new tests plus every pre-existing `TestHandleSync_*`)

- [ ] **Step 7: Run the full backend suite**

Run: `cd backend && go test ./...`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add backend/internal/api/sync.go backend/internal/api/sync_test.go
git commit -m "feat: accept data_files in the sync payload"
```

---

### Task 5: Plugin settings, types and client method

**Files:**
- Modify: `obsidian-plugin/src/types.ts` (settings fields + defaults + `DataFileEntry`)
- Create: `obsidian-plugin/src/datafiles.ts`
- Create: `obsidian-plugin/tests/datafiles.test.ts`
- Modify: `obsidian-plugin/src/client.ts` (`listDataFiles`, `sync` gains `dataFiles`)
- Modify: `obsidian-plugin/src/settings.ts` (two new settings, after the Backend URL block)

**Interfaces:**
- Consumes: `GET /api/repos/{id}/data-files` (Task 3), `data_files` sync field (Task 4)
- Produces:
  - `parseDataFileExtensions(raw: string): string[]`
  - `isDataFilePath(path: string, exts: string[]): boolean`
  - `utf8ByteLength(s: string): number`
  - `PubObsSettings.dataFileExtensions: string`, `PubObsSettings.dataFileMaxMB: number`
  - `BackendClient.listDataFiles(repoId, exts, maxBytes): Promise<DataFileListResponse>`
  - `BackendClient.sync(repoId, files, assets, deletedPaths, dataFiles)`
  - `interface SyncDataFile { path: string; content: string }`

- [ ] **Step 1: Write the failing test**

Create `obsidian-plugin/tests/datafiles.test.ts`:

```ts
import { parseDataFileExtensions, isDataFilePath, utf8ByteLength } from '../src/datafiles';

describe('parseDataFileExtensions', () => {
  test('parses the default setting', () => {
    expect(parseDataFileExtensions('base, csv, json, yaml, yml'))
      .toEqual(['base', 'csv', 'json', 'yaml', 'yml']);
  });

  test('tolerates dots, case, blanks and duplicates', () => {
    expect(parseDataFileExtensions(' .CSV , csv,, JSON ')).toEqual(['csv', 'json']);
  });

  test('drops md — notes have their own sync path', () => {
    expect(parseDataFileExtensions('csv, md, MD')).toEqual(['csv']);
  });

  test('empty input yields no extensions', () => {
    expect(parseDataFileExtensions('')).toEqual([]);
    expect(parseDataFileExtensions('  ,  ,')).toEqual([]);
  });
});

describe('isDataFilePath', () => {
  const exts = ['csv', 'base'];

  test('matches on extension, case-insensitively', () => {
    expect(isDataFilePath('data/a.csv', exts)).toBe(true);
    expect(isDataFilePath('data/A.CSV', exts)).toBe(true);
    expect(isDataFilePath('views/tasks.base', exts)).toBe(true);
  });

  test('rejects non-matching and extensionless paths', () => {
    expect(isDataFilePath('notes/a.md', exts)).toBe(false);
    expect(isDataFilePath('LICENSE', exts)).toBe(false);
    expect(isDataFilePath('a.csv.bak', exts)).toBe(false);
  });

  test('a dot in a directory name is not an extension', () => {
    expect(isDataFilePath('my.data/file', exts)).toBe(false);
  });
});

describe('utf8ByteLength', () => {
  test('counts bytes, not UTF-16 code units', () => {
    expect(utf8ByteLength('abc')).toBe(3);
    expect(utf8ByteLength('данные')).toBe(12);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd obsidian-plugin && npx jest tests/datafiles.test.ts`
Expected: FAIL — cannot find module `../src/datafiles`

- [ ] **Step 3: Write the module**

Create `obsidian-plugin/src/datafiles.ts`:

```ts
/**
 * Data files are non-note repo files (CSV/JSON/YAML/Obsidian `.base`) synced
 * verbatim between the repo and the vault. They are never rendered, never
 * encrypted, and never become notes.
 */

/**
 * Parses the `dataFileExtensions` setting: a comma-separated list, tolerant of
 * leading dots, surrounding whitespace, mixed case and duplicates.
 *
 * `md` is dropped rather than rejected — notes already have their own sync
 * path, and a user who types it is expressing a reasonable-but-redundant
 * intent, not an error worth failing the whole sync over. The backend refuses
 * it outright, so it must never be sent.
 */
export function parseDataFileExtensions(raw: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const part of raw.split(',')) {
    const ext = part.trim().replace(/^\./, '').toLowerCase();
    if (!ext || ext === 'md' || seen.has(ext)) continue;
    seen.add(ext);
    out.push(ext);
  }
  return out;
}

/** True when path's final extension is in exts (which must be lowercase). */
export function isDataFilePath(path: string, exts: string[]): boolean {
  const slash = path.lastIndexOf('/');
  const dot = path.lastIndexOf('.');
  if (dot <= slash + 1) return false; // no extension, or a dotfile/dotted dir
  return exts.includes(path.slice(dot + 1).toLowerCase());
}

/**
 * Byte length of a string as UTF-8 — what the size cap is expressed in.
 * `String.length` counts UTF-16 code units, so it understates any non-ASCII
 * file (a Cyrillic CSV would be undercounted by roughly half).
 */
export function utf8ByteLength(s: string): number {
  return new TextEncoder().encode(s).byteLength;
}
```

- [ ] **Step 4: Run the test**

Run: `cd obsidian-plugin && npx jest tests/datafiles.test.ts`
Expected: PASS

- [ ] **Step 5: Add settings fields**

In `obsidian-plugin/src/types.ts`, add to `PubObsSettings` (after `noteKeys`):

```ts
  // Comma-separated extension allowlist for data files — non-note repo files
  // synced verbatim in both directions. See src/datafiles.ts.
  dataFileExtensions: string;
  // Per-file size cap in MB. The backend clamps this to its own hard ceiling
  // (gitcache.MaxDataFileBytes, 25 MB); this setting can only lower it.
  dataFileMaxMB: number;
```

and to `DEFAULT_SETTINGS`:

```ts
  dataFileExtensions: 'base, csv, json, yaml, yml',
  dataFileMaxMB: 5,
```

Add the response types to `types.ts`:

```ts
export interface DataFileEntry {
  path: string;    // repo-relative path (e.g. "data/table.csv")
  content: string; // raw text content, byte-exact
  sha: string;     // git blob SHA for deduplication
  size: number;    // bytes
}

export interface SkippedDataFile {
  path: string;
  size: number;
  reason: 'too_large' | 'not_utf8';
}

export interface DataFileListResponse {
  files: DataFileEntry[];
  skipped: SkippedDataFile[];
}
```

- [ ] **Step 6: Add the client methods**

In `obsidian-plugin/src/client.ts`, add the payload type next to `SyncAsset`:

```ts
export interface SyncDataFile {
  path: string;    // repo-relative path
  content: string; // raw text — not base64, not encrypted
}
```

Add `DataFileListResponse` to the `./types` import, then add the method next to `listFiles`:

```ts
  async listDataFiles(repoId: string, exts: string[], maxBytes: number): Promise<DataFileListResponse> {
    const q = `ext=${encodeURIComponent(exts.join(','))}&max_bytes=${maxBytes}`;
    return this.request({ url: `${this.baseUrl}/api/repos/${repoId}/data-files?${q}` });
  }
```

and extend `sync` (new parameter defaults to `[]`, so no other caller changes):

```ts
  async sync(
    repoId: string, files: SyncFile[], assets: SyncAsset[], deletedPaths: string[],
    dataFiles: SyncDataFile[] = [],
  ): Promise<{ commit_sha: string; note_keys?: Record<string, string>; skipped_paths?: SkippedDataFile[] }> {
    const body = await gzipCompress(JSON.stringify({
      files, assets, deleted_paths: deletedPaths, data_files: dataFiles,
    }));
    return this.request({
      url: `${this.baseUrl}/api/repos/${repoId}/sync`,
      method: 'POST',
      contentType: 'application/json',
      headers: { 'Content-Encoding': 'gzip' },
      body,
    });
  }
```

- [ ] **Step 7: Add the settings UI**

In `obsidian-plugin/src/settings.ts`, after the Backend URL `new Setting(containerEl)` block:

```ts
    new Setting(containerEl)
      .setName('Data file types')
      .setDesc('Comma-separated extensions synced both ways alongside notes (Bases, CSV, JSON, YAML). Leave empty to sync notes only.')
      .addText(text =>
        text
          .setPlaceholder('base, csv, json, yaml, yml')
          .setValue(this.plugin.settings.dataFileExtensions)
          .onChange(async v => {
            this.plugin.settings.dataFileExtensions = v;
            await this.plugin.saveSettings();
          })
      );

    new Setting(containerEl)
      .setName('Data file size limit (MB)')
      .setDesc('Data files larger than this are skipped in both directions and named in the sync report. The server caps this at 25 MB.')
      .addText(text =>
        text
          .setPlaceholder('5')
          .setValue(String(this.plugin.settings.dataFileMaxMB))
          .onChange(async v => {
            const n = Number(v.trim());
            this.plugin.settings.dataFileMaxMB = Number.isFinite(n) && n > 0 ? n : 5;
            await this.plugin.saveSettings();
          })
      );
```

- [ ] **Step 8: Typecheck and run all plugin tests**

Run: `cd obsidian-plugin && npx tsc --noEmit -p tsconfig.json && npx jest`
Expected: no type errors; all tests PASS

- [ ] **Step 9: Commit**

```bash
git add obsidian-plugin/src/datafiles.ts obsidian-plugin/tests/datafiles.test.ts \
        obsidian-plugin/src/types.ts obsidian-plugin/src/client.ts obsidian-plugin/src/settings.ts
git commit -m "feat: plugin settings, types and client for data file sync"
```

---

### Task 6: Push data files from the vault

**Files:**
- Modify: `obsidian-plugin/src/sync.ts` (push phase, ~lines 248-400)
- Modify: `obsidian-plugin/tests/sync.test.ts` (mock client + new tests)

**Interfaces:**
- Consumes: `parseDataFileExtensions`, `isDataFilePath`, `utf8ByteLength` (Task 5); `client.sync(..., dataFiles)` (Task 5)
- Produces: data-file paths present in `settings.syncHashes[repoId]` and in the push phase's `currentRepoPaths`

**Why push comes before pull:** `knownPaths` (built from `syncHashes` + `pullSHAs` + `noteKeys`) minus `currentRepoPaths` becomes `deleted_paths`. `currentRepoPaths` is populated from a `.md`-only vault listing today. If the pull side landed first, every pulled data file would enter `pullSHAs`, be absent from `currentRepoPaths`, and be reported as deleted — wiping it from the repo. Building the push enumeration first means that state never exists.

- [ ] **Step 1: Write the failing test**

In `obsidian-plugin/tests/sync.test.ts`, extend `makeMockClient` in the `SyncManager metadata readiness and per-file isolation` describe block so it can serve data files:

```ts
  function makeMockClient() {
    return {
      listFiles: jest.fn().mockResolvedValue([]),
      listDataFiles: jest.fn().mockResolvedValue({ files: [], skipped: [] }),
      getNoteKey: jest.fn().mockResolvedValue(KEY),
      sync: jest.fn().mockResolvedValue({ commit_sha: 'abc1234567890', note_keys: {} }),
    };
  }
```

Extend `makeSettings` in the same block:

```ts
  function makeSettings() {
    return {
      repoMappings: { 'repo-1': { repoName: 'Test', vaultFolder: 'Published', subfolder: '' } },
      pullSHAs: {},
      syncHashes: {},
      noteKeys: {} as Record<string, Record<string, string>>,
      dataFileExtensions: 'base, csv, json, yaml, yml',
      dataFileMaxMB: 5,
    };
  }
```

Then append these tests to that describe block:

```ts
  test('data files under the vault folder are pushed alongside notes', async () => {
    const note = makeMockFile('Published/notes/a.md');
    const csv = makeMockFile('Published/data/table.csv');
    const base = makeMockFile('Published/views/tasks.base');
    const outside = makeMockFile('Elsewhere/other.csv');
    const ignored = makeMockFile('Published/notes.txt');
    const metadataCache = makeMetadataCache(new Set([note.path]));
    const app = makeMockApp([note, csv, base, outside, ignored], metadataCache);
    app.vault.read = jest.fn((f: { path: string }) =>
      Promise.resolve(f.path.endsWith('.md') ? '# Hello' : 'a,b\n1,2\n'));
    const client = makeMockClient();

    const manager = new SyncManager(app as any, client as any, makeSettings() as any, jest.fn().mockResolvedValue(undefined));
    await manager.syncRepo('repo-1');

    const dataFiles = (client.sync as jest.Mock).mock.calls[0][4];
    const paths = dataFiles.map((d: { path: string }) => d.path).sort();
    expect(paths).toEqual(['data/table.csv', 'views/tasks.base']);
    expect(dataFiles[0].content).toBe('a,b\n1,2\n');
  });

  // The deletion trap: a data file that exists in both the vault and the repo
  // must never be reported as deleted just because the note enumeration
  // doesn't see it.
  test('an unchanged data file is never reported as deleted', async () => {
    const csv = makeMockFile('Published/data/table.csv');
    const metadataCache = makeMetadataCache(new Set([csv.path]));
    const app = makeMockApp([csv], metadataCache);
    app.vault.read = jest.fn().mockResolvedValue('a,b\n1,2\n');
    const client = makeMockClient();
    const settings = makeSettings();
    // Simulate a previous sync having recorded this data file.
    settings.syncHashes = { 'repo-1': { 'data/table.csv': 'stale-hash' } } as any;

    const manager = new SyncManager(app as any, client as any, settings as any, jest.fn().mockResolvedValue(undefined));
    await manager.syncRepo('repo-1');

    const deletedPaths = (client.sync as jest.Mock).mock.calls[0][3];
    expect(deletedPaths).toEqual([]);
  });

  test('a data file removed from the vault is reported as deleted', async () => {
    const metadataCache = makeMetadataCache();
    const app = makeMockApp([], metadataCache);
    const client = makeMockClient();
    const settings = makeSettings();
    settings.syncHashes = { 'repo-1': { 'data/gone.csv': 'h' } } as any;

    const manager = new SyncManager(app as any, client as any, settings as any, jest.fn().mockResolvedValue(undefined));
    await manager.syncRepo('repo-1');

    const deletedPaths = (client.sync as jest.Mock).mock.calls[0][3];
    expect(deletedPaths).toEqual(['data/gone.csv']);
  });

  test('an oversized data file is skipped with a Notice naming it', async () => {
    const csv = makeMockFile('Published/data/huge.csv');
    const metadataCache = makeMetadataCache(new Set([csv.path]));
    const app = makeMockApp([csv], metadataCache);
    app.vault.read = jest.fn().mockResolvedValue('x'.repeat(2 * 1024 * 1024));
    const client = makeMockClient();
    const settings = makeSettings();
    settings.dataFileMaxMB = 1;

    const manager = new SyncManager(app as any, client as any, settings as any, jest.fn().mockResolvedValue(undefined));
    await manager.syncRepo('repo-1');

    expect(getNotices().some(n => n.includes('huge.csv') && n.includes('too large'))).toBe(true);
    // The oversized file was the only candidate, so there is nothing left to
    // send and the sync request is never made.
    expect(client.sync).not.toHaveBeenCalled();
  });

  test('no data file types configured means notes-only behavior', async () => {
    const note = makeMockFile('Published/notes/a.md');
    const csv = makeMockFile('Published/data/table.csv');
    const metadataCache = makeMetadataCache(new Set([note.path]));
    const app = makeMockApp([note, csv], metadataCache);
    const client = makeMockClient();
    const settings = makeSettings();
    settings.dataFileExtensions = '';

    const manager = new SyncManager(app as any, client as any, settings as any, jest.fn().mockResolvedValue(undefined));
    await manager.syncRepo('repo-1');

    expect((client.sync as jest.Mock).mock.calls[0][4]).toEqual([]);
  });
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd obsidian-plugin && npx jest tests/sync.test.ts`
Expected: FAIL — `client.sync` is called with 4 arguments, so `mock.calls[0][4]` is `undefined`

- [ ] **Step 3: Import the helpers**

At the top of `obsidian-plugin/src/sync.ts`, extend the imports:

```ts
import type { BackendClient, SyncFile, SyncAsset, SyncDataFile } from './client';
import { parseDataFileExtensions, isDataFilePath, utf8ByteLength } from './datafiles';
```

- [ ] **Step 4: Enumerate and collect data files in the push phase**

In `syncRepo`, immediately after the note-enumeration `for` loop ends and before `notice.hide()`, insert:

```ts
    // ── Data files ────────────────────────────────────────────────────────────
    // Enumerated here, in the same phase as notes, because currentRepoPaths
    // drives deletion detection: a path known from a previous sync but missing
    // from this set is reported in deletedPaths and removed from the repo.
    // Data files live in the same syncHashes map as notes, so if they were not
    // registered here every one of them would be deleted on the next sync.
    const dataExts = parseDataFileExtensions(this.settings.dataFileExtensions ?? '');
    const dataMaxBytes = Math.max(1, this.settings.dataFileMaxMB ?? 5) * 1024 * 1024;
    const syncDataFiles: SyncDataFile[] = [];
    const oversized: string[] = [];

    if (dataExts.length > 0) {
      const vaultDataFiles = this.app.vault
        .getFiles()
        .filter((f: TFile) =>
          isDataFilePath(f.path, dataExts) &&
          (vaultFolder === '' || f.path.startsWith(vaultFolder + '/')));

      for (const f of vaultDataFiles) {
        let relative = f.path;
        if (vaultFolder && relative.startsWith(vaultFolder + '/')) {
          relative = relative.slice(vaultFolder.length + 1);
        }
        const repoPath = subfolder ? `${subfolder.replace(/\/$/, '')}/${relative}` : relative;
        currentRepoPaths.add(repoPath);

        try {
          const content = await this.app.vault.read(f);
          const bytes = utf8ByteLength(content);
          if (bytes > dataMaxBytes) {
            oversized.push(`${f.path} (${(bytes / 1024 / 1024).toFixed(1)} MB)`);
            // Deliberately keep the previous hash: not sending the file must
            // not look like "unchanged" next time either.
            newHashes[repoPath] = storedHashes[repoPath] ?? '';
            continue;
          }
          const hash = fnv1a(content);
          newHashes[repoPath] = hash;
          if (!force && storedHashes[repoPath] === hash) {
            skipped++;
            continue;
          }
          syncDataFiles.push({ path: repoPath, content });
        } catch (e) {
          const reason = e instanceof Error ? e.message : String(e);
          failedFiles.push({ path: f.path, reason });
          console.error(`[PubObs] failed to read data file "${f.path}": ${reason}`, e);
        }
      }
    }

    if (oversized.length > 0) {
      new Notice(
        `PubObs: ${oversized.length} data file(s) too large for the ${this.settings.dataFileMaxMB} MB limit — ${oversized.join(', ')}`,
        10000,
      );
    }
```

- [ ] **Step 5: Include data files in the request and the early return**

Change the "nothing changed" guard to account for data files:

```ts
    if (syncFiles.length === 0 && syncDataFiles.length === 0 && deletedPaths.length === 0) {
```

Change the sync call:

```ts
      const result = await this.client.sync(repoId, syncFiles, syncAssets, deletedPaths, syncDataFiles);
```

And the success Notice, so data files are visible in the report:

```ts
      const dataSuffix = syncDataFiles.length > 0 ? `, ${syncDataFiles.length} data file(s)` : '';
      new Notice(`PubObs: ${syncFiles.length} synced${dataSuffix}, ${deletedPaths.length} deleted, ${skipped} unchanged${failedSuffix} — ${result.commit_sha.slice(0, 7)}`);

      // The server enforces its own 25 MB ceiling regardless of this client's
      // setting, and reports anything it dropped. A sync that "succeeded"
      // while quietly omitting a file from the commit is exactly the case
      // this surfaces — never let it pass silently.
      if (result.skipped_paths && result.skipped_paths.length > 0) {
        const names = result.skipped_paths.map(s => s.path).join(', ');
        new Notice(`PubObs: the server skipped ${result.skipped_paths.length} file(s) — ${names}`, 10000);
      }
```

- [ ] **Step 6: Run the tests**

Run: `cd obsidian-plugin && npx jest tests/sync.test.ts`
Expected: PASS

- [ ] **Step 7: Typecheck and run everything**

Run: `cd obsidian-plugin && npx tsc --noEmit -p tsconfig.json && npx jest`
Expected: no type errors; all tests PASS

- [ ] **Step 8: Commit**

```bash
git add obsidian-plugin/src/sync.ts obsidian-plugin/tests/sync.test.ts
git commit -m "feat: push vault data files to the repo"
```

---

### Task 7: Pull data files into the vault

**Files:**
- Modify: `obsidian-plugin/src/sync.ts` (pull phase, after the note pull's `saveSettings`)
- Modify: `obsidian-plugin/tests/sync.test.ts` (new tests)

**Interfaces:**
- Consumes: `client.listDataFiles` (Task 5); `parseDataFileExtensions` (Task 5); the push enumeration from Task 6
- Produces: data-file entries in `settings.pullSHAs[repoId]`

- [ ] **Step 1: Write the failing test**

Append to the same describe block in `obsidian-plugin/tests/sync.test.ts`:

```ts
  test('a data file in the repo is written into the mapped vault folder', async () => {
    const metadataCache = makeMetadataCache();
    const app = makeMockApp([], metadataCache);
    const client = makeMockClient();
    (client.listDataFiles as jest.Mock).mockResolvedValue({
      files: [{ path: 'data/table.csv', content: 'a,b\n1,2\n', sha: 'sha1', size: 8 }],
      skipped: [],
    });
    const settings = makeSettings();

    const manager = new SyncManager(app as any, client as any, settings as any, jest.fn().mockResolvedValue(undefined));
    await manager.syncRepo('repo-1');

    expect(app.vault.create).toHaveBeenCalledWith('Published/data/table.csv', 'a,b\n1,2\n');
    expect(settings.pullSHAs['repo-1']['data/table.csv']).toBe('sha1');
  });

  test('a data file with unpushed local edits is not overwritten by the pull', async () => {
    const csv = makeMockFile('Published/data/table.csv');
    const metadataCache = makeMetadataCache(new Set([csv.path]));
    const app = makeMockApp([csv], metadataCache);
    app.vault.getAbstractFileByPath = jest.fn((p: string) => (p === csv.path ? csv : null));
    app.vault.read = jest.fn().mockResolvedValue('locally,edited\n');
    const client = makeMockClient();
    (client.listDataFiles as jest.Mock).mockResolvedValue({
      files: [{ path: 'data/table.csv', content: 'from,repo\n', sha: 'sha2', size: 10 }],
      skipped: [],
    });
    const settings = makeSettings();
    // Last synced content hashed to something else — i.e. local edits exist.
    settings.syncHashes = { 'repo-1': { 'data/table.csv': 'different-hash' } } as any;

    const manager = new SyncManager(app as any, client as any, settings as any, jest.fn().mockResolvedValue(undefined));
    await manager.syncRepo('repo-1');

    expect(app.vault.modify).not.toHaveBeenCalledWith(csv, 'from,repo\n');
  });

  test('files the server skipped are reported to the user', async () => {
    const metadataCache = makeMetadataCache();
    const app = makeMockApp([], metadataCache);
    const client = makeMockClient();
    (client.listDataFiles as jest.Mock).mockResolvedValue({
      files: [],
      skipped: [{ path: 'data/huge.csv', size: 60000000, reason: 'too_large' }],
    });

    const manager = new SyncManager(app as any, client as any, makeSettings() as any, jest.fn().mockResolvedValue(undefined));
    await manager.syncRepo('repo-1');

    expect(getNotices().some(n => n.includes('data/huge.csv'))).toBe(true);
  });

  // An older backend has no /data-files route. That must degrade to
  // notes-only syncing, not break the sync.
  test('a backend without the data-files endpoint still syncs notes', async () => {
    const note = makeMockFile('Published/notes/a.md');
    const metadataCache = makeMetadataCache(new Set([note.path]));
    const app = makeMockApp([note], metadataCache);
    const client = makeMockClient();
    const err = new Error('HTTP 404');
    (err as unknown as { status: number }).status = 404;
    (client.listDataFiles as jest.Mock).mockRejectedValue(err);

    const manager = new SyncManager(app as any, client as any, makeSettings() as any, jest.fn().mockResolvedValue(undefined));
    await expect(manager.syncRepo('repo-1')).resolves.toBeUndefined();

    expect(client.sync).toHaveBeenCalled();
    expect((client.sync as jest.Mock).mock.calls[0][1]).toHaveLength(1);
  });
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd obsidian-plugin && npx jest tests/sync.test.ts`
Expected: FAIL — `vault.create` is never called with the CSV; `pullSHAs` has no entry

- [ ] **Step 3: Add the data-file pull**

In `syncRepo`, inside the pull-phase `try` block, immediately after `this.settings.pullSHAs[repoId] = storedPullSHAs;` and its `await this.saveSettings();`, insert:

```ts
      // Data files are pulled in their own try/catch: a backend that predates
      // this feature returns 404 here, and that must degrade to notes-only
      // syncing rather than failing the whole pull.
      try {
        const exts = parseDataFileExtensions(this.settings.dataFileExtensions ?? '');
        if (exts.length > 0) {
          const maxBytes = Math.max(1, this.settings.dataFileMaxMB ?? 5) * 1024 * 1024;
          const { files: dataFiles, skipped } = await this.client.listDataFiles(repoId, exts, maxBytes);

          for (const file of dataFiles) {
            if (storedPullSHAs[file.path] === file.sha) continue;

            const vaultPath = repoPathToVaultPath(file.path, vaultFolder, subfolder);
            const existing = this.app.vault.getAbstractFileByPath(vaultPath);
            if (existing instanceof TFile) {
              // Same protection notes get: never overwrite edits that were
              // made locally and haven't been pushed yet.
              const localContent = await this.app.vault.read(existing);
              const lastSyncedHash = (this.settings.syncHashes[repoId] ?? {})[file.path];
              if (lastSyncedHash !== undefined && fnv1a(localContent) !== lastSyncedHash) {
                continue;
              }
              await this.app.vault.modify(existing, file.content);
            } else {
              const dir = vaultPath.split('/').slice(0, -1).join('/');
              if (dir && !this.app.vault.getAbstractFileByPath(dir)) {
                await this.app.vault.createFolder(dir);
              }
              await this.app.vault.create(vaultPath, file.content);
            }
            storedPullSHAs[file.path] = file.sha;
            pulledData++;
          }

          if (skipped.length > 0) {
            const names = skipped.map(s => `${s.path} (${s.reason === 'too_large' ? 'too large' : 'not text'})`);
            new Notice(`PubObs: ${skipped.length} data file(s) skipped — ${names.join(', ')}`, 10000);
          }

          this.settings.pullSHAs[repoId] = storedPullSHAs;
          await this.saveSettings();
        }
      } catch (e) {
        const reason = e instanceof Error ? e.message : String(e);
        console.error('[PubObs] data file pull failed:', e);
        new Notice(`PubObs: data files not synced — ${reason}`, 10000);
      }
```

Declare the counter next to `let pulled = 0;` in the same scope:

```ts
      let pulledData = 0;
```

and extend the existing progress message below it:

```ts
      if (pulled > 0 || pulledData > 0) {
        notice.setMessage(`PubObs: pulled ${pulled} note(s), ${pulledData} data file(s), pushing local changes…`);
      }
```

- [ ] **Step 4: Run the tests**

Run: `cd obsidian-plugin && npx jest tests/sync.test.ts`
Expected: PASS

- [ ] **Step 5: Typecheck and run everything**

Run: `cd obsidian-plugin && npx tsc --noEmit -p tsconfig.json && npx jest`
Expected: no type errors; all tests PASS

- [ ] **Step 6: Round-trip check against the real backend**

Run the backend locally and confirm a pulled file pushes back unchanged (this is what the working-tree read in Task 2 exists to guarantee):

```bash
cd backend && go test ./internal/api/ -run 'TestHandleSync_dataFiles' -v
```

Expected: PASS — content is byte-identical including the trailing newline.

- [ ] **Step 7: Commit**

```bash
git add obsidian-plugin/src/sync.ts obsidian-plugin/tests/sync.test.ts
git commit -m "feat: pull repo data files into the vault"
```

---

### Task 8: Docs, version bump and deployable artifacts

**Files:**
- Modify: `README.md` (document the two settings)
- Modify: `obsidian-plugin/manifest.json`, `manifest.json` (repo root), `obsidian-plugin/package.json` — version `0.3.0`
- Build: `obsidian-plugin/main.js`, `backend/bin/pubobs-linux-amd64`, `backend/bin/pubobs-linux-arm64`

**Interfaces:**
- Consumes: everything above
- Produces: a deployable build

- [ ] **Step 1: Document the settings in the README**

Add to the plugin settings section:

```markdown
### Data files

Besides notes, PubObs syncs non-markdown files both ways — Obsidian Bases
(`.base`), CSV, JSON and YAML by default. Two plugin settings control this:

- **Data file types** — comma-separated extensions. Empty means notes only.
- **Data file size limit (MB)** — default 5. Larger files are skipped in both
  directions and named in the sync report. The server caps this at 25 MB.

Data files are stored in git alongside your notes but are never published:
they get no note row, no encryption key and no rendered HTML, so they never
appear in the reader or the notes list.
```

- [ ] **Step 2: Bump the version in all three manifests**

Set `"version": "0.3.0"` in `obsidian-plugin/manifest.json`, `manifest.json` (repo root) and `obsidian-plugin/package.json`. The root manifest must match the plugin's exactly, or BRAT reports "manifest is wrong".

- [ ] **Step 3: Verify the three versions match**

Run:

```bash
grep '"version"' manifest.json obsidian-plugin/manifest.json obsidian-plugin/package.json
```

Expected: `0.3.0` three times.

- [ ] **Step 4: Build the plugin**

Run: `cd obsidian-plugin && npm run build`
Expected: `main.js` rebuilt, no errors.

- [ ] **Step 5: Run every test suite one final time**

Run:

```bash
cd backend && go test ./... && cd ../obsidian-plugin && npx jest
```

Expected: all PASS

- [ ] **Step 6: Commit source and docs**

```bash
git add README.md manifest.json obsidian-plugin/manifest.json obsidian-plugin/package.json obsidian-plugin/main.js
git commit -m "docs: document data file sync; bump plugin to 0.3.0"
```

- [ ] **Step 7: Build the server binaries**

Run: `cd backend && make build`
Expected: `bin/pubobs-linux-amd64` and `bin/pubobs-linux-arm64` rebuilt.

- [ ] **Step 8: Commit the binaries separately**

The binaries go in their own commit, per the README's release procedure:

```bash
git add backend/bin/pubobs-linux-amd64 backend/bin/pubobs-linux-arm64
git commit -m "build: rebuild deployed binaries (data file sync)"
```

- [ ] **Step 9: Push**

The VPS updater clones `origin/main`, so nothing deploys until this is pushed:

```bash
git push
```

Then run the updater on the VPS (or the in-app update button).

---

## Self-Review

**Spec coverage**

| Spec section | Task |
|---|---|
| `Cache.ListDataFiles` (ext filter, `_pubobs`, cap, UTF-8) | 2 |
| `GET /data-files` (reader role, ext validation, `md` rejected, `max_bytes` clamp) | 3 |
| `data_files` on sync, server-side cap re-enforcement | 4 |
| Deletion via existing `deleted_paths` | 4 (test) |
| `MaxDataFileBytes` ceiling | 2 (defined), 3 + 4 (enforced) |
| Plugin settings `dataFileExtensions` / `dataFileMaxMB` | 5 |
| Pull: SHA skip, local-edit protection, mapped path, 404 tolerance | 7 |
| Push: enumeration, hashing, cap | 6 |
| Deletion trap regression test | 6 |
| `validRepoPath` on all four path sources | 1 (three), 4 (data_files) |
| Error handling table | 3, 4, 6, 7 |
| Deployment (binaries, manifests, BRAT) | 8 |

**Type consistency**

`DataFile` / `SkippedDataFile` / `DataFileList` (Go, Task 2) serialize to
`path`/`content`/`sha`/`size` and `path`/`size`/`reason`, matching
`DataFileEntry` / `SkippedDataFile` / `DataFileListResponse` (TS, Task 5).
`SyncDataFile { path, content }` (TS, Task 5) matches `syncDataFilePayload`
(Go, Task 4) via `data_files`. `parseDataFileExts` (Go) and
`parseDataFileExtensions` (TS) are deliberately distinct names for
deliberately different behavior: the Go parser *rejects* `md` and malformed
input because it guards the git command; the TS parser *drops* them because it
guards a free-text setting the user is still typing into.
