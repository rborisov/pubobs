package gitcache

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrGitPushRejected signals that the remote accepted the connection and
// authenticated the request, then refused the ref update itself — a read-only
// or insufficiently-scoped token, a protected branch, or a pre-receive hook
// that declined.
//
// This is deliberately distinct from ErrGitNonFastForward. Both surface as
// some flavor of "rejected" in git's output, but they need opposite remedies:
// a rejected push cannot be fixed by retrying or by reconciling history (the
// credential simply may not write here), while a non-fast-forward is a
// transient divergence that a re-sync resolves. Reporting the former as the
// latter is what sent a user hunting for a merge conflict that never existed
// on a brand-new repo whose token had no write access.
var ErrGitPushRejected = errors.New("remote rejected the push")

// ErrGitNonFastForward signals that the pushed ref could not be
// fast-forwarded because the remote branch has commits the local clone does
// not. In this codebase's sync path that is a genuine race, not a persistent
// state: Sync always fetches and hard-resets to the remote tip immediately
// before committing (see getOrClone/FetchReset), so the only way to lose the
// fast-forward is for the branch to move in the seconds between that reset
// and the push. It is therefore retryable, unlike ErrGitPushRejected.
var ErrGitNonFastForward = errors.New("push was not a fast-forward")

// nonFastForwardPatterns mark a push failure as divergence, checked (and
// matched case-insensitively) before the generic remote-rejection markers
// because a server with receive.denyNonFastForwards reports divergence as a
// *remote* rejection — the specific cause must win over the generic one.
//
// Deliberately excludes "failed to push some refs": git prints that line for
// every push failure, auth and permission ones included, so matching it is
// exactly the over-broad test this classification exists to replace.
var nonFastForwardPatterns = []string{
	"non-fast-forward",
	"fetch first",
}

// pushRejectedPatterns mark a push that reached the remote and was refused
// there, as opposed to failing at the network or credential layer.
var pushRejectedPatterns = []string{
	"[remote rejected]",
	"pre-receive hook declined",
	"protected branch",
	"push declined",
	"permission denied for writing",
	"denied to",
}

// classifyPushError wraps a failed push with ErrGitNonFastForward or
// ErrGitPushRejected when git's output identifies which of the two it was.
// Any other failure — including an ErrGitAuthFailed already classified by
// classifyGitError, and plain network errors — is returned untouched.
func classifyPushError(err error) error {
	if err == nil {
		return nil
	}
	// Both verbs are %w so the classification is *added* to whatever the inner
	// error already was rather than replacing it. Using %v for the inner error
	// severed its chain: an ErrGitAuthFailed whose text also matched a
	// push-rejected pattern lost that identity, and Cache.recordGitOpResult
	// keys the auth-failure backoff off errors.Is(err, ErrGitAuthFailed) — so
	// a doomed network operation would stop backing off and re-run on every
	// request.
	lower := strings.ToLower(err.Error())
	for _, pat := range nonFastForwardPatterns {
		if strings.Contains(lower, pat) {
			return fmt.Errorf("%w: %w", ErrGitNonFastForward, err)
		}
	}
	for _, pat := range pushRejectedPatterns {
		if strings.Contains(lower, pat) {
			return fmt.Errorf("%w: %w", ErrGitPushRejected, err)
		}
	}
	return err
}

var (
	// remoteLineRE captures what the git server itself printed ("remote: ...").
	remoteLineRE = regexp.MustCompile(`(?m)^remote:\s*(.+?)\s*$`)
	// rejectedReasonRE captures git's own parenthesised reason on the
	// "! [remote rejected] HEAD -> main (…)" line.
	rejectedReasonRE = regexp.MustCompile(`!\s*\[(?:remote )?rejected\][^(]*\(([^)]+)\)`)
	// credentialInURLRE matches the userinfo section of a URL. Credentials are
	// injected into the remote URL handed to git (see credentialedURL), so any
	// text derived from git's output must be scrubbed before it can be shown
	// to a client.
	//
	// The userinfo class deliberately allows "/" and ":". An earlier version
	// used [^/@\s]*, which silently failed to redact anything whose password
	// contained a slash — and passwords do: GitLab PATs, base64-derived
	// secrets, and any hand-typed password can carry one. A user who pasted a
	// repository URL into the token field produced
	// `https://user:https://github.com/o/r@github.com/o/r/`, which that class
	// could not span, so the whole credential was echoed to the client
	// verbatim. Excluding only whitespace and quotes means the match runs to
	// the LAST "@" in the token; when a path also contains "@" this redacts
	// more than strictly necessary, which is the correct direction to err.
	credentialInURLRE = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^\s'"]*@`)
)

// PushRejectionReason extracts the human-meaningful explanation from a failed
// push: whatever the remote printed for itself, plus git's own parenthesised
// reason. Returns "" when git's output carries neither, so callers can fall
// back to a generic message rather than echoing raw multi-line git output.
//
// The result is always credential-scrubbed and safe to return to a client.
func PushRejectionReason(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()

	var parts []string
	seen := map[string]bool{}
	for _, m := range remoteLineRE.FindAllStringSubmatch(text, -1) {
		line := strings.TrimSpace(m[1])
		// Progress/counting chatter every push emits; not a reason.
		if line == "" || strings.HasPrefix(line, "Resolving deltas") || strings.HasPrefix(line, "Counting objects") {
			continue
		}
		if !seen[line] {
			parts = append(parts, line)
			seen[line] = true
		}
	}
	if m := rejectedReasonRE.FindStringSubmatch(text); m != nil {
		reason := strings.TrimSpace(m[1])
		if len(parts) == 0 {
			parts = append(parts, reason)
		} else {
			parts = append(parts, "("+reason+")")
		}
	}
	return Redact(strings.Join(parts, " "))
}

// Redact removes any user:password userinfo embedded in a URL, keeping the
// host so the message still identifies which remote failed. Every git failure
// this package returns can quote the authenticated remote URL it was handed
// (see credentialedURL), so callers must run this over anything derived from
// git output before it leaves the server.
func Redact(s string) string {
	return credentialInURLRE.ReplaceAllString(s, "$1")
}
