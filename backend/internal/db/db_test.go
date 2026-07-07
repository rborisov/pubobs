package db_test

import (
	"path/filepath"
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

// On-disk databases should get WAL mode, a non-zero busy_timeout, and a
// pool of more than one connection — otherwise every request in the process
// serializes behind a single connection (see the dashboard hang this fixed).
// In-memory databases (used elsewhere in the test suite) must stay pinned to
// exactly one connection, since a second pooled connection would otherwise
// see a completely separate, empty database.
func TestOpen_onDiskGetsWALAndConnectionPool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(path)
	require.NoError(t, err)
	defer d.Close()

	var journalMode string
	require.NoError(t, d.QueryRow("PRAGMA journal_mode").Scan(&journalMode))
	require.Equal(t, "wal", journalMode)

	var busyTimeout int
	require.NoError(t, d.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout))
	require.Equal(t, 5000, busyTimeout)

	require.Greater(t, d.Stats().MaxOpenConnections, 1)
}

func TestOpen_inMemoryStaysSingleConnection(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()

	require.Equal(t, 1, d.Stats().MaxOpenConnections)
}
