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
	sha, err := cache.Sync(context.Background(), repo, "", files, []gitcache.SyncAsset{}, "sync 2024-01-01 by alice")
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
