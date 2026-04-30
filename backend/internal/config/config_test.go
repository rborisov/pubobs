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
	require.Equal(t, float64(20), cfg.DiskWarnPct)
	require.Equal(t, float64(5), cfg.DiskCritPct)
	require.Equal(t, "/data/repos", cfg.RepoCacheDir)
	require.Equal(t, "/data/db/pubobs.db", cfg.DBPath)
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
