package jobs_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pubobs/backend/internal/config"
	"github.com/pubobs/backend/internal/db"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/jobs"
	"github.com/pubobs/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func TestEvictionJob_evictsStaleRepo(t *testing.T) {
	d, _ := db.Open(":memory:")
	defer d.Close()
	s := store.New(d)
	ctx := context.Background()
	cacheDir := t.TempDir()
	cache := gitcache.NewCache(cacheDir)

	cfg := &config.Config{
		RepoCacheDir: cacheDir,
		RepoCacheTTL: 100 * time.Millisecond,
		DiskWarnPct:  20,
		DiskCritPct:  5,
	}

	s.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "", "main")
	repoDir := filepath.Join(cacheDir, "r1")
	os.MkdirAll(repoDir, 0755)
	s.UpdateRepoLocalPath(ctx, "r1", repoDir, time.Now().UTC().Add(-1*time.Hour))

	time.Sleep(200 * time.Millisecond)
	jobs.RunEvictionCycle(ctx, s, cache, cfg)

	repo, _ := s.GetRepo(ctx, "r1")
	require.Nil(t, repo.LocalPath, "stale repo local_path should be cleared")
	_, err := os.Stat(repoDir)
	require.True(t, os.IsNotExist(err), "clone directory should be deleted")
}
