// backend/internal/jobs/repo_migration_test.go
package jobs_test

import (
	"context"
	"testing"

	"github.com/pubobs/backend/internal/jobs"
	"github.com/pubobs/backend/internal/renderstore"
	"github.com/stretchr/testify/require"
)

func TestRunRepoMigration_movesOnlyThatRepo(t *testing.T) {
	ctx := context.Background()
	srcRenders := renderstore.NewLocal(t.TempDir())
	dstRenders := renderstore.NewLocal(t.TempDir())
	srcAssets := renderstore.NewLocal(t.TempDir())
	dstAssets := renderstore.NewLocal(t.TempDir())

	require.NoError(t, srcRenders.Write("r1", "note.md", []byte("render-1")))
	require.NoError(t, srcAssets.Write("r1", "img.png", []byte("asset-1")))
	// A different repo's data must be untouched.
	require.NoError(t, srcRenders.Write("r2", "other.md", []byte("render-2")))

	migrated, failed, err := jobs.RunRepoMigration(ctx, "r1", srcRenders, dstRenders, srcAssets, dstAssets)
	require.NoError(t, err)
	require.Equal(t, 2, migrated)
	require.Equal(t, 0, failed)

	// r1 moved.
	gotR, err := dstRenders.Read("r1", "note.md")
	require.NoError(t, err)
	require.Equal(t, []byte("render-1"), gotR)
	gotA, err := dstAssets.Read("r1", "img.png")
	require.NoError(t, err)
	require.Equal(t, []byte("asset-1"), gotA)
	// r1 source deleted after verified write.
	srcGone, err := srcRenders.Read("r1", "note.md")
	require.NoError(t, err)
	require.Nil(t, srcGone)

	// r2 untouched.
	r2, err := srcRenders.Read("r2", "other.md")
	require.NoError(t, err)
	require.Equal(t, []byte("render-2"), r2)
	r2moved, err := dstRenders.Read("r2", "other.md")
	require.NoError(t, err)
	require.Nil(t, r2moved)
}

// TestRunRepoMigration_verifyFailure_doesNotDeleteSource proves that
// RunRepoMigration's own verify-before-delete guard (a separate copy of the
// same logic as RunMigrationCycle's, since RunRepoMigration scopes to a
// single repoID and enumerates via repoWalker) is actually load-bearing: if
// the post-write verification read from the destination render store comes
// back wrong, the source copy must survive and the entry must count as
// failed, not migrated — and that failure must not stop a second, cleanly
// migrating entry in the same run.
func TestRunRepoMigration_verifyFailure_doesNotDeleteSource(t *testing.T) {
	ctx := context.Background()

	srcRenders := renderstore.NewLocal(t.TempDir())
	dstRendersInner := renderstore.NewLocal(t.TempDir())
	dstRenders := newVerifyFaultStore(dstRendersInner)

	srcAssets := renderstore.NewLocal(t.TempDir())
	dstAssets := renderstore.NewLocal(t.TempDir())

	// "bad.md" writes fine but its destination verification read comes back
	// with different bytes than what was written. "good.md" migrates
	// cleanly and must still succeed despite "bad.md"'s failure.
	require.NoError(t, srcRenders.Write("r1", "bad.md", []byte("original-data")))
	require.NoError(t, srcRenders.Write("r1", "good.md", []byte("good-data")))
	dstRenders.corruptWith("r1", "bad.md", []byte("corrupted-data"))

	migrated, failed, err := jobs.RunRepoMigration(ctx, "r1", srcRenders, dstRenders, srcAssets, dstAssets)
	require.NoError(t, err)
	require.Equal(t, 1, migrated, "only the entry that verifies cleanly should count as migrated")
	require.Equal(t, 1, failed, "the verify-mismatch entry should count as failed, not migrated")

	// The entry whose verification failed must still be readable from the
	// source — it was never deleted.
	stillThere, err := srcRenders.Read("r1", "bad.md")
	require.NoError(t, err)
	require.Equal(t, []byte("original-data"), stillThere, "source copy must survive a verify mismatch")

	// The good entry migrated and was removed from the source.
	gone, err := srcRenders.Read("r1", "good.md")
	require.NoError(t, err)
	require.Nil(t, gone, "the entry that verified cleanly should be removed from the source")
	migratedData, err := dstRendersInner.Read("r1", "good.md")
	require.NoError(t, err)
	require.Equal(t, []byte("good-data"), migratedData)
}
