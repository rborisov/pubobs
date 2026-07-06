package main

import (
	"context"
	"testing"

	"github.com/pubobs/backend/internal/config"
	"github.com/pubobs/backend/internal/db"
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
