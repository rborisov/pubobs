package store

import (
	"context"
	"fmt"

	"github.com/pubobs/backend/internal/model"
)

var roleOrder = map[string]int{
	"reader": 1, "commentator": 2, "editor": 3, "admin": 4,
}

func (s *Store) GrantAccess(ctx context.Context, id, repoID, principalType, principalID, role string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO repo_access (id, repo_id, principal_type, principal_id, role)
		VALUES (?,?,?,?,?)
		ON CONFLICT(repo_id, principal_type, principal_id) DO UPDATE SET role=excluded.role`,
		id, repoID, principalType, principalID, role,
	)
	return err
}

func (s *Store) RevokeAccess(ctx context.Context, accessID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM repo_access WHERE id=?`, accessID)
	return err
}

func (s *Store) ListRepoAccess(ctx context.Context, repoID string) ([]*model.RepoAccess, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, repo_id, principal_type, principal_id, role FROM repo_access WHERE repo_id=?`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.RepoAccess
	for rows.Next() {
		var a model.RepoAccess
		if err := rows.Scan(&a.ID, &a.RepoID, &a.PrincipalType, &a.PrincipalID, &a.Role); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// GetUserRole returns the highest role the user has on the repo (direct or via group).
// Returns "" if the user has no access.
func (s *Store) GetUserRole(ctx context.Context, userID, repoID string) (string, error) {
	groupIDs, err := s.GetUserGroupIDs(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("get user groups: %w", err)
	}

	best := ""
	setBest := func(role string) {
		if roleOrder[role] > roleOrder[best] {
			best = role
		}
	}

	var directRole string
	if err := s.db.QueryRowContext(ctx,
		`SELECT role FROM repo_access WHERE repo_id=? AND principal_type='user' AND principal_id=?`,
		repoID, userID,
	).Scan(&directRole); err == nil {
		setBest(directRole)
	}

	for _, gid := range groupIDs {
		var groupRole string
		if err := s.db.QueryRowContext(ctx,
			`SELECT role FROM repo_access WHERE repo_id=? AND principal_type='group' AND principal_id=?`,
			repoID, gid,
		).Scan(&groupRole); err == nil {
			setBest(groupRole)
		}
	}

	return best, nil
}

// ListUserRepos returns all repos the user has any access to (direct or via group).
func (s *Store) ListUserRepos(ctx context.Context, userID string) ([]*model.Repo, error) {
	groupIDs, err := s.GetUserGroupIDs(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user groups: %w", err)
	}

	seen := map[string]bool{}

	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT repo_id FROM repo_access WHERE principal_type='user' AND principal_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rid string
		rows.Scan(&rid)
		seen[rid] = true
	}

	for _, gid := range groupIDs {
		rows2, err := s.db.QueryContext(ctx,
			`SELECT DISTINCT repo_id FROM repo_access WHERE principal_type='group' AND principal_id=?`, gid)
		if err != nil {
			return nil, err
		}
		for rows2.Next() {
			var rid string
			rows2.Scan(&rid)
			seen[rid] = true
		}
		rows2.Close()
	}

	var out []*model.Repo
	for rid := range seen {
		r, err := s.GetRepo(ctx, rid)
		if err != nil {
			return nil, err
		}
		if r != nil {
			out = append(out, r)
		}
	}
	return out, nil
}
