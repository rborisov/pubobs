package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	g, err := s.CreateGroup(ctx, "grp1", "Engineering")
	require.NoError(t, err)
	require.Equal(t, "grp1", g.ID)
	require.Equal(t, "Engineering", g.Name)

	groups, err := s.ListGroups(ctx)
	require.NoError(t, err)
	require.Len(t, groups, 1)
}

func TestGroupMembers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.UpsertUser(ctx, "u2", "b@x.com", "B")
	s.CreateGroup(ctx, "g1", "Team")

	require.NoError(t, s.AddGroupMember(ctx, "g1", "u1"))
	require.NoError(t, s.AddGroupMember(ctx, "g1", "u2"))

	members, err := s.GetGroupMembers(ctx, "g1")
	require.NoError(t, err)
	require.Len(t, members, 2)

	require.NoError(t, s.RemoveGroupMember(ctx, "g1", "u1"))
	members, _ = s.GetGroupMembers(ctx, "g1")
	require.Len(t, members, 1)
}
