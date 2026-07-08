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
		if err := deps.Store.DeleteNote(ctx, repo.ID, n.Path); err != nil {
			fmt.Printf("prune orphan note %s/%s: %v\n", repo.ID, n.Path, err)
			continue
		}
		if rstore, rerr := deps.Resolver.RenderStoreFor(ctx, repo.ID); rerr == nil {
			if derr := rstore.Delete(repo.ID, n.Path); derr != nil {
				fmt.Printf("prune orphan render %s/%s: %v\n", repo.ID, n.Path, derr)
			}
		}
	}
	return filtered, nil
}
