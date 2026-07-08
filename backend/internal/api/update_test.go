// backend/internal/api/update_test.go
package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pubobs/backend/internal/api"
	"github.com/stretchr/testify/require"
)

func newTestDepsWithUpdate(t *testing.T, cfg func(*api.Deps)) *api.Deps {
	t.Helper()
	deps := newTestDeps(t)
	deps.Config.UpdateRepoURL = "https://example.invalid/pubobs.git"
	deps.Config.UpdateBranch = "main"
	deps.Config.UpdateInstallDir = t.TempDir()
	deps.Update = api.NewUpdateManager(deps.Config)
	if cfg != nil {
		cfg(deps)
	}
	return deps
}

// stubLatestVersion swaps LatestRemoteVersionFunc for the duration of the
// test so no real network access to a git remote is required.
func stubLatestVersion(t *testing.T, fn func(repoURL, branch string) (string, error)) {
	t.Helper()
	orig := api.LatestRemoteVersionFunc
	api.LatestRemoteVersionFunc = fn
	t.Cleanup(func() { api.LatestRemoteVersionFunc = orig })
}

// stubRunUpdateCmd swaps RunUpdateCmdFunc for the duration of the test so no
// real git/Docker execution is required.
func stubRunUpdateCmd(t *testing.T, fn func(dir, name string, args ...string) ([]byte, error)) {
	t.Helper()
	orig := api.RunUpdateCmdFunc
	api.RunUpdateCmdFunc = fn
	t.Cleanup(func() { api.RunUpdateCmdFunc = orig })
}

func TestUpdateManager_Check_versionComparison(t *testing.T) {
	stubLatestVersion(t, func(repoURL, branch string) (string, error) {
		require.Equal(t, "https://example.invalid/pubobs.git", repoURL)
		require.Equal(t, "main", branch)
		return "deadbeefcafef00d", nil
	})
	deps := newTestDepsWithUpdate(t, nil)

	st := deps.Update.Check()
	require.Equal(t, "idle", st.Status)
	require.True(t, st.UpdateAvailable, "current build is always \"dev\" in tests, so any resolved latest SHA should read as an available update")
	require.Equal(t, "deadbeefcafef00d", st.Latest)
	require.Equal(t, "deadbeefcafe", st.LatestShort)
}

func TestUpdateManager_Check_noUpdateWhenSameVersion(t *testing.T) {
	stubLatestVersion(t, func(string, string) (string, error) { return api.Version, nil })
	deps := newTestDepsWithUpdate(t, nil)
	require.NoError(t, os.WriteFile(
		filepath.Join(deps.Config.UpdateInstallDir, ".pubobs-version"),
		[]byte(api.Version+"\n"), 0644))

	st := deps.Update.Check()
	require.False(t, st.UpdateAvailable)
	require.Equal(t, "idle", st.Status)
}

func TestUpdateManager_Check_usesDeployedMarkerNotEmbeddedBuild(t *testing.T) {
	const deployed = "f1ff9525fce17f6b65d16f7671871c5912679ce8"
	stubLatestVersion(t, func(string, string) (string, error) { return deployed, nil })
	deps := newTestDepsWithUpdate(t, nil)
	require.NoError(t, os.WriteFile(
		filepath.Join(deps.Config.UpdateInstallDir, ".pubobs-version"),
		[]byte(deployed+"\n"), 0644))

	st := deps.Update.Check()
	require.False(t, st.UpdateAvailable)
	require.Equal(t, deployed, st.Current)
}

func TestUpdateManager_Check_remoteError(t *testing.T) {
	stubLatestVersion(t, func(string, string) (string, error) { return "", errors.New("network unreachable") })
	deps := newTestDepsWithUpdate(t, nil)

	st := deps.Update.Check()
	require.Equal(t, "error", st.Status)
	require.Contains(t, st.Message, "network unreachable")
}

