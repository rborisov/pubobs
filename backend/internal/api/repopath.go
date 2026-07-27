package api

import "strings"

// maxRepoPathLen bounds a client-supplied path. Well beyond any real vault
// path, but short enough that a pathological payload can't be used to build
// enormous filesystem paths.
const maxRepoPathLen = 1024

// validRepoPath reports whether a client-supplied, repo-relative path is safe
// to join onto a repo's local clone directory.
//
// Every path in a sync payload is passed to filepath.Join against the clone
// dir and then written or removed (gitcache.Sync). filepath.Join *cleans* its
// result, so a path like "../../../etc/cron.d/x" resolves to an absolute path
// outside the clone entirely — and the backend container runs as root. Nothing
// upstream constrains these paths: they arrive verbatim from the plugin's JSON.
//
// Rejecting rather than sanitizing is deliberate. A silently-rewritten path
// would write the user's note to a location they didn't ask for; a rejection
// tells them their payload was wrong.
func validRepoPath(p string) bool {
	if p == "" || len(p) > maxRepoPathLen {
		return false
	}
	if strings.ContainsRune(p, 0) {
		return false
	}
	// Backslashes are never a separator here (repo paths are always
	// slash-separated) and would otherwise smuggle traversal past the
	// segment checks below on a Windows host.
	if strings.Contains(p, `\`) {
		return false
	}
	if strings.HasPrefix(p, "/") {
		return false
	}
	// Windows drive-absolute ("C:\...") and drive-relative ("C:x") paths.
	if len(p) >= 2 && p[1] == ':' {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".", "..", ".git":
			return false
		}
	}
	return true
}
