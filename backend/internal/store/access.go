package store

import (
	"context"
	"fmt"
	"strings"

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
	repoIDs, err := s.accessibleRepoIDs(ctx, userID, groupIDs)
	if err != nil {
		return nil, err
	}
	// A single batched fetch instead of one GetRepo round trip per repo ID —
	// with many repos this was the dominant cost of loading a member's
	// dashboard on the single shared SQLite connection.
	return s.getReposByIDs(ctx, repoIDs)
}

// accessibleRepoIDs returns the deduplicated set of repo IDs reachable by the
// user directly or through any of the given groups.
func (s *Store) accessibleRepoIDs(ctx context.Context, userID string, groupIDs []string) ([]string, error) {
	seen := map[string]bool{}

	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT repo_id FROM repo_access WHERE principal_type='user' AND principal_id=?`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var rid string
		if err := rows.Scan(&rid); err != nil {
			rows.Close()
			return nil, err
		}
		seen[rid] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if len(groupIDs) > 0 {
		placeholders := make([]string, len(groupIDs))
		args := make([]interface{}, len(groupIDs))
		for i, gid := range groupIDs {
			placeholders[i] = "?"
			args[i] = gid
		}
		query := fmt.Sprintf(
			`SELECT DISTINCT repo_id FROM repo_access WHERE principal_type='group' AND principal_id IN (%s)`,
			strings.Join(placeholders, ","))
		rows2, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows2.Next() {
			var rid string
			if err := rows2.Scan(&rid); err != nil {
				rows2.Close()
				return nil, err
			}
			seen[rid] = true
		}
		if err := rows2.Err(); err != nil {
			rows2.Close()
			return nil, err
		}
		rows2.Close()
	}

	ids := make([]string, 0, len(seen))
	for rid := range seen {
		ids = append(ids, rid)
	}
	return ids, nil
}

// GetUserRolesForRepos resolves the user's best role (direct or via group) on
// each of the given repos in a single query, rather than the O(repos) round
// trips (each re-fetching group membership) that calling GetUserRole in a
// loop would cost. Repos the user has no access to are simply absent from
// the returned map.
func (s *Store) GetUserRolesForRepos(ctx context.Context, userID string, repoIDs []string) (map[string]string, error) {
	roles := make(map[string]string, len(repoIDs))
	if len(repoIDs) == 0 {
		return roles, nil
	}
	groupIDs, err := s.GetUserGroupIDs(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user groups: %w", err)
	}

	repoPlaceholders := make([]string, len(repoIDs))
	args := make([]interface{}, 0, len(repoIDs)+len(groupIDs)+1)
	for i, id := range repoIDs {
		repoPlaceholders[i] = "?"
		args = append(args, id)
	}

	principalClause := "(principal_type='user' AND principal_id=?)"
	args = append(args, userID)
	if len(groupIDs) > 0 {
		groupPlaceholders := make([]string, len(groupIDs))
		for i, gid := range groupIDs {
			groupPlaceholders[i] = "?"
			args = append(args, gid)
		}
		principalClause += fmt.Sprintf(" OR (principal_type='group' AND principal_id IN (%s))", strings.Join(groupPlaceholders, ","))
	}

	query := fmt.Sprintf(
		`SELECT repo_id, role FROM repo_access WHERE repo_id IN (%s) AND (%s)`,
		strings.Join(repoPlaceholders, ","), principalClause)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var repoID, role string
		if err := rows.Scan(&repoID, &role); err != nil {
			return nil, err
		}
		if roleOrder[role] > roleOrder[roles[repoID]] {
			roles[repoID] = role
		}
	}
	return roles, rows.Err()
}
