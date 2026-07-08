package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests guard against a real production incident: PUBOBS_ASSET_DIR
// (/data/assets, see config.go) has been a fully supported local storage
// location since the per-destination storage feature landed, but the
// deploy config never grew a matching host bind mount or mkdir for it.
// Every asset (image, etc.) synced by a repo on local storage was silently
// written into the container's own ephemeral writable layer instead of the
// host's bind-mounted data directory: invisible to `du` on the documented
// data directory, not covered by backups, and lost outright the next time
// the container is recreated (e.g. `docker compose down && up`, or
// `install.sh --reinstall`) — while still consuming real host disk the
// whole time, exactly the kind of "where did my disk space go" mystery an
// admin has no way to diagnose from inside the app. See docker-compose.yml
// and install.sh for the actual fix; these tests just make sure the four
// data subdirectories the config package hands out defaults for
// (db/repos/renders/assets) always stay in lockstep across both files.
const wantDataSubdirs = 4

func TestDockerCompose_mountsAllDataSubdirs(t *testing.T) {
	raw, err := os.ReadFile("../../docker-compose.yml")
	require.NoError(t, err, "read backend/docker-compose.yml")
	content := string(raw)

	for _, mount := range []string{
		"./data/db:/data/db",
		"./data/repos:/data/repos",
		"./data/renders:/data/renders",
		"./data/assets:/data/assets",
	} {
		require.Containsf(t, content, mount, "docker-compose.yml is missing bind mount %q — data written there would silently live only in the container's writable layer", mount)
	}
	require.Equal(t, wantDataSubdirs, strings.Count(content, "./data/"), "docker-compose.yml should bind-mount exactly the db/repos/renders/assets subdirectories — update wantDataSubdirs if a new persisted subdirectory is intentionally added")
}

func TestInstallScript_createsAllDataSubdirs(t *testing.T) {
	raw, err := os.ReadFile("../../../install.sh")
	require.NoError(t, err, "read install.sh")
	content := string(raw)

	for _, fn := range []string{"start_containers()", "restart_containers()"} {
		idx := strings.Index(content, fn)
		require.Greaterf(t, idx, -1, "install.sh missing %s", fn)
		// Look at the mkdir line immediately following the function header.
		body := content[idx:]
		mkdirIdx := strings.Index(body, "mkdir -p")
		require.Greaterf(t, mkdirIdx, -1, "%s has no mkdir -p call", fn)
		lineEnd := strings.Index(body[mkdirIdx:], "\n")
		mkdirLine := body[mkdirIdx : mkdirIdx+lineEnd]
		for _, dir := range []string{"data/db", "data/repos", "data/renders", "data/assets"} {
			require.Containsf(t, mkdirLine, dir, "%s's mkdir -p call is missing %q", fn, dir)
		}
	}
}
