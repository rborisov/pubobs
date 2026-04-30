package store

import (
	"context"
	"time"

	"github.com/pubobs/backend/internal/model"
)

func (s *Store) UpsertHealth(ctx context.Context, diskFreePct float64, diskFreeBytes int64, status string, lastEviction *time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system_health (id, disk_free_pct, disk_free_bytes, disk_status, last_eviction_at, checked_at)
		VALUES (1,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			disk_free_pct=excluded.disk_free_pct,
			disk_free_bytes=excluded.disk_free_bytes,
			disk_status=excluded.disk_status,
			last_eviction_at=excluded.last_eviction_at,
			checked_at=excluded.checked_at`,
		diskFreePct, diskFreeBytes, status, lastEviction, time.Now().UTC(),
	)
	return err
}

func (s *Store) GetHealth(ctx context.Context) (*model.SystemHealth, error) {
	var h model.SystemHealth
	var lastEviction interface{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, disk_free_pct, disk_free_bytes, disk_status, last_eviction_at, checked_at FROM system_health WHERE id=1`,
	).Scan(&h.ID, &h.DiskFreePct, &h.DiskFreeBytes, &h.DiskStatus, &lastEviction, &h.CheckedAt)
	if err != nil {
		return nil, err
	}
	if t, ok := lastEviction.(time.Time); ok {
		h.LastEvictionAt = &t
	}
	return &h, nil
}
