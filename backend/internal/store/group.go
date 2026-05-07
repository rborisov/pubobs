package store

import (
	"context"
	"fmt"
	"time"

	"github.com/pubobs/backend/internal/model"
)

func (s *Store) CreateGroup(ctx context.Context, id, name string) (*model.Group, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO groups (id, name, created_at) VALUES (?,?,?)`,
		id, name, time.Now().UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("create group: %w", err)
	}
	return &model.Group{ID: id, Name: name}, nil
}

func (s *Store) ListGroups(ctx context.Context) ([]*model.Group, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, created_at FROM groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Group
	for rows.Next() {
		var g model.Group
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}

func (s *Store) AddGroupMember(ctx context.Context, groupID, userID, role string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO group_members (group_id, user_id, role) VALUES (?,?,?)
         ON CONFLICT(group_id, user_id) DO NOTHING`,
		groupID, userID, role)
	return err
}

func (s *Store) RemoveGroupMember(ctx context.Context, groupID, userID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM group_members WHERE group_id=? AND user_id=?`, groupID, userID)
	return err
}

func (s *Store) GetGroupMembers(ctx context.Context, groupID string) ([]*model.User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.email, u.name, u.is_instance_admin, u.is_banned, u.is_admin, u.created_at
		FROM users u
		JOIN group_members gm ON gm.user_id = u.id
		WHERE gm.group_id=?`, groupID)
	if err != nil {
		return nil, err
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

func (s *Store) ListGroupMembers(ctx context.Context, groupID string) ([]*model.GroupMember, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT group_id, user_id, role FROM group_members WHERE group_id=? ORDER BY user_id`,
		groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.GroupMember
	for rows.Next() {
		var m model.GroupMember
		if err := rows.Scan(&m.GroupID, &m.UserID, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

func (s *Store) IsGroupAdmin(ctx context.Context, groupID, userID string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id=? AND user_id=? AND role='admin')`,
		groupID, userID,
	).Scan(&exists)
	return exists == 1, err
}

func (s *Store) SetGroupMemberRole(ctx context.Context, groupID, userID, role string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE group_members SET role=? WHERE group_id=? AND user_id=?`,
		role, groupID, userID)
	return err
}

func (s *Store) ListAdminGroups(ctx context.Context, userID string) ([]*model.Group, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT g.id, g.name, g.created_at
        FROM groups g
        JOIN group_members gm ON gm.group_id = g.id
        WHERE gm.user_id=? AND gm.role='admin'
        ORDER BY g.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Group
	for rows.Next() {
		var g model.Group
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}

func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM groups WHERE id=?`, id)
	return err
}

// GetUserGroupIDs returns all group IDs a user belongs to.
func (s *Store) GetUserGroupIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT group_id FROM group_members WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
