// backend/internal/jobs/migration_test.go
package jobs_test

import (
	"context"
	"testing"

	"github.com/pubobs/backend/internal/jobs"
	"github.com/pubobs/backend/internal/renderstore"
	"github.com/stretchr/testify/require"
)

func TestRunMigrationCycle_movesRenderAndAssetBlobs(t *testing.T) {
	ctx := context.Background()

	sourceRenders := renderstore.NewLocal(t.TempDir())
	destRenders := renderstore.NewLocal(t.TempDir())
	sourceAssets := renderstore.NewLocal(t.TempDir())
	destAssets := renderstore.NewLocal(t.TempDir())

	require.NoError(t, sourceRenders.Write("r1", "note1.md", []byte("render-1")))
	require.NoError(t, sourceRenders.Write("r2", "note2.md", []byte("render-2")))
	require.NoError(t, sourceAssets.Write("r1", "img.png", []byte("asset-1")))

	migrated, failed, err := jobs.RunMigrationCycle(ctx, sourceRenders, destRenders, sourceAssets, destAssets)
	require.NoError(t, err)
	require.Equal(t, 3, migrated)
	require.Equal(t, 0, failed)

	got, err := destRenders.Read("r1", "note1.md")
	require.NoError(t, err)
	require.Equal(t, []byte("render-1"), got)
	got2, err := destRenders.Read("r2", "note2.md")
	require.NoError(t, err)
	require.Equal(t, []byte("render-2"), got2)
	gotAsset, err := destAssets.Read("r1", "img.png")
	require.NoError(t, err)
	require.Equal(t, []byte("asset-1"), gotAsset)

	// Source copies are removed only after a verified write to the destination.
	afterSource, err := sourceRenders.Read("r1", "note1.md")
	require.NoError(t, err)
	require.Nil(t, afterSource, "migrated file should be removed from source")
}
