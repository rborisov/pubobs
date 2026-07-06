// backend/cmd/server/storage_boot.go
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"

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
