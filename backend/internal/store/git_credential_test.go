package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pubobs/backend/internal/db"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return New(conn)
}

func TestUserCredential_roundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, err := s.CreateRepo(ctx, "r1", "R", "https://x/r1.git", "", "main")
	require.NoError(t, err)

	// none yet
	_, ok, err := s.GetUserCredentialSecret(ctx, "r1", "u1")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, s.UpsertUserCredential(ctx, "r1", "u1", "ENC", "Alice", "a@x.com"))
	sec, ok, err := s.GetUserCredentialSecret(ctx, "r1", "u1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "ENC", sec)

	require.NoError(t, s.SetUserCredentialVerification(ctx, "r1", "u1", "verified", ""))
	list, err := s.ListUserCredentials(ctx, "r1")
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "Alice", list[0].GitName)
	require.Equal(t, "verified", list[0].VerifyStatus)

	require.NoError(t, s.DeleteUserCredential(ctx, "r1", "u1"))
	_, ok, _ = s.GetUserCredentialSecret(ctx, "r1", "u1")
	require.False(t, ok)
}

func TestRepoOwnerAndStrict(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, err := s.CreateRepo(ctx, "r1", "R", "https://x/r1.git", "", "main")
	require.NoError(t, err)
	require.NoError(t, s.SetRepoOwner(ctx, "r1", "u1"))
	require.NoError(t, s.SetRepoStrictCredentials(ctx, "r1", true))
	repo, err := s.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.NotNil(t, repo.OwnerUserID)
	require.Equal(t, "u1", *repo.OwnerUserID)
	require.True(t, repo.StrictCredentials)
}
