package store_test

import (
	"context"
	"testing"

	"github.com/pubobs/backend/internal/store"
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

func TestGetOrCreateNoteKey(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "c", "main")
	n, _ := s.UpsertNote(ctx, "r1", "intro.md")

	// Empty initially.
	require.Empty(t, n.EncryptionKey)
	require.False(t, n.SharedPublicly)

	key, err := s.GetOrCreateNoteKey(ctx, n.ID)
	require.NoError(t, err)
	require.NotEmpty(t, key)

	got, err := s.GetNoteByID(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, key, got.EncryptionKey)

	// Calling again is idempotent — same key, not regenerated.
	key2, err := s.GetOrCreateNoteKey(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, key, key2)
}

func TestSetNoteShared(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.CreateRepo(ctx, "r1", "R", "https://x.com/r.git", "c", "main")
	n, _ := s.UpsertNote(ctx, "r1", "intro.md")

	key, err := s.GetOrCreateNoteKey(ctx, n.ID)
	require.NoError(t, err)

	require.NoError(t, s.SetNoteShared(ctx, n.ID, true, key))

	got, err := s.GetNoteByID(ctx, n.ID)
	require.NoError(t, err)
	require.True(t, got.SharedPublicly)
	require.Equal(t, key, got.EncryptionKey)

	// A subsequent GetOrCreateNoteKey call reuses this key rather than
	// regenerating it (mirrors the "public" share-mode handler behavior).
	same, err := s.GetOrCreateNoteKey(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, key, same)

	// Revoking updates both columns together.
	newKey, err := store.GenerateNoteKey()
	require.NoError(t, err)
	require.NotEqual(t, key, newKey)
	require.NoError(t, s.SetNoteShared(ctx, n.ID, false, newKey))

	got, err = s.GetNoteByID(ctx, n.ID)
	require.NoError(t, err)
	require.False(t, got.SharedPublicly)
	require.Equal(t, newKey, got.EncryptionKey)
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
