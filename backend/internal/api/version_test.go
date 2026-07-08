package api_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/stretchr/testify/require"
)

func TestVersionsEqual(t *testing.T) {
	full := "f1ff9525fce17f6b65d16f7671871c5912679ce8"
	short := "f1ff9525"

	require.True(t, api.VersionsEqual(full, full))
	require.True(t, api.VersionsEqual(full, short))
	require.True(t, api.VersionsEqual(short, full))
	require.False(t, api.VersionsEqual(full, "f95f6331d6c44d17b7eb18f12d1544af469ba8dd"))
	require.False(t, api.VersionsEqual("", full))
	require.True(t, api.VersionsEqual("", ""))
}

func TestReadDeployedVersion_markerPreferred(t *testing.T) {
	dir := t.TempDir()
	marker := "f1ff9525fce17f6b65d16f7671871c5912679ce8"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".pubobs-version"), []byte(marker+"\n"), 0644))

	require.Equal(t, marker, api.ReadDeployedVersion(dir))
}

func TestReadDeployedVersion_fallsBackToEmbeddedBuild(t *testing.T) {
	require.Equal(t, api.Version, api.ReadDeployedVersion(t.TempDir()))
}
