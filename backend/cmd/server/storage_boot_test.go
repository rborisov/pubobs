package main

import (
	"context"
	"testing"

	"github.com/pubobs/backend/internal/config"
	"github.com/pubobs/backend/internal/db"
	"github.com/pubobs/backend/internal/model"
	"github.com/pubobs/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func TestLoadOrSeedStorageSettings_seedsFromEnvOnFirstBoot(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()
	s := store.New(d)
	cfg := &config.Config{
		RenderStoreType: "local",
		S3Endpoint:      "should-be-ignored-for-local",
	}

	settings, err := loadOrSeedStorageSettings(context.Background(), s, cfg)
	require.NoError(t, err)
	require.Equal(t, "local", settings.StoreType)
	require.Len(t, settings.AssetEncryptionKey, 64, "should generate a 32-byte hex key")
	require.Equal(t, "idle", settings.MigrationStatus)

	// A second call must NOT re-seed — it loads the persisted row instead.
	settings2, err := loadOrSeedStorageSettings(context.Background(), s, cfg)
	require.NoError(t, err)
	require.Equal(t, settings.AssetEncryptionKey, settings2.AssetEncryptionKey, "key must be stable across boots")
}

func TestConvertLegacyStorageSettings_s3BecomesDefaultDestination(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()
	s := store.New(d)
	ctx := context.Background()

	_, err = s.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	require.NoError(t, err)

	legacy := &model.StorageSettings{
		StoreType: "s3", S3Endpoint: "s3.example.com", S3Bucket: "b",
		S3AccessKey: "AK", S3SecretKey: "SK", S3Region: "us-east-1", S3UseSSL: true,
		AssetEncryptionKey: "00", MigrationStatus: "idle",
	}
	require.NoError(t, convertLegacyStorageSettings(ctx, s, legacy))

	dests, err := s.ListStorageDestinations(ctx)
	require.NoError(t, err)
	require.Len(t, dests, 1)
	require.Equal(t, "default", dests[0].Name)
	require.Equal(t, "s3.example.com", dests[0].S3Endpoint)

	repo, err := s.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.NotNil(t, repo.StorageDestinationID)
	require.Equal(t, dests[0].ID, *repo.StorageDestinationID)

	// Idempotent: a second call does not create a duplicate destination.
	require.NoError(t, convertLegacyStorageSettings(ctx, s, legacy))
	dests2, err := s.ListStorageDestinations(ctx)
	require.NoError(t, err)
	require.Len(t, dests2, 1)
}

// TestConvertLegacyStorageSettings_persistsConvertedMarker asserts that after
// one successful conversion, a SECOND boot (reloading the settings row from the
// DB) does NOT re-create a destination or reassign repos — the persisted
// StoreType flip to "local" makes re-conversion a no-op. This guards against a
// silent re-conversion after an admin deletes all destinations.
func TestConvertLegacyStorageSettings_persistsConvertedMarker(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()
	s := store.New(d)
	ctx := context.Background()

	_, err = s.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	require.NoError(t, err)

	// Seed the legacy S3 settings into the DB (id=1 singleton).
	require.NoError(t, s.UpsertStorageSettings(ctx, &model.StorageSettings{
		StoreType: "s3", S3Endpoint: "s3.example.com", S3Bucket: "b",
		S3AccessKey: "AK", S3SecretKey: "SK", S3Region: "us-east-1", S3UseSSL: true,
		AssetEncryptionKey: "00", MigrationStatus: "idle",
	}))

	// First boot: load settings, convert.
	legacy1, err := s.GetStorageSettings(ctx)
	require.NoError(t, err)
	require.NoError(t, convertLegacyStorageSettings(ctx, s, legacy1))

	dests, err := s.ListStorageDestinations(ctx)
	require.NoError(t, err)
	require.Len(t, dests, 1)
	firstDestID := dests[0].ID

	// The converted marker is persisted: StoreType flipped to "local".
	reloaded, err := s.GetStorageSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "local", reloaded.StoreType)

	// Simulate an admin reassigning all repos to Local and deleting every
	// destination before the next boot.
	require.NoError(t, s.SetRepoStorageDestination(ctx, "r1", nil))
	require.NoError(t, s.DeleteStorageDestination(ctx, firstDestID))

	// Second boot: reload settings (now "local") and convert again → no-op.
	legacy2, err := s.GetStorageSettings(ctx)
	require.NoError(t, err)
	require.NoError(t, convertLegacyStorageSettings(ctx, s, legacy2))

	dests2, err := s.ListStorageDestinations(ctx)
	require.NoError(t, err)
	require.Empty(t, dests2, "second boot must NOT re-create a destination")
	repo, err := s.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.Nil(t, repo.StorageDestinationID, "second boot must NOT reassign the repo")
}

func TestConvertLegacyStorageSettings_localIsNoop(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()
	s := store.New(d)
	ctx := context.Background()
	_, err = s.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	require.NoError(t, err)

	require.NoError(t, convertLegacyStorageSettings(ctx, s, &model.StorageSettings{StoreType: "local"}))
	dests, err := s.ListStorageDestinations(ctx)
	require.NoError(t, err)
	require.Empty(t, dests)
	repo, err := s.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.Nil(t, repo.StorageDestinationID)
}
