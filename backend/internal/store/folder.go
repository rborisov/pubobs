package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/pubobs/backend/internal/model"
)

func (s *Store) UpsertFolderMapping(ctx context.Context, userID, repoID, vaultFolder, repoSubfolder string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO folder_mappings (user_id, repo_id, vault_folder, repo_subfolder) VALUES (?,?,?,?)
		ON CONFLICT(user_id, repo_id) DO UPDATE SET vault_folder=excluded.vault_folder, repo_subfolder=excluded.repo_subfolder`,
		userID, repoID, vaultFolder, repoSubfolder,
	)
	return err
}

func (s *Store) GetFolderMapping(ctx context.Context, userID, repoID string) (*model.FolderMapping, error) {
	var m model.FolderMapping
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, repo_id, vault_folder, repo_subfolder FROM folder_mappings WHERE user_id=? AND repo_id=?`,
		userID, repoID,
	).Scan(&m.UserID, &m.RepoID, &m.VaultFolder, &m.RepoSubfolder)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &m, err
}

func (s *Store) ListUserFolderMappings(ctx context.Context, userID string) ([]*model.FolderMapping, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, repo_id, vault_folder, repo_subfolder FROM folder_mappings WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.FolderMapping
	for rows.Next() {
		var m model.FolderMapping
		if err := rows.Scan(&m.UserID, &m.RepoID, &m.VaultFolder, &m.RepoSubfolder); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}
