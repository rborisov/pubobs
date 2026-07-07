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
