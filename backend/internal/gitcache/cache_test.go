package gitcache_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/model"
	"github.com/stretchr/testify/require"
)

func TestCache_SyncAndListFiles(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	cacheDir := t.TempDir()
	cache := gitcache.NewCache(cacheDir)

	repo := &model.Repo{
		ID:             "r1",
		RemoteURL:      bareURL,
		EncryptedCreds: "",
		DefaultBranch:  "main",
	}

	files := []gitcache.SyncFile{
		{Path: "newdoc.md", MDContent: "# New"},
	}
	sha, err := cache.Sync(context.Background(), repo, "", files, []gitcache.SyncAsset{}, nil, "sync 2024-01-01 by alice")
	require.NoError(t, err)
	require.NotEmpty(t, sha)

	localPath := filepath.Join(cacheDir, "r1")
	_, err = os.Stat(localPath)
	require.NoError(t, err)

	entries, err := cache.ListFiles(context.Background(), repo, "")
	require.NoError(t, err)
	var paths []string
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	require.Contains(t, paths, "newdoc.md")
}

// TestCache_ListFiles_NonASCIIFilename is the end-to-end regression test for
// the reported bug: a plugin pull-phase failure ("git show: exit status 128")
// caused by git's default core.quotePath=true C-quoting/octal-escaping
// non-ASCII paths in `git ls-files` output, which was then reused verbatim
// (still quoted/escaped) as the <path> argument to `git show HEAD:<path>`.
// This exercises the exact call chain from the bug report: Cache.ListFiles ->
// GitRunner.ListFiles (ls-files) -> GitRunner.ReadFile (show) + BlobSHA (ls-tree).
func TestCache_ListFiles_NonASCIIFilename(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	const nonASCIIName = "аппаратура.md"
	work := t.TempDir()
	runGit(t, work, "clone", bareURL, ".")
	require.NoError(t, os.WriteFile(filepath.Join(work, nonASCIIName), []byte("# Equipment"), 0644))
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "add non-ascii file")
	runGit(t, work, "push", "origin", "HEAD:main")

	cacheDir := t.TempDir()
	cache := gitcache.NewCache(cacheDir)

	repo := &model.Repo{
		ID:             "non-ascii-repo",
		RemoteURL:      bareURL,
		EncryptedCreds: "",
		DefaultBranch:  "main",
	}

	entries, err := cache.ListFiles(context.Background(), repo, "")
	require.NoError(t, err, "ListFiles must not fail on a repo containing a non-ASCII filename")

	var found *model.FileEntry
	for i := range entries {
		if entries[i].Path == nonASCIIName {
			found = &entries[i]
		}
	}
	require.NotNil(t, found, "non-ASCII filename should be listed with a correctly decoded UTF-8 path, not a quoted/escaped one")
	require.Equal(t, "# Equipment", found.Content)
	require.NotEmpty(t, found.SHA)
}

// TestCache_ListFiles_RecoversFromCorruptedClone simulates a local clone left
// in a corrupted state by an interrupted git operation (e.g. disk exhaustion
// mid-fetch, or a killed process) — .git exists on disk but HEAD points at a
// ref that doesn't resolve to any commit. Before the self-healing fix, this
// would permanently wedge ListFiles for the repo; it should now transparently
// wipe the broken clone and re-clone from the remote.
func TestCache_ListFiles_RecoversFromCorruptedClone(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	cacheDir := t.TempDir()
	cache := gitcache.NewCache(cacheDir)

	repo := &model.Repo{
		ID:             "corrupt-repo",
		RemoteURL:      bareURL,
		EncryptedCreds: "",
		DefaultBranch:  "main",
	}

	// Establish a healthy local clone first.
	_, err := cache.ListFiles(context.Background(), repo, "")
	require.NoError(t, err)

	localPath := filepath.Join(cacheDir, "corrupt-repo")
	headPath := filepath.Join(localPath, ".git", "HEAD")
	require.NoError(t, os.WriteFile(headPath, []byte("ref: refs/heads/does-not-exist\n"), 0644))

	entries, err := cache.ListFiles(context.Background(), repo, "")
	require.NoError(t, err, "ListFiles should self-heal a corrupted local clone by re-cloning")
	var paths []string
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	require.Contains(t, paths, "hello.md")
}

