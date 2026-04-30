package store_test

import (
	"context"
	"testing"

	"github.com/pubobs/backend/internal/db"
	"github.com/pubobs/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	return store.New(d)
}

func TestUpsertAndGetUser(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u, err := s.UpsertUser(ctx, "user1", "alice@example.com", "Alice")
	require.NoError(t, err)
	require.Equal(t, "user1", u.ID)
	require.Equal(t, "alice@example.com", u.Email)
	require.False(t, u.IsInstanceAdmin)

	u2, err := s.UpsertUser(ctx, "user1", "alice@example.com", "Alice Smith")
	require.NoError(t, err)
	require.Equal(t, "Alice Smith", u2.Name)

	got, err := s.GetUserByID(ctx, "user1")
	require.NoError(t, err)
	require.Equal(t, "Alice Smith", got.Name)
}

func TestGetUserByEmail(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.UpsertUser(ctx, "user2", "bob@example.com", "Bob")
	require.NoError(t, err)

	got, err := s.GetUserByEmail(ctx, "bob@example.com")
	require.NoError(t, err)
	require.Equal(t, "user2", got.ID)
}

func TestListUsers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.UpsertUser(ctx, "u2", "b@x.com", "B")

	users, err := s.ListUsers(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
}

func TestSetInstanceAdmin(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.UpsertUser(ctx, "u1", "a@x.com", "A")

	err := s.SetInstanceAdmin(ctx, "u1", true)
	require.NoError(t, err)

	u, err := s.GetUserByID(ctx, "u1")
	require.NoError(t, err)
	require.True(t, u.IsInstanceAdmin)
}
