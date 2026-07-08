package api

import (
	"context"
	"encoding/json"
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
		//
		// Notes that were synced under the render-encryption scheme always
		// have a non-empty encryption_key. Keep those rows too: a share→
		// unshare cycle clears shared_publicly but the note (and its render
		// blob) must survive list reconcile so repo members can still open
		// it without ?key= — see TestPubListNotes_unsharedEncryptedNoteSurvivesReconcileWithoutGitPath.
		if n.SharedPublicly || n.EncryptionKey != "" {
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

// ensureNoteFromGit recreates a missing notes row (and snapshot metadata) from
// git when the path is still tracked. This heals rows that were incorrectly
// pruned by an older reconcile pass before shared/encrypted-note guards
// existed, so the next open attempt succeeds instead of returning 404 forever.
func ensureNoteFromGit(ctx context.Context, deps *Deps, repo *model.Repo, notePath, syncedBy string) (*model.Note, error) {
	if deps.Cache == nil || syncedBy == "" {
		return nil, nil
	}
	credJSON, err := decryptCreds(deps, repo.EncryptedCreds)
	if err != nil {
		return nil, err
	}
	paths, err := deps.Cache.ListFilePaths(ctx, repo, credJSON)
	if err != nil {
		return nil, err
	}
	found := false
	for _, p := range paths {
		if p == notePath {
			found = true
			break
		}
	}
	if !found {
		return nil, nil
	}
	content, err := deps.Cache.ReadRawFile(repo.ID, notePath)
	if err != nil {
		return nil, err
	}
	note, err := deps.Store.UpsertNote(ctx, repo.ID, notePath)
	if err != nil || note == nil {
		return note, err
	}
	headSHA, _ := deps.Cache.HeadSHA(repo.ID)
	meta := extractMetadata(content, map[string]any{})
	metaJSON, _ := json.Marshal(meta)
	if err := deps.Store.UpsertSnapshot(ctx, note.ID, "", string(metaJSON), syncedBy, headSHA); err != nil {
		return note, err
	}
	if err := deps.Store.UpsertNoteLinks(ctx, note.ID, meta.Links); err != nil {
		return note, err
	}
	return deps.Store.GetNote(ctx, repo.ID, notePath)
}
