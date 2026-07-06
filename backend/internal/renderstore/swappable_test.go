package renderstore_test

import (
	"testing"

	"github.com/pubobs/backend/internal/renderstore"
	"github.com/stretchr/testify/require"
)

func TestSwappableStore_swapRedirectsSubsequentCalls(t *testing.T) {
	first := renderstore.NewLocal(t.TempDir())
	second := renderstore.NewLocal(t.TempDir())

	s := renderstore.NewSwappableStore(first)
	require.NoError(t, s.Write("r1", "a.md", []byte("via-first")))

	s.Swap(second)
	require.NoError(t, s.Write("r1", "a.md", []byte("via-second")))

	// The write after Swap went to `second`, not `first`.
	fromFirst, err := first.Read("r1", "a.md")
	require.NoError(t, err)
	require.Equal(t, []byte("via-first"), fromFirst)

	fromSecond, err := second.Read("r1", "a.md")
	require.NoError(t, err)
	require.Equal(t, []byte("via-second"), fromSecond)

	// Reads through the wrapper reflect whichever store is current.
	viaWrapper, err := s.Read("r1", "a.md")
	require.NoError(t, err)
	require.Equal(t, []byte("via-second"), viaWrapper)

	require.Equal(t, second, s.Current())
}
