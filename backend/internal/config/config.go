package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	Port               string
	BaseURL            string
	OIDCIssuer         string
	OIDCClientID       string
	OIDCClientSecret   string
	SecretKey          []byte
	RepoCacheDir       string
	RepoCacheTTL       time.Duration
	CacheCheckInterval time.Duration
	DiskWarnPct        float64
	DiskCritPct        float64
	DBPath             string
}

func Load() (*Config, error) {
	home, _ := os.UserHomeDir()
	defaultCacheDir := filepath.Join(home, ".pubobs", "repos")
	defaultDBPath := filepath.Join(home, ".pubobs", "pubobs.db")
	if _, err := os.Stat("/data"); err == nil {
		defaultCacheDir = "/data/repos"
		defaultDBPath = "/data/db/pubobs.db"
	}

	cfg := &Config{
		Port:             getEnv("PUBOBS_PORT", "8080"),
		BaseURL:          getEnv("PUBOBS_BASE_URL", ""),
		OIDCIssuer:       getEnv("PUBOBS_OIDC_ISSUER", ""),
		OIDCClientID:     getEnv("PUBOBS_OIDC_CLIENT_ID", ""),
		OIDCClientSecret: getEnv("PUBOBS_OIDC_CLIENT_SECRET", ""),
		RepoCacheDir:     getEnv("PUBOBS_REPO_CACHE_DIR", defaultCacheDir),
		DBPath:           getEnv("PUBOBS_DB_PATH", defaultDBPath),
	}

	if raw := os.Getenv("PUBOBS_SECRET_KEY"); raw != "" {
		key, err := hex.DecodeString(raw)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("PUBOBS_SECRET_KEY must be 64 hex chars (32 bytes)")
		}
		cfg.SecretKey = key
	} else {
		return nil, fmt.Errorf("PUBOBS_SECRET_KEY is required")
	}

	var err error
	if cfg.RepoCacheTTL, err = parseDuration("PUBOBS_REPO_CACHE_TTL", "24h"); err != nil {
		return nil, err
	}
	if cfg.CacheCheckInterval, err = parseDuration("PUBOBS_CACHE_CHECK_INTERVAL", "1h"); err != nil {
		return nil, err
	}
	if cfg.DiskWarnPct, err = parseFloat("PUBOBS_DISK_WARN_PCT", 20); err != nil {
		return nil, err
	}
	if cfg.DiskCritPct, err = parseFloat("PUBOBS_DISK_CRIT_PCT", 5); err != nil {
		return nil, err
	}

	for _, check := range []struct{ name, val string }{
		{"PUBOBS_BASE_URL", cfg.BaseURL},
		{"PUBOBS_OIDC_ISSUER", cfg.OIDCIssuer},
		{"PUBOBS_OIDC_CLIENT_ID", cfg.OIDCClientID},
		{"PUBOBS_OIDC_CLIENT_SECRET", cfg.OIDCClientSecret},
	} {
		if check.val == "" {
			return nil, fmt.Errorf("%s is required", check.name)
		}
	}

	return cfg, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseDuration(key, def string) (time.Duration, error) {
	raw := getEnv(key, def)
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q: %w", key, raw, err)
	}
	return d, nil
}

func parseFloat(key string, def float64) (float64, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid number %q: %w", key, raw, err)
	}
	return f, nil
}
