// backend/internal/jobs/repo_migration_test.go
package jobs_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/pubobs/backend/internal/jobs"
	"github.com/pubobs/backend/internal/renderstore"
	"github.com/stretchr/testify/require"
)

// TestMain shrinks RunRepoMigration's write pacing/backoff to near-zero for
// the whole test binary. Production defaults (250ms pacing, backoff in the
// hundreds of ms) exist to stay under a real destination's write-rate
// ceiling; a test suite has no such ceiling to respect and would otherwise
// pay that delay on every write in every test in this package.
func TestMain(m *testing.M) {
	jobs.MigrationWriteInterval = 0
	jobs.MigrationRetryBackoff = time.Millisecond
	os.Exit(m.Run())
}

// writeFaultStore wraps a real *renderstore.LocalRenderStore and delegates
// Read/Delete to it untouched, but makes Write fail a configured number of
// times for specific (repoID, notePath) keys before succeeding — simulating
// a destination that rate-limits writes, the way a real S3-compatible
// destination did during a per-repo migration ("Write request rate limit
// exceeded. Limit is 5 req/sec.").
type writeFaultStore struct {
	inner             *renderstore.LocalRenderStore
	failuresRemaining map[string]int
	writeAttempts     map[string]int
}

func newWriteFaultStore(inner *renderstore.LocalRenderStore) *writeFaultStore {
	return &writeFaultStore{
		inner:             inner,
		failuresRemaining: map[string]int{},
		writeAttempts:     map[string]int{},
	}
}

func (s *writeFaultStore) key(repoID, notePath string) string {
	return repoID + "/" + notePath
}

// failWritesTimes makes the next n Write calls for (repoID, notePath) return
// a rate-limit-shaped error before a subsequent Write is allowed through.
func (s *writeFaultStore) failWritesTimes(repoID, notePath string, n int) {
	s.failuresRemaining[s.key(repoID, notePath)] = n
}

func (s *writeFaultStore) attempts(repoID, notePath string) int {
	return s.writeAttempts[s.key(repoID, notePath)]
}

func (s *writeFaultStore) Write(repoID, notePath string, data []byte) error {
	k := s.key(repoID, notePath)
	s.writeAttempts[k]++
	if s.failuresRemaining[k] > 0 {
		s.failuresRemaining[k]--
		return errors.New("mock: Write request rate limit exceeded. Limit is 5 req/sec.")
	}
	return s.inner.Write(repoID, notePath, data)
}

func (s *writeFaultStore) Read(repoID, notePath string) ([]byte, error) {
	return s.inner.Read(repoID, notePath)
}

func (s *writeFaultStore) Delete(repoID, notePath string) error {
	return s.inner.Delete(repoID, notePath)
}

func TestRunRepoMigration_movesOnlyThatRepo(t *testing.T) {
	ctx := context.Background()
	srcRenders := renderstore.NewLocal(t.TempDir())
	dstRenders := renderstore.NewLocal(t.TempDir())
	srcAssets := renderstore.NewLocal(t.TempDir())
	dstAssets := renderstore.NewLocal(t.TempDir())

	require.NoError(t, srcRenders.Write("r1", "note.md", []byte("render-1")))
	require.NoError(t, srcAssets.Write("r1", "img.png", []byte("asset-1")))
	// A different repo's data must be untouched.
	require.NoError(t, srcRenders.Write("r2", "other.md", []byte("render-2")))

	migrated, failed, err := jobs.RunRepoMigration(ctx, "r1", srcRenders, dstRenders, srcAssets, dstAssets)
	require.NoError(t, err)
	require.Equal(t, 2, migrated)
	require.Equal(t, 0, failed)

	// r1 moved.
	gotR, err := dstRenders.Read("r1", "note.md")
	require.NoError(t, err)
	require.Equal(t, []byte("render-1"), gotR)
	gotA, err := dstAssets.Read("r1", "img.png")
	require.NoError(t, err)
	require.Equal(t, []byte("asset-1"), gotA)
	// r1 source deleted after verified write.
	srcGone, err := srcRenders.Read("r1", "note.md")
	require.NoError(t, err)
	require.Nil(t, srcGone)

	// r2 untouched.
	r2, err := srcRenders.Read("r2", "other.md")
	require.NoError(t, err)
	require.Equal(t, []byte("render-2"), r2)
	r2moved, err := dstRenders.Read("r2", "other.md")
	require.NoError(t, err)
	require.Nil(t, r2moved)
}

