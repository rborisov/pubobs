package config_test

import (
	"testing"
	"time"

	"github.com/pubobs/backend/internal/config"
	"github.com/stretchr/testify/require"
)

func withRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PUBOBS_SECRET_KEY", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2")
	t.Setenv("PUBOBS_OIDC_ISSUER", "https://accounts.example.com")
	t.Setenv("PUBOBS_OIDC_CLIENT_ID", "client-id")
	t.Setenv("PUBOBS_OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("PUBOBS_BASE_URL", "https://pubobs.example.com")
}

func TestLoad_defaults(t *testing.T) {
	withRequiredEnv(t)
	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, "8080", cfg.Port)
	require.Equal(t, 24*time.Hour, cfg.RepoCacheTTL)
	require.Equal(t, time.Hour, cfg.CacheCheckInterval)
	require.Equal(t, 3*time.Minute, cfg.GitCloneTimeout)
	require.Equal(t, 25*time.Second, cfg.GitFetchTimeout)
	require.Equal(t, 2*time.Minute, cfg.GitLocalOpTimeout)
	require.Equal(t, float64(20), cfg.DiskWarnPct)
	require.Equal(t, float64(5), cfg.DiskCritPct)
	require.NotEmpty(t, cfg.RepoCacheDir)
	require.NotEmpty(t, cfg.DBPath)
	require.Len(t, cfg.SecretKey, 32)
}

func TestLoad_missingRequired(t *testing.T) {
	_, err := config.Load()
	require.Error(t, err)
}

func TestLoad_badSecretKey(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("PUBOBS_SECRET_KEY", "notenoughbytes")
	_, err := config.Load()
	require.Error(t, err)
}

func TestLoad_customDuration(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("PUBOBS_REPO_CACHE_TTL", "48h")
	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, 48*time.Hour, cfg.RepoCacheTTL)
}

func TestLoad_customGitCloneTimeout(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("PUBOBS_GIT_CLONE_TIMEOUT", "90s")
	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, 90*time.Second, cfg.GitCloneTimeout)
}

func TestLoad_customGitFetchTimeout(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("PUBOBS_GIT_FETCH_TIMEOUT", "10s")
	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, 10*time.Second, cfg.GitFetchTimeout)
}

func TestLoad_customGitLocalOpTimeout(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("PUBOBS_GIT_LOCAL_OP_TIMEOUT", "30s")
	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, cfg.GitLocalOpTimeout)
}

func TestLoad_updateDefaults(t *testing.T) {
	withRequiredEnv(t)
	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, config.DefaultUpdateRepoURL, cfg.UpdateRepoURL)
	require.Equal(t, config.DefaultUpdateBranch, cfg.UpdateBranch)
	require.Equal(t, "/opt/pubobs", cfg.UpdateInstallDir)
}

func TestLoad_normalizesGogsDevelopSettings(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("PUBOBS_UPDATE_REPO_URL", "https://gogs.example.com/team/pubobs.git")
	t.Setenv("PUBOBS_UPDATE_BRANCH", "develop")
	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, config.DefaultUpdateRepoURL, cfg.UpdateRepoURL)
	require.Equal(t, config.DefaultUpdateBranch, cfg.UpdateBranch)
}

func TestNormalizeUpdateSettings(t *testing.T) {
	repo, branch := config.NormalizeUpdateSettings("https://github.com/acme/pubobs.git", "develop")
	require.Equal(t, config.DefaultUpdateRepoURL, repo)
	require.Equal(t, config.DefaultUpdateBranch, branch)

	repo, branch = config.NormalizeUpdateSettings("https://github.com/acme/pubobs.git", "main")
	require.Equal(t, "https://github.com/acme/pubobs.git", repo)
	require.Equal(t, "main", branch)
}
