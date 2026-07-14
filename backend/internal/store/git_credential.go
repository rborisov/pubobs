package store

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) SetRepoOwner(ctx context.Context, repoID, ownerUserID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE repos SET owner_user_id=? WHERE id=?`, ownerUserID, repoID)
	return err
}

func (s *Store) SetRepoStrictCredentials(ctx context.Context, repoID string, strict bool) error {
	v := 0
	if strict {
		v = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE repos SET strict_credentials=? WHERE id=?`, v, repoID)
	return err
}

type UserCredential struct {
	RepoID       string
	UserID       string
	GitName      string
	GitEmail     string
	VerifyStatus string
	VerifyError  string
	VerifiedAt   *time.Time
}

func (s *Store) UpsertUserCredential(ctx context.Context, repoID, userID, encryptedCreds, gitName, gitEmail string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO repo_user_credentials (repo_id, user_id, encrypted_creds, git_name, git_email, verify_status, updated_at)
		VALUES (?, ?, ?, ?, ?, 'unverified', ?)
		ON CONFLICT(repo_id, user_id) DO UPDATE SET
			encrypted_creds=excluded.encrypted_creds,
			git_name=excluded.git_name,
			git_email=excluded.git_email,
			verify_status='unverified', verify_error='', verified_at=NULL,
			updated_at=excluded.updated_at`,
		repoID, userID, encryptedCreds, gitName, gitEmail, time.Now().UTC())
	return err
}

func (s *Store) GetUserCredentialSecret(ctx context.Context, repoID, userID string) (string, bool, error) {
	var enc string
	err := s.db.QueryRowContext(ctx,
		`SELECT encrypted_creds FROM repo_user_credentials WHERE repo_id=? AND user_id=?`, repoID, userID).Scan(&enc)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return enc, true, nil
}

func (s *Store) DeleteUserCredential(ctx context.Context, repoID, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM repo_user_credentials WHERE repo_id=? AND user_id=?`, repoID, userID)
	return err
}

func (s *Store) SetUserCredentialVerification(ctx context.Context, repoID, userID, status, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE repo_user_credentials SET verify_status=?, verify_error=?, verified_at=? WHERE repo_id=? AND user_id=?`,
		status, errMsg, time.Now().UTC(), repoID, userID)
	return err
}

func (s *Store) ListUserCredentials(ctx context.Context, repoID string) ([]UserCredential, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT repo_id, user_id, git_name, git_email, verify_status, verify_error, verified_at
		 FROM repo_user_credentials WHERE repo_id=? ORDER BY user_id`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserCredential
	for rows.Next() {
		var c UserCredential
		if err := rows.Scan(&c.RepoID, &c.UserID, &c.GitName, &c.GitEmail, &c.VerifyStatus, &c.VerifyError, &c.VerifiedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
