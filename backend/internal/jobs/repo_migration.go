// backend/internal/jobs/repo_migration.go
package jobs

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/pubobs/backend/internal/renderstore"
)

// repoWalker is implemented by both *LocalRenderStore and *S3RenderStore
// (Task 3). It enumerates the keys stored for one repo.
type repoWalker interface {
	WalkRepoEntries(repoID string, fn func(notePath string) error) error
}

// Pacing/retry tunables for RunRepoMigration's writes against the
// destination store. Some S3-compatible destinations enforce a per-second
// write-request ceiling (a real per-repo migration hit Yandex Object
// Storage's "Write request rate limit exceeded. Limit is 5 req/sec." after
// several repos' migrations ran back-to-back against the same destination).
// Without pacing, a burst of writes trips that ceiling, individual entries
// fail, and the repo's migration_status flips to "failed" even though the
// data was never lost (the verify-before-delete guard below still protects
// it) — it's just stranded on the source with the repo's storage pointer
// already aimed at a destination that's missing it.
//
// Exported (not const) so tests can shrink them to keep the suite fast, and
// so an operator could tune them without a rebuild if these ever move to
// config/env in the future.
var (
	// MigrationWriteInterval is the minimum gap enforced between successive
	// writes to the destination store, across both the render and asset
	// side of one repo's migration. 250ms ≈ 4 writes/sec, under most
	// providers' 5/sec ceilings with a little headroom.
	MigrationWriteInterval = 250 * time.Millisecond
	// MigrationMaxRetries is how many times a single entry's write is
	// retried after a rate-limit rejection before it's counted as failed.
	MigrationMaxRetries = 5
	// MigrationRetryBackoff is the base backoff delay after a rate-limited
	// write; the actual wait grows linearly with the attempt number
	// (attempt 1 waits 1x, attempt 2 waits 2x, ...).
	MigrationRetryBackoff = 500 * time.Millisecond
)

// isRateLimitError reports whether err looks like a write-rate-limit
// rejection from the destination store. Providers phrase this differently
// (HTTP 429, S3 error codes like "SlowDown"/"TooManyRequests", or a plain
// message such as Yandex Object Storage's "Write request rate limit
// exceeded..."), so this matches on message content rather than a single
// concrete error type or SDK-specific error code.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "slow down") ||
		strings.Contains(msg, "slowdown") ||
		strings.Contains(msg, "toomanyrequests") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "429")
}

// RunRepoMigration copies one repo's render and asset entries from the source
// stores to the destination stores, verifying each write by reading it back
// before deleting the source copy. A per-entry failure is logged and skipped;
// the source is never deleted unless its destination write verifies.
//
// Writes to the destination are paced (MigrationWriteInterval) and a
// rate-limit rejection is retried with backoff (MigrationMaxRetries,
// MigrationRetryBackoff) before the entry is given up on and counted as
// failed — see the package-level var docs above for why.
//
// Callers MUST pass PLAIN asset stores (the store *under* any EncryptingStore,
// via renderstore.EncryptingStore.Inner()) for srcAssets/dstAssets, so asset
// ciphertext copies verbatim and is never re-encrypted. srcRenders/dstRenders
// are already plain (render blobs are never server-side encrypted).
func RunRepoMigration(
	ctx context.Context,
	repoID string,
	srcRenders, dstRenders, srcAssets, dstAssets renderstore.RenderStore,
) (migrated, failed int, err error) {
	var lastWriteAt time.Time

	// writeWithRetry paces writes to dest and retries a rate-limited write
	// with backoff, sharing lastWriteAt across every call in this migration
	// run (both the render and asset side) so the combined write rate to
	// the destination — not just one side's — stays under the ceiling.
	writeWithRetry := func(dest renderstore.RenderStore, path string, data []byte) error {
		var werr error
		for attempt := 0; attempt <= MigrationMaxRetries; attempt++ {
			if MigrationWriteInterval > 0 {
				if wait := MigrationWriteInterval - time.Since(lastWriteAt); wait > 0 {
					time.Sleep(wait)
				}
			}
			werr = dest.Write(repoID, path, data)
			lastWriteAt = time.Now()
			if werr == nil {
				return nil
			}
			if !isRateLimitError(werr) {
				return werr
			}
			if attempt < MigrationMaxRetries {
				backoff := time.Duration(attempt+1) * MigrationRetryBackoff
				log.Printf("repo-migration: rate limited writing %s/%s, retrying in %s (attempt %d/%d)",
					repoID, path, backoff, attempt+1, MigrationMaxRetries)
				time.Sleep(backoff)
			}
		}
		return werr
	}

	migrateOne := func(source, dest renderstore.RenderStore, path string) {
		data, rerr := source.Read(repoID, path)
		if rerr != nil || data == nil {
			log.Printf("repo-migration: read %s/%s: %v", repoID, path, rerr)
			failed++
			return
		}
		if werr := writeWithRetry(dest, path, data); werr != nil {
			log.Printf("repo-migration: write %s/%s: %v", repoID, path, werr)
			failed++
			return
		}
		verify, verr := dest.Read(repoID, path)
		if verr != nil || string(verify) != string(data) {
			log.Printf("repo-migration: verify %s/%s failed", repoID, path)
			failed++
			return
		}
		if derr := source.Delete(repoID, path); derr != nil {
			log.Printf("repo-migration: delete source %s/%s: %v", repoID, path, derr)
			// Not counted as failed — data is safely on the destination.
		}
		migrated++
	}

	migrateSide := func(source, dest renderstore.RenderStore) error {
		walker, ok := source.(repoWalker)
		if !ok {
			return nil // source can't be enumerated (e.g. an unexpected wrapper); nothing to migrate
		}
		return walker.WalkRepoEntries(repoID, func(notePath string) error {
			migrateOne(source, dest, notePath)
			return nil
		})
	}

	if werr := migrateSide(srcRenders, dstRenders); werr != nil {
		return migrated, failed, werr
	}
	if werr := migrateSide(srcAssets, dstAssets); werr != nil {
		return migrated, failed, werr
	}
	_ = ctx // reserved for cancellation; unused for now
	return migrated, failed, nil
}