// TestCache_Sync_ClearsStaleLockFile simulates a leftover .git/index.lock
// from a previous git process killed mid-operation. Since all git operations
// for a repo are serialized through Cache's per-repo mutex, this lock file
// can never be legitimately held by a concurrently-running process, so it
// must be cleared automatically rather than wedging every future operation.
func TestCache_Sync_ClearsStaleLockFile(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	cacheDir := t.TempDir()
	cache := gitcache.NewCache(cacheDir)

	repo := &model.Repo{
		ID:             "locked-repo",
		RemoteURL:      bareURL,
		EncryptedCreds: "",
		DefaultBranch:  "main",
	}

	_, err := cache.ListFiles(context.Background(), repo, "")
	require.NoError(t, err)

	localPath := filepath.Join(cacheDir, "locked-repo")
	lockPath := filepath.Join(localPath, ".git", "index.lock")
	require.NoError(t, os.WriteFile(lockPath, []byte(""), 0644))

	_, err = cache.Sync(context.Background(), repo, "", []gitcache.SyncFile{
		{Path: "second.md", MDContent: "# Second"},
	}, []gitcache.SyncAsset{}, nil, "sync 2024-01-02 by alice")
	require.NoError(t, err, "Sync should clear a stale index.lock left by an interrupted process")

	_, statErr := os.Stat(lockPath)
	require.True(t, os.IsNotExist(statErr), "stale lock file should have been removed")
}

// TestCache_Sync_DeletesRemovedPaths is the regression test for a bug where
// deletedPaths sent by the plugin were never actually removed from the git
// working tree: Sync wrote/committed new files but silently ignored
// deletedPaths, so `git add -A` had nothing to stage for the removal and the
// old file lived on in the remote forever.
func TestCache_Sync_DeletesRemovedPaths(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	cacheDir := t.TempDir()
	cache := gitcache.NewCache(cacheDir)

	repo := &model.Repo{
		ID:             "r3",
		RemoteURL:      bareURL,
		EncryptedCreds: "",
		DefaultBranch:  "main",
	}

	// hello.md was pushed by seedBareRepo — confirm it's visible first.
	entries, err := cache.ListFiles(context.Background(), repo, "")
	require.NoError(t, err)
	var pathsBefore []string
	for _, e := range entries {
		pathsBefore = append(pathsBefore, e.Path)
	}
	require.Contains(t, pathsBefore, "hello.md")

	sha, err := cache.Sync(context.Background(), repo, "", nil, nil, []string{"hello.md"}, "sync: delete hello.md")
	require.NoError(t, err)
	require.NotEmpty(t, sha)

	entries, err = cache.ListFiles(context.Background(), repo, "")
	require.NoError(t, err)
	var pathsAfter []string
	for _, e := range entries {
		pathsAfter = append(pathsAfter, e.Path)
	}
	require.NotContains(t, pathsAfter, "hello.md", "deleted path must be removed from the git working tree and no longer listed")
}

// TestCache_Sync_DeletedPathAlreadyGone verifies a deletedPaths entry for a
// file that doesn't actually exist on disk (e.g. already removed by a prior
// partially-failed sync) is a harmless no-op rather than an error.
func TestCache_Sync_DeletedPathAlreadyGone(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	cacheDir := t.TempDir()
	cache := gitcache.NewCache(cacheDir)

	repo := &model.Repo{
		ID:             "r4",
		RemoteURL:      bareURL,
		EncryptedCreds: "",
		DefaultBranch:  "main",
	}

	sha, err := cache.Sync(context.Background(), repo, "", nil, nil, []string{"never-existed.md"}, "sync: delete nonexistent path")
	require.NoError(t, err)
	require.NotEmpty(t, sha)
}

func TestCache_AppendComment(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	cacheDir := t.TempDir()
	cache := gitcache.NewCache(cacheDir)

	repo := &model.Repo{
		ID:             "r2",
		RemoteURL:      bareURL,
		EncryptedCreds: "",
		DefaultBranch:  "main",
	}

	ctx := context.Background()
	err := cache.AppendComment(ctx, repo, "", "notes/test.md", "Alice", "alice@example.com", "Hello world", "sha1")
	require.NoError(t, err)

	commentsPath := filepath.Join(cacheDir, "r2", "notes", "test-comments.md")
	data, err := os.ReadFile(commentsPath)
	require.NoError(t, err, "comments file should exist after AppendComment")

	content := string(data)
	require.Contains(t, content, "type: comments", "should contain frontmatter header")
	require.Contains(t, content, "Alice", "should contain author name")
	require.Contains(t, content, "alice@example.com", "should contain author email")
	require.Contains(t, content, "Hello world", "should contain comment body")

	err = cache.AppendComment(ctx, repo, "", "notes/test.md", "Bob", "bob@example.com", "Second comment", "sha2")
	require.NoError(t, err)

	data, err = os.ReadFile(commentsPath)
	require.NoError(t, err)

	content = string(data)
	require.Contains(t, content, "Alice", "should still contain first author after second comment")
	require.Contains(t, content, "Bob", "should contain second author")
}