func TestUpdateManager_ConcurrentStartRejected(t *testing.T) {
	// Block the background run's first step on a channel so we can
	// deterministically observe both Start() calls racing while the first
	// run is still "in flight", without depending on real timing.
	release := make(chan struct{})
	stubLatestVersion(t, func(string, string) (string, error) {
		<-release
		return "", errors.New("stubbed: stop before touching git/docker")
	})
	deps := newTestDepsWithUpdate(t, nil)

	require.NoError(t, deps.Update.Start(), "first Start should succeed and begin running in the background")

	err := deps.Update.Start()
	require.Error(t, err, "a second Start while one is already running must be rejected")
	require.Contains(t, err.Error(), "already running")

	close(release)
	require.Eventually(t, func() bool {
		return deps.Update.State().Status == "error"
	}, time.Second, 10*time.Millisecond, "background run should finish (with our stubbed error) after being released")
}

func TestUpdateManager_Run_alreadyUpToDate(t *testing.T) {
	stubLatestVersion(t, func(string, string) (string, error) { return api.Version, nil })
	stubRunUpdateCmd(t, func(dir, name string, args ...string) ([]byte, error) {
		t.Fatalf("no git/docker command should run when already up to date, got: %s %v", name, args)
		return nil, nil
	})
	deps := newTestDepsWithUpdate(t, nil)
	require.NoError(t, os.WriteFile(
		filepath.Join(deps.Config.UpdateInstallDir, ".pubobs-version"),
		[]byte(api.Version+"\n"), 0644))

	require.NoError(t, deps.Update.Start())
	require.Eventually(t, func() bool {
		return deps.Update.State().Status == "done"
	}, time.Second, 10*time.Millisecond)

	st := deps.Update.State()
	require.False(t, st.Maintenance)
	require.Equal(t, "Already up to date", st.Message)
}

func TestUpdateManager_Run_fullSuccess(t *testing.T) {
	const latestSHA = "1111222233334444555566667777888899990000"
	stubLatestVersion(t, func(string, string) (string, error) { return latestSHA, nil })

	var ran []string
	stubRunUpdateCmd(t, func(dir, name string, args ...string) ([]byte, error) {
		ran = append(ran, name)
		switch name {
		case "git":
			// Simulate the sparse clone by creating the 3 files the real
			// `git clone` + `sparse-checkout set` would have produced,
			// wherever the manager passed as the clone destination (the
			// last positional arg of `git clone ... <dest>`, or `dir` for
			// the sparse-checkout step run inside that same dir).
			cloneDir := dir
			if cloneDir == "" && len(args) > 0 {
				cloneDir = args[len(args)-1]
			}
			require.NoError(t, os.MkdirAll(filepath.Join(cloneDir, "backend", "bin"), 0755))
			os.WriteFile(filepath.Join(cloneDir, "backend", "Dockerfile"), []byte("FROM alpine"), 0644)
			os.WriteFile(filepath.Join(cloneDir, "backend", "docker-compose.yml"), []byte("services: {}"), 0644)
			for _, arch := range []string{"amd64", "arm64"} {
				os.WriteFile(filepath.Join(cloneDir, "backend", "bin", "pubobs-linux-"+arch), []byte("binary"), 0755)
			}
		case "docker":
			require.Equal(t, []string{"compose", "up", "-d", "--build"}, args)
		default:
			t.Fatalf("unexpected command: %s %v", name, args)
		}
		return []byte("ok\n"), nil
	})
	deps := newTestDepsWithUpdate(t, nil)

	require.NoError(t, deps.Update.Start())
	require.Eventually(t, func() bool {
		return deps.Update.State().Status == "done"
	}, time.Second, 10*time.Millisecond)

	require.Contains(t, ran, "git")
	require.Contains(t, ran, "docker")

	installDir := deps.Config.UpdateInstallDir
	versionMarker, err := os.ReadFile(filepath.Join(installDir, ".pubobs-version"))
	require.NoError(t, err)
	require.Equal(t, latestSHA+"\n", string(versionMarker))

	st := deps.Update.State()
	require.False(t, st.Maintenance)
	require.NotEmpty(t, st.Log)
}

