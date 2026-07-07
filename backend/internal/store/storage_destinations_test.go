package store_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/pubobs/backend/internal/db"
	"github.com/pubobs/backend/internal/model"
	"github.com/pubobs/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func TestStorageDestinations_crud(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()
	s := store.New(d)
	ctx := context.Background()

	// Empty list to start.
	list, err := s.ListStorageDestinations(ctx)
	require.NoError(t, err)
	require.Empty(t, list)

	dest := &model.StorageDestination{
		ID: "d1", Name: "archive", S3Endpoint: "s3.example.com", S3Bucket: "b",
		S3AccessKey: "AK", S3SecretKey: "SK", S3Region: "us-east-1", S3UseSSL: true,
	}
	require.NoError(t, s.CreateStorageDestination(ctx, dest))

	got, err := s.GetStorageDestination(ctx, "d1")
	require.NoError(t, err)
	require.Equal(t, "archive", got.Name)
	require.Equal(t, "s3.example.com", got.S3Endpoint)
	require.True(t, got.S3UseSSL)

	list, err = s.ListStorageDestinations(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)

	got.Name = "archive-renamed"
	got.S3UseSSL = false
	require.NoError(t, s.UpdateStorageDestination(ctx, got))
	after, err := s.GetStorageDestination(ctx, "d1")
	require.NoError(t, err)
	require.Equal(t, "archive-renamed", after.Name)
	require.False(t, after.S3UseSSL)

	require.NoError(t, s.DeleteStorageDestination(ctx, "d1"))
	_, err = s.GetStorageDestination(ctx, "d1")
	require.True(t, errors.Is(err, sql.ErrNoRows))
}
