package jobs

import (
	"context"
	"log"
	"time"

	"github.com/pubobs/backend/internal/config"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/store"
)

// StartEvictionJob runs the eviction + disk monitoring loop in a goroutine.
// Cancel ctx to stop the loop.
func StartEvictionJob(ctx context.Context, s *store.Store, cache *gitcache.Cache, cfg *config.Config) {
	go func() {
		ticker := time.NewTicker(cfg.CacheCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				RunEvictionCycle(ctx, s, cache, cfg)
			}
		}
	}()
}

// RunEvictionCycle is the single-pass eviction + health update logic, exported for testing.
func RunEvictionCycle(ctx context.Context, s *store.Store, cache *gitcache.Cache, cfg *config.Config) {
	cutoff := time.Now().Add(-cfg.RepoCacheTTL)
	stale, err := s.ListStaleRepos(ctx, cutoff)
	if err != nil {
		log.Printf("eviction: list stale repos: %v", err)
		return
	}

	now := time.Now().UTC()
	evicted := 0
	for _, repo := range stale {
		if err := cache.Evict(repo.ID); err != nil {
			log.Printf("eviction: evict %s: %v", repo.ID, err)
			continue
		}
		if err := s.ClearRepoLocalPath(ctx, repo.ID); err != nil {
			log.Printf("eviction: clear local_path %s: %v", repo.ID, err)
		}
		evicted++
	}
	if evicted > 0 {
		log.Printf("eviction: evicted %d repo(s)", evicted)
	}

	freeBytes, freePct, err := cache.DiskUsage()
	if err != nil {
		log.Printf("eviction: disk usage check failed: %v", err)
		return
	}
	status := "ok"
	if freePct < cfg.DiskCritPct {
		status = "crit"
	} else if freePct < cfg.DiskWarnPct {
		status = "warn"
	}
	var lastEviction *time.Time
	if evicted > 0 {
		lastEviction = &now
	}
	if err := s.UpsertHealth(ctx, freePct, freeBytes, status, lastEviction); err != nil {
		log.Printf("eviction: upsert health: %v", err)
	}
}
