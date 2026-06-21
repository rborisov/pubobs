// backend/internal/renderstore/store_test.go
package renderstore_test

import (
	"testing"

	"github.com/pubobs/backend/internal/renderstore"
	"github.com/stretchr/testify/require"
)

func TestLocalRenderStore(t *testing.T) {
	dir := t.TempDir()
	s := renderstore.NewLocal(dir)

	const repoID = "repo-1"
	const path = "notes/hello.md"
	data := []byte("encrypted-bytes")

	// Write then Read
	require.NoError(t, s.Write(repoID, path, data))
	got, err := s.Read(repoID, path)
	require.NoError(t, err)
	require.Equal(t, data, got)

	// Read missing key returns nil, no error
	missing, err := s.Read(repoID, "does/not/exist.md")
	require.NoError(t, err)
	require.Nil(t, missing)

	// Delete removes the entry
	require.NoError(t, s.Delete(repoID, path))
	after, err := s.Read(repoID, path)
	require.NoError(t, err)
	require.Nil(t, after)

	// Delete of missing key is a no-op
	require.NoError(t, s.Delete(repoID, "ghost.md"))
}

func TestLocalRenderStore_nestedPath(t *testing.T) {
	dir := t.TempDir()
	s := renderstore.NewLocal(dir)

	require.NoError(t, s.Write("r1", "a/b/c/note.md", []byte("data")))
	got, err := s.Read("r1", "a/b/c/note.md")
	require.NoError(t, err)
	require.Equal(t, []byte("data"), got)
}
