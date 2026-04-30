package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pubobs/backend/internal/model"
)

func (s *Store) CreateRepo(ctx context.Context, id, name, remoteURL, encCreds, branch string) (*model.Repo, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO repos (id, name, remote_url, encrypted_creds, default_branch, created_at)
		VALUES (?,?,?,?,?,?)`,
		id, name, remoteURL, encCreds, branch, time.Now().UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("create repo: %w", err)
	}
	return s.GetRepo(ctx, id)
}

func (s *Store) GetRepo(ctx context.Context, id string) (*model.Repo, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, remote_url, encrypted_creds, default_branch,
		       local_path, cloned_at, last_used_at, created_at
		FROM repos WHERE id=?`, id)
	return scanRepo(row)
}

func (s *Store) ListRepos(ctx context.Context) ([]*model.Repo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, remote_url, encrypted_creds, default_branch,
		       local_path, cloned_at, last_used_at, created_at
		FROM repos ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Repo
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpdateRepo(ctx context.Context, id, name, remoteURL, encCreds, branch string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE repos SET name=?, remote_url=?, encrypted_creds=?, default_branch=?
		WHERE id=?`, name, remoteURL, encCreds, branch, id)
	return err
}

func (s *Store) DeleteRepo(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM repos WHERE id=?`, id)
	return err
}

func (s *Store) UpdateRepoLocalPath(ctx context.Context, id, localPath string, clonedAt interface{}) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE repos SET local_path=?, cloned_at=?, last_used_at=? WHERE id=?`,
		localPath, clonedAt, time.Now().UTC(), id)
	return err
}

func (s *Store) ClearRepoLocalPath(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE repos SET local_path=NULL, cloned_at=NULL WHERE id=?`, id)
	return err
}

func (s *Store) TouchLastUsedAt(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE repos SET last_used_at=? WHERE id=?`, time.Now().UTC(), id)
	return err
}

func (s *Store) ListStaleRepos(ctx context.Context, cutoff time.Time) ([]*model.Repo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, remote_url, encrypted_creds, default_branch,
		       local_path, cloned_at, last_used_at, created_at
		FROM repos WHERE local_path IS NOT NULL AND last_used_at < ?`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Repo
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanRepo(row scanner) (*model.Repo, error) {
	var r model.Repo
	var localPath sql.NullString
	var clonedAt, lastUsedAt sql.NullTime
	err := row.Scan(
		&r.ID, &r.Name, &r.RemoteURL, &r.EncryptedCreds, &r.DefaultBranch,
		&localPath, &clonedAt, &lastUsedAt, &r.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan repo: %w", err)
	}
	if localPath.Valid {
		r.LocalPath = &localPath.String
	}
	if clonedAt.Valid {
		r.ClonedAt = &clonedAt.Time
	}
	if lastUsedAt.Valid {
		r.LastUsedAt = &lastUsedAt.Time
	}
	return &r, nil
}
