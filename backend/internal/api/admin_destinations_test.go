// backend/internal/api/admin_destinations_test.go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/model"
	"github.com/stretchr/testify/require"
)

func TestAdminDestinations_createListDeleteGuard(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertUser(ctx, "admin1", "admin@x.com", "Admin")
	deps.Resolver = newTestResolver(t, deps)
	// Substitute S3 validation so create doesn't need a live endpoint.
	orig := api.S3ValidateFunc
	api.S3ValidateFunc = func(_ *model.StorageSettings) error { return nil }
	defer func() { api.S3ValidateFunc = orig }()

	// Create.
	body := `{"name":"arch","s3_endpoint":"s3.example.com","s3_bucket":"b","s3_access_key":"AK","s3_secret_key":"SK","s3_region":"us-east-1","s3_use_ssl":true}`
	req := httptest.NewRequest("POST", "/api/admin/storage-destinations", strings.NewReader(body))
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var created map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &created))
	destID, _ := created["id"].(string)
	require.NotEmpty(t, destID)

	// List omits secret key.
	req = httptest.NewRequest("GET", "/api/admin/storage-destinations", nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr = httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.NotContains(t, rr.Body.String(), "SK", "secret key must not be echoed")

	// Assign a repo, then delete must be rejected.
	deps.Store.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	deps.Store.SetRepoStorageDestination(ctx, "r1", &destID)
	req = httptest.NewRequest("DELETE", "/api/admin/storage-destinations/"+destID, nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr = httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())

	// Unassign, then delete succeeds.
	deps.Store.SetRepoStorageDestination(ctx, "r1", nil)
	req = httptest.NewRequest("DELETE", "/api/admin/storage-destinations/"+destID, nil)
	req.Header.Set("Authorization", bearerHeader(t, deps, "admin1", "admin@x.com", true))
	rr = httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
}
