package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func mustParseTime(s string) interface{} { return s }

func TestRepoCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r, err := s.CreateRepo(ctx, "r1", "My Repo", "https://github.com/org/repo.git", "enc-creds", "main")
	require.NoError(t, err)
	require.Equal(t, "r1", r.ID)
	require.Nil(t, r.LocalPath)

	got, err := s.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.Equal(t, "My Repo", got.Name)

	err = s.UpdateRepoLocalPath(ctx, "r1", "/data/repos/r1", mustParseTime("2024-01-01T00:00:00Z"))
	require.NoError(t, err)

	got, _ = s.GetRepo(ctx, "r1")
	require.NotNil(t, got.LocalPath)
	require.Equal(t, "/data/repos/r1", *got.LocalPath)

	err = s.ClearRepoLocalPath(ctx, "r1")
	require.NoError(t, err)
	got, _ = s.GetRepo(ctx, "r1")
	require.Nil(t, got.LocalPath)

	repos, err := s.ListRepos(ctx)
	require.NoError(t, err)
	require.Len(t, repos, 1)

	err = s.DeleteRepo(ctx, "r1")
	require.NoError(t, err)
	repos, _ = s.ListRepos(ctx)
	require.Len(t, repos, 0)
}

func TestGetUserRole(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertUser(ctx, "u1", "a@x.com", "A")
	s.UpsertUser(ctx, "u2", "b@x.com", "B")
	s.CreateGroup(ctx, "g1", "Readers")
	s.AddGroupMember(ctx, "g1", "u2")
	s.CreateRepo(ctx, "r1", "Repo", "https://x.com/r.git", "c", "main")

	err := s.GrantAccess(ctx, "acc1", "r1", "user", "u1", "editor")
	require.NoError(t, err)
	err = s.GrantAccess(ctx, "acc2", "r1", "group", "g1", "reader")
	require.NoError(t, err)

	role, err := s.GetUserRole(ctx, "u1", "r1")
	require.NoError(t, err)
	require.Equal(t, "editor", role)

	role, err = s.GetUserRole(ctx, "u2", "r1")
	require.NoError(t, err)
	require.Equal(t, "reader", role)

	// u1 also in group (reader), but direct editor wins
	s.AddGroupMember(ctx, "g1", "u1")
	role, _ = s.GetUserRole(ctx, "u1", "r1")
	require.Equal(t, "editor", role)

	// unknown user → empty role
	role, err = s.GetUserRole(ctx, "nobody", "r1")
	require.NoError(t, err)
	require.Equal(t, "", role)
}
