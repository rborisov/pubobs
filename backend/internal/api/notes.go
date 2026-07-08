package api

import (
	"context"
	"fmt"

	"github.com/pubobs/backend/internal/model"
)

// reconcileNotesWithGit returns only notes whose paths still exist in the
// repo's git tree and have a published snapshot (required by GetNote handlers).
// It deletes broken DB rows — paths no longer tracked in git, or rows without
// a snapshot — plus any associated render blobs. If git paths cannot be
// resolved (no cache, cred decrypt failure, clone/fetch error), notes are
// returned unchanged so list endpoints still work in tests and transient
// outage scenarios.
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
		if _, ok := existing[n.Path]; !ok {
			pruneBrokenNote(ctx, deps, repo.ID, n.Path)
			continue
		}
		snap, err := deps.Store.GetSnapshot(ctx, n.ID)
		if err != nil {
			return nil, err
		}
		if snap == nil {
			pruneBrokenNote(ctx, deps, repo.ID, n.Path)
			continue
		}
		filtered = append(filtered, n)
	}
	return filtered, nil
}

func pruneBrokenNote(ctx context.Context, deps *Deps, repoID, path string) {
	if err := deps.Store.DeleteNote(ctx, repoID, path); err != nil {
		fmt.Printf("prune broken note %s/%s: %v\n", repoID, path, err)
		return
	}
	if rstore, rerr := deps.Resolver.RenderStoreFor(ctx, repoID); rerr == nil {
		if derr := rstore.Delete(repoID, path); derr != nil {
			fmt.Printf("prune broken note render %s/%s: %v\n", repoID, path, derr)
		}
	}
}
