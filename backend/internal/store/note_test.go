package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNoteUpsertAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "c", "main")

	n, err := s.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.NotEmpty(t, n.ID)
	require.Equal(t, "docs/intro.md", n.Path)

	// Upsert same path → returns same note ID
	n2, err := s.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.Equal(t, n.ID, n2.ID)

	notes, err := s.ListNotes(ctx, "r1")
	require.NoError(t, err)
	require.Len(t, notes, 1)
}

func TestNoteSnapshot(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "c", "main")
	n, _ := s.UpsertNote(ctx, "r1", "intro.md")

	err := s.UpsertSnapshot(ctx, n.ID, "<h1>Intro</h1>", `{"links":["other"]}`, "u1", "abc1234")
	require.NoError(t, err)

	snap, err := s.GetSnapshot(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, "<h1>Intro</h1>", snap.HTMLContent)
	require.Equal(t, "abc1234", snap.GitCommitSHA)
}

func TestNoteLinks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "c", "main")
	src, _ := s.UpsertNote(ctx, "r1", "intro.md")
	tgt, _ := s.UpsertNote(ctx, "r1", "other.md")

	err := s.UpsertNoteLinks(ctx, src.ID, []string{"other.md", "missing.md"})
	require.NoError(t, err)

	backlinks, err := s.GetBacklinks(ctx, "r1", "other.md")
	require.NoError(t, err)
	require.Len(t, backlinks, 1)
	require.Equal(t, src.ID, backlinks[0].ID)
	_ = tgt
}
