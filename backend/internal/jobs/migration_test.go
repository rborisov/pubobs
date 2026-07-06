// backend/internal/jobs/migration_test.go
package jobs_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pubobs/backend/internal/jobs"
	"github.com/pubobs/backend/internal/renderstore"
	"github.com/stretchr/testify/require"
)

// verifyFaultStore wraps a real *renderstore.LocalRenderStore and delegates
// Write/Delete to it untouched, but intercepts Read to inject a fault for
// specific (repoID, notePath) keys. RunMigrationCycle's migrateOne only ever
// calls Read on the destination store once per entry — immediately after
// Write, to verify the blob landed correctly — so corrupting Read here
// deterministically targets that verification step without needing to
// distinguish it from any other call.
type verifyFaultStore struct {
	inner *renderstore.LocalRenderStore
	// faults maps "repoID/notePath" -> the corrupted bytes to return from
	// Read instead of what was actually written.
	faults map[string][]byte
	// errFaults marks keys whose Read should instead return an error.
	errFaults map[string]bool
}

var errInjectedVerifyFailure = errors.New("injected verify read failure")

func newVerifyFaultStore(inner *renderstore.LocalRenderStore) *verifyFaultStore {
	return &verifyFaultStore{
		inner:     inner,
		faults:    map[string][]byte{},
		errFaults: map[string]bool{},
	}
}

func (s *verifyFaultStore) key(repoID, notePath string) string {
	return repoID + "/" + notePath
}

func (s *verifyFaultStore) corruptWith(repoID, notePath string, wrongBytes []byte) {
	s.faults[s.key(repoID, notePath)] = wrongBytes
}

func (s *verifyFaultStore) failWith(repoID, notePath string) {
	s.errFaults[s.key(repoID, notePath)] = true
}

func (s *verifyFaultStore) Write(repoID, notePath string, data []byte) error {
	return s.inner.Write(repoID, notePath, data)
}

func (s *verifyFaultStore) Delete(repoID, notePath string) error {
	return s.inner.Delete(repoID, notePath)
}

func (s *verifyFaultStore) Read(repoID, notePath string) ([]byte, error) {
	k := s.key(repoID, notePath)
	if s.errFaults[k] {
		return nil, errInjectedVerifyFailure
	}
	if wrong, ok := s.faults[k]; ok {
		return wrong, nil
	}
	return s.inner.Read(repoID, notePath)
}

func TestRunMigrationCycle_movesRenderAndAssetBlobs(t *testing.T) {
	ctx := context.Background()

	sourceRenders := renderstore.NewLocal(t.TempDir())
	destRenders := renderstore.NewLocal(t.TempDir())
	sourceAssets := renderstore.NewLocal(t.TempDir())
	destAssets := renderstore.NewLocal(t.TempDir())

	require.NoError(t, sourceRenders.Write("r1", "note1.md", []byte("render-1")))
	require.NoError(t, sourceRenders.Write("r2", "note2.md", []byte("render-2")))
	require.NoError(t, sourceAssets.Write("r1", "img.png", []byte("asset-1")))

	migrated, failed, err := jobs.RunMigrationCycle(ctx, sourceRenders, destRenders, sourceAssets, destAssets)
	require.NoError(t, err)
	require.Equal(t, 3, migrated)
	require.Equal(t, 0, failed)

	got, err := destRenders.Read("r1", "note1.md")
	require.NoError(t, err)
	require.Equal(t, []byte("render-1"), got)
	got2, err := destRenders.Read("r2", "note2.md")
	require.NoError(t, err)
	require.Equal(t, []byte("render-2"), got2)
	gotAsset, err := destAssets.Read("r1", "img.png")
	require.NoError(t, err)
	require.Equal(t, []byte("asset-1"), gotAsset)

	// Source copies are removed only after a verified write to the destination.
	afterSource, err := sourceRenders.Read("r1", "note1.md")
	require.NoError(t, err)
	require.Nil(t, afterSource, "migrated file should be removed from source")
}

// TestRunMigrationCycle_verifyFailure_doesNotDeleteSource proves the core
// data-safety property of RunMigrationCycle: if the post-write verification
// read from the destination comes back wrong (mismatched bytes, or an
// error), the source copy must survive and the entry must count as failed,
// not migrated — and a failure on one entry must not stop other entries in
// the same run from migrating successfully.
func TestRunMigrationCycle_verifyFailure_doesNotDeleteSource(t *testing.T) {
	ctx := context.Background()

	sourceRenders := renderstore.NewLocal(t.TempDir())
	destRendersInner := renderstore.NewLocal(t.TempDir())
	destRenders := newVerifyFaultStore(destRendersInner)

	sourceAssets := renderstore.NewLocal(t.TempDir())
	destAssets := renderstore.NewLocal(t.TempDir())

	// Three render entries:
	//   - "good.md" migrates cleanly.
	//   - "mismatch.md" writes fine, but the verification read comes back
	//     with different bytes than what was written (e.g. corruption).
	//   - "readerr.md" writes fine, but the verification read itself errors.
	require.NoError(t, sourceRenders.Write("r1", "good.md", []byte("good-data")))
	require.NoError(t, sourceRenders.Write("r1", "mismatch.md", []byte("original-data")))
	require.NoError(t, sourceRenders.Write("r1", "readerr.md", []byte("readerr-data")))

	destRenders.corruptWith("r1", "mismatch.md", []byte("corrupted-data"))
	destRenders.failWith("r1", "readerr.md")

	migrated, failed, err := jobs.RunMigrationCycle(ctx, sourceRenders, destRenders, sourceAssets, destAssets)
	require.NoError(t, err)
	require.Equal(t, 1, migrated, "only the entry that verifies cleanly should count as migrated")
	require.Equal(t, 2, failed, "both the mismatch and the read-error entries should count as failed")

	// The two entries whose verification failed must still be readable from
	// the source — they were never deleted.
	stillThere, err := sourceRenders.Read("r1", "mismatch.md")
	require.NoError(t, err)
	require.Equal(t, []byte("original-data"), stillThere, "source copy must survive a verify mismatch")

	stillThere2, err := sourceRenders.Read("r1", "readerr.md")
	require.NoError(t, err)
	require.Equal(t, []byte("readerr-data"), stillThere2, "source copy must survive a verify read error")

	// The successful entry should be gone from the source and present on
	// the (real, underlying) destination.
	gone, err := sourceRenders.Read("r1", "good.md")
	require.NoError(t, err)
	require.Nil(t, gone, "the entry that verified cleanly should be removed from the source")

	migratedData, err := destRendersInner.Read("r1", "good.md")
	require.NoError(t, err)
	require.Equal(t, []byte("good-data"), migratedData)

	// The failed entries must NOT have been written to the real underlying
	// destination store under the corrupted bytes we return from Read —
	// i.e. destRendersInner genuinely has whatever migrateOne actually
	// wrote (the real bytes), confirming the fault is purely in the
	// verification read, not a broken Write.
	rawMismatch, err := destRendersInner.Read("r1", "mismatch.md")
	require.NoError(t, err)
	require.Equal(t, []byte("original-data"), rawMismatch, "Write itself should have succeeded with the correct bytes")
}
