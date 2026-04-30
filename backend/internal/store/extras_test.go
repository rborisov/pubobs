package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestComments(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "c", "main")
	note, _ := s.UpsertNote(ctx, "r1", "intro.md")

	c1, err := s.CreateComment(ctx, "c1", note.ID, "u1", nil, "Hello!")
	require.NoError(t, err)
	require.Equal(t, "Hello!", c1.Body)

	// Reply
	c2, err := s.CreateComment(ctx, "c2", note.ID, "u1", &c1.ID, "Reply!")
	require.NoError(t, err)
	require.NotNil(t, c2.ParentID)

	comments, err := s.ListComments(ctx, note.ID)
	require.NoError(t, err)
	require.Len(t, comments, 2)
}

func TestFolderMappings(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "c", "main")

	err := s.UpsertFolderMapping(ctx, "u1", "r1", "MyVaultFolder", "docs")
	require.NoError(t, err)

	m, err := s.GetFolderMapping(ctx, "u1", "r1")
	require.NoError(t, err)
	require.Equal(t, "MyVaultFolder", m.VaultFolder)
	require.Equal(t, "docs", m.RepoSubfolder)

	// Upsert updates
	s.UpsertFolderMapping(ctx, "u1", "r1", "NewFolder", "")
	m, _ = s.GetFolderMapping(ctx, "u1", "r1")
	require.Equal(t, "NewFolder", m.VaultFolder)
}

func TestSystemHealth(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	err := s.UpsertHealth(ctx, 75.5, 1000000, "ok", &now)
	require.NoError(t, err)

	h, err := s.GetHealth(ctx)
	require.NoError(t, err)
	require.Equal(t, "ok", h.DiskStatus)
	require.InDelta(t, 75.5, h.DiskFreePct, 0.01)
}
