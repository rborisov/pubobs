package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pubobs/backend/internal/model"
)

const noteColumns = `id, repo_id, path, updated_at, encryption_key, shared_publicly`

// scanNote scans a row/rows shaped like noteColumns into a model.Note.
func scanNote(row scanner) (*model.Note, error) {
	var n model.Note
	var shared int
	err := row.Scan(&n.ID, &n.RepoID, &n.Path, &n.UpdatedAt, &n.EncryptionKey, &shared)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan note: %w", err)
	}
	n.SharedPublicly = shared != 0
	return &n, nil
}

func (s *Store) UpsertNote(ctx context.Context, repoID, path string) (*model.Note, error) {
	id := uuid.NewString()
	now := time.Now().UTC()
	// encryption_key/shared_publicly are intentionally absent from the
	// UPDATE clause: a re-sync of an existing note must never clobber an
	// already-issued key or its share state.
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
	row := s.db.QueryRowContext(ctx,
		`SELECT `+noteColumns+` FROM notes WHERE repo_id=? AND path=?`,
		repoID, path,
	)
	return scanNote(row)
}

func (s *Store) GetNoteByID(ctx context.Context, id string) (*model.Note, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+noteColumns+` FROM notes WHERE id=?`, id,
	)
	return scanNote(row)
}

func (s *Store) ListNotes(ctx context.Context, repoID string) ([]*model.Note, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+noteColumns+` FROM notes WHERE repo_id=? ORDER BY path`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// GenerateNoteKey returns a fresh base64url-encoded 32-byte random key. It's
// exported so both the lazy get-or-create path below and the forced-rotate
// path used by the unshare/revoke handler can share the exact same
// generation logic without duplicating it.
func GenerateNoteKey() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate note key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// GetOrCreateNoteKey returns the note's current encryption key, generating
// and persisting one if it doesn't have one yet. The write is guarded by a
// WHERE encryption_key='' clause so two near-simultaneous callers can't each
// mint a different key and disagree about which one is actually stored:
// whichever UPDATE lands first wins, and the loser re-reads to return the
// same key the winner persisted.
func (s *Store) GetOrCreateNoteKey(ctx context.Context, noteID string) (string, error) {
	note, err := s.GetNoteByID(ctx, noteID)
	if err != nil {
		return "", err
	}
	if note == nil {
		return "", fmt.Errorf("note not found: %s", noteID)
	}
	if note.EncryptionKey != "" {
		return note.EncryptionKey, nil
	}
	newKey, err := GenerateNoteKey()
	if err != nil {
		return "", err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE notes SET encryption_key=? WHERE id=? AND encryption_key=''`,
		newKey, noteID)
	if err != nil {
		return "", fmt.Errorf("persist note key: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if n == 0 {
		// Someone else's UPDATE already won the race — read back whatever
		// they persisted instead of returning our unused, now-orphaned key.
		note, err = s.GetNoteByID(ctx, noteID)
		if err != nil {
			return "", err
		}
		if note == nil {
			return "", fmt.Errorf("note not found: %s", noteID)
		}
		return note.EncryptionKey, nil
	}
	return newKey, nil
}

// SetNoteShared always writes shared_publicly and encryption_key together so
// the two columns can never drift out of sync (e.g. shared=true with a
// stale/empty key).
func (s *Store) SetNoteShared(ctx context.Context, noteID string, shared bool, key string) error {
	v := 0
	if shared {
		v = 1
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE notes SET shared_publicly=?, encryption_key=? WHERE id=?`,
		v, key, noteID)
	return err
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

func (s *Store) DeleteNote(ctx context.Context, repoID, path string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE repo_id=? AND path=?`, repoID, path)
	return err
}

// GetBacklinks returns notes (in the same repo) that link to targetPath.
func (s *Store) GetBacklinks(ctx context.Context, repoID, targetPath string) ([]*model.Note, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.repo_id, n.path, n.updated_at, n.encryption_key, n.shared_publicly
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
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