// TestRunRepoMigration_verifyFailure_doesNotDeleteSource proves that
// RunRepoMigration's own verify-before-delete guard (a separate copy of the
// same logic as RunMigrationCycle's, since RunRepoMigration scopes to a
// single repoID and enumerates via repoWalker) is actually load-bearing: if
// the post-write verification read from the destination render store comes
// back wrong, the source copy must survive and the entry must count as
// failed, not migrated — and that failure must not stop a second, cleanly
// migrating entry in the same run.
func TestRunRepoMigration_verifyFailure_doesNotDeleteSource(t *testing.T) {
	ctx := context.Background()

	srcRenders := renderstore.NewLocal(t.TempDir())
	dstRendersInner := renderstore.NewLocal(t.TempDir())
	dstRenders := newVerifyFaultStore(dstRendersInner)

	srcAssets := renderstore.NewLocal(t.TempDir())
	dstAssets := renderstore.NewLocal(t.TempDir())

	// "bad.md" writes fine but its destination verification read comes back
	// with different bytes than what was written. "good.md" migrates
	// cleanly and must still succeed despite "bad.md"'s failure.
	require.NoError(t, srcRenders.Write("r1", "bad.md", []byte("original-data")))
	require.NoError(t, srcRenders.Write("r1", "good.md", []byte("good-data")))
	dstRenders.corruptWith("r1", "bad.md", []byte("corrupted-data"))

	migrated, failed, err := jobs.RunRepoMigration(ctx, "r1", srcRenders, dstRenders, srcAssets, dstAssets)
	require.NoError(t, err)
	require.Equal(t, 1, migrated, "only the entry that verifies cleanly should count as migrated")
	require.Equal(t, 1, failed, "the verify-mismatch entry should count as failed, not migrated")

	// The entry whose verification failed must still be readable from the
	// source — it was never deleted.
	stillThere, err := srcRenders.Read("r1", "bad.md")
	require.NoError(t, err)
	require.Equal(t, []byte("original-data"), stillThere, "source copy must survive a verify mismatch")

	// The good entry migrated and was removed from the source.
	gone, err := srcRenders.Read("r1", "good.md")
	require.NoError(t, err)
	require.Nil(t, gone, "the entry that verified cleanly should be removed from the source")
	migratedData, err := dstRendersInner.Read("r1", "good.md")
	require.NoError(t, err)
	require.Equal(t, []byte("good-data"), migratedData)
}

// TestRunRepoMigration_retriesRateLimitedWrite_thenSucceeds reproduces the
// real incident: a destination rejects a write as rate-limited a couple of
// times before accepting it. RunRepoMigration must retry rather than
// immediately counting the entry as failed.
func TestRunRepoMigration_retriesRateLimitedWrite_thenSucceeds(t *testing.T) {
	ctx := context.Background()

	srcRenders := renderstore.NewLocal(t.TempDir())
	dstRendersInner := renderstore.NewLocal(t.TempDir())
	dstRenders := newWriteFaultStore(dstRendersInner)

	srcAssets := renderstore.NewLocal(t.TempDir())
	dstAssets := renderstore.NewLocal(t.TempDir())

	require.NoError(t, srcRenders.Write("r1", "flaky.md", []byte("flaky-data")))
	dstRenders.failWritesTimes("r1", "flaky.md", 2) // fails twice, succeeds on the 3rd attempt

	migrated, failed, err := jobs.RunRepoMigration(ctx, "r1", srcRenders, dstRenders, srcAssets, dstAssets)
	require.NoError(t, err)
	require.Equal(t, 1, migrated, "the entry should eventually migrate once retries succeed")
	require.Equal(t, 0, failed)
	require.Equal(t, 3, dstRenders.attempts("r1", "flaky.md"), "expected 2 failed attempts + 1 successful attempt")

	gotData, err := dstRendersInner.Read("r1", "flaky.md")
	require.NoError(t, err)
	require.Equal(t, []byte("flaky-data"), gotData)

	srcGone, err := srcRenders.Read("r1", "flaky.md")
	require.NoError(t, err)
	require.Nil(t, srcGone, "source should be deleted once the retried write verifies")
}

// TestRunRepoMigration_rateLimitExhausted_countsFailedWithoutStoppingOthers
// proves that a destination which never stops rate-limiting one entry
// eventually gives up on it (rather than retrying forever), counts it as
// failed, leaves its source copy intact, and — critically — does not abort
// the rest of the repo's migration.
func TestRunRepoMigration_rateLimitExhausted_countsFailedWithoutStoppingOthers(t *testing.T) {
	ctx := context.Background()

	srcRenders := renderstore.NewLocal(t.TempDir())
	dstRendersInner := renderstore.NewLocal(t.TempDir())
	dstRenders := newWriteFaultStore(dstRendersInner)

	srcAssets := renderstore.NewLocal(t.TempDir())
	dstAssets := renderstore.NewLocal(t.TempDir())

	require.NoError(t, srcRenders.Write("r1", "stuck.md", []byte("stuck-data")))
	require.NoError(t, srcRenders.Write("r1", "good.md", []byte("good-data")))
	// Always fails: more failures than the retry budget allows.
	dstRenders.failWritesTimes("r1", "stuck.md", jobs.MigrationMaxRetries+1)

	migrated, failed, err := jobs.RunRepoMigration(ctx, "r1", srcRenders, dstRenders, srcAssets, dstAssets)
	require.NoError(t, err)
	require.Equal(t, 1, migrated, "good.md should still migrate despite stuck.md exhausting retries")
	require.Equal(t, 1, failed, "stuck.md should be counted failed once retries are exhausted")
	require.Equal(t, jobs.MigrationMaxRetries+1, dstRenders.attempts("r1", "stuck.md"),
		"should attempt the initial write plus every retry, then stop")

	stillThere, err := srcRenders.Read("r1", "stuck.md")
	require.NoError(t, err)
	require.Equal(t, []byte("stuck-data"), stillThere, "source copy must survive exhausted retries")

	gone, err := srcRenders.Read("r1", "good.md")
	require.NoError(t, err)
	require.Nil(t, gone)
}
