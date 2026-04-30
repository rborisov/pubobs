package db_test

import (
	"testing"

	"github.com/pubobs/backend/internal/db"
	"github.com/stretchr/testify/require"
)

func TestOpen_createsAllTables(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()

	tables := []string{
		"users", "groups", "group_members", "repos", "repo_access",
		"notes", "note_snapshots", "comments", "note_links",
		"folder_mappings", "system_health",
	}
	for _, tbl := range tables {
		var name string
		err := d.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl,
		).Scan(&name)
		require.NoError(t, err, "table %q should exist", tbl)
		require.Equal(t, tbl, name)
	}
}

func TestOpen_idempotent(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	d.Close()

	d2, err := db.Open(":memory:")
	require.NoError(t, err)
	d2.Close()
}
