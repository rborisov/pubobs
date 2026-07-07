// backend/cmd/server/storage_boot.go
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"

	"github.com/google/uuid"
	"github.com/pubobs/backend/internal/config"
	"github.com/pubobs/backend/internal/model"
	"github.com/pubobs/backend/internal/store"
)

// loadOrSeedStorageSettings loads the persisted storage_settings row, or —
// on first boot, when the table is empty — seeds it from the PUBOBS_* env
// vars (so existing installs keep working unchanged) plus a freshly
// generated asset-encryption key. After the first boot, the DB row is
// authoritative and env vars are no longer consulted for this setting.
func loadOrSeedStorageSettings(ctx context.Context, s *store.Store, cfg *config.Config) (*model.StorageSettings, error) {
	settings, err := s.GetStorageSettings(ctx)
	if err == nil {
		return settings, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, err
	}
	seeded := &model.StorageSettings{
		StoreType:          cfg.RenderStoreType,
		S3Endpoint:         cfg.S3Endpoint,
		S3Bucket:           cfg.S3Bucket,
		S3AccessKey:        cfg.S3AccessKey,
		S3SecretKey:        cfg.S3SecretKey,
		S3Region:           cfg.S3Region,
		S3UseSSL:           cfg.S3UseSSL,
		AssetEncryptionKey: hex.EncodeToString(keyBytes),
		MigrationStatus:    "idle",
	}
	if err := s.UpsertStorageSettings(ctx, seeded); err != nil {
		return nil, err
	}
	return seeded, nil
}

// convertLegacyStorageSettings performs a one-time upgrade: if the prior
// instance-wide storage_settings row was configured for S3 and no
// destinations exist yet, create a "default" destination from it and assign
// every existing repo to it, so their already-uploaded renders/assets keep
// resolving to the same bucket. Idempotent: a no-op once any destination
// exists, or when the legacy settings were local.
func convertLegacyStorageSettings(ctx context.Context, s *store.Store, legacy *model.StorageSettings) error {
	if legacy.StoreType != "s3" {
		return nil
	}
	existing, err := s.ListStorageDestinations(ctx)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil // conversion already ran (or destinations otherwise exist)
	}
	dest := &model.StorageDestination{
		ID:          uuid.NewString(),
		Name:        "default",
		S3Endpoint:  legacy.S3Endpoint,
		S3Bucket:    legacy.S3Bucket,
		S3AccessKey: legacy.S3AccessKey,
		S3SecretKey: legacy.S3SecretKey,
		S3Region:    legacy.S3Region,
		S3UseSSL:    legacy.S3UseSSL,
	}
	if err := s.CreateStorageDestination(ctx, dest); err != nil {
		return err
	}
	repos, err := s.ListRepos(ctx)
	if err != nil {
		return err
	}
	for _, repo := range repos {
		if err := s.SetRepoStorageDestination(ctx, repo.ID, &dest.ID); err != nil {
			return err
		}
	}
	return nil
}
