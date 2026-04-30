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
		{Path: "newdoc.md", MDContent: "# New", HTMLContent: "<h1>New</h1>"},
	}
	sha, err := cache.Sync(context.Background(), repo, "", files, "sync 2024-01-01 by alice")
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
