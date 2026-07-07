// backend/internal/jobs/repo_migration.go
package jobs

import (
	"context"
	"log"

	"github.com/pubobs/backend/internal/renderstore"
)

// repoWalker is implemented by both *LocalRenderStore and *S3RenderStore
// (Task 3). It enumerates the keys stored for one repo.
type repoWalker interface {
	WalkRepoEntries(repoID string, fn func(notePath string) error) error
}

// RunRepoMigration copies one repo's render and asset entries from the source
// stores to the destination stores, verifying each write by reading it back
// before deleting the source copy. A per-entry failure is logged and skipped;
// the source is never deleted unless its destination write verifies.
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
	migrateOne := func(source, dest renderstore.RenderStore, path string) {
		data, rerr := source.Read(repoID, path)
		if rerr != nil || data == nil {
			log.Printf("repo-migration: read %s/%s: %v", repoID, path, rerr)
			failed++
			return
		}
		if werr := dest.Write(repoID, path, data); werr != nil {
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
