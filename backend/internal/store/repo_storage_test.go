package store_test

import (
	"context"
	"testing"

	"github.com/pubobs/backend/internal/db"
	"github.com/pubobs/backend/internal/model"
	"github.com/pubobs/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func TestRepoStorageDestination_setAndCount(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()
	s := store.New(d)
	ctx := context.Background()

	require.NoError(t, s.CreateStorageDestination(ctx, &model.StorageDestination{ID: "d1", Name: "arch"}))
	_, err = s.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	require.NoError(t, err)

	// Default: no destination assigned (local), idle migration.
	repo, err := s.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.Nil(t, repo.StorageDestinationID)
	require.Equal(t, "idle", repo.MigrationStatus)

	// Assign to d1.
	destID := "d1"
	require.NoError(t, s.SetRepoStorageDestination(ctx, "r1", &destID))
	repo, err = s.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.NotNil(t, repo.StorageDestinationID)
	require.Equal(t, "d1", *repo.StorageDestinationID)

	count, err := s.CountReposUsingDestination(ctx, "d1")
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// Migration status round-trips.
	require.NoError(t, s.SetRepoMigrationStatus(ctx, "r1", "done", 5, 5))
	repo, err = s.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.Equal(t, "done", repo.MigrationStatus)
	require.Equal(t, 5, repo.MigrationTotal)
	require.Equal(t, 5, repo.MigrationDone)

	// Reassign to local (nil) → count drops to 0.
	require.NoError(t, s.SetRepoStorageDestination(ctx, "r1", nil))
	count, err = s.CountReposUsingDestination(ctx, "d1")
	require.NoError(t, err)
	require.Equal(t, 0, count)
}
