// backend/internal/renderstore/walkrepo_test.go
package renderstore_test

import (
	"sort"
	"testing"

	"github.com/pubobs/backend/internal/renderstore"
	"github.com/stretchr/testify/require"
)

func TestLocalRenderStore_WalkRepoEntries(t *testing.T) {
	s := renderstore.NewLocal(t.TempDir())
	require.NoError(t, s.Write("r1", "a.md", []byte("1")))
	require.NoError(t, s.Write("r1", "sub/b.md", []byte("2")))
	require.NoError(t, s.Write("r2", "c.md", []byte("3")))

	var got []string
	require.NoError(t, s.WalkRepoEntries("r1", func(notePath string) error {
		got = append(got, notePath)
		return nil
	}))
	sort.Strings(got)
	require.Equal(t, []string{"a.md", "sub/b.md"}, got)

	// Missing repo dir → no entries, no error.
	require.NoError(t, s.WalkRepoEntries("nope", func(string) error { return nil }))
}
