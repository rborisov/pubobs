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

	require.NoError(t, s.AddGroupMember(ctx, "g1", "u1", "member"))
	require.NoError(t, s.AddGroupMember(ctx, "g1", "u2", "member"))

	members, err := s.GetGroupMembers(ctx, "g1")
	require.NoError(t, err)
	require.Len(t, members, 2)

	require.NoError(t, s.RemoveGroupMember(ctx, "g1", "u1"))
	members, _ = s.GetGroupMembers(ctx, "g1")
	require.Len(t, members, 1)
}

func TestGroupMemberRoles(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.UpsertUser(ctx, "u2", "b@x.com", "B")
	s.CreateGroup(ctx, "g1", "Team")

	require.NoError(t, s.AddGroupMember(ctx, "g1", "u1", "admin"))
	require.NoError(t, s.AddGroupMember(ctx, "g1", "u2", "member"))

	members, err := s.ListGroupMembers(ctx, "g1")
	require.NoError(t, err)
	require.Len(t, members, 2)

	roles := map[string]string{}
	for _, m := range members {
		roles[m.UserID] = m.Role
	}
	require.Equal(t, "admin", roles["u1"])
	require.Equal(t, "member", roles["u2"])
}

func TestIsGroupAdmin(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.UpsertUser(ctx, "u2", "b@x.com", "B")
	s.CreateGroup(ctx, "g1", "Team")
	s.AddGroupMember(ctx, "g1", "u1", "admin")
	s.AddGroupMember(ctx, "g1", "u2", "member")

	ok, err := s.IsGroupAdmin(ctx, "g1", "u1")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = s.IsGroupAdmin(ctx, "g1", "u2")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestSetGroupMemberRole(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.CreateGroup(ctx, "g1", "Team")
	s.AddGroupMember(ctx, "g1", "u1", "member")

	require.NoError(t, s.SetGroupMemberRole(ctx, "g1", "u1", "admin"))

	ok, _ := s.IsGroupAdmin(ctx, "g1", "u1")
	require.True(t, ok)
}

func TestListAdminGroups(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.CreateGroup(ctx, "g1", "Alpha")
	s.CreateGroup(ctx, "g2", "Beta")
	s.AddGroupMember(ctx, "g1", "u1", "admin")
	s.AddGroupMember(ctx, "g2", "u1", "member")

	groups, err := s.ListAdminGroups(ctx, "u1")
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, "g1", groups[0].ID)
}

func TestDeleteGroup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateGroup(ctx, "g1", "Team")
	require.NoError(t, s.DeleteGroup(ctx, "g1"))

	groups, _ := s.ListGroups(ctx)
	require.Len(t, groups, 0)
}
