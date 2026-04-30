package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pubobs/backend/internal/model"
)

func (s *Store) UpsertUser(ctx context.Context, id, email, name string) (*model.User, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (id, email, name, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET email=excluded.email, name=excluded.name`,
		id, email, name, time.Now().UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("upsert user: %w", err)
	}
	return s.GetUserByID(ctx, id)
}

func (s *Store) GetUserByID(ctx context.Context, id string) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, email, name, is_instance_admin, created_at FROM users WHERE id=?`, id))
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, email, name, is_instance_admin, created_at FROM users WHERE email=?`, email))
}

func (s *Store) ListUsers(ctx context.Context) ([]*model.User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, email, name, is_instance_admin, created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var out []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) SetInstanceAdmin(ctx context.Context, userID string, admin bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET is_instance_admin=? WHERE id=?`, admin, userID)
	return err
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanUser(row scanner) (*model.User, error) {
	var u model.User
	var admin int
	err := row.Scan(&u.ID, &u.Email, &u.Name, &admin, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	u.IsInstanceAdmin = admin == 1
	return &u, nil
}
