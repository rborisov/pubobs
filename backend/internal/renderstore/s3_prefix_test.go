package renderstore_test

import (
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/pubobs/backend/internal/renderstore"
	"github.com/stretchr/testify/require"
)

func TestNew_localIgnoresKeyPrefix(t *testing.T) {
	dir := t.TempDir()
	s, err := renderstore.New("local", dir, "", "", "", "", "", false, "assets/")
	require.NoError(t, err)

	require.NoError(t, s.Write("repo-1", "img.png", []byte("data")))
	got, err := s.Read("repo-1", "img.png")
	require.NoError(t, err)
	require.Equal(t, []byte("data"), got)
}

func TestNewS3_keyPrefixSeparatesNamespaces(t *testing.T) {
	// NewS3 only builds a client and never dials out until an operation is
	// performed, so this only exercises the key-building logic without needing
	// a real S3 endpoint: same repoID+path under different prefixes.
	rendersStore, err := renderstore.NewS3("localhost:9000", "bucket", "ak", "sk", "us-east-1", false, "renders/")
	require.NoError(t, err)
	assetsStore, err := renderstore.NewS3("localhost:9000", "bucket", "ak", "sk", "us-east-1", false, "assets/")
	require.NoError(t, err)

	require.NotEqual(t, rendersStore.ObjectKey("repo-1", "note.md"), assetsStore.ObjectKey("repo-1", "note.md"))
	require.Equal(t, "renders/repo-1/note.md.enc", rendersStore.ObjectKey("repo-1", "note.md"))
	require.Equal(t, "assets/repo-1/note.md.enc", assetsStore.ObjectKey("repo-1", "note.md"))
}

func TestSumObjectSizesWithPrefix(t *testing.T) {
	objects := []minio.ObjectInfo{
		{Key: "renders/repo1/a.md.enc", Size: 100},
		{Key: "renders/repo2/b.md.enc", Size: 250},
		{Key: "assets/repo1/img.png.enc", Size: 4000},
	}
	require.Equal(t, int64(350), renderstore.SumObjectSizesWithPrefix(objects, "renders/"))
	require.Equal(t, int64(4000), renderstore.SumObjectSizesWithPrefix(objects, "assets/"))
	require.Equal(t, int64(0), renderstore.SumObjectSizesWithPrefix(objects, "nope/"))
}
