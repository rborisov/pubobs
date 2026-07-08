package api

import (
	"context"
	"fmt"

	"github.com/pubobs/backend/internal/model"
)

// reconcileNotesWithGit returns only notes whose paths still exist in the
// repo's git tree, and deletes orphan DB rows (plus render blobs) for paths
// that are no longer tracked. If git paths cannot be resolved (no cache,
// cred decrypt failure, clone/fetch error), notes are returned unchanged so
// list endpoints still work in tests and transient outage scenarios.
func reconcileNotesWithGit(ctx context.Context, deps *Deps, repo *model.Repo, notes []*model.Note) ([]*model.Note, error) {
	if deps.Cache == nil || len(notes) == 0 {
		return notes, nil
	}

	credJSON, err := decryptCreds(deps, repo.EncryptedCreds)
	if err != nil {
		return notes, nil
	}

	paths, err := deps.Cache.ListFilePaths(ctx, repo, credJSON)
	if err != nil {
		return notes, nil
	}

	existing := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		existing[p] = struct{}{}
	}

	filtered := make([]*model.Note, 0, len(notes))
	for _, n := range notes {
		if _, ok := existing[n.Path]; ok {
			filtered = append(filtered, n)
			continue
		}
		// A publicly shared note must stay in the DB even when git
		// reconcile hasn't caught up (or transiently disagrees with the
		// DB). List endpoints hide it below, but handlePubGetNote still
		// needs the row so a direct ?key= share link keeps working — see
		// TestPubListNotes_sharedNoteSurvivesReconcileWithoutGitPath.
		if n.SharedPublicly {
			continue
		}
		pruneOrphanNote(ctx, deps, repo.ID, n.Path)
	}
	return filtered, nil
}

func pruneOrphanNote(ctx context.Context, deps *Deps, repoID, path string) {
	if err := deps.Store.DeleteNote(ctx, repoID, path); err != nil {
		fmt.Printf("prune orphan note %s/%s: %v\n", repoID, path, err)
		return
	}
	if rstore, rerr := deps.Resolver.RenderStoreFor(ctx, repoID); rerr == nil {
		if derr := rstore.Delete(repoID, path); derr != nil {
			fmt.Printf("prune orphan render %s/%s: %v\n", repoID, path, derr)
		}
	}
}
