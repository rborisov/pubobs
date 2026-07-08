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

// TestStartEvictionJob_runsImmediatelyOnStartup pins down that the health row
// (and thus the disk status an operator/admin dashboard would see) is
// populated right away on process start, not only after the first tick of
// CacheCheckInterval. Without this, a container restart intended to force a
// fresh disk check wouldn't actually do so until up to a full interval later
// — using a 1h interval here means the assertion can only pass if the
// eviction cycle ran immediately, before the ticker ever fires.
func TestStartEvictionJob_runsImmediatelyOnStartup(t *testing.T) {
	d, _ := db.Open(":memory:")
	defer d.Close()
	s := store.New(d)
	cacheDir := t.TempDir()
	cache := gitcache.NewCache(cacheDir)

	cfg := &config.Config{
		RepoCacheDir:       cacheDir,
		RepoCacheTTL:       24 * time.Hour,
		CacheCheckInterval: time.Hour, // long enough that only an immediate run could satisfy this test
		DiskWarnPct:        20,
		DiskCritPct:        5,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := s.GetHealth(ctx); err == nil {
		t.Fatal("expected no health row before the job has run")
	}

	jobs.StartEvictionJob(ctx, s, cache, cfg)

	require.Eventually(t, func() bool {
		h, err := s.GetHealth(ctx)
		return err == nil && h != nil
	}, 2*time.Second, 10*time.Millisecond, "health row should be populated immediately on startup, not after a 1h tick")
}
