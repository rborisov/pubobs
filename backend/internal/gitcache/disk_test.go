package gitcache_test

import (
	"testing"

	"github.com/pubobs/backend/internal/gitcache"
	"github.com/stretchr/testify/require"
)

func TestEvaluateDiskStatus(t *testing.T) {
	const gib = int64(1 << 30)

	cases := []struct {
		name      string
		freeBytes int64
		freePct   float64
		warnPct   float64
		critPct   float64
		want      string
	}{
		{"plenty free", 10 * gib, 50, 20, 5, "ok"},
		{"below warn only", 10 * gib, 15, 20, 5, "warn"},
		{"below crit pct and bytes", gib / 2, 2, 20, 5, "crit"},
		{"below crit pct but large disk", 10 * gib, 2, 20, 5, "warn"}, // low % but >1GiB free never crits
		{"exactly at crit boundary is not crit", gib, 2, 20, 5, "warn"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := gitcache.EvaluateDiskStatus(tc.freeBytes, tc.freePct, tc.warnPct, tc.critPct)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestCache_DiskStatus_liveCheck(t *testing.T) {
	cache := gitcache.NewCache(t.TempDir())

	// The actual free space on the machine running this test varies (CI vs.
	// a nearly-full dev laptop), so this doesn't assert a specific status —
	// it pins down that DiskStatus performs a real, working live statfs and
	// classifies it consistently with EvaluateDiskStatus, rather than only
	// compiling or silently erroring.
	warnPct, critPct := 20.0, 5.0
	status, freeBytes, freePct, err := cache.DiskStatus(warnPct, critPct)
	require.NoError(t, err)
	require.Positive(t, freeBytes)
	require.Positive(t, freePct)
	require.Equal(t, gitcache.EvaluateDiskStatus(freeBytes, freePct, warnPct, critPct), status)
}
