package store

import (
	"context"
	"time"

	"github.com/pubobs/backend/internal/model"
)

func (s *Store) CreateStorageDestination(ctx context.Context, d *model.StorageDestination) error {
	useSSL := 0
	if d.S3UseSSL {
		useSSL = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO storage_destinations
			(id, name, s3_endpoint, s3_bucket, s3_access_key, s3_secret_key, s3_region, s3_use_ssl, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		d.ID, d.Name, d.S3Endpoint, d.S3Bucket, d.S3AccessKey, d.S3SecretKey,
		d.S3Region, useSSL, time.Now().UTC(),
	)
	return err
}

func (s *Store) ListStorageDestinations(ctx context.Context) ([]*model.StorageDestination, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, s3_endpoint, s3_bucket, s3_access_key, s3_secret_key, s3_region, s3_use_ssl, created_at
		FROM storage_destinations ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.StorageDestination
	for rows.Next() {
		d, err := scanStorageDestination(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetStorageDestination(ctx context.Context, id string) (*model.StorageDestination, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, s3_endpoint, s3_bucket, s3_access_key, s3_secret_key, s3_region, s3_use_ssl, created_at
		FROM storage_destinations WHERE id=?`, id)
	return scanStorageDestination(row)
}

func (s *Store) UpdateStorageDestination(ctx context.Context, d *model.StorageDestination) error {
	useSSL := 0
	if d.S3UseSSL {
		useSSL = 1
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE storage_destinations SET
			name=?, s3_endpoint=?, s3_bucket=?, s3_access_key=?, s3_secret_key=?, s3_region=?, s3_use_ssl=?
		WHERE id=?`,
		d.Name, d.S3Endpoint, d.S3Bucket, d.S3AccessKey, d.S3SecretKey, d.S3Region, useSSL, d.ID,
	)
	return err
}

func (s *Store) DeleteStorageDestination(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM storage_destinations WHERE id=?`, id)
	return err
}

// scanStorageDestination reads one row from a *sql.Row or *sql.Rows.
func scanStorageDestination(sc interface{ Scan(...any) error }) (*model.StorageDestination, error) {
	var d model.StorageDestination
	var useSSL int
	if err := sc.Scan(&d.ID, &d.Name, &d.S3Endpoint, &d.S3Bucket, &d.S3AccessKey,
		&d.S3SecretKey, &d.S3Region, &useSSL, &d.CreatedAt); err != nil {
		return nil, err
	}
	d.S3UseSSL = useSSL != 0
	return &d, nil
}
