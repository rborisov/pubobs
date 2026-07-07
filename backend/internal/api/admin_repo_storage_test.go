// backend/internal/api/admin_repo_storage_test.go
package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/model"
	"github.com/stretchr/testify/require"
)

// TestAdminRepoStorage_assignAndMigrate exercises the assignment endpoint
// end-to-end, but deliberately does NOT assert byte-level readability at the
// new (d1) destination: d1's S3 config is an empty placeholder (no live S3
// server), so a real write against it may legitimately fail. The point of
// this test is that (a) the assignment persists immediately and (b) the
// background migration goroutine runs to completion and records a terminal
// status ("done" or "failed") — not that the bytes land somewhere real.
// Byte-level migration correctness (data actually copied and verified) is
// already covered by Task 4's RunRepoMigration unit tests using real local
// stores on both ends.
func TestAdminRepoStorage_assignAndMigrate(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")

	// d1 is a valid assignment target row. Its S3 endpoint is a syntactically
	// valid host:port with nothing listening (127.0.0.1:1 — a reserved,
	// normally-unassigned port), so the resolver can construct a client for
	// it (unlike a blank endpoint, which fails client construction outright)
	// but any real read/write against it fails fast with connection refused.
	// There's no live S3 server in this test. Create the destination BEFORE
	// building the resolver so its store-set is present in the resolver's
	// per-destination map (built once at construction time).
	require.NoError(t, deps.Store.CreateStorageDestination(ctx, &model.StorageDestination{
		ID: "d1", Name: "d1", S3Endpoint: "127.0.0.1:1", S3Bucket: "b", S3Region: "us-east-1",
	}))
	deps.Resolver = newTestResolver(t, deps)

	// Seed r1's render data into its CURRENT (local) store.
	rs, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rs.Write("r1", "note.md", []byte("data")))

	body := `{"storage_destination_id":"d1"}`
	req := httptest.NewRequest("PUT", "/api/admin/repos/r1/storage", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusAccepted, rr.Code, rr.Body.String())

	// Assignment persisted immediately.
	repo, err := deps.Store.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.NotNil(t, repo.StorageDestinationID)
	require.Equal(t, "d1", *repo.StorageDestinationID)

	// Background migration reaches a terminal status. Either "done" or
	// "failed" is acceptable — see comment above.
	require.Eventually(t, func() bool {
		rp, err := deps.Store.GetRepo(ctx, "r1")
		if err != nil || rp == nil {
			return false
		}
		return rp.MigrationStatus == "done" || rp.MigrationStatus == "failed"
	}, 2*time.Second, 10*time.Millisecond)
}

// TestAdminRepoStorage_alreadyRunningConflict guards against starting a
// second concurrent migration for the same repo.
func TestAdminRepoStorage_alreadyRunningConflict(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	deps.Resolver = newTestResolver(t, deps)
	require.NoError(t, deps.Store.SetRepoMigrationStatus(ctx, "r1", "running", 0, 0))

	body := `{"storage_destination_id":null}`
	req := httptest.NewRequest("PUT", "/api/admin/repos/r1/storage", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())
}

// TestAdminRepoStorage_sameDestinationIsNoop is the data-loss regression guard.
// Reassigning a repo to the destination it already has must be a no-op: without
// the guard, the migration's source and destination store pairs alias the same
// objects, so every key is copied onto itself and then DELETED — wiping the
// repo's renders and assets.
func TestAdminRepoStorage_sameDestinationIsNoop(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	deps.Resolver = newTestResolver(t, deps)

	// Seed a render entry into r1's (local) store.
	rs, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rs.Write("r1", "note.md", []byte("precious")))

	// Reassign local → local (null → null).
	body := `{"storage_destination_id":null}`
	req := httptest.NewRequest("PUT", "/api/admin/repos/r1/storage", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "unchanged")

	// No migration ran: status never left "idle".
	repo, err := deps.Store.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.Equal(t, "idle", repo.MigrationStatus)
	require.Nil(t, repo.StorageDestinationID)

	// CRUCIAL: the seeded render entry survives intact.
	got, err := rs.Read("r1", "note.md")
	require.NoError(t, err)
	require.Equal(t, []byte("precious"), got, "same-destination reassignment must not delete data")
}

// TestAdminRepoStorage_sameDestinationD1IsNoop covers the non-nil == non-nil
// branch: a repo already on "d1" reassigned back to "d1" is a no-op.
func TestAdminRepoStorage_sameDestinationD1IsNoop(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	require.NoError(t, deps.Store.CreateStorageDestination(ctx, &model.StorageDestination{
		ID: "d1", Name: "d1", S3Endpoint: "127.0.0.1:1", S3Bucket: "b", S3Region: "us-east-1",
	}))
	deps.Resolver = newTestResolver(t, deps)
	destID := "d1"
	require.NoError(t, deps.Store.SetRepoStorageDestination(ctx, "r1", &destID))

	body := `{"storage_destination_id":"d1"}`
	req := httptest.NewRequest("PUT", "/api/admin/repos/r1/storage", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "unchanged")

	// No migration ran: status stays "idle", assignment unchanged.
	repo, err := deps.Store.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.Equal(t, "idle", repo.MigrationStatus)
	require.NotNil(t, repo.StorageDestinationID)
	require.Equal(t, "d1", *repo.StorageDestinationID)
}

// TestAdminRepoStorage_unknownDestinationRejected validates that a
// non-existent destination id is rejected before anything is persisted.
func TestAdminRepoStorage_unknownDestinationRejected(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Store.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	deps.Resolver = newTestResolver(t, deps)

	body := `{"storage_destination_id":"does-not-exist"}`
	req := httptest.NewRequest("PUT", "/api/admin/repos/r1/storage", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())

	repo, err := deps.Store.GetRepo(ctx, "r1")
	require.NoError(t, err)
	require.Nil(t, repo.StorageDestinationID)
}
