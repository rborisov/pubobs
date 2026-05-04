package store

import (
	"context"
	"strings"
	"time"

	"github.com/pubobs/backend/internal/model"
)

// IsEmailAllowed returns true when the email matches an allowlist entry, or
// when the allowlist is empty (open registration).
func (s *Store) IsEmailAllowed(ctx context.Context, email string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM registration_allowlist`).Scan(&count); err != nil {
		return false, err
	}
	if count == 0 {
		return true, nil // open registration
	}
	// Exact match
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM registration_allowlist WHERE pattern=?`, email).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	// Domain match (@domain.com)
	if at := strings.LastIndex(email, "@"); at >= 0 {
		domain := email[at:]
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM registration_allowlist WHERE pattern=?`, domain).Scan(&n); err != nil {
			return false, err
		}
		if n > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) ListAllowlist(ctx context.Context) ([]*model.AllowlistEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, pattern, created_at FROM registration_allowlist ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.AllowlistEntry
	for rows.Next() {
		var e model.AllowlistEntry
		if err := rows.Scan(&e.ID, &e.Pattern, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

func (s *Store) AddAllowlistEntry(ctx context.Context, id, pattern string) (*model.AllowlistEntry, error) {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO registration_allowlist (id, pattern, created_at) VALUES (?,?,?)`,
		id, pattern, now)
	if err != nil {
		return nil, err
	}
	return &model.AllowlistEntry{ID: id, Pattern: pattern, CreatedAt: now}, nil
}

func (s *Store) RemoveAllowlistEntry(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM registration_allowlist WHERE id=?`, id)
	return err
}
