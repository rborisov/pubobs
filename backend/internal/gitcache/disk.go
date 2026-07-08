package gitcache

// critMinBytes ensures a volume is never flagged "crit" when more than this
// much is free in absolute terms, even if that's a low percentage of a very
// large disk — a low percentage alone isn't actionable if there's still
// plenty of headroom in bytes.
const critMinBytes = 1 << 30 // 1 GiB

// EvaluateDiskStatus classifies free disk space into "ok", "warn", or "crit"
// given free bytes/percentage and the configured thresholds. This is the
// single source of truth for the classification, shared by the periodic
// eviction/health job (which persists it for the admin dashboard) and any
// call site that needs a live, uncached answer (see Cache.DiskStatus).
func EvaluateDiskStatus(freeBytes int64, freePct, warnPct, critPct float64) string {
	if freePct < critPct && freeBytes < critMinBytes {
		return "crit"
	}
	if freePct < warnPct {
		return "warn"
	}
	return "ok"
}

// DiskStatus performs a live, uncached statfs check on the cache volume and
// classifies it with EvaluateDiskStatus. Callers that must never act on
// stale data — e.g. rejecting a sync because disk is critically low — should
// use this instead of a periodically-refreshed cached health row: a single
// statfs call is cheap enough to run on every such request, and doing so
// means a reject decision can never lag behind an operator who just freed up
// disk space (unlike a cache that only refreshes on an hourly ticker).
func (c *Cache) DiskStatus(warnPct, critPct float64) (status string, freeBytes int64, freePct float64, err error) {
	freeBytes, freePct, err = c.DiskUsage()
	if err != nil {
		return "", freeBytes, freePct, err
	}
	return EvaluateDiskStatus(freeBytes, freePct, warnPct, critPct), freeBytes, freePct, nil
}