func TestCopyUpdateFiles_fakeSourceDir(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(src, "backend", "bin"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "backend", "Dockerfile"), []byte("FROM alpine:3.21\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "backend", "docker-compose.yml"), []byte("services: {}\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "backend", "bin", "pubobs-linux-amd64"), []byte("fake-binary-contents"), 0755))
	// A file for the other arch must NOT be required/copied.
	require.NoError(t, os.WriteFile(filepath.Join(src, "backend", "bin", "pubobs-linux-arm64"), []byte("other-arch"), 0755))

	require.NoError(t, api.CopyUpdateFiles(src, dest, "amd64"))

	dockerfile, err := os.ReadFile(filepath.Join(dest, "backend", "Dockerfile"))
	require.NoError(t, err)
	require.Equal(t, "FROM alpine:3.21\n", string(dockerfile))

	compose, err := os.ReadFile(filepath.Join(dest, "backend", "docker-compose.yml"))
	require.NoError(t, err)
	require.Equal(t, "services: {}\n", string(compose))

	bin, err := os.ReadFile(filepath.Join(dest, "backend", "bin", "pubobs-linux-amd64"))
	require.NoError(t, err)
	require.Equal(t, "fake-binary-contents", string(bin))

	_, err = os.Stat(filepath.Join(dest, "backend", "bin", "pubobs-linux-arm64"))
	require.True(t, os.IsNotExist(err), "only the requested arch's binary should be copied")
}

func TestCopyUpdateFiles_missingSourceFile(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()
	err := api.CopyUpdateFiles(src, dest, "amd64")
	require.Error(t, err)
}

func TestAdminUpdateEndpoints_requireAdmin(t *testing.T) {
	stubLatestVersion(t, func(string, string) (string, error) { return "abc123", nil })
	deps := newTestDepsWithUpdate(t, nil)
	deps.Store.UpsertUser(context.Background(), "u1", "user@x.com", "User")

	for _, tc := range []struct {
		method, path string
	}{
		{"GET", "/api/admin/update/check"},
		{"POST", "/api/admin/update/start"},
		{"GET", "/api/admin/update/status"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", bearerHeader(t, deps, "u1", "user@x.com", false))
		rr := httptest.NewRecorder()
		api.BuildRouter(deps).ServeHTTP(rr, req)
		require.Equal(t, http.StatusForbidden, rr.Code, "%s %s should require instance admin", tc.method, tc.path)
	}
}

func TestAdminUpdateEndpoints_adminFlow(t *testing.T) {
	stubLatestVersion(t, func(string, string) (string, error) { return "abc123", nil })
	stubRunUpdateCmd(t, func(dir, name string, args ...string) ([]byte, error) {
		return nil, errors.New("stubbed: no real git/docker in this test")
	})
	deps := newTestDepsWithUpdate(t, nil)
	deps.Store.UpsertUser(context.Background(), "admin1", "admin@x.com", "Admin")
	deps.Store.SetInstanceAdmin(context.Background(), "admin1", true)
	auth := bearerHeader(t, deps, "admin1", "admin@x.com", true)

	req := httptest.NewRequest("GET", "/api/admin/update/check", nil)
	req.Header.Set("Authorization", auth)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	req = httptest.NewRequest("POST", "/api/admin/update/start", nil)
	req.Header.Set("Authorization", auth)
	rr = httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusAccepted, rr.Code, rr.Body.String())

	// A second concurrent start is rejected with 409.
	req = httptest.NewRequest("POST", "/api/admin/update/start", nil)
	req.Header.Set("Authorization", auth)
	rr = httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())

	req = httptest.NewRequest("GET", "/api/admin/update/status", nil)
	req.Header.Set("Authorization", auth)
	rr = httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
}

func TestMaintenanceMode_blocksDuringUpdate_exceptEssentialRoutes(t *testing.T) {
	release := make(chan struct{})
	stubLatestVersion(t, func(string, string) (string, error) {
		<-release
		return "", errors.New("stubbed")
	})
	defer close(release)
	deps := newTestDepsWithUpdate(t, nil)
	deps.Store.UpsertUser(context.Background(), "admin1", "admin@x.com", "Admin")
	deps.Store.SetInstanceAdmin(context.Background(), "admin1", true)
	auth := bearerHeader(t, deps, "admin1", "admin@x.com", true)

	require.NoError(t, deps.Update.Start())
	require.Eventually(t, func() bool {
		return deps.Update.State().Maintenance
	}, time.Second, 5*time.Millisecond)

	// Health check must stay reachable during maintenance.
	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	// Update status must stay reachable during maintenance (so the UI can poll it).
	req = httptest.NewRequest("GET", "/api/admin/update/status", nil)
	req.Header.Set("Authorization", auth)
	rr = httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	// An ordinary API route must be blocked with 503 during maintenance.
	req = httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", auth)
	rr = httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
}
