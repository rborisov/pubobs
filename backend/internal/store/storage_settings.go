package store

import (
	"context"
	"time"

	"github.com/pubobs/backend/internal/model"
)

func (s *Store) GetStorageSettings(ctx context.Context) (*model.StorageSettings, error) {
	var st model.StorageSettings
	var useSSL int
	err := s.db.QueryRowContext(ctx, `
		SELECT id, store_type, s3_endpoint, s3_bucket, s3_access_key, s3_secret_key,
		       s3_region, s3_use_ssl, asset_encryption_key, migration_status,
		       migration_total, migration_done, updated_at
		FROM storage_settings WHERE id=1`,
	).Scan(&st.ID, &st.StoreType, &st.S3Endpoint, &st.S3Bucket, &st.S3AccessKey,
		&st.S3SecretKey, &st.S3Region, &useSSL, &st.AssetEncryptionKey,
		&st.MigrationStatus, &st.MigrationTotal, &st.MigrationDone, &st.UpdatedAt)
	if err != nil {
		return nil, err
	}
	st.S3UseSSL = useSSL != 0
	return &st, nil
}

func (s *Store) UpsertStorageSettings(ctx context.Context, st *model.StorageSettings) error {
	useSSL := 0
	if st.S3UseSSL {
		useSSL = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO storage_settings (
			id, store_type, s3_endpoint, s3_bucket, s3_access_key, s3_secret_key,
			s3_region, s3_use_ssl, asset_encryption_key, migration_status,
			migration_total, migration_done, updated_at
		) VALUES (1,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			store_type=excluded.store_type,
			s3_endpoint=excluded.s3_endpoint,
			s3_bucket=excluded.s3_bucket,
			s3_access_key=excluded.s3_access_key,
			s3_secret_key=excluded.s3_secret_key,
			s3_region=excluded.s3_region,
			s3_use_ssl=excluded.s3_use_ssl,
			asset_encryption_key=excluded.asset_encryption_key,
			migration_status=excluded.migration_status,
			migration_total=excluded.migration_total,
			migration_done=excluded.migration_done,
			updated_at=excluded.updated_at`,
		st.StoreType, st.S3Endpoint, st.S3Bucket, st.S3AccessKey, st.S3SecretKey,
		st.S3Region, useSSL, st.AssetEncryptionKey, st.MigrationStatus,
		st.MigrationTotal, st.MigrationDone, time.Now().UTC(),
	)
	return err
}
