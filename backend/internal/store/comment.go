package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/pubobs/backend/internal/model"
)

func (s *Store) CreateComment(ctx context.Context, id, noteID, userID string, parentID *string, body string) (*model.Comment, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO comments (id, note_id, user_id, parent_id, body, created_at) VALUES (?,?,?,?,?,?)`,
		id, noteID, userID, parentID, body, time.Now().UTC(),
	)
	if err != nil {
		return nil, err
	}
	return &model.Comment{
		ID: id, NoteID: noteID, UserID: userID, ParentID: parentID, Body: body,
	}, nil
}

type CommentWithAuthor struct {
	ID          string
	ParentID    *string
	Body        string
	CreatedAt   time.Time
	AuthorEmail string
	AuthorName  string
}

func (s *Store) ListCommentsWithAuthor(ctx context.Context, noteID string) ([]*CommentWithAuthor, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.parent_id, c.body, c.created_at,
		       COALESCE(u.email, ''), COALESCE(u.name, u.email, '')
		FROM comments c
		LEFT JOIN users u ON u.id = c.user_id
		WHERE c.note_id = ? ORDER BY c.created_at`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CommentWithAuthor
	for rows.Next() {
		var c CommentWithAuthor
		var parentID sql.NullString
		if err := rows.Scan(&c.ID, &parentID, &c.Body, &c.CreatedAt, &c.AuthorEmail, &c.AuthorName); err != nil {
			return nil, err
		}
		if parentID.Valid {
			c.ParentID = &parentID.String
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (s *Store) ListComments(ctx context.Context, noteID string) ([]*model.Comment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, note_id, user_id, parent_id, body, created_at
		FROM comments WHERE note_id=? ORDER BY created_at`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Comment
	for rows.Next() {
		var c model.Comment
		var parentID sql.NullString
		if err := rows.Scan(&c.ID, &c.NoteID, &c.UserID, &parentID, &c.Body, &c.CreatedAt); err != nil {
			return nil, err
		}
		if parentID.Valid {
			c.ParentID = &parentID.String
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}
