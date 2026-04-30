package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pubobs/backend/internal/model"
)

func (s *Store) UpsertNote(ctx context.Context, repoID, path string) (*model.Note, error) {
	id := uuid.NewString()
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notes (id, repo_id, path, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(repo_id, path) DO UPDATE SET updated_at=excluded.updated_at`,
		id, repoID, path, now,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert note: %w", err)
	}
	return s.GetNote(ctx, repoID, path)
}

func (s *Store) GetNote(ctx context.Context, repoID, path string) (*model.Note, error) {
	var n model.Note
	err := s.db.QueryRowContext(ctx,
		`SELECT id, repo_id, path, updated_at FROM notes WHERE repo_id=? AND path=?`,
		repoID, path,
	).Scan(&n.ID, &n.RepoID, &n.Path, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &n, err
}

func (s *Store) GetNoteByID(ctx context.Context, id string) (*model.Note, error) {
	var n model.Note
	err := s.db.QueryRowContext(ctx,
		`SELECT id, repo_id, path, updated_at FROM notes WHERE id=?`, id,
	).Scan(&n.ID, &n.RepoID, &n.Path, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &n, err
}

func (s *Store) ListNotes(ctx context.Context, repoID string) ([]*model.Note, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, repo_id, path, updated_at FROM notes WHERE repo_id=? ORDER BY path`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Note
	for rows.Next() {
		var n model.Note
		if err := rows.Scan(&n.ID, &n.RepoID, &n.Path, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}

func (s *Store) UpsertSnapshot(ctx context.Context, noteID, htmlContent, metadataJSON, syncedBy, commitSHA string) error {
	id := uuid.NewString()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO note_snapshots (id, note_id, html_content, metadata_json, synced_by, git_commit_sha, synced_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(note_id) DO UPDATE SET
			html_content=excluded.html_content,
			metadata_json=excluded.metadata_json,
			synced_by=excluded.synced_by,
			git_commit_sha=excluded.git_commit_sha,
			synced_at=excluded.synced_at`,
		id, noteID, htmlContent, metadataJSON, syncedBy, commitSHA, time.Now().UTC(),
	)
	return err
}

func (s *Store) GetSnapshot(ctx context.Context, noteID string) (*model.NoteSnapshot, error) {
	var snap model.NoteSnapshot
	err := s.db.QueryRowContext(ctx, `
		SELECT id, note_id, html_content, metadata_json, synced_by, git_commit_sha, synced_at
		FROM note_snapshots WHERE note_id=?`, noteID,
	).Scan(&snap.ID, &snap.NoteID, &snap.HTMLContent, &snap.MetadataJSON,
		&snap.SyncedBy, &snap.GitCommitSHA, &snap.SyncedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &snap, err
}

func (s *Store) UpsertNoteLinks(ctx context.Context, sourceNoteID string, targetPaths []string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM note_links WHERE source_note_id=?`, sourceNoteID); err != nil {
		return err
	}
	for _, tp := range targetPaths {
		if _, err := s.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO note_links (source_note_id, target_path) VALUES (?,?)`,
			sourceNoteID, tp); err != nil {
			return err
		}
	}
	return nil
}

// GetBacklinks returns notes (in the same repo) that link to targetPath.
func (s *Store) GetBacklinks(ctx context.Context, repoID, targetPath string) ([]*model.Note, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.repo_id, n.path, n.updated_at
		FROM notes n
		JOIN note_links nl ON nl.source_note_id = n.id
		WHERE n.repo_id=? AND nl.target_path=?`,
		repoID, targetPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Note
	for rows.Next() {
		var n model.Note
		if err := rows.Scan(&n.ID, &n.RepoID, &n.Path, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}
