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

func TestStorageSettings_getBeforeSeedReturnsNoRows(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()
	s := store.New(d)

	_, err = s.GetStorageSettings(context.Background())
	require.True(t, errors.Is(err, sql.ErrNoRows))
}

func TestStorageSettings_upsertThenGet(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()
	s := store.New(d)
	ctx := context.Background()

	settings := &model.StorageSettings{
		StoreType:          "s3",
		S3Endpoint:         "s3.example.com",
		S3Bucket:           "my-bucket",
		S3AccessKey:        "AK",
		S3SecretKey:        "SK",
		S3Region:           "us-east-1",
		S3UseSSL:           true,
		AssetEncryptionKey: "abcd1234",
		MigrationStatus:    "idle",
	}
	require.NoError(t, s.UpsertStorageSettings(ctx, settings))

	got, err := s.GetStorageSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "s3", got.StoreType)
	require.Equal(t, "s3.example.com", got.S3Endpoint)
	require.Equal(t, "my-bucket", got.S3Bucket)
	require.True(t, got.S3UseSSL)
	require.Equal(t, "abcd1234", got.AssetEncryptionKey)

	// Upsert again updates the single row rather than inserting a second one.
	got.StoreType = "local"
	got.S3UseSSL = false
	require.NoError(t, s.UpsertStorageSettings(ctx, got))

	after, err := s.GetStorageSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "local", after.StoreType)
	require.False(t, after.S3UseSSL)
	require.Equal(t, 1, after.ID)
}
