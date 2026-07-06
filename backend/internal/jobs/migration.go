// backend/internal/jobs/migration.go
package jobs

import (
	"context"
	"log"

	"github.com/pubobs/backend/internal/renderstore"
)

// RunMigrationCycle copies every entry from sourceRenders/sourceAssets to
// destRenders/destAssets, verifying each write before deleting the source
// copy. A per-entry failure is logged and skipped — it never aborts the
// whole run, and the source copy is only ever removed after its
// destination write is confirmed by reading it back.
func RunMigrationCycle(
	ctx context.Context,
	sourceRenders, destRenders, sourceAssets, destAssets renderstore.RenderStore,
) (migrated, failed int, err error) {
	migrateOne := func(source, dest renderstore.RenderStore, repoID, path string) {
		data, rerr := source.Read(repoID, path)
		if rerr != nil || data == nil {
			log.Printf("migration: read %s/%s: %v", repoID, path, rerr)
			failed++
			return
		}
		if werr := dest.Write(repoID, path, data); werr != nil {
			log.Printf("migration: write %s/%s: %v", repoID, path, werr)
			failed++
			return
		}
		verify, verr := dest.Read(repoID, path)
		if verr != nil || string(verify) != string(data) {
			log.Printf("migration: verify %s/%s failed", repoID, path)
			failed++
			return
		}
		if derr := source.Delete(repoID, path); derr != nil {
			log.Printf("migration: delete source %s/%s: %v", repoID, path, derr)
			// Not counted as failed — the data is safely on the destination;
			// a leftover source copy just means disk space wasn't fully
			// reclaimed this round, which is safe to retry later.
		}
		migrated++
	}

	if localRenders, ok := sourceRenders.(*renderstore.LocalRenderStore); ok {
		werr := localRenders.WalkEntries(func(repoID, notePath string) error {
			migrateOne(sourceRenders, destRenders, repoID, notePath)
			return nil
		})
		if werr != nil {
			return migrated, failed, werr
		}
	}
	if localAssets, ok := sourceAssets.(*renderstore.LocalRenderStore); ok {
		werr := localAssets.WalkEntries(func(repoID, notePath string) error {
			migrateOne(sourceAssets, destAssets, repoID, notePath)
			return nil
		})
		if werr != nil {
			return migrated, failed, werr
		}
	}

	_ = ctx // reserved for future cancellation support; unused for now
	return migrated, failed, nil
}
